#!/usr/bin/env bash
# Case R — is retryFailedClusterVolumeCreate NEEDED, or does RolloutDiskToPVC's own
# internal retry budget (virtctl --retry 10 --wait-secs 600, wrapped in the outer
# maxRetries=10 loop) already absorb realistic transients?
#
# Decisive variable: the create worker only parks a volume (ClusterStorageTransientErr
# =true) when RolloutDiskToPVC RETURNS an error. Once parked, ONLY the gc-tick retry
# re-drives it (established by 02-transient.sh: post-park convergence == retry fired).
# So for a SELF-CLEARING fault:
#   - if the volume ever parks transient and then converges  => the worker returned =>
#     the retry did real work  => retry NEEDED for that fault.
#   - if it never parks and converges straight through       => virtctl's internal
#     budget absorbed the fault => retry NOT needed for that fault.
#
# 02-transient.sh only ever exercised a HELD teardown (fault kept down for STUCK_WINDOW
# then restored) and had to escalate to scaling cdi-operator+uploadproxy to 0 to make
# the worker return at all — a synthetic condition. This case measures where the
# park/absorb boundary actually sits:
#
#   Part A  duration sweep: break the upload PATH (uploadproxy->0) for a CONTROLLED D
#           seconds, restore, classify park/absorb. Finds D* = the minimum down-time
#           that escapes virtctl's internal retry and forces a worker return.
#   Part B  realistic fault: delete the single uploadproxy POD (k8s self-heals it in
#           ~10-30s). This is what an actual CDI blip looks like in the field. Classify
#           park/absorb.
#
# Interpretation: if D* is larger than realistic transient durations (pod restart
# ~10-30s, apiserver blip ~seconds) AND Part B ABSORBS, then retryFailedClusterVolume-
# Create rarely/never fires in the field and the gate + virtctl budget suffice -> the
# gc-tick retry + TransientError classification + ClusterStorageTransientErr field can
# likely be dropped (move the retry loop inside RolloutDiskToPVC, honoring ctx). If D*
# is small or Part B PARKS, the retry occupies a real niche and stays.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"; . "$HERE/lib.sh"

IMG="${IMG:-docker://nginx:latest}"
CONVERGE_TIMEOUT="${CONVERGE_TIMEOUT:-1200}"   # per-trial convergence budget after restore
# Part A sweep, seconds of SUSTAINED upload-path-down. Bracketed around the measured
# virtctl internal-retry exhaustion point D*~=310s (see the ~/notes finding): below it
# the worker absorbs the outage and converges (ABSORBED); at/above it the worker returns
# and parks transient (PARKED). Values <300 should ABSORB, >=320 should PARK.
DURATIONS="${DURATIONS:-120 240 300 340 420}"
SAMPLE="${SAMPLE:-5}"                          # transient-latch poll interval

results=()   # collected "phase\tlabel\tlatched\toutcome" rows

# --- fault primitives (upload PATH down; operator first so it can't reconcile) ---
uploadproxy_replicas() {
    kctl "-n cdi get deploy cdi-uploadproxy -o jsonpath={.spec.replicas}" | tr -d '[:space:]'
}
fault_path_down() {
    # cdi-uploadproxy is owned by the CDI CR, which cdi-operator reconciles back to
    # its desired replicas within ~10s. Scaling both back-to-back is NOT enough --
    # the still-terminating operator re-scales the proxy up before the upload even
    # starts (verified: proxy ready again by t=10s). Scale the operator to 0 and WAIT
    # until its pod is fully gone, THEN scale the proxy down so nothing reconciles it.
    kctl "-n cdi scale deploy cdi-operator --replicas=0"
    local n
    for _ in $(seq 1 40); do
        n=$(kctl "-n cdi get pods -l name=cdi-operator --no-headers 2>/dev/null" | grep -vc '^$' || true)
        [[ "$n" == "0" ]] && break
        sleep 3
    done
    kctl "-n cdi scale deploy cdi-uploadproxy --replicas=0"
    wait_upload_down
}
fault_path_restore() {   # $1 = original uploadproxy replica count
    kctl "-n cdi scale deploy cdi-operator --replicas=1"
    kctl "-n cdi scale deploy cdi-uploadproxy --replicas=${1:-1}"
}
# realistic self-healing blip: drop the single uploadproxy pod, let the Deployment
# recreate it (operator stays up). Natural downtime, not externally held.
fault_pod_delete() {
    kctl "-n cdi delete pod -l cdi.kubevirt.io=cdi-uploadproxy --wait=false" 2>/dev/null \
      || kctl "-n cdi delete pod -l app=containerized-data-importer --wait=false" 2>/dev/null || true
}

# wait_vol_gone KEY TIMEOUT: block until no VolumeStatus matches KEY (clean slate
# for the next trial, so vol_field's DisplayName-substring match can't hit a stale
# volume).
wait_vol_gone() {
    local key="$1" timeout="${2:-180}" t=0
    while (( t < timeout )); do
        [[ -z "$(vol_field "$key" State)" ]] && return 0
        sleep 3; t=$((t+3))
    done
    log "warn: $key VolumeStatus still present after ${timeout}s"
}

