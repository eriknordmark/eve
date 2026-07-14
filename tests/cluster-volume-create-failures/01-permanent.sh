#!/usr/bin/env bash
# Case P — a PERMANENT create failure must NOT be retried (no loop).
#
# A ResourceQuota forbidding PVCs makes CreatePVC return 403 Forbidden. asTransient
# classifies IsForbidden as permanent, so ClusterStorageTransientErr stays false and
# retryFailedClusterVolumeCreate skips the volume. This is the branch the black-box
# soak cannot reach, so it is the highest-value case.
#
# Asserts: volume parks in CREATING_VOLUME with an error; verdict is permanent
# (ClusterStorageTransientErr=false); ErrorTime stays frozen across several gc ticks
# (i.e. it is never resubmitted).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"; . "$HERE/lib.sh"

APP="${APP:-voltest-perm}"
IMG="${IMG:-docker://nginx:latest}"
# Must exceed several gc ticks. With timer.gc.vdisk=60 the ticker is ~6s, so 60s is
# ~10 ticks — plenty to prove a transient error WOULD have advanced ErrorTime.
NORETRY_WINDOW="${NORETRY_WINDOW:-60}"

cleanup() {
    log "P: cleanup"
    kctl "-n $KUBENS delete resourcequota harness-noprovision --ignore-not-found" || true
    $EDEN pod delete "$APP" 2>/dev/null || true
    $EDEN pod delete voltest-warmup 2>/dev/null || true
}
trap cleanup EXIT

# Warm up first (before applying the quota): converge a normal volume so we know the
# storage-ready gate is truly open — on a fresh EVE-k the gate also requires a
# SCHEDULABLE Longhorn disk, which lags longhorn-manager readiness by minutes. Without
# this, the quota volume just defers (State stays < CREATING_VOLUME) instead of
# reaching CreatePVC and hitting the quota.
ensure_gate_open

log "P: forbidding PVC creation in $KUBENS via ResourceQuota"
kctl "create quota harness-noprovision -n $KUBENS --hard=persistentvolumeclaims=0" \
    || fail "P: could not create ResourceQuota"

log "P: deploying $APP (its volume create will hit the quota)"
$EDEN pod deploy -n "$APP" "$IMG"

log "P: waiting for the volume to park in CREATING_VOLUME with an error"
wait_vol_parked "$APP" 300

verdict=$(vol_field "$APP" ClusterStorageTransientErr)
[[ "$verdict" == "false" ]] \
    || fail "P: expected permanent verdict (ClusterStorageTransientErr=false), got '$verdict'"
pass "P: permanent verdict recorded (ClusterStorageTransientErr=false)"

log "P: confirming NO retry for ${NORETRY_WINDOW}s (ErrorTime must stay frozen)"
et1=$(errortime "$APP"); sleep "$NORETRY_WINDOW"; et2=$(errortime "$APP")
[[ -n "$et1" && "$et1" == "$et2" ]] \
    || fail "P: ErrorTime moved ($et1 -> $et2) => the volume was retried (must not be)"
pass "P: ErrorTime frozen ($et1) => permanent error is not retried"

log "P: PASS"
