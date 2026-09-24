#!/bin/bash
# Copyright (c) 2026, Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0
#
# Keyless cosign signing and verification of EVE images; docs/SIGNING.md
# describes the policy.
#
# Usage:
#   eve-cosign.sh classify <outdir>
#       Split the package platform manifests in the linuxkit cache into
#       <outdir>/pulled (the registry already serves the digest) and
#       <outdir>/built (built by this job).
#   eve-cosign.sh verify <file>
#       Verify every entry of <file>.
#   eve-cosign.sh verify-bases <file> <arch>...
#       Verify the `FROM lfedge/eve-*` base images of the packages in <file>
#       for the given architectures.
#   eve-cosign.sh sign-pkgs <file>
#       Sign every entry of <file> that does not already verify.
#   eve-cosign.sh sign-bases <file> <arch>...
#       Sign the base images verify-bases would check, unless already verified.
#   eve-cosign.sh sign <ref>...
#       Sign the digest each ref resolves to, unless it already verifies.
#   eve-cosign.sh verify-ref <ref>
#       Verify the digest <ref> resolves to and print <repo>@<digest>.
#   eve-cosign.sh attest-sbom <ref> <spdx.json>
#       Attach an SPDX attestation to the digest <ref> resolves to.
#
# <file> holds one "<name>@sha256:<hex>" per line, <name> being the package
# repository name, e.g. eve-pillar.
#
# Environment:
#   EVE_COSIGN_IDENTITY_REGEXP  required: regexp the certificate SAN must match
#   EVE_COSIGN_REPOSITORY       required: owner/repo the signing workflow ran in
#   EVE_COSIGN_ENFORCE          "true" makes verify/verify-bases failures fatal;
#                               otherwise they are reported as warnings
#   EVE_PKG_PREFIX              where packages are pulled from
#                               (default docker.io/lfedge)
#   EVE_SIG_PREFIX              where package signatures live
#                               (default EVE_PKG_PREFIX)
#   EVE_COSIGN_EXCLUDE          regexp of package names not signed by EVE's
#                               workflows (default ^eve-kernel$)
#   LINUXKIT_CACHE              linuxkit cache (default ~/.linuxkit/cache)

set -euo pipefail

EVE="$(cd "$(dirname "$0")/.." && pwd)"
OIDC_ISSUER=https://token.actions.githubusercontent.com
PKG_PREFIX="${EVE_PKG_PREFIX:-docker.io/lfedge}"
SIG_PREFIX="${EVE_SIG_PREFIX:-$PKG_PREFIX}"
EXCLUDE="${EVE_COSIGN_EXCLUDE:-^eve-kernel$}"
CACHE="${LINUXKIT_CACHE:-$HOME/.linuxkit/cache}"

die() {
    echo "eve-cosign: $*" >&2
    exit 1
}

require_policy() {
    [ -n "${EVE_COSIGN_IDENTITY_REGEXP:-}" ] || die "EVE_COSIGN_IDENTITY_REGEXP is not set"
    [ -n "${EVE_COSIGN_REPOSITORY:-}" ] || die "EVE_COSIGN_REPOSITORY is not set"
}

verified() {
    cosign verify \
        --certificate-oidc-issuer "$OIDC_ISSUER" \
        --certificate-identity-regexp "$EVE_COSIGN_IDENTITY_REGEXP" \
        --certificate-github-workflow-repository "$EVE_COSIGN_REPOSITORY" \
        "$1" > /dev/null 2>&1
}

sign_digest() {
    if verified "$1"; then
        echo "already signed: $1"
        return
    fi
    cosign sign --yes "$1"
    verified "$1" || die "new signature on $1 does not verify under the policy"
    echo "signed: $1"
}

# resolve <ref>: print <repo>@<digest> of the manifest or index <ref> names.
resolve() {
    local digest
    digest=$(docker buildx imagetools inspect --format '{{json .Manifest}}' "$1" | jq -r .digest)
    case "$digest" in
    sha256:*) ;;
    *) die "cannot resolve $1" ;;
    esac
    case "$1" in
    *@*) echo "${1%@*}@$digest" ;;
    *) echo "${1%:*}@$digest" ;;
    esac
}

# The platform manifests of every cached $PKG_PREFIX/eve-* entry whose
# manifest blob is present, attestation manifests and $EXCLUDE left out.
cached_pkg_manifests() {
    local blobs="$CACHE/blobs/sha256"
    jq -r --arg p "$PKG_PREFIX/eve-" '.manifests[]
        | select((.annotations["org.opencontainers.image.ref.name"] // "") | startswith($p))
        | [(.annotations["org.opencontainers.image.ref.name"] | sub(":[^:/]*$"; "") | sub(".*/"; "")),
           .mediaType, .digest] | @tsv' "$CACHE/index.json" |
    while IFS=$'\t' read -r name type digest; do
        [[ "$name" =~ $EXCLUDE ]] && continue
        case "$type" in
        *index*|*manifest.list*)
            jq -r '.manifests[]
                | select(.annotations["vnd.docker.reference.type"] != "attestation-manifest")
                | .digest' "$blobs/${digest#sha256:}" |
            while read -r m; do
                if [ -f "$blobs/${m#sha256:}" ]; then echo "$name@$m"; fi
            done
            ;;
        *)
            echo "$name@$digest"
            ;;
        esac
    done | sort -u
}

