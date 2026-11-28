#!/bin/sh
# Case table for eve_overrides, from docs/GRUB-OVERRIDES.md and the field
# overrides found in tickets and the Help Center. Usage: run.sh RUNNER
#
#   acc LINE [PRESET...]       LINE alone is accepted
#   rej LINE [PRESET...]       LINE alone is rejected
#   val LINE VAR=VALUE         LINE is accepted and leaves VAR=VALUE
#   accx/rejx FMT              as acc/rej, FMT is a printf format (no newline
#                              added), for CR, NUL and missing newlines
#   golden NAME INPUT [FLAG...] cases/INPUT.cfg run with FLAGs gives exactly
#                              cases/NAME.out
#
# Every single-line case starts from the board defaults rootfs.cfg sets on
# generic amd64 (DEFAULTS).
# Single quotes keep GRUB's $VAR literal; trailing commas are test values.
# shellcheck disable=SC2016,SC2258
set -u

RUN=$1
DIR=$(dirname "$0")
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
pass=0
fail=0

DEFAULTS="dom0_console=console=tty0 console=ttyS0,115200
dom0_platform_tweaks=pcie_aspm=off
dom0_extra_args=
hv_dom0_mem_settings=dom0_mem=800M,max:800M
hv_dom0_cpu_settings=dom0_max_vcpus=1"

runcase() {
	# runcase FILE: run with DEFAULTS, output in $TMP/out
	set --
	while IFS= read -r d; do set -- "$@" -e "$d"; done <<EOF
$DEFAULTS
EOF
	"$RUN" "$@" "$f" >"$TMP/out" 2>"$TMP/err" || {
		echo "runner failed:"; cat "$TMP/err"; return 1; }
}

ok() { pass=$((pass + 1)); }
bad() {
	fail=$((fail + 1))
	echo "FAIL: $1"
	sed 's/^/    /' "$TMP/out"
}

check_acc() {
	if grep -qx 'set\* eve_overrides_rejected=0' "$TMP/out"; then ok; else bad "accept: $1"; fi
}
check_rej() {
	if grep -qx 'set\* eve_overrides_rejected=1' "$TMP/out" &&
		! grep -v '^set\* eve_overrides_' "$TMP/out" | grep -q '^set'; then
		ok
	else
		bad "reject: $1"
	fi
}

acc() { f=$TMP/case; printf '%s\n' "$1" >"$f"; runcase && check_acc "$1"; }
rej() { f=$TMP/case; printf '%s\n' "$1" >"$f"; runcase && check_rej "$1"; }
# shellcheck disable=SC2059
accx() { f=$TMP/case; printf "$1" >"$f"; runcase && check_acc "$1"; }
# shellcheck disable=SC2059
rejx() { f=$TMP/case; printf "$1" >"$f"; runcase && check_rej "$1"; }
val() {
	f=$TMP/case
	printf '%s\n' "$1" >"$f"
	runcase || return
	if grep -qxF "set* $2" "$TMP/out"; then ok; else bad "value: $1 -> $2"; fi
}
golden() {
	name=$1
	f=$DIR/cases/$2.cfg
	shift 2
	flags="$*"
	set --
	while IFS= read -r d; do set -- "$@" -e "$d"; done <<EOF
$DEFAULTS
EOF
	# shellcheck disable=SC2086
	"$RUN" "$@" $flags "$f" >"$TMP/out" 2>"$TMP/err" || { bad "golden $name: runner failed"; return; }
	if diff -u "$DIR/cases/$name.out" "$TMP/out" >"$TMP/diff"; then ok; else
		fail=$((fail + 1)); echo "FAIL: golden $name"; cat "$TMP/diff"
	fi
}
long() {
	# long CHAR N: N copies of CHAR
	printf "%${2}s" "" | tr ' ' "$1"
}

