#!/bin/bash
# Copyright (c) 2026 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0
#
# Produces the Secure Boot (signed) image from an upstream EVE rootfs, for
# the two-stage GRUB design: stage-1 on the ESP and stage-2 in the rootfs,
# both db-signed and enforcing GPG signatures on every file they open; a
# db-signed kernel; and a dm-verity root hash signed by the db key.
#
# Runs in the image built from this directory. Mounts:
#   /in    rootfs.img (upstream), and optionally disk*.raw: live.raw or
#          installer.raw images built with that rootfs.img
#   /keys  db.key, db.crt (PEM), gnupg/ (GNUPGHOME holding the GPG signing
#          key), optional GPG_KEY file naming the key
#   /out   signed rootfs.img, stage1.efi, and live.raw / installer.raw for the
#          disks in /in
# Only amd64 for now.

set -euo pipefail

export GNUPGHOME=/tmp/gnupg
cp -r /keys/gnupg "$GNUPGHOME"
chmod 700 "$GNUPGHOME"
GPG_KEY=$(cat /keys/GPG_KEY 2>/dev/null || true)
DB_KEY=/keys/db.key
DB_CRT=/keys/db.crt
W=/tmp/work
mkdir -p $W

gpgsig() {
   gpg --batch --yes --quiet --digest-algo SHA512 ${GPG_KEY:+--local-user "$GPG_KEY"} \
      --detach-sign -o "$1.sig" "$1"
}
dbsign() {
   sbsign --key $DB_KEY --cert $DB_CRT --output "$2" "$1"
}

gpg --export ${GPG_KEY:+"$GPG_KEY"} > $W/pub.gpg
[ -s $W/pub.gpg ] || { echo "no GPG public key exported" >&2; exit 1; }
printf '%s\n%s\n' \
   'sbat,1,SBAT Version,sbat,1,https://github.com/rhboot/shim/blob/main/SBAT.md' \
   'grub,4,Free Software Foundation,grub,2.12,https://www.gnu.org/software/grub/' > $W/sbat.csv

MODS1="part_gpt fat squash4 gptprio chain pgp gcry_rsa gcry_sha256 gcry_sha512 tpm boot reboot serial terminal"
# pkg/grub's portable and amd64 modules, less network, loopback and filesystems
# the boot path never reads
MODS2="$MODS1 eve_overrides getenv normal linux configfile memdisk tar gzio xzio acpi cpio crypto disk crc64 \
search_disk_uuid search_part_uuid search_part_label search_label video gfxterm probe regexp smbios \
part_msdos cat echo test true measurefs efi_gop efi_uga gpt"
mkimage() {
   grub-mkimage -d /usr/lib/grub/x86_64-efi -O x86_64-efi -p /EFI/BOOT --disable-shim-lock \
      --pubkey $W/pub.gpg --sbat $W/sbat.csv "$@"
}

# sign_root <dir> <out image> <squashfs image it came from>: signs the boot
# files of an unpacked EVE root, replaces its GRUB with a stage-2, and writes it
# as a squashfs with a dm-verity tree and a db-signed root hash. The root must
# boot through the verity initrd, or it would be mounted unverified.
sign_root() {
   local R=$1 OUT=$2 SRC=$3 M=$W/memdisk f
   local BLOCK BYTES DOFF ROOT_HASH SIG_LEN
   [ -f "$R/boot/initrd.img" ] || { echo "$SRC has no verity initrd (boot/initrd.img)" >&2; exit 1; }
   BLOCK=$(od -An -tu4 -j12 -N4 "$SRC" | tr -d ' ')

   dbsign "$R/boot/kernel" $W/kernel && mv $W/kernel "$R/boot/kernel"
   for f in boot/kernel boot/initrd.img boot/ucode.img boot/cmdline etc/eve-release etc/eve-hv-type; do
      [ -f "$R/$f" ] && gpgsig "$R/$f"
   done

   # stage-2: the root's grub.cfg runs from its memdisk under signature enforcement
   rm -rf $M && mkdir -p $M
   {
      # shellcheck disable=SC2016 # $cmddevice is a GRUB variable
      printf 'set superusers="eve"\nexport superusers\nset check_signatures=enforce\nset root=$cmddevice\n'
      cat "$R/EFI/BOOT/grub.cfg"
   } > $M/grub.cfg
   gpgsig $M/grub.cfg
   tar -C $M -cf $W/memdisk.tar grub.cfg grub.cfg.sig
   # shellcheck disable=SC2086
   mkimage -c /stage2.cfg -m $W/memdisk.tar -o $W/stage2.efi $MODS2
   dbsign $W/stage2.efi "$R/EFI/BOOT/BOOTX64.EFI"
   gpgsig "$R/EFI/BOOT/BOOTX64.EFI"

   mksquashfs "$R" "$OUT" -noappend -comp xz -Xbcj x86,arm -b "$BLOCK" -no-recovery -quiet
   /verity-append "$OUT"
   BYTES=$(od -An -tu8 -j40 -N8 "$OUT" | tr -d ' ')
   DOFF=$(( ( (BYTES + 4095) / 4096 * 4096 + 1048575) / 1048576 * 1048576 ))
   dd if="$OUT" bs=4096 skip=$((DOFF / 4096)) count=1 status=none | tr -d '\0' > $W/desc
   ROOT_HASH=$(sed -n 's/^root_hash=//p' $W/desc)
   printf '%s' "$ROOT_HASH" > $W/roothash
   openssl smime -sign -nocerts -noattr -binary -in $W/roothash -inkey $DB_KEY -signer $DB_CRT \
      -outform der -out $W/roothash.p7s
   SIG_LEN=$(stat -c %s $W/roothash.p7s)
   [ "$SIG_LEN" -le $((1048576 - 4096)) ]
   sed -i "s/^sig_len=0$/sig_len=$SIG_LEN/" $W/desc
   grep -q "^sig_len=$SIG_LEN$" $W/desc
   head -c 4096 /dev/zero | dd of="$OUT" bs=4096 seek=$((DOFF / 4096)) conv=notrunc status=none
   dd if=$W/desc of="$OUT" bs=4096 seek=$((DOFF / 4096)) conv=notrunc status=none
   dd if=$W/roothash.p7s of="$OUT" bs=4096 seek=$((DOFF / 4096 + 1)) conv=notrunc status=none
   echo "signed $(basename "$OUT"): root_hash=$ROOT_HASH sig_len=$SIG_LEN"
}

