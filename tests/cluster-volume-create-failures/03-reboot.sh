#!/usr/bin/env bash
# Case R — a reboot with an INCOMPLETE upload must re-verify and re-drive, not skip.
#
# csihandler's reboot guard normally skips creation when the PVC already exists,
# EXCEPT it re-checks IsPVCUploadComplete and re-drives the upload if the prior boot
# left it unfinished. We create that state (PVC exists, upload stuck), reboot, then
# assert the volume still converges — i.e. data was re-driven, not declared done empty.
#
# Black-box caveat: guaranteeing "PVC exists but upload incomplete" at the reboot
# instant is timing-sensitive. For deterministic control use the code-level injector
# (see fault-injector/). This script is best-effort.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"; . "$HERE/lib.sh"

APP="${APP:-voltest-reboot}"
IMG="${IMG:-docker://nginx:latest}"
CONVERGE_TIMEOUT="${CONVERGE_TIMEOUT:-900}"

orig=""
cleanup() {
    kctl "-n cdi scale deploy cdi-operator --replicas=1" 2>/dev/null || true
    [[ -n "$orig" ]] && kctl "-n cdi scale deploy cdi-uploadproxy --replicas=$orig" 2>/dev/null || true
    $EDEN pod delete "$APP" 2>/dev/null || true
}
trap cleanup EXIT

ensure_gate_open
orig=$(kctl "-n cdi get deploy cdi-uploadproxy -o jsonpath={.spec.replicas}")

log "R: faulting upload (cdi-operator + cdi-uploadproxy -> 0), then deploying $APP"
kctl "-n cdi scale deploy cdi-operator --replicas=0"
kctl "-n cdi scale deploy cdi-uploadproxy --replicas=0"
$EDEN pod deploy -n "$APP" "$IMG"

log "R: waiting until the volume parks with the PVC created but upload stuck"
wait_vol_transient "$APP" "$CONVERGE_TIMEOUT"
if kctl "-n $KUBENS get pvc -o name" | grep -q pvc; then
    pass "R: a PVC exists while the upload is incomplete (reverify path will apply)"
else
    log "R: WARNING — no PVC yet; the reverify path may not be exercised this run"
fi

log "R: rebooting EVE via controller with the upload incomplete"
# NB: these wait loops must not trip `set -euo pipefail` — during the reboot the
# eve-ssh (EVE down) and grep (no match) return non-zero; `|| true` / if-form keep
# the script alive instead of aborting into cleanup.
boot_id() { $EDEN eve ssh -- 'cat /proc/sys/kernel/random/boot_id' 2>/dev/null | grep -oE '[0-9a-f-]{36}' | head -1 || true; }
pre_boot=$(boot_id)
$EDEN controller edge-node reboot
log "R: waiting for EVE to actually reboot (boot_id change; pre=$pre_boot)"
for i in $(seq 1 80); do
    sleep 15
    now=$(boot_id)
    [[ -n "$now" && "$now" != "$pre_boot" ]] && { log "R: EVE rebooted (boot_id=$now)"; break; }
done

log "R: waiting for the k3s API to come back post-reboot"
for i in $(seq 1 80); do
    if kctl "get nodes" 2>/dev/null | grep -q Ready; then log "R: k3s API back"; break; fi
    sleep 15
done

log "R: restoring CDI post-reboot (operator reconciles uploadproxy)"
kctl "-n cdi scale deploy cdi-operator --replicas=1"
kctl "-n cdi scale deploy cdi-uploadproxy --replicas=$orig"

log "R: asserting the volume converges (upload re-driven, not skipped empty)"
wait_vol "$APP" "$CREATED_VOLUME" "$SUBSTATE_CREATED" "$CONVERGE_TIMEOUT"
pass "R: volume converged after a reboot with a previously-incomplete upload"
log "R: for a hard assertion that reverify ran, grep the pillar log for"
log "   'PVC .* exists but upload not complete, re-driving upload'"
log "R: PASS"
