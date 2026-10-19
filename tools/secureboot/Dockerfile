# Copyright (c) 2026 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0

# Image for sign-image.sh, built by sign.sh. GRUB_BUILD is the grub-build
# stage of pkg/grub, which provides grub-mkimage and the EFI modules of the
# release being signed; the verity context is pkg/mkrootfs-squash.
ARG GRUB_BUILD
FROM ${GRUB_BUILD} AS grub

FROM alpine:3.22
RUN apk add --no-cache bash coreutils cryptsetup gnupg libintl mtools openssl sbsigntool sgdisk squashfs-tools util-linux xz xz-libs
COPY --from=grub /grub/grub-mkimage /usr/bin/grub-mkimage
COPY --from=grub /grub-lib/grub /usr/lib/grub
COPY --from=verity verity-append /
COPY sign-image.sh stage1.cfg stage2.cfg /
ENTRYPOINT ["/sign-image.sh"]
