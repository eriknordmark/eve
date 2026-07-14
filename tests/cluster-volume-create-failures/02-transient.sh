#!/usr/bin/env bash
# Case T — a TRANSIENT failure PAST the gate must be retried and converge once the
# fault clears.
#
# Open the gate (warmup), then break the CDI upload PATH (scale cdi-uploadproxy to
# 0) so RolloutDiskToPVC's virtctl upload fails. Every RolloutDiskToPVC failure exit
# is transientf, so ClusterStorageTransientErr latches true and
# retryFailedClusterVolumeCreate re-drives the volume. Restore CDI and it converges.
#
# Fault choice — why break the upload PATH, not pod SCHEDULING: RolloutDiskToPVC runs
# `virtctl image-upload ... --retry 10 --wait-secs 600`. Faults that leave the CDI
# upload pod unschedulable (e.g. cordoning the node) are absorbed INSIDE that
# per-attempt wait budget — virtctl sits Pending for up to ~100 min before returning,
# so the PR's classification (which runs only on the RETURNED error) never fires in a
# bounded window and the volume just shows transient=false. Scaling uploadproxy to 0
# instead lets the upload pod schedule and become ready (satisfying --wait-secs) but
# makes the upload POST fail fast (no proxy endpoints), so RolloutDiskToPVC returns a
# transient error within a couple of minutes. cdi-operator is scaled to 0 FIRST so it
# cannot reconcile uploadproxy back up under us.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"; . "$HERE/lib.sh"

APP="${APP:-voltest-trans}"
IMG="${IMG:-docker://nginx:latest}"
CONVERGE_TIMEOUT="${CONVERGE_TIMEOUT:-900}"

orig=""
cleanup() {
    log "T: cleanup — restoring CDI + deleting app"
    kctl "-n cdi scale deploy cdi-operator --replicas=1" 2>/dev/null || true
    [[ -n "$orig" ]] && kctl "-n cdi scale deploy cdi-uploadproxy --replicas=$orig" 2>/dev/null || true
    $EDEN pod delete "$APP" 2>/dev/null || true
}
trap cleanup EXIT

ensure_gate_open
orig=$(kctl "-n cdi get deploy cdi-uploadproxy -o jsonpath={.spec.replicas}" | tr -d '[:space:]')
[[ -n "$orig" ]] || fail "T: could not read cdi-uploadproxy replica count"

log "T: faulting upload past the gate — cdi-operator + cdi-uploadproxy -> 0"
kctl "-n cdi scale deploy cdi-operator --replicas=0"
# cdi-uploadproxy is owned by the CDI CR, which cdi-operator reconciles back to its
# desired replicas within ~10s. Scaling both back-to-back races that reconcile and is
# flaky (the still-terminating operator can re-scale the proxy up before the upload
# starts). Wait until the operator pod is fully gone, THEN scale the proxy down.
for _ in $(seq 1 40); do
    n=$(kctl "-n cdi get pods -l name=cdi-operator --no-headers 2>/dev/null" | grep -vc '^$' || true)
    [[ "$n" == "0" ]] && break
    sleep 3
done
kctl "-n cdi scale deploy cdi-uploadproxy --replicas=0"
wait_upload_down   # ensure the proxy is truly down before deploying (scale is async)

log "T: deploying $APP (create passes the cached-open gate, then fails at upload)"
$EDEN pod deploy -n "$APP" "$IMG"

log "T: waiting for the volume to park transient in CREATING_VOLUME"
wait_vol_transient "$APP" "$CONVERGE_TIMEOUT"
pass "T: transient verdict recorded (ClusterStorageTransientErr=true)"

# Confirm it stays parked-transient while the fault persists (it cannot converge
# without a working upload), so a later convergence is attributable to the retry.
log "T: confirming it stays parked-transient under the fault (${STUCK_WINDOW:-90}s)"
sleep "${STUCK_WINDOW:-90}"
[[ "$(vol_field "$APP" State)" == "$CREATING_VOLUME" ]] || fail "T: left CREATING_VOLUME while fault active"
[[ "$(vol_field "$APP" ClusterStorageTransientErr)" == "true" ]] || fail "T: transient verdict cleared while fault active"
pass "T: stays parked-transient under the fault"

# Rigorous retry proof: an error-parked volume is only ever re-driven by
# retryFailedClusterVolumeCreate, so convergence after the fault clears == the retry
# fired. (An ErrorTime-advance probe is unreliable here: with a fast gc tick the
# retry clears Error the instant it re-drives, and a re-driven RolloutDiskToPVC
# against a fully-down upload can run longer than any sampling window.)
log "T: clearing the fault — restoring cdi-operator + cdi-uploadproxy"
kctl "-n cdi scale deploy cdi-operator --replicas=1"
kctl "-n cdi scale deploy cdi-uploadproxy --replicas=$orig"

log "T: waiting for the volume to converge to CREATED_VOLUME"
wait_vol "$APP" "$CREATED_VOLUME" "$SUBSTATE_CREATED" "$CONVERGE_TIMEOUT"
[[ -z "$(vol_field "$APP" Error)" ]] || fail "T: converged but error still set"
pass "T: volume converged after the fault cleared"

log "T: PASS"