### Line forms
val 'set_global dom0_console "console=ttyS1,115200"' 'dom0_console=console=ttyS1,115200'
val 'set dom0_console="console=ttyS1"' 'dom0_console=console=ttyS1'
val 'set_global dom0_console console=ttyS1' 'dom0_console=console=ttyS1'
val 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox"' 'dom0_extra_args= eve_blackbox'
val 'set_global dom0_extra_args "${dom0_extra_args} eve_blackbox"' 'dom0_extra_args= eve_blackbox'
val 'set dom0_extra_args="$dom0_extra_args eve_blackbox"' 'dom0_extra_args= eve_blackbox'
val 'set_global dom0_console "$dom0_console console=ttyS1"' 'dom0_console=console=tty0 console=ttyS0,115200 console=ttyS1'
val 'set_global dom0_platform_tweaks "$dom0_platform_tweaks acpi_no_watchdog"' 'dom0_platform_tweaks=pcie_aspm=off acpi_no_watchdog'
val 'set_global dom0_platform_tweaks "acpi_no_watchdog"' 'dom0_platform_tweaks=acpi_no_watchdog'
val 'set_no_dirsync' 'dom0_extra_args= eve_no_dirsync'
val 'set_isolcpus' 'dom0_extra_args= isolcpus=inverse,0 nohz_full=inverse,0'
val 'set_global timeout 10' 'timeout=10'
val 'set timeout=0' 'timeout=0'
acc 'set_global timeout 999'
acc 'export dom0_extra_args'
acc 'export timeout'
acc 'set_global eve_flavor kvm'
acc 'set_global eve_flavor k'
acc ''
acc '# a comment'
acc '   # an indented comment'
acc 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox" # trailing comment'
acc 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox"   '
acc '   set_no_dirsync'
accx 'set_global dom0_console "console=ttyS1"\r\n'
accx 'set_no_dirsync'
accx 'set_global\tdom0_console\t"console=ttyS1"\n'
accx 'set_global hv_eve_mem_settings  "eve_mem=520M,max:650M"\n'

rej 'set_global dom0_extra_args "eve_blackbox"'
rej 'set_global hv_dom0_mem_settings "$hv_dom0_mem_settings dom0_mem=1G"'
rej 'set_global dom0_console ""'
rej 'set_global dom0_console'
rej 'set_global'
rej 'set dom0_console = "console=ttyS1"'
rej 'set dom0_console'
rej 'set ="console=ttyS1"'
rej 'export dom0_extra_args dom0_console'
rej 'export initrd'
rej 'set_global timeout 1000'
rej 'set_global timeout -1'
rej 'set_global timeout ""'
rej 'set_global timeout 5s'
rej 'set_global eve_flavor kvmkvmkvm'
rej 'set_global eve_flavor ""'
rej 'set_global eve_flavor k-v'
rej 'set_no_dirsync now'
rej 'set_isolcpus 0'
rej 'set_getty'
rej 'source /config/other.cfg'
rej 'configfile /config/other.cfg'
rej 'linux /boot/kernel'
rej 'initrd /boot/initrd'
rej 'insmod tpm'
rej 'menuentry x {'
rej 'if true; then set_no_dirsync; fi'
rej 'function set_no_dirsync {'
rej 'unset dom0_extra_args'
rej 'echo hello'
rej 'unknown_command'

### Variables that must not be settable
for v in initrd eve_root dom0_cmdline dom0_rootfs dom0_flavor_tweaks check_signatures \
	lockdown load_dom0_cmd load_hv_cmd load_initrd_cmd load_devicetree_cmd devicetree \
	dom0 hv hv_console hv_extra_args hv_platform_tweaks hv_watchdog_timer config_grub_cfg \
	efi_grub_cfg root prefix pager superusers verity_root eve_overrides_status \
	eve_overrides_rejected linuxkit_cmdline; do
	rej "set_global $v \"eve_blackbox\""
	rej "set $v=\"eve_blackbox\""
	rej "set_global $v \"\$$v eve_blackbox\""
done

