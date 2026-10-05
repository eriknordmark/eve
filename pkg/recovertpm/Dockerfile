# Copyright (c) 2023 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0

# hadolint ignore=DL3029
FROM --platform=$BUILDPLATFORM eriknordmark/eve-alpine:4d19012d2cb16c8c1c7e5ad178f9abfc4f725050 AS build
ARG TARGETARCH

# build the tpm-recovery tool
WORKDIR /
COPY src/ recover-tpm/

WORKDIR /recover-tpm
SHELL ["/bin/ash", "-eo", "pipefail", "-c"]
# hadolint ignore=SC2046
RUN echo "Running go vet" && GOARCH=${TARGETARCH} go vet ./... && echo "Running go fmt" && \
    ERR=$(gofmt -e -l -s $(find . -name \*.go | grep -v /vendor/)) && \
    if [ -n "$ERR" ] ; then echo "go fmt Failed - ERR: $ERR"; exit 1 ; fi
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags "-s -w" -o recovertpm .

FROM scratch
WORKDIR /

COPY --from=build /recover-tpm/recovertpm /usr/bin/recovertpm