# part <disk> <name>: number, first and last sector of the lowest-numbered
# partition named <name>. ESP-B shares the "EFI System" name with ESP-A.
part() {
   sgdisk -p "$1" | awk -v n="$2" '$1 ~ /^[0-9]+$/ {
      name = $7; for (i = 8; i <= NF; i++) name = name " " $i
      if (name == n) print $1, $2, $3 }' | sort -n | head -1
}

# stage1_esp <disk>: stage-1 alone on the ESP
stage1_esp() {
   local ES ESP
   read -r _ ES _ < <(part "$1" "EFI System") || true
   [ -n "$ES" ] || { echo "no EFI System partition in $1" >&2; exit 1; }
   ESP="$1@@$((ES * 512))"
   mdel -i "$ESP" ::/EFI/BOOT/grub.cfg 2>/dev/null || true
   mcopy -o -i "$ESP" /out/stage1.efi ::/EFI/BOOT/BOOTX64.EFI
}

# 1. rootfs
R=$W/root
unsquashfs -q -d $R /in/rootfs.img
sign_root $R /out/rootfs.img /in/rootfs.img

# 2. stage-1
# shellcheck disable=SC2086
mkimage -c /stage1.cfg -o $W/stage1.efi $MODS1
dbsign $W/stage1.efi /out/stage1.efi

# 3. disks
for D in /in/disk*.raw; do
   [ -f "$D" ] || continue
   read -r _ AS AE < <(part "$D" IMGA) || true
   read -r IN IS IE < <(part "$D" INSTALLER) || true
   if [ -n "$AS" ]; then
      # live: the signed rootfs on IMGA
      cp --sparse=always "$D" /out/live.raw
      [ $(( (AE - AS + 1) * 512 )) -ge "$(stat -c %s /out/rootfs.img)" ]
      dd if=/out/rootfs.img of=/out/live.raw bs=512 seek="$AS" conv=notrunc status=none
      stage1_esp /out/live.raw
   elif [ -n "$IS" ]; then
      # installer: its own root boots through a stage-2 on the verity initrd,
      # installs the signed rootfs, and puts stage-1 on the target's ESP
      # (pkg/installer/install)
      cp --sparse=always "$D" /out/installer.raw
      dd if="$D" of=$W/installer.img bs=512 skip="$IS" count=$((IE - IS + 1)) status=none
      I=$W/installer
      unsquashfs -q -d $I $W/installer.img
      cmp -s $I/rootfs.img /in/rootfs.img ||
         { echo "$D does not install the given rootfs.img" >&2; exit 1; }
      cp /out/rootfs.img $I/rootfs.img
      cp $R/boot/initrd.img $I/boot/initrd.img
      cp /out/stage1.efi $I/EFI/BOOT/stage1.efi
      sign_root $I $W/installer.signed $W/installer.img
      SECTORS=$(( ($(stat -c %s $W/installer.signed) + 511) / 512 ))
      if [ "$SECTORS" -gt $((IE - IS + 1)) ]; then
         # recreate the partition, same identity, in the largest free block
         INFO=$(sgdisk -i "$IN" /out/installer.raw)
         TYPE=$(sed -n 's/^Partition GUID code: \([0-9A-F-]*\).*/\1/p' <<< "$INFO")
         GUID=$(sed -n 's/^Partition unique GUID: //p' <<< "$INFO")
         ATTR=$((16#$(sed -n 's/^Attribute flags: //p' <<< "$INFO")))
         # Alpine's sgdisk cannot parse a hex mask, so set the bits one by one
         SET=()
         for b in $(seq 0 63); do
            if [ $(( (ATTR >> b) & 1 )) = 1 ]; then SET+=(-A "$IN:set:$b"); fi
         done
         sgdisk -d "$IN" -n "$IN:0:+$(( (SECTORS + 2047) / 2048 ))M" -t "$IN:$TYPE" \
            -u "$IN:$GUID" -c "$IN:INSTALLER" "${SET[@]}" /out/installer.raw >/dev/null
         read -r _ IS IE < <(part /out/installer.raw INSTALLER)
         [ "$SECTORS" -le $((IE - IS + 1)) ]
      fi
      dd if=$W/installer.signed of=/out/installer.raw bs=512 seek="$IS" conv=notrunc status=none
      stage1_esp /out/installer.raw
   else
      echo "$D has neither an IMGA nor an INSTALLER partition" >&2
      exit 1
   fi
done
