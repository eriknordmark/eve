FROM eriknordmark/eve-alpine:4d19012d2cb16c8c1c7e5ad178f9abfc4f725050 AS tools

COPY collected_sources.tar.gz /var/collected_sources.tar.gz
RUN mkdir -p /var/sources && tar -C /var/sources -xzf /var/collected_sources.tar.gz

FROM scratch
COPY --from=tools /var/sources /