### Injection: the whole line is rejected
rej 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox; set check_signatures=no"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox";set check_signatures=no'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox\"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox" x"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox"x'
rej "set_global dom0_extra_args '\$dom0_extra_args eve_blackbox'"
rej 'set_global dom0_extra_args "$dom0_extra_args $dom0_cmdline"'
rej 'set_global dom0_extra_args "$dom0_console eve_blackbox"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox $dom0_extra_args"'
rej 'set_global dom0_extra_args "${dom0_extra_args eve_blackbox"'
rej 'set_global dom0_extra_args "${dom0_extra_args}eve_blackbox"'
rej 'set_global dom0_extra_args "$dom0_extra_argsx eve_blackbox"'
rej 'set_global dom0_extra_args "$dom0_extra_args$dom0_extra_args"'
rej 'set_global dom0_extra_args "$(echo x)"'
rej 'set_global dom0_extra_args "$dom0_extra_args `echo x`"'
rej 'set_global dom0_extra_args "$dom0_extra_args getty"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox getty"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_install_server=getty.example.com"'
rej 'set_global dom0_extra_args "$dom0_extra_args video=getty"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_install_disk=getty"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox init=/bin/sh"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox rdinit=/bin/sh"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_root=/dev/sda2"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_disable_verify"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_soft_serial=x"'
rej 'set_global dom0_extra_args "$dom0_extra_args dm_verity.require_signatures=0"'
rej 'set_global dom0_extra_args "$dom0_extra_args module.sig_enforce=0"'
rej 'set_global dom0_extra_args "$dom0_extra_args lockdown=none"'
rej 'set_global dom0_extra_args "$dom0_extra_args systemd.unit=rescue.target"'
rej 'set_global dom0_extra_args "$dom0_extra_args single"'
rej 'set_global dom0_extra_args "$dom0_extra_args 1"'
rej 'set_global dom0_extra_args "$dom0_extra_args quiet"'
rej 'set_global dom0_extra_args "$dom0_extra_args panic=0"'
rej 'set_global dom0_extra_args "$dom0_extra_args apparmor=0"'
rej 'set_global dom0_extra_args "$dom0_extra_args efi=novamap"'
rej 'set_global dom0_extra_args "$dom0_extra_args root=/dev/sda2"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox --"'
rej 'set_global dom0_extra_args "$dom0_extra_args -- init=/bin/sh"'
rej 'set_global dom0_extra_args "$dom0_extra_args consolex=ttyS0"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_blackboxx"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_blackbo"'
rej 'set_global dom0_extra_args "$dom0_extra_args EVE_BLACKBOX"'
rej 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox=1"'
rej 'set_global dom0_extra_args "$dom0_extra_args =eve_blackbox"'
rejx 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox\001"\n'
rejx 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox\377"\n'
rejx 'set_global dom0_extra_args "$dom0_extra_args eve_blackbox\rinit=/bin/sh"\n'
rejx 'set_global dom0_extra_args "$dom0_extra_args\vinit=/bin/sh"\n'

### Cgroup keys (hv_* variables only)
acc 'set_global hv_dom0_mem_settings "dom0_mem=800M,max:800M"'
acc 'set_global hv_dom0_mem_settings "dom0_mem=999999K"'
acc 'set_global hv_dom0_mem_settings "dom0_mem=1G,max:2G"'
acc 'set_global hv_dom0_mem_settings "dom0_mem=20000M,max:24000M"'
acc 'set_global hv_eve_mem_settings "eve_mem=650M"'
acc 'set_global hv_ctrd_mem_settings "ctrd_mem=400M,max:1G"'
acc 'set_global hv_dom0_cpu_settings "dom0_max_vcpus=1 dom0_vcpus_pin"'
acc 'set_global hv_dom0_cpu_settings "dom0_max_vcpus=999"'
acc 'set_global hv_eve_cpu_settings "eve_max_vcpus=4"'
acc 'set_global hv_ctrd_cpu_settings "ctrd_max_vcpus=2"'
rej 'set_global hv_dom0_mem_settings "dom0_mem=1000000"'
rej 'set_global hv_dom0_mem_settings "dom0_mem=800T"'
rej 'set_global hv_dom0_mem_settings "dom0_mem=800m"'
rej 'set_global hv_dom0_mem_settings "dom0_mem=800M,max:"'
rej 'set_global hv_dom0_mem_settings "dom0_mem=800M,min:1G"'
rej 'set_global hv_dom0_mem_settings "dom0_mem=800M,max:1G,max:2G"'
rej 'set_global hv_dom0_mem_settings "dom0_mem=,max:1G"'
rej 'set_global hv_dom0_mem_settings "dom0_mem=800M dom0_mem=900M"'
rej 'set_global hv_dom0_mem_settings "dom0_mem"'
rej 'set_global hv_dom0_mem_settings "dom0_mem="'
rej 'set_global hv_dom0_mem_settings "eve_mem=1G"'
rej 'set_global hv_dom0_mem_settings "console=ttyS0"'
rej 'set_global hv_eve_mem_settings "dom0_mem=1G"'
rej 'set_global hv_dom0_cpu_settings "dom0_max_vcpus=1000"'
rej 'set_global hv_dom0_cpu_settings "dom0_max_vcpus="'
rej 'set_global hv_dom0_cpu_settings "dom0_vcpus_pin=1"'
rej 'set_global hv_dom0_cpu_settings "dom0_vcpus_pin dom0_vcpus_pin"'
rej 'set_global hv_dom0_cpu_settings "dom0_max_vcpus=1 dom0_max_vcpus=2"'
rej 'set_global hv_eve_cpu_settings "dom0_max_vcpus=1"'
rej 'set_global dom0_extra_args "$dom0_extra_args dom0_mem=1G"'
rej 'set_global dom0_platform_tweaks "eve_mem=1G"'
rej 'set_global dom0_console "dom0_max_vcpus=1"'

