FROM lfedge/eve-alpine:753a7e1767239815f1e6b65124e56c962017226d AS build

ENV PKGS dosfstools libarchive-tools binutils mtools sfdisk sgdisk xfsprogs \
         e2fsprogs util-linux coreutils multipath-tools squashfs-tools
RUN eve-alpine-deploy.sh

COPY . /out/

FROM scratch
COPY --from=build /out/ /
ENTRYPOINT [ "/make-rootfs" ]
