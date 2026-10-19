# Copyright (c) 2026 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0

# The initrd that opens the EVE rootfs on IMGA/IMGB through dm-verity. The
# output is /boot/initrd.img, which the rootfs carries and rootfs.cfg loads.
FROM lfedge/eve-alpine:61283a3a978a7cdc9073a1ba678717fc3ea1fe97 AS build
ENV PKGS="cryptsetup blkid"
RUN eve-alpine-deploy.sh

COPY init /out/init
WORKDIR /out
RUN rm -rf etc/apk lib/apk var/cache usr/share sbin/apk && \
    mkdir -p dev proc sys run newroot && \
    find . | sort | cpio -o -H newc -R 0:0 --quiet | gzip -9n > /initrd.img

FROM scratch
COPY --from=build /initrd.img /boot/initrd.img