### console=
for c in ttyS0 ttyS99 ttyS0,115200 ttyS0,115200n8 ttyS0,9600 ttyS0,1234567 ttyAMA0 \
	ttyAMA10 ttymxc0 ttyTCU0 ttyMSM0 hvc0 tty0 tty1; do
	acc "set_global dom0_console \"console=$c\""
	acc "set_global dom0_extra_args \"\$dom0_extra_args console=$c\""
done
for c in ttyS100 ttyS ttyS0,115 ttyS0,12345678 ttyS0,115200n7 ttyS0,115200n8x \
	ttyS0,115200,n8 ttyS0, ttyUSB0 ttyUTC0 /dev/ttyS0 lp0 netcon0 tty ttyS-1 ttyS0x ''; do
	rej "set_global dom0_console \"console=$c\""
done
rej 'set_global dom0_console "console"'
rej 'set_global dom0_console "eve_blackbox"'
rej 'set_global dom0_console "earlycon=ttyS0"'
rej 'set_global dom0_console "console=ttyS0 earlycon=ttyS0"'

### Other keys, appended to dom0_extra_args
for k in eve_no_dirsync eve_blackbox eve_pause_before_install eve_pause_after_install \
	eve_reboot_after_install eve_install_skip_config eve_install_skip_persist \
	eve_install_skip_rootfs eve_install_skip_zfs_checks eve_nuke_all_disks \
	acpi_no_watchdog rootwait eve_install_zfs_with_raid_level; do
	acc "set_global dom0_extra_args \"\$dom0_extra_args $k\""
	acc "set_global dom0_platform_tweaks \"\$dom0_platform_tweaks $k\""
	rej "set_global dom0_extra_args \"\$dom0_extra_args $k=1\""
done
for t in eve_install_disk=sda eve_install_disk=/dev/nvme0n1 eve_install_disk= \
	eve_install_disk=sda,sdb eve_persist_disk=sdb,sdc,sdd eve_nuke_disks=/dev/sda \
	eve_install_disk=mmcblk0 \
	"eve_install_disk=$(long a 128)" \
	eve_install_zfs_with_raid_level=none eve_install_zfs_with_raid_level=raid1 \
	eve_install_zfs_with_raid_level=raid5 eve_install_zfs_with_raid_level=raid6 \
	eve_install_k3s_etcd_sizeGB=10 eve_install_k3s_etcd_sizeGB=9999 \
	eve_install_server=zedcloud.example.com eve_install_server=zedcloud.example.com:443 \
	eve_install_server=https://user@zedcloud.example.com/path \
	"eve_install_server=$(long a 128)" \
	isolcpus=inverse,0 isolcpus=2-7 nohz_full=1-3 isolcpus=nohz,domain,2-7 \
	"isolcpus=$(long 1 64)" \
	video=efifb:off video=1024x768 video=HDMI-A-1:1920x1080@60 \
	pci=noaer pci=realloc=off pci=realloc=on pci=noaer,nomsi pci=pcie_bus_perf \
	pci=pcie_bus_safe,noaer \
	pcie_aspm=off pcie_aspm=force pcie_port_pm=off pcie_port_pm=on pcie_port_pm=force \
	rootdelay=10 rootdelay=999 log_buf_len=4M log_buf_len=16384 log_buf_len=1G \
	modprobe.blacklist=wdat_wdt modprobe.blacklist=intel_pch_thermal \
	modprobe.blacklist=intel_pch_thermal,wdat_wdt \
	kempld_core.force_device_id=MVV1 kempld_core.force_device_id=ABCD1234 \
	vfio_pci.disable_idle_d3=0 vfio_pci.disable_idle_d3=1 \
	nvethernet.macsec_enable=0 nvethernet.macsec_enable=1; do
	acc "set_global dom0_extra_args \"\$dom0_extra_args $t\""
