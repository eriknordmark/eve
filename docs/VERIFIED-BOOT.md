# Verified boot (UEFI Secure Boot)

> **Draft, prototype.** amd64 only. Needs a kernel with the Secure Boot
> options of [lf-edge/eve-kernel#299](https://github.com/lf-edge/eve-kernel/pull/299),
> which is not merged: the default amd64 kernel has no `DM_VERITY`.

EVE's measured boot detects a changed boot chain after the fact, through the
TPM seal and attestation (see [SECURITY-ARCHITECTURE.md](SECURITY-ARCHITECTURE.md)). Verified boot adds
verification before use: under UEFI Secure Boot, every stage checks the next one
against keys the signer controls, and the root filesystem is read through
dm-verity with a signed root hash. Measured boot and attestation stay as they
are.

## Boot chain

```text
firmware --db--> stage-1 GRUB (ESP)
                  gptprio picks IMGA or IMGB
                  --GPG + db--> stage-2 GRUB (EFI/BOOT/BOOTX64.EFI in the rootfs)
                                 rootfs.cfg from its memdisk, /config/grub.cfg overrides
                                 --GPG + db--> kernel (lockdown, signed modules and kexec)
                                 --GPG-------> ucode.img + verity initrd (failure -> reboot)
initrd: dm-verity on IMGx, root hash signature checked against db
        --> root=/dev/mapper/eve-root
```

- **db key** (X.509, in the firmware's db): signs stage-1, stage-2 and the
  kernel (Authenticode), and the dm-verity root hash (PKCS#7). Linux loads db
  certificates into its platform keyring, which dm-verity checks the root hash
  signature against.
- **GPG key** (RSA, embedded in both GRUB stages): signs every file GRUB opens.
- Both GRUB stages set `check_signatures=enforce` and lock the menu editor and
  command line. The boot entry is unrestricted.

## Two images from one build

The upstream EVE image is built without secret keys. A signer derives the
signed image from it with `tools/secureboot/sign.sh`.

| | Upstream image | Signed image |
|---|---|---|
| GRUB | unsigned | stage-1 on the ESP, stage-2 in the rootfs, both db-signed with the GPG key embedded |
| Kernel | as built | plus db signature and GPG `.sig` |
| initrd, `ucode.img` | as built | plus GPG `.sig` |
| dm-verity root hash | unsigned | signed by the db key; the kernel gets `dm_verity.require_signatures=1` |
| `/config/grub.cfg` | sourced | see [GRUB-OVERRIDES.md](GRUB-OVERRIDES.md) |

Both images boot through the verity initrd (`root=/dev/mapper/eve-root
eve_root=PARTUUID=...`). Jetson images leave the initrd out.

On a device with firmware Secure Boot off, the signed image boots as the upstream
one does: stage-2 turns off signature enforcement, sources `/config/grub.cfg`,
and does not require a root hash signature.

The cosign signature on an upstream release (see [SIGNING.md](SIGNING.md))
covers the published `lfedge/eve` image only. Verify the release with cosign
before signing it. After that, the db key, not cosign, vouches for the signed
image.

## Building

1. Build EVE with the Secure Boot kernel, for example:

   ```shell
   make KERNEL_TAG=docker.io/lfedge/eve-kernel:<tag> HV=kvm live-raw
   ```

   This writes `installer/rootfs.img` and `live.raw` under `dist/amd64/current/`.

   The rootfs then carries `/boot/initrd.img` from `pkg/verity-initrd`, and
   `rootfs.img` ends with the dm-verity tree and descriptor
   (`pkg/mkrootfs-squash/verity-append`).

2. Prepare a keys directory:

   | Path | Content |
   |---|---|
   | `db.key`, `db.crt` | db signing key and certificate, PEM |
   | `gnupg/` | a GnuPG home holding the GPG signing key |
   | `GPG_KEY` | optional, the key id to sign with |

   Signatures are SHA-512. Use an RSA GPG key; GRUB does not accept others.

3. Sign:

   ```shell
   tools/secureboot/sign.sh <keys dir> <out dir> dist/amd64/current/installer/rootfs.img dist/amd64/current/live.raw
   ```

   `<out dir>` receives the signed `rootfs.img`, `stage1.efi` and, when
   `live.raw` is given, a signed `live.raw` with stage-1 alone on the ESP and
   the signed rootfs on IMGA. `sign.sh` builds GRUB from `pkg/grub` of the same
   tree, so run it from the tree the image was built from.

4. Enroll the db certificate in the device's firmware and turn Secure Boot on.
   A db holding only Microsoft certificates does not boot either image (no shim).

## Trying it in QEMU

Ubuntu's `ovmf` package ships Secure Boot firmware whose db holds a test key,
`OVMF_CODE_4M.snakeoil.fd` and `OVMF_VARS_4M.snakeoil.fd`. Signing with that key
(`/usr/share/ovmf/PkKek-1-snakeoil.key` and `.pem` as `db.key` and `db.crt`)
gives an image those firmware files boot with Secure Boot enforcing. Attach a
TPM (swtpm): EVE assumes one, and without it `/config/grub.cfg` is ignored under
Secure Boot.

The evetest `TestSecureBootOnboarded` (`evetest/tests/security`) runs a signed
image on an onboarded device and checks that Secure Boot and lockdown are on,
that the root is on dm-verity with signatures required, and that an unsigned
`/config/grub.cfg` is applied only as far as the allow-list permits. It takes the
image as a local live build, in a directory laid out as `make live` writes it:

```text
<dist>/amd64/<version>/live.qcow2              qemu-img convert -O qcow2 live.raw
<dist>/amd64/<version>/installer/rootfs.img    the signed rootfs.img
<dist>/amd64/<version>/installer/eve_version
<dist>/amd64/<version>/installer/config.img
<dist>/amd64/<version>/installer/firmware/OVMF_CODE.fd, OVMF_VARS.fd, OVMF.fd   snakeoil firmware
```

```shell
EVETEST_EVE_LIVE_IMAGE=true EVETEST_EVE_DIST_DIR=<dist> EVETEST_EVE_VERSION=<version> \
    make evetest NAME=TestSecureBootOnboarded
```

## PCRs and the vault

Moving a device from the upstream image to the signed one changes PCR 4, 7, 8,
9 and 13 once. PCR 13 (`measurefs` over the rootfs) differs per signer, because
the signed objects sit inside the squashfs. The operator accepts the new values,
and the controller then releases the backup vault key, as for any base OS update.

## Not covered yet

- Installer images and arm64.
- Signed images without a TPM keep no `/config/grub.cfg` overrides.
- Upgrade between signed images (IMGB) has not been tested.
- Bounded retries before falling back to the other partition, and reporting the
  reason a verification failed to the controller.
- Revocation of a signed release (seal policy, `dbx`) and a version for stage-1.
- Real hardware: QEMU covers the boot-chain logic only.