# wait_in_upload KEY TIMEOUT: non-fatal wait until the volume reaches
# CREATING_VOLUME/PrepareDone -- i.e. the create worker has finished content-tree
# prep and is now inside RolloutDiskToPVC (the upload). Timing the fault window from
# here (not from deploy) guarantees the downtime actually overlaps the upload attempt,
# so a short D isn't spuriously ABSORBED just because the fault cleared before the
# upload even started. Returns 0 if reached, 1 on timeout.
wait_in_upload() {
    local key="$1" timeout="${2:-300}" t=0
    while (( t < timeout )); do
        [[ "$(vol_field "$key" State)" == "$CREATING_VOLUME" \
           && "$(vol_field "$key" SubState)" == "$SUBSTATE_PREPAREDONE" ]] && return 0
        sleep 3; t=$((t+3))
    done
    return 1
}

# run_trial PHASE LABEL APP: APP has just been deployed under an active fault.
# Poll State + ClusterStorageTransientErr every SAMPLE sec, latching whether the
# volume EVER parked transient, until it converges or CONVERGE_TIMEOUT elapses.
# The caller restores the fault (on its own schedule) concurrently. Records one row.
run_trial() {
    local phase="$1" label="$2" app="$3"
    local t=0 latched="no" s="" tr="" outcome="STUCK"
    while (( t < CONVERGE_TIMEOUT )); do
        s=$(vol_field "$app" State)
        tr=$(vol_field "$app" ClusterStorageTransientErr)
        [[ "$tr" == "true" ]] && latched="yes"
        if [[ "$s" == "$CREATED_VOLUME" ]]; then outcome="CONVERGED"; break; fi
        sleep "$SAMPLE"; t=$((t+SAMPLE))
    done
    local verdict
    if [[ "$outcome" == "CONVERGED" && "$latched" == "yes" ]]; then
        verdict="PARKED(retry-NEEDED)"
    elif [[ "$outcome" == "CONVERGED" ]]; then
        verdict="ABSORBED(retry-not-needed)"
    else
        verdict="STUCK(inconclusive)"
    fi
    log "  -> $label: parked_transient=$latched outcome=$outcome => $verdict"
    results+=("$(printf '%s\t%s\t%s\t%s' "$phase" "$label" "$latched" "$verdict")")
}

cleanup() {
    log "R: cleanup — restoring CDI + deleting trial apps"
    kctl "-n cdi scale deploy cdi-operator --replicas=1" 2>/dev/null || true
    kctl "-n cdi scale deploy cdi-uploadproxy --replicas=${ORIG:-1}" 2>/dev/null || true
    for a in $CREATED_APPS; do $EDEN pod delete "$a" 2>/dev/null || true; done
}
CREATED_APPS=""
trap cleanup EXIT

ensure_gate_open
ORIG=$(uploadproxy_replicas); [[ -n "$ORIG" ]] || fail "R: could not read cdi-uploadproxy replicas"
log "R: baseline uploadproxy replicas=$ORIG; gate is open"

# ---------- Part A: controlled duration sweep ----------
for D in $DURATIONS; do
    app="voltest-r-d${D}"
    $EDEN pod delete "$app" 2>/dev/null || true; wait_vol_gone "$app"
    CREATED_APPS="$CREATED_APPS $app"
    log "R/A d=${D}s: upload path DOWN, deploy, hold ${D}s from upload-start, restore"
    fault_path_down
    $EDEN pod deploy -n "$app" "$IMG" >/dev/null
    if ! wait_in_upload "$app"; then
        log "  -> d=${D}s: volume never reached the upload stage; skipping"
        results+=("$(printf 'A(down=%ss)\td=%ss\tn/a\tNO-UPLOAD(skipped)' "$D" "$D")")
        fault_path_restore "$ORIG"; $EDEN pod delete "$app" 2>/dev/null || true; wait_vol_gone "$app"
        continue
    fi
    ( sleep "$D"; fault_path_restore "$ORIG" ) &      # hold D from upload-start, then restore
    restore_pid=$!
    run_trial "A(down=${D}s)" "d=${D}s" "$app"
    wait "$restore_pid" 2>/dev/null || true
    fault_path_restore "$ORIG"                        # idempotent belt-and-suspenders
    $EDEN pod delete "$app" 2>/dev/null || true; wait_vol_gone "$app"
done

# ---------- Part B: realistic self-healing pod restart ----------
appb="voltest-r-podrestart"
$EDEN pod delete "$appb" 2>/dev/null || true; wait_vol_gone "$appb"
CREATED_APPS="$CREATED_APPS $appb"
log "R/B: deploy, wait for upload stage, then delete the uploadproxy pod (self-healing blip)"
$EDEN pod deploy -n "$appb" "$IMG" >/dev/null
if wait_in_upload "$appb"; then
    fault_pod_delete
    run_trial "B(pod-delete)" "pod-restart" "$appb"
else
    log "  -> Part B: volume never reached the upload stage; skipping"
    results+=("$(printf 'B(pod-delete)\tpod-restart\tn/a\tNO-UPLOAD(skipped)')")
fi
$EDEN pod delete "$appb" 2>/dev/null || true; wait_vol_gone "$appb"

# ---------- report ----------
printf '\n===== Case R: retry-necessity results =====\n' >&2
printf '%-16s %-14s %-14s %s\n' "PHASE" "FAULT" "PARKED?" "VERDICT" >&2
for r in "${results[@]}"; do
    IFS=$'\t' read -r phase label latched verdict <<<"$r"
    printf '%-16s %-14s %-14s %s\n' "$phase" "$label" "$latched" "$verdict" >&2
done
printf '\nRead-off: smallest down-time D that flips ABSORBED->PARKED is D* (virtctl\n' >&2
printf 'internal-retry escape threshold). Part B shows whether a real pod restart\n' >&2
printf 'crosses it. All-ABSORBED (incl. Part B) => retry likely redundant in the field.\n' >&2
log "R: done"