classify() {
    local out="$1" entry
    mkdir -p "$out"
    : > "$out/pulled"
    : > "$out/built"
    for entry in $(cached_pkg_manifests); do
        if docker buildx imagetools inspect --raw "$PKG_PREFIX/$entry" > /dev/null 2>&1; then
            echo "$entry" >> "$out/pulled"
        else
            echo "$entry" >> "$out/built"
        fi
    done
    echo "pulled: $(wc -l < "$out/pulled")  built here: $(wc -l < "$out/built")"
}

# report <failures>: apply EVE_COSIGN_ENFORCE to a count of failed checks.
report() {
    [ "$1" -eq 0 ] && return 0
    if [ "${EVE_COSIGN_ENFORCE:-}" = true ]; then
        echo "::error::$1 image(s) without a valid signature"
        return 1
    fi
    echo "::warning::$1 image(s) without a valid signature; EVE_COSIGN_ENFORCE is not true"
}

verify_entries() {
    local failed=0 ref
    while read -r ref; do
        [ -n "$ref" ] || continue
        if verified "$ref"; then
            echo "verified: $ref"
        else
            echo "::warning::no valid signature for $ref"
            failed=$((failed + 1))
        fi
    done
    report "$failed"
}

verify_pkgs() {
    sed "s|^|$SIG_PREFIX/|" "$1" | verify_entries
}

# The platform manifests, for the given architectures, of the `FROM
# lfedge/eve-*:<tag>` images that the packages listed in $1 are built from.
# A base the registry does not serve is printed as its bare tag, which then
# fails verification; bases listed in $1 themselves are skipped, since this
# job built them and signs them after pushing.
base_manifests() {
    local list="$1" name ref arch index
    shift
    sed 's/@.*//' "$list" | sort -u | while read -r name; do
        cat "$EVE/pkg/${name#eve-}"/Dockerfile* 2>/dev/null || true
    done |
    sed -nE 's|^FROM[[:space:]]+(docker\.io/)?lfedge/(eve-[a-z0-9-]+:[0-9a-f]+).*|\2|p' | sort -u |
    while read -r ref; do
        [[ "${ref%:*}" =~ $EXCLUDE ]] && continue
        grep -q "^${ref%:*}@sha256:" "$list" && continue
        if ! index=$(docker buildx imagetools inspect --raw "$PKG_PREFIX/$ref" 2> /dev/null); then
            echo "$SIG_PREFIX/$ref"
            continue
        fi
        for arch in "$@"; do
            jq -r --arg a "$arch" '.manifests[]
                | select(.platform.os == "linux" and .platform.architecture == $a) | .digest' <<< "$index" |
                sed "s|^|$SIG_PREFIX/${ref%:*}@|"
        done
    done | sort -u
}

attest_sbom() {
    local ref
    ref=$(resolve "$1")
    cosign attest --yes --type spdxjson --predicate "$2" "$ref"
    cosign verify-attestation --type spdxjson \
        --certificate-oidc-issuer "$OIDC_ISSUER" \
        --certificate-identity-regexp "$EVE_COSIGN_IDENTITY_REGEXP" \
        --certificate-github-workflow-repository "$EVE_COSIGN_REPOSITORY" \
        "$ref" > /dev/null
    echo "attested SBOM: $ref"
}

cmd="${1:-}"
[ $# -gt 0 ] && shift
case "$cmd" in
classify)
    [ $# -eq 1 ] || die "usage: classify <outdir>"
    classify "$1"
    ;;
verify)
    [ $# -eq 1 ] || die "usage: verify <file>"
    require_policy
    verify_pkgs "$1"
    ;;
verify-bases)
    [ $# -ge 2 ] || die "usage: verify-bases <file> <arch>..."
    require_policy
    base_manifests "$@" | verify_entries
    ;;
sign-pkgs)
    [ $# -eq 1 ] || die "usage: sign-pkgs <file>"
    require_policy
    while read -r entry; do
        [ -n "$entry" ] && sign_digest "$SIG_PREFIX/$entry"
    done < "$1"
    ;;
sign-bases)
    [ $# -ge 2 ] || die "usage: sign-bases <file> <arch>..."
    require_policy
    base_manifests "$@" | while read -r ref; do
        case "$ref" in
        *@sha256:*) sign_digest "$ref" ;;
        *) die "registry does not serve base image $ref" ;;
        esac
    done
    ;;
sign)
    [ $# -ge 1 ] || die "usage: sign <ref>..."
    require_policy
    for ref in "$@"; do
        sign_digest "$(resolve "$ref")"
    done
    ;;
verify-ref)
    [ $# -eq 1 ] || die "usage: verify-ref <ref>"
    require_policy
    ref=$(resolve "$1")
    verified "$ref" || die "no valid signature for $ref"
    echo "$ref"
    ;;
attest-sbom)
    [ $# -eq 2 ] || die "usage: attest-sbom <ref> <spdx.json>"
    require_policy
    attest_sbom "$1" "$2"
    ;;
*)
    die "usage: $0 classify|verify|verify-bases|sign-pkgs|sign-bases|sign|verify-ref|attest-sbom ..."
    ;;
esac
