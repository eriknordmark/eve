FROM eriknordmark/eve-alpine:4d19012d2cb16c8c1c7e5ad178f9abfc4f725050 AS build

ENV PKGS mtools dosfstools
RUN eve-alpine-deploy.sh

COPY make-config /out/
RUN mkdir -p /out/conf/raw

FROM scratch
COPY --from=build /out /

WORKDIR /
ENTRYPOINT [ "/make-config" ]
