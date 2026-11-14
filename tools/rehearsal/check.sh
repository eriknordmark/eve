#!/bin/bash
# FORK ONLY: check.sh <label> signed|unsigned slot|archtag|index [<arch>]:
# print "REHEARSAL CHECK <label>: PASS|FAIL" for whether the rehearsal
# package's platform manifest for <arch> in its index (slot), its
# <tag>-<arch> (archtag) or its index verifies as expected under the fork's
# policy, and fail on FAIL.
set -uo pipefail
label="$1" want="$2" what="$3" arch="${4:-}"
ref=$(make -s rehearsal-show-tag | tail -1)
name="${ref%:*}"
tag="${ref##*:}"
case "$what" in
slot)
    d=$(docker buildx imagetools inspect --raw "$ref" | jq -r --arg a "$arch" \
        '[.manifests[] | select(.platform.os == "linux" and .platform.architecture == $a) | .digest] | first // empty')
    ;;
archtag) d=$(oras manifest fetch --descriptor "$name:$tag-$arch" | jq -r '.digest // empty') ;;
index) d=$(oras manifest fetch --descriptor "$ref" | jq -r '.digest // empty') ;;
*) echo "unknown check $what" >&2; exit 2 ;;
esac
got=unsigned
if [ -n "$d" ] && cosign verify --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity-regexp "$EVE_COSIGN_IDENTITY_REGEXP" \
    --certificate-github-workflow-repository "$EVE_COSIGN_REPOSITORY" "$name@$d" > /dev/null 2>&1; then
    got=signed
fi
if [ "$got" = "$want" ]; then
    echo "REHEARSAL CHECK $label: PASS ($what $arch ${d:-none} $got)"
else
    echo "REHEARSAL CHECK $label: FAIL ($what $arch ${d:-none} $got, expected $want)"
    exit 1
fi
