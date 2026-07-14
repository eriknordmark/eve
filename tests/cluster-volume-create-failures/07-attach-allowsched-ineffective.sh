#!/usr/bin/env bash
# Longhorn-ATTACH transient probe (the typed-error-path caveat eriknordmark flagged:
# "creation can stall past the gate -- CDI upload, Longhorn attach"). Fault: set the
# Longhorn Node + disk allowScheduling=false so a NEW PVC's replica cannot be scheduled
# (PV never provisioned -> PVC stays Pending -> attach cannot happen). The readiness
# gate is sticky-true from earlier converged volumes, so the create proceeds past it and
# hits the attach fault -- exactly the post-gate scenario the retry is meant to cover.
#
# Measures: does it park transient? how fast (vs the upload D*~=310s)? does the gc-tick
# retry recover it once scheduling is restored? Logs PVC phase + node schedulable each
# sample to prove the fault is real and sustained.
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"; . "$HERE/lib.sh"
APP="${APP:-voltest-lh}"
IMG="${IMG:-docker://nginx:latest}"
HOLD="${HOLD:-420}"
INT="${INT:-10}"
LHNS="longhorn-system"

NODE=$(kctl "-n $LHNS get nodes.longhorn.io --no-headers" | awk '{print $1}' | head -1)
# Resolve NODE/DISK and read the Schedulable condition via `-o json` + host jq:
# a kubectl jsonpath filter ([?(@.type=='Schedulable')]) contains '(' which the
# remote sh under `eve ssh` parses (syntax error), so keep all filtering host-side.
DISK=$(kctl "-n $LHNS get nodes.longhorn.io $NODE -o json" \
        | jq -r '.spec.disks | keys[0] // empty' 2>/dev/null)
log "LH: node=$NODE disk=$DISK"
[[ -n "$NODE" && -n "$DISK" ]] || fail "LH: could not resolve longhorn node/disk"

sched() {
    kctl "-n $LHNS get nodes.longhorn.io $NODE -o json" \
      | jq -r --arg d "$DISK" \
          '.status.diskStatus[$d].conditions[]? | select(.type=="Schedulable") | .status' 2>/dev/null
}
pvc_phase() {
    local disp; disp=$(vol_field "$APP" DisplayName)
    kctl "-n $KUBENS get pvc --no-headers 2>/dev/null" | grep -iE 'pvc' | awk '{print $2}' | tr '\n' ','
}

restore() {
    log "LH: restoring allowScheduling=true (node+disk)"
    kctl "-n $LHNS patch nodes.longhorn.io $NODE --type merge -p '{\"spec\":{\"allowScheduling\":true,\"disks\":{\"$DISK\":{\"allowScheduling\":true}}}}'" >/dev/null 2>&1 || true
}
trap 'restore; $EDEN pod delete "$APP" 2>/dev/null || true' EXIT

# 0) warmup to guarantee the sticky gate is open (and baseline attach works)
ensure_gate_open voltest-lh-warm
$EDEN pod delete voltest-lh-warm 2>/dev/null || true

$EDEN pod delete "$APP" 2>/dev/null || true
for _ in $(seq 1 40); do [[ -z "$(vol_field "$APP" State)" ]] && break; sleep 3; done

# 1) fault: make the node+disk unschedulable
log "LH: faulting -- allowScheduling=false on node+disk"
kctl "-n $LHNS patch nodes.longhorn.io $NODE --type merge -p '{\"spec\":{\"allowScheduling\":false,\"disks\":{\"$DISK\":{\"allowScheduling\":false}}}}'" >/dev/null
for _ in $(seq 1 20); do [[ "$(sched)" == "False" ]] && break; sleep 3; done
log "LH: disk Schedulable now = '$(sched)'"

# 2) deploy under fault, monitor
log "LH: deploying $APP under the attach fault"
$EDEN pod deploy -n "$APP" "$IMG" >/dev/null
t=0 parked_at=""
while (( t < HOLD )); do
    st=$(vol_field "$APP" State); ss=$(vol_field "$APP" SubState)
    tr=$(vol_field "$APP" ClusterStorageTransientErr); er=$(vol_field "$APP" Error)
    phases=$(kctl "-n $KUBENS get pvc --no-headers 2>/dev/null" | awk '{print $2}' | tr '\n' ',')
    printf '%s t=%3ds State=%s Sub=%s transient=%s sched=%s pvc_phases=[%s] err=%.60s\n' \
        "$(date +%H:%M:%S)" "$t" "$st" "$ss" "${tr:-x}" "$(sched)" "${phases:-}" "${er:-}" >&2
    [[ "$tr" == "true" && -z "$parked_at" ]] && { parked_at="$t"; log "LH: >>> PARKED transient at t=${t}s"; }
    [[ "$st" == "$CREATED_VOLUME" ]] && { log "LH: converged at t=${t}s WHILE FAULTED (sched=$(sched)) -- attach not gated!"; break; }
    sleep "$INT"; t=$((t+INT))
done

# 3) restore + confirm recovery
restore
for _ in $(seq 1 20); do [[ "$(sched)" == "True" ]] && break; sleep 3; done
log "LH: disk Schedulable restored = '$(sched)'; waiting up to 900s for convergence"
conv=no
for _ in $(seq 1 180); do
    [[ "$(vol_field "$APP" State)" == "$CREATED_VOLUME" ]] && { conv=yes; break; }
    sleep 5
done
log "LH: RESULT parked_at=${parked_at:-never} converged_after_restore=$conv final_state=$(vol_field "$APP" State)"
