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
#   /in    rootfs.img (upstream), and optionally live.raw
#   /keys  db.key, db.crt (PEM), gnupg/ (GNUPGHOME holding the GPG signing
#          key), optional GPG_KEY file naming the key
#   /out   signed rootfs.img, stage1.efi, and live.raw if /in has one
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
MODS2="$MODS1 normal linux configfile memdisk tar gzio xzio acpi cpio crypto disk crc64 \
search_disk_uuid search_part_uuid search_part_label search_label video gfxterm probe regexp smbios \
part_msdos cat echo test true measurefs efi_gop efi_uga gpt"
mkimage() {
   grub-mkimage -d /usr/lib/grub/x86_64-efi -O x86_64-efi -p /EFI/BOOT --disable-shim-lock \
      --pubkey $W/pub.gpg --sbat $W/sbat.csv "$@"
}

# 1. rootfs content
R=$W/root
unsquashfs -q -d $R /in/rootfs.img
BLOCK=$(od -An -tu4 -j12 -N4 /in/rootfs.img | tr -d ' ')

dbsign $R/boot/kernel $W/kernel && mv $W/kernel $R/boot/kernel
for f in boot/kernel boot/initrd.img boot/ucode.img boot/cmdline etc/eve-release etc/eve-hv-type; do
   [ -f "$R/$f" ] && gpgsig "$R/$f"
done

# 2. stage-2: rootfs.cfg runs from its memdisk under signature enforcement
M=$W/memdisk
mkdir -p $M
{
   # shellcheck disable=SC2016 # $cmddevice is a GRUB variable
   printf 'set superusers="eve"\nexport superusers\nset check_signatures=enforce\nset root=$cmddevice\n'
   cat $R/EFI/BOOT/grub.cfg
} > $M/grub.cfg
gpgsig $M/grub.cfg
tar -C $M -cf $W/memdisk.tar grub.cfg grub.cfg.sig
# shellcheck disable=SC2086
mkimage -c /stage2.cfg -m $W/memdisk.tar -o $W/stage2.efi $MODS2
dbsign $W/stage2.efi $R/EFI/BOOT/BOOTX64.EFI
gpgsig $R/EFI/BOOT/BOOTX64.EFI

# 3. squashfs, verity tree, signed root hash
mksquashfs $R /out/rootfs.img -noappend -comp xz -Xbcj x86,arm -b "$BLOCK" -no-recovery -quiet
/verity-append /out/rootfs.img
BYTES=$(od -An -tu8 -j40 -N8 /out/rootfs.img | tr -d ' ')
DOFF=$(( ( (BYTES + 4095) / 4096 * 4096 + 1048575) / 1048576 * 1048576 ))
dd if=/out/rootfs.img bs=4096 skip=$((DOFF / 4096)) count=1 status=none | tr -d '\0' > $W/desc
ROOT_HASH=$(sed -n 's/^root_hash=//p' $W/desc)
printf '%s' "$ROOT_HASH" > $W/roothash
openssl smime -sign -nocerts -noattr -binary -in $W/roothash -inkey $DB_KEY -signer $DB_CRT \
   -outform der -out $W/roothash.p7s
SIG_LEN=$(stat -c %s $W/roothash.p7s)
[ "$SIG_LEN" -le $((1048576 - 4096)) ]
sed -i "s/^sig_len=0$/sig_len=$SIG_LEN/" $W/desc
grep -q "^sig_len=$SIG_LEN$" $W/desc
head -c 4096 /dev/zero | dd of=/out/rootfs.img bs=4096 seek=$((DOFF / 4096)) conv=notrunc status=none
dd if=$W/desc of=/out/rootfs.img bs=4096 seek=$((DOFF / 4096)) conv=notrunc status=none
dd if=$W/roothash.p7s of=/out/rootfs.img bs=4096 seek=$((DOFF / 4096 + 1)) conv=notrunc status=none

# 4. stage-1
# shellcheck disable=SC2086
mkimage -c /stage1.cfg -o $W/stage1.efi $MODS1
dbsign $W/stage1.efi /out/stage1.efi

# 5. live image: stage-1 alone on the ESP, the signed rootfs on IMGA
if [ -f /in/live.raw ]; then
   cp --sparse=always /in/live.raw /out/live.raw
   # ESP-A is the lowest-numbered "EFI System" partition; ESP-B shares its name
   part() {
      sgdisk -p /out/live.raw | awk -v n="$1" '$1 ~ /^[0-9]+$/ {
         name = $7; for (i = 8; i <= NF; i++) name = name " " $i
         if (name == n) print $1, $2, $3 }' | sort -n | head -1 | cut -d' ' -f2-
   }
   read -r ES _ < <(part "EFI System")
   read -r AS AE < <(part IMGA)
   [ -n "$ES" ] && [ -n "$AS" ] || { echo "no EFI or IMGA partition in live.raw" >&2; exit 1; }
   [ $(( (AE - AS + 1) * 512 )) -ge "$(stat -c %s /out/rootfs.img)" ]
   ESP="/out/live.raw@@$((ES * 512))"
   mdel -i "$ESP" ::/EFI/BOOT/grub.cfg 2>/dev/null || true
   mcopy -o -i "$ESP" /out/stage1.efi ::/EFI/BOOT/BOOTX64.EFI
   dd if=/out/rootfs.img of=/out/live.raw bs=512 seek="$AS" conv=notrunc status=none
fi

echo "signed: root_hash=$ROOT_HASH sig_len=$SIG_LEN"
