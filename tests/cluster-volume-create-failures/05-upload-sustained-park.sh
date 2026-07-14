#!/usr/bin/env bash
# Corrected sustained-fault probe. The CDI CR's owner (cdi-operator) reconciles a
# scaled-down cdi-uploadproxy back within ~10s, so a naive scale-to-0 does NOT stay
# down. Here: scale cdi-operator to 0 and WAIT until its pod is fully gone, THEN scale
# cdi-uploadproxy to 0 -- with no operator alive to reconcile, the proxy stays down for
# the whole hold. Monitor whether/when the create worker parks the volume transient
# (=> retry needed) vs converges anyway. Each sample logs uploadproxy_ready to PROVE
# the fault is actually sustained this time.
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"; . "$HERE/lib.sh"
APP="${APP:-voltest-probe2}"
IMG="${IMG:-docker://nginx:latest}"
HOLD="${HOLD:-420}"
INT="${INT:-10}"

$EDEN pod delete "$APP" 2>/dev/null || true
for _ in $(seq 1 40); do [[ -z "$(vol_field "$APP" State)" ]] && break; sleep 3; done

orig=$(kctl "-n cdi get deploy cdi-uploadproxy -o jsonpath={.spec.replicas}" | tr -d '[:space:]')
log "P2: baseline uploadproxy replicas=$orig; scaling cdi-operator to 0 and waiting for its pod to terminate"
kctl "-n cdi scale deploy cdi-operator --replicas=0"
# wait until no cdi-operator pod remains (so it can't reconcile)
opgone=no
for _ in $(seq 1 40); do
    n=$(kctl "-n cdi get pods -l name=cdi-operator --no-headers 2>/dev/null" | grep -vc '^$' || true)
    [[ "$n" == "0" ]] && { opgone=yes; break; }
    sleep 3
done
log "P2: cdi-operator pod gone=$opgone; now scaling cdi-uploadproxy to 0"
kctl "-n cdi scale deploy cdi-uploadproxy --replicas=0"
wait_upload_down
# confirm it STAYS down for ~20s before deploying
stable=yes
for _ in 1 2 3 4; do
    r=$(kctl "-n cdi get deploy cdi-uploadproxy -o jsonpath={.status.readyReplicas}" | tr -d '[:space:]')
    [[ -n "$r" && "$r" != "0" ]] && stable=no
    sleep 5
done
log "P2: uploadproxy stayed-down-over-20s=$stable"

log "P2: deploying $APP under the sustained fault"
$EDEN pod deploy -n "$APP" "$IMG" >/dev/null

t=0 parked_at=""
while (( t < HOLD )); do
    st=$(vol_field "$APP" State); ss=$(vol_field "$APP" SubState)
    tr=$(vol_field "$APP" ClusterStorageTransientErr); er=$(vol_field "$APP" Error)
    upr=$(kctl "-n cdi get deploy cdi-uploadproxy -o jsonpath={.status.readyReplicas}" | tr -d '[:space:]')
    printf '%s t=%3ds State=%s Sub=%s transient=%s uploadproxy_ready=%s err=%.70s\n' \
        "$(date +%H:%M:%S)" "$t" "$st" "$ss" "${tr:-x}" "${upr:-0}" "${er:-}" >&2
    [[ "$tr" == "true" && -z "$parked_at" ]] && { parked_at="$t"; log "P2: >>> worker PARKED transient at t=${t}s"; }
    [[ "$st" == "$CREATED_VOLUME" ]] && { log "P2: converged at t=${t}s (uploadproxy_ready=${upr:-0})"; break; }
    sleep "$INT"; t=$((t+INT))
done

log "P2: restoring cdi-operator=1 (it will reconcile uploadproxy back)"
kctl "-n cdi scale deploy cdi-operator --replicas=1"
for _ in $(seq 1 24); do
    r=$(kctl "-n cdi get deploy cdi-uploadproxy -o jsonpath={.status.readyReplicas}" | tr -d '[:space:]')
    [[ "$r" == "1" ]] && break; sleep 5
done
kctl "-n cdi scale deploy cdi-uploadproxy --replicas=${orig:-1}" 2>/dev/null || true

log "P2: waiting up to 900s for convergence after restore"
conv=no
for _ in $(seq 1 180); do
    [[ "$(vol_field "$APP" State)" == "$CREATED_VOLUME" ]] && { conv=yes; break; }
    sleep 5
done
log "P2: RESULT parked_at=${parked_at:-never} converged_after_restore=$conv final_state=$(vol_field "$APP" State)"
$EDEN pod delete "$APP" 2>/dev/null || true
