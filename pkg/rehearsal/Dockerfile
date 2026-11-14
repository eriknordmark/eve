# Copyright (c) 2026 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0
# FORK ONLY: the package the fork's publish.yml pushes and signs. Each build
# differs, as a rebuild of a real package does.
FROM alpine:3.22
RUN head -c 16 /dev/urandom | od -An -tx1 > /nonce