done
for t in eve_install_disk=sda.1 eve_install_disk=sda:1 eve_install_disk=sda=1 \
	"eve_install_disk=$(long a 129)" eve_persist_disk=sd~a \
	eve_install_zfs_with_raid_level=raid0 eve_install_zfs_with_raid_level=RAID1 \
	eve_install_zfs_with_raid_level= \
	eve_install_k3s_etcd_sizeGB=10000 eve_install_k3s_etcd_sizeGB= eve_install_k3s_etcd_sizeGB=1G \
	eve_install_k3s_etcd_sizeGB \
	eve_install_server=a=b eve_install_server=a?b eve_install_server=a%20b \
	eve_install_server= "eve_install_server=$(long a 129)" \
	isolcpus=1.2 isolcpus= "isolcpus=$(long 1 65)" isolcpus=1:2 \
	video= video=a=b video=a+b \
	pci=noaer, pci=,noaer pci=nommconf pci= pci=noaer,,nomsi pci=NOAER \
	pcie_aspm=on pcie_aspm= pcie_port_pm=auto \
	rootdelay=1000 rootdelay= rootdelay=-1 log_buf_len=1T log_buf_len=1000000M \
	modprobe.blacklist=nouveau modprobe.blacklist=wdat_wdt, modprobe.blacklist= \
	modprobe.blacklist=wdat_wdt,nouveau \
	kempld_core.force_device_id=abcd kempld_core.force_device_id=ABCDEFGH1 \
	kempld_core.force_device_id= \
	vfio_pci.disable_idle_d3=2 vfio_pci.disable_idle_d3=01 nvethernet.macsec_enable=; do
	rej "set_global dom0_extra_args \"\$dom0_extra_args $t\""
done

### Field corpus: every override found in the field must be accepted
while IFS= read -r l; do
	[ -n "$l" ] && acc "$l"
done <"$DIR/cases/field.cfg"
### Field overrides the signed image deliberately drops
rej 'set_getty'
rej 'set_global initrd "(hd0,gpt1)/initrd.img"'

### Limits
acc "set_no_dirsync #$(long x 1008)"
rej "set_no_dirsync #$(long x 1009)"
golden value-limit value-limit -r
golden over16 over16 -r
golden escape escape -r
golden nul nul -r

### TPM handling
golden golden golden -r
golden no-tpm golden -n -r
golden measure-fails golden -F -r
golden no-tpm-optional golden -n
golden pcr14 golden -r -p 14
f=$TMP/big
for _ in $(seq 64); do long '#' 1023; echo; done >"$f"
"$RUN" "$f" >"$TMP/out" 2>&1
if grep -qx 'set\* eve_overrides_rejected=0' "$TMP/out"; then ok; else bad "64 KiB file accepted"; fi
echo >>"$f"
"$RUN" "$f" >"$TMP/out" 2>&1
big='eve_overrides: rejected file /config/grub.cfg: 65537 bytes, limit 65536'
if grep -qx 'ret 2' "$TMP/out" && ! grep -q '^set' "$TMP/out" &&
	[ "$(grep '^event' "$TMP/out")" = "event 8 ${#big} $big | $big" ]; then
	ok
else
	bad "file over 64 KiB refused and recorded"
fi
"$RUN" -n "$f" >"$TMP/out" 2>&1
if grep -qx 'ret 2' "$TMP/out" && ! grep -q '^set\|^event' "$TMP/out"; then ok; else bad "file over 64 KiB, no TPM"; fi

echo "eve_overrides: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
