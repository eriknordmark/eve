#!/bin/bash
# FORK ONLY: plant.sh <arch>: put an unsigned manifest under the rehearsal
# package's tag for <arch>, in the index and in <tag>-<arch>, as linuxkit does
# when a build that does not sign pushes the same tag. Writes the planted
# reference to $PLANT_OUT if set.
set -euo pipefail
arch="$1"
ref=$(make -s rehearsal-show-tag | tail -1)
name="${ref%:*}"
tag="${ref##*:}"
d=$(mktemp -d)
date +%s%N > "$d/f"
printf 'FROM scratch\nCOPY f /f\n' > "$d/Dockerfile"
docker buildx create --name plant > /dev/null
docker buildx build --builder plant --platform "linux/$arch" --provenance=false --sbom=false \
    --push -t "$name:$tag-$arch" "$d"
docker buildx rm plant
desc=$(oras manifest fetch --descriptor "$name:$tag-$arch")
oras manifest fetch "$ref" > "$d/index.json"
jq --argjson m "$desc" --arg a "$arch" '
    [.manifests[] | select(.platform.os == "linux" and .platform.architecture == $a) | .digest] as $old
    | .manifests |= ([.[] | select(.digest as $x | $old | index($x) | not)
            | select((.annotations["vnd.docker.reference.digest"] // "") as $r | $old | index($r) | not)]
        + [{mediaType: $m.mediaType, digest: $m.digest, size: $m.size, platform: {architecture: $a, os: "linux"}}])' \
    "$d/index.json" > "$d/new.json"
oras manifest push "$ref" "$d/new.json" > /dev/null
planted="$name@$(jq -r .digest <<< "$desc")"
echo "planted $planted for $arch in $ref"
if [ -n "${PLANT_OUT:-}" ]; then echo "$planted" > "$PLANT_OUT"; fi
