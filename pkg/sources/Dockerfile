FROM lfedge/eve-alpine:32c9b42d3bee7681d4fd38619e2cc1f6f238909e AS tools

COPY collected_sources.tar.gz /var/collected_sources.tar.gz
RUN mkdir -p /var/sources && tar -C /var/sources -xzf /var/collected_sources.tar.gz

FROM scratch
COPY --from=tools /var/sources /
