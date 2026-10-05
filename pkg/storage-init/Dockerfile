# Copyright (c) 2023 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0

FROM eriknordmark/eve-alpine:4d19012d2cb16c8c1c7e5ad178f9abfc4f725050 AS build
ENV PKGS="alpine-baselayout musl-utils bash glib squashfs-tools util-linux e2fsprogs e2fsprogs-extra keyutils dosfstools coreutils sgdisk smartmontools"
RUN eve-alpine-deploy.sh

# storage-resizer drives the EVE-kvm<->EVE-k boot-disk repartition; storage-init
# execs it for the offline shrink/grow and the post-mount restore/cleanup. The
# filesystem/partition tools it shells out to (resize2fs, e2fsck, fsck.fat,
# sgdisk, partx) are in the PKGS above.
FROM eriknordmark/eve-storage-resizer:663f27965132f3dcd17e29cd41f01db9f0ca81e8 AS storage-resizer

FROM scratch
COPY --from=build /out/ /
COPY --from=storage-resizer /usr/bin/storage-resizer /usr/bin/storage-resizer
COPY storage-init.sh /
COPY storage-resize.sh /

WORKDIR /
ENTRYPOINT []
CMD ["/storage-init.sh"]
