#!/usr/bin/env bash
# Stress/soak driver — repeatedly exercise the defer/retry machinery under load,
# cycling through fault phases and asserting the per-phase invariants each cycle.
#
# Shared-infra faults (uploadproxy scale, namespace ResourceQuota) are cluster-wide,
# not per-volume, so a profile is applied per PHASE across a batch of volumes rather
# than randomized per volume. Per-volume randomized profiles require the code-level
# injector (see fault-injector/).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"; . "$HERE/lib.sh"

N="${N:-4}"                          # volumes per batch
CYCLES="${CYCLES:-20}"               # soak cycles
IMG="${IMG:-docker://nginx:latest}"
CONVERGE_TIMEOUT="${CONVERGE_TIMEOUT:-1200}"

batch=()
orig=""
cleanup() {
    log "stress: cleanup"
    kctl "-n cdi scale deploy cdi-operator --replicas=1" 2>/dev/null || true
    [[ -n "$orig" ]] && kctl "-n cdi scale deploy cdi-uploadproxy --replicas=$orig" 2>/dev/null || true
    kctl "-n $KUBENS delete resourcequota harness-noprovision --ignore-not-found" 2>/dev/null || true
    for a in "${batch[@]}"; do $EDEN pod delete "$a" 2>/dev/null || true; done
}
trap cleanup EXIT

deploy_batch() {   # $1=tag
    batch=()
    local i a
    # Sequential, NOT parallel: concurrent `eden pod deploy` invocations
    # read-modify-write the controller device config and the last writer clobbers
    # the others, so a backgrounded batch silently loses app instances.
    for i in $(seq 1 "$N"); do
        a="voltest-$1-$i"; batch+=("$a")
        $EDEN pod deploy -n "$a" "$IMG" >/dev/null
    done
}
delete_batch() { local a; for a in "${batch[@]}"; do $EDEN pod delete "$a" 2>/dev/null || true; done; }

phase_baseline() {
    log "== baseline: $N volumes, no fault, expect all converge clean =="
    deploy_batch base
    local a
    for a in "${batch[@]}"; do
        wait_vol "$a" "$CREATED_VOLUME" "$SUBSTATE_CREATED" "$CONVERGE_TIMEOUT"
        [[ "$(vol_field "$a" ClusterStorageTransientErr)" == "false" ]] \
            || fail "baseline: $a spuriously flagged transient"
    done
    pass "baseline: all $N converged clean"
    delete_batch
}

phase_transient() {
    log "== transient storm: fault upload, expect all park transient + retry, then converge =="
    # operator FIRST so it cannot reconcile uploadproxy back up mid-fault (see 02-transient.sh)
    kctl "-n cdi scale deploy cdi-operator --replicas=0"
    kctl "-n cdi scale deploy cdi-uploadproxy --replicas=0"
    wait_upload_down
    deploy_batch trans
    local a
    # wait_vol_transient latches on the verdict bool (set after the error is
    # classified), avoiding the PrepareDone-before-error race that a plain
    # wait_vol would hit.
    for a in "${batch[@]}"; do
        wait_vol_transient "$a" "$CONVERGE_TIMEOUT"
    done
    pass "transient: all $N parked transient under the fault"
    # Retry proof is convergence-after-restore: an error-parked volume is only
    # re-driven by retryFailedClusterVolumeCreate, so converging once the fault
    # clears == the retry fired. An ErrorTime-advance probe is unreliable here:
    # each RolloutDiskToPVC attempt against a down uploadproxy runs for minutes
    # (virtctl --retry/--wait-secs), so ErrorTime advances on that cadence, not
    # per gc tick.
    kctl "-n cdi scale deploy cdi-operator --replicas=1"
    kctl "-n cdi scale deploy cdi-uploadproxy --replicas=$orig"
    for a in "${batch[@]}"; do wait_vol "$a" "$CREATED_VOLUME" "$SUBSTATE_CREATED" "$CONVERGE_TIMEOUT"; done
    pass "transient: all converged after fault cleared"
    delete_batch
}

phase_permanent() {
    log "== permanent storm: quota forbids PVCs, expect all park permanent + NO retry =="
    kctl "create quota harness-noprovision -n $KUBENS --hard=persistentvolumeclaims=0" || true
    deploy_batch perm
    local a et1 et2
    # wait_vol_parked waits until Error is actually recorded, so the ErrorTime
    # frozen-probe below samples a real timestamp (not the pre-error zero time,
    # which would look like a spurious "retry" when the first error lands).
    for a in "${batch[@]}"; do
        wait_vol_parked "$a" "$CONVERGE_TIMEOUT"
        [[ "$(vol_field "$a" ClusterStorageTransientErr)" == "false" ]] \
            || fail "permanent: $a wrongly flagged transient"
    done
    et1=$(errortime "${batch[0]}"); sleep 60; et2=$(errortime "${batch[0]}")
    [[ "$et1" == "$et2" ]] || fail "permanent: ${batch[0]} was retried (ErrorTime moved $et1 -> $et2)"
    pass "permanent: all parked permanent, none retried"
    kctl "-n $KUBENS delete resourcequota harness-noprovision --ignore-not-found"
    delete_batch
}

ensure_gate_open
orig=$(kctl "-n cdi get deploy cdi-uploadproxy -o jsonpath={.spec.replicas}")
[[ -n "$orig" ]] || fail "stress: could not read cdi-uploadproxy replica count"

phases=(phase_baseline phase_transient phase_permanent)
# PHASES="phase_x phase_y" forces a deterministic phase set (default: random mix).
[[ -n "${PHASES:-}" ]] && read -ra phases <<< "$PHASES"
for c in $(seq 1 "$CYCLES"); do
    ph=${phases[$((RANDOM % ${#phases[@]}))]}
    log "### cycle $c/$CYCLES -> $ph ###"
    "$ph"
done
pass "stress: completed $CYCLES cycles with no invariant violation"
