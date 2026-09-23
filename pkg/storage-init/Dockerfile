# Copyright (c) 2023 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0

FROM lfedge/eve-alpine:080e2b4c683508649d72d2ae9b166153d819ca5d AS build
ENV PKGS="alpine-baselayout musl-utils bash glib squashfs-tools util-linux e2fsprogs e2fsprogs-extra keyutils dosfstools coreutils sgdisk smartmontools"
RUN eve-alpine-deploy.sh

# storage-resizer drives the EVE-kvm<->EVE-k boot-disk repartition; storage-init
# execs it for the offline shrink/grow and the post-mount restore/cleanup. The
# filesystem/partition tools it shells out to (resize2fs, e2fsck, fsck.fat,
# sgdisk, partx) are in the PKGS above.
FROM lfedge/eve-storage-resizer:9864ab75027a2520765f10d32164c6b37973e7c1 AS storage-resizer

FROM scratch
COPY --from=build /out/ /
COPY --from=storage-resizer /usr/bin/storage-resizer /usr/bin/storage-resizer
COPY storage-init.sh /
COPY storage-resize.sh /

WORKDIR /
ENTRYPOINT []
CMD ["/storage-init.sh"]
