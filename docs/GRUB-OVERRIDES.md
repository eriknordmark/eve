# GRUB overrides on the CONFIG partition

> **Draft.** The allow-list below is what the prototype GRUB command
> `eve_overrides` accepts. It has not been reviewed key by key yet; see
> [Open review items](#open-review-items).

`/config/grub.cfg` on the CONFIG partition adjusts the boot that
[`rootfs.cfg`](../pkg/grub/rootfs.cfg) sets up: cgroup limits, consoles, installer
options and extra kernel parameters. What each option does is described in
[BOOTING.md](BOOTING.md#grub-options-on-the-config-partition). This document lists
the forms the file may take and, for a [verified boot](VERIFIED-BOOT.md) image,
which of them are applied.

## How the file is read

| Image | `/config/grub.cfg` |
|---|---|
| Upstream (unsigned) | Sourced as a GRUB script; any command works |
| Signed, firmware Secure Boot off | As upstream |
| Signed, Secure Boot on, `/config/grub.cfg.sig` present | Sourced, provided the signature verifies against a GPG key embedded in stage-2 |
| Signed, Secure Boot on, no `.sig`, TPM present | Read as data by `eve_overrides --require-tpm`; only allow-listed lines are applied |
| Signed, Secure Boot on, no `.sig`, no TPM | Ignored |

The file is measured in every case: GRUB measures it into PCR 9 (and, when
sourced, its commands into PCR 8), and `measure-config` extends PCR 14 with it
after boot. A changed override therefore changes the PCRs the vault key is sealed
to and is visible in attestation.

Under Secure Boot the signed image also skips the other unsigned inputs the
upstream image reads: `EFI/BOOT/grub_include.cfg`, `EFI/BOOT/grub-hv.cfg`,
`/config/eve.dtb` and the `eve-kernel-extra-cmdline` EFI variable.

## Line forms

`eve_overrides` never executes the file. It parses one statement per line:

| Line | Effect |
|---|---|
| `set_global VAR "VALUE"`, `set VAR="VALUE"` | Replace `VAR` with `VALUE` |
| `set_global VAR "$VAR VALUE"` (or `${VAR}`) | Append `VALUE` to `VAR` |
| `export VAR` | Accepted, no effect (`VAR` must be a variable below, or `timeout`) |
| `set_global timeout N`, `set timeout=N` | GRUB menu timeout, 1-3 digits |
| `set_no_dirsync` | Appends `eve_no_dirsync` to `dom0_extra_args` |
| `set_isolcpus` | Appends `isolcpus=inverse,0 nohz_full=inverse,0` to `dom0_extra_args` |
| `set_global eve_flavor NAME` | Accepted, no effect: the flavor is chosen before the file is read |
| Empty line, `# comment` | Ignored; `#` starts a comment at the start of a line or after a blank |

`VALUE` is a space-separated list of tokens, `KEY` or `KEY=VALUE`. A line is
applied whole or rejected whole: one disallowed token rejects the line. A line is
also rejected if it contains a quote other than the enclosing pair, `\`, `;`, a
`$` other than the leading self-reference, or a token containing `getty`.

Limits: the file at most 64 KiB, a line at most 1024 bytes, a variable's
resulting value at most 1024 bytes.

## Variables

| Variable | Replace | Append | Keys |
|---|---|---|---|
| `hv_dom0_mem_settings` | yes | no | `dom0_mem` |
| `hv_eve_mem_settings` | yes | no | `eve_mem` |
| `hv_ctrd_mem_settings` | yes | no | `ctrd_mem` |
| `hv_dom0_cpu_settings` | yes | no | `dom0_max_vcpus`, `dom0_vcpus_pin` |
| `hv_eve_cpu_settings` | yes | no | `eve_max_vcpus` |
| `hv_ctrd_cpu_settings` | yes | no | `ctrd_max_vcpus` |
| `dom0_console` | yes | yes | `console` |
| `dom0_platform_tweaks` | yes | yes | any kernel/EVE key below |
| `dom0_extra_args` | no | yes | any kernel/EVE key below |

In the `hv_*` variables each key may appear at most once. Replacing
`dom0_console` or `dom0_platform_tweaks` drops the board defaults `rootfs.cfg`
put there; append to keep them.

## Keys

Cgroup keys (only in the `hv_*` variables above):

| Key | Value |
|---|---|
| `dom0_mem`, `eve_mem`, `ctrd_mem` | `SIZE[,max:SIZE]`, `SIZE` = 1-6 digits with optional `K`, `M` or `G` |
| `dom0_max_vcpus`, `eve_max_vcpus`, `ctrd_max_vcpus` | 1-3 digits |
| `dom0_vcpus_pin` | bare |

Kernel and EVE keys (`dom0_platform_tweaks`, `dom0_extra_args`; `console` also
in `dom0_console`):

| Key | Value |
|---|---|
| `console` | `DEV[,BAUD[n8]]`: `DEV` is `ttyS`, `ttyAMA`, `ttymxc`, `ttyTCU`, `ttyMSM`, `hvc` or `tty` plus 1-2 digits; `BAUD` 4-7 digits |
| `eve_no_dirsync`, `eve_blackbox` | bare |
| `eve_pause_before_install`, `eve_pause_after_install`, `eve_reboot_after_install` | bare |
| `eve_install_skip_config`, `eve_install_skip_persist`, `eve_install_skip_rootfs`, `eve_install_skip_zfs_checks` | bare |
| `eve_nuke_all_disks` | bare |
| `eve_install_disk`, `eve_persist_disk`, `eve_nuke_disks` | empty, or 1-128 characters from letters, digits and `, / _ -` |
| `eve_install_zfs_with_raid_level` | bare, or `none`, `raid1`, `raid5`, `raid6` |
| `eve_install_k3s_etcd_sizeGB` | 1-4 digits |
| `eve_install_server` | 1-128 characters from letters, digits and `. _ : / @ , -` |
| `isolcpus`, `nohz_full` | 1-64 characters from letters, digits and `, _ -` |
| `video` | 1-128 characters from letters, digits and `. _ : / @ , -` |
| `pci` | comma list of `realloc=off`, `realloc=on`, `noaer`, `nomsi`, `pcie_bus_perf`, `pcie_bus_safe` |
| `pcie_aspm` | `off`, `force` |
| `pcie_port_pm` | `off`, `on`, `force` |
| `acpi_no_watchdog`, `rootwait` | bare |
| `rootdelay` | 1-3 digits |
| `log_buf_len` | `SIZE` as above |
| `modprobe.blacklist` | comma list of `intel_pch_thermal`, `wdat_wdt` |
| `kempld_core.force_device_id` | 1-8 upper-case letters and digits |
| `vfio_pci.disable_idle_d3`, `nvethernet.macsec_enable` | `0`, `1` |

Not accepted, among others: `eve_root`, `eve_soft_serial`, `eve_disable_verify`,
`init=`, `getty` and `set_getty`, a custom `initrd`, `devicetree`, and any GRUB
command other than the forms above. On a signed image these need a
`/config/grub.cfg` signed by the image's GPG key.

## Rejected lines

`eve_overrides` prints one summary line, `eve_overrides: <file>: N lines
accepted, M rejected: <line numbers>`, and sets `eve_overrides_status` (`applied`
or `no-tpm`) and `eve_overrides_rejected` (the count). The first 16 rejected
lines are also measured into PCR 8 with the event description
`eve_overrides: rejected line N: TEXT` (`TEXT` escaped and truncated to 160
characters), so they reach the controller in the TPM event log sent with
attestation.

## Open review items

- Confirm, key by key, that every allowed value keeps the verified posture.
  First: `console=`, `nvethernet.macsec_enable`, the watchdog keys
  (`acpi_no_watchdog`, `modprobe.blacklist`, `kempld_core.force_device_id`), and
  replacing (rather than appending to) `dom0_console` and `dom0_platform_tweaks`.
- `dom0_vcpus_pin` and `console=hvcN` are Xen-only; `rootfs.cfg` no longer sets
  them. They stay accepted so that existing files are not rejected whole.
- `eve_install_server` overrides `/config/server` in the installer, which the
  signed image does not cover yet.
- Pillar does not yet log a warning per rejected line found in the event log.
