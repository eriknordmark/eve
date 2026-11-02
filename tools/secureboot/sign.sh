#!/bin/sh
# Copyright (c) 2026 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0
#
# sign.sh <keys dir> <out dir> <rootfs.img> [<live.raw | installer.raw>...]
#
# Builds the signer image from this tree and produces the Secure Boot image
# from an upstream rootfs.img, and signed live and installer disks from disks
# built with it. See sign-image.sh for the keys directory layout and the
# outputs.

set -e

[ $# -ge 3 ] || { echo "usage: $0 <keys dir> <out dir> <rootfs.img> [<live.raw | installer.raw>...]" >&2; exit 2; }
KEYS=$(realpath "$1")
OUT=$(realpath "$2")
ROOTFS=$(realpath "$3")
shift 3
# the disks become docker -v arguments in place of their paths
N=0
for d in "$@"; do
   N=$((N + 1))
   set -- "$@" -v "$(realpath "$d"):/in/disk$N.raw:ro"
done
shift $N
TOP=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
TAG=$(git -C "$TOP" log -1 --format=%h -- pkg/grub pkg/mkrootfs-squash/verity-append tools/secureboot)

# The default builder sees the local grub-build image the signer starts FROM
export BUILDX_BUILDER=default
docker build -q --target grub-build -t "eve-secureboot-grub:$TAG" "$TOP/pkg/grub" >/dev/null
docker build -q --build-arg GRUB_BUILD="eve-secureboot-grub:$TAG" \
   --build-context verity="$TOP/pkg/mkrootfs-squash" \
   -t "eve-secureboot-signer:$TAG" "$TOP/tools/secureboot" >/dev/null

mkdir -p "$OUT"
docker run --rm -v "$KEYS:/keys:ro" -v "$OUT:/out" \
   -v "$ROOTFS:/in/rootfs.img:ro" "$@" \
   "eve-secureboot-signer:$TAG"
docker run --rm -v "$OUT:/out" --entrypoint chown "eve-secureboot-signer:$TAG" -R "$(id -u):$(id -g)" /out
