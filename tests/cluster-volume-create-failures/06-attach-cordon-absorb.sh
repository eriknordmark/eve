#!/usr/bin/env bash
# Longhorn/scheduling attach-fault probe via node CORDON. With the node cordoned, the
# CDI importer/upload pod cannot be scheduled -> the PVC's volume cannot attach -> the
# upload cannot proceed. This is the "creation stalls past the gate on a non-uploadproxy
# cause" scenario (Longhorn attach / pod scheduling). Question: does this park FAST
# (making the gc retry distinctly valuable) or is it ABSORBED into virtctl's long
# --wait-secs 600 x retry budget (like the upload path, only longer)?
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"; . "$HERE/lib.sh"
APP="${APP:-voltest-cordon}"
IMG="${IMG:-docker://nginx:latest}"
HOLD="${HOLD:-420}"
INT="${INT:-15}"
# Resolve the single k8s node name (override with NODE=... for a named/multi-node device).
NODE="${NODE:-$(kctl "get nodes -o name" | head -1 | cut -d/ -f2 | tr -d '[:space:]')}"

uncordon() { kctl "uncordon $NODE" >/dev/null 2>&1 || true; }
trap 'uncordon; $EDEN pod delete "$APP" 2>/dev/null || true' EXIT

$EDEN pod delete "$APP" 2>/dev/null || true
for _ in $(seq 1 40); do [[ -z "$(vol_field "$APP" State)" ]] && break; sleep 3; done

log "CORDON: cordoning node $NODE (new pods, incl CDI importer, will stay Pending)"
kctl "cordon $NODE" 2>&1 | grep -vE 'DEPRECATION|known hosts' || true
uns=$(kctl "get node $NODE -o jsonpath={.spec.unschedulable}" | tr -d '[:space:]')
log "CORDON: node.spec.unschedulable=$uns"

log "CORDON: deploying $APP under cordon"
$EDEN pod deploy -n "$APP" "$IMG" >/dev/null
t=0 parked_at=""
while (( t < HOLD )); do
    st=$(vol_field "$APP" State); ss=$(vol_field "$APP" SubState)
    tr=$(vol_field "$APP" ClusterStorageTransientErr); er=$(vol_field "$APP" Error)
    # count importer/upload pods and how many are Pending
    pend=$(kctl "-n $KUBENS get pods --no-headers 2>/dev/null" | grep -iE 'importer|upload|prime' | grep -c Pending || true)
    run=$(kctl "-n $KUBENS get pods --no-headers 2>/dev/null" | grep -iE 'importer|upload|prime' | grep -c Running || true)
    printf '%s t=%3ds State=%s Sub=%s transient=%s cdi_pods(pending/running)=%s/%s err=%.55s\n' \
        "$(date +%H:%M:%S)" "$t" "$st" "$ss" "${tr:-x}" "${pend:-0}" "${run:-0}" "${er:-}" >&2
    [[ "$tr" == "true" && -z "$parked_at" ]] && { parked_at="$t"; log "CORDON: >>> PARKED transient at t=${t}s"; }
    [[ "$st" == "$CREATED_VOLUME" ]] && { log "CORDON: converged at t=${t}s (unexpected under cordon)"; break; }
    sleep "$INT"; t=$((t+INT))
done

log "CORDON: uncordoning; waiting up to 900s for convergence"
uncordon
conv=no
for _ in $(seq 1 180); do
    [[ "$(vol_field "$APP" State)" == "$CREATED_VOLUME" ]] && { conv=yes; break; }
    sleep 5
done
log "CORDON: RESULT parked_at=${parked_at:-never-in-${HOLD}s} converged_after_uncordon=$conv final_state=$(vol_field "$APP" State)"
