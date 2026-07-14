#!/usr/bin/env bash
# Shared helpers for the PR #6121 volumemgr EVE-k volume defer/retry
# fault-injection harness. Runs on the eden HOST; drives faults and reads state
# over `eden eve ssh`. The target MUST be an isolated eden (not the host slot)
# running an EVE-k image built from PR #6121. See README.md.

set -euo pipefail

: "${EDEN:=eden}"                     # eden binary/wrapper on PATH
KUBENS="eve-kube-app"                 # types.EVEKubeNameSpace
VOLSTATUS_DIR="/run/volumemgr/VolumeStatus"

# SwState / volumeSubState numeric values from pkg/pillar/types, asserted from the
# on-device VolumeStatus JSON.
readonly CREATING_VOLUME=109         # types.CREATING_VOLUME
readonly CREATED_VOLUME=110          # types.CREATED_VOLUME
readonly SUBSTATE_PREPAREDONE=2      # types.VolumeSubStatePrepareDone
readonly SUBSTATE_CREATED=3          # types.VolumeSubStateCreated

log()  { printf '%s [harness] %s\n' "$(date +%H:%M:%S)" "$*" >&2; }
pass() { printf '\033[32mPASS\033[0m %s\n' "$*" >&2; }
fail() { printf '\033[31mFAIL\033[0m %s\n' "$*" >&2; exit 1; }

# eve_ssh CMD...: run a shell command string inside EVE (pillar namespace).
eve_ssh() { $EDEN eve ssh -- "$*"; }

# kctl ARGS...: run kubectl in the kube (k3s) container.
# NOTE: verify this exec form on your image. Some builds need an explicit
# --kubeconfig=/run/.kube/k3s/k3s.yaml, or KUBECONFIG set in the kube container.
kctl() { eve_ssh "eve exec kube kubectl $*"; }

# volstatus_all: emit every VolumeStatus object as one JSON array (host-side jq).
# Each on-device file is a single JSON object; we newline-join then `jq -s`.
# Resilient to a transient eve-ssh failure (e.g. EVE briefly down across a reboot):
# always emits valid JSON and exits 0, so callers under `set -euo pipefail` don't
# abort — they just see an empty array until EVE is reachable again.
volstatus_all() {
    eve_ssh "eve exec pillar sh -c 'for f in $VOLSTATUS_DIR/*.json; do cat \"\$f\"; echo; done'" \
        2>/dev/null | jq -s '.' 2>/dev/null || echo '[]'
}

# vol_field DISPLAYNAME_SUBSTR FIELD: jq FIELD from the first VolumeStatus whose
# DisplayName contains the substring. Empty string if no match / no field.
vol_field() {
    local key="$1" field="$2"
    # NB: do NOT use `.[0][$f] // empty` — jq's `//` treats a boolean `false`
    # (and null) as the alternative, so a `false` field would come back empty.
    # Emit "" only when no matching volume / absent field; otherwise tostring so
    # false -> "false", true -> "true".
    volstatus_all | jq -r --arg k "$key" --arg f "$field" \
        'map(select(.DisplayName|test($k))) | .[0] as $o
         | if $o == null then "" else ($o[$f] | if . == null then "" else tostring end) end'
}

# wait_vol KEY STATE SUBSTATE TIMEOUT_SEC: block until the volume reaches
# STATE/SUBSTATE, else fail after TIMEOUT_SEC.
wait_vol() {
    local key="$1" state="$2" substate="$3" timeout="$4" t=0 s="" ss=""
    while (( t < timeout )); do
        s=$(vol_field "$key" State); ss=$(vol_field "$key" SubState)
        [[ "$s" == "$state" && "$ss" == "$substate" ]] && return 0
        sleep 5; t=$((t+5))
    done
    fail "timeout: $key never reached State=$state SubState=$substate in ${timeout}s (last State=$s SubState=$ss)"
}

# wait_vol_parked KEY TIMEOUT_SEC: wait until the volume is parked in
# CREATING_VOLUME/PrepareDone with an error actually recorded. The create worker
# sits at PrepareDone with NO error for tens of seconds while RolloutDiskToPVC /
# CreatePVC runs, so checking Error the instant State hits PrepareDone races the
# worker; poll until Error is non-empty.
wait_vol_parked() {
    local key="$1" timeout="$2" t=0 s="" ss="" err=""
    while (( t < timeout )); do
        s=$(vol_field "$key" State); ss=$(vol_field "$key" SubState); err=$(vol_field "$key" Error)
        [[ "$s" == "$CREATING_VOLUME" && "$ss" == "$SUBSTATE_PREPAREDONE" && -n "$err" ]] && return 0
        sleep 5; t=$((t+5))
    done
    fail "timeout: $key did not park with an error in ${timeout}s (State=$s SubState=$ss err='${err:0:50}')"
}

# wait_vol_transient KEY TIMEOUT_SEC: wait until the volume is parked in
# CREATING_VOLUME/PrepareDone with a TRANSIENT verdict. Unlike wait_vol_parked,
# this latches on ClusterStorageTransientErr==true (which persists), NOT on Error
# being non-empty: once the retry starts re-driving a transient failure it clears
# the Error each gc tick, so with a fast tick Error is empty most of the time and
# an Error-based wait races the retry. The verdict bool is the stable signal.
wait_vol_transient() {
    local key="$1" timeout="$2" t=0 s="" ss="" tr=""
    while (( t < timeout )); do
        s=$(vol_field "$key" State); ss=$(vol_field "$key" SubState); tr=$(vol_field "$key" ClusterStorageTransientErr)
        [[ "$s" == "$CREATING_VOLUME" && "$ss" == "$SUBSTATE_PREPAREDONE" && "$tr" == "true" ]] && return 0
        sleep 5; t=$((t+5))
    done
    fail "timeout: $key did not reach transient-parked in ${timeout}s (State=$s SubState=$ss transient=$tr)"
}

# errortime KEY: the volume's current ErrorTime (RFC3339; empty if no error).
# The retry re-stamps ErrorTime on every re-failure, so its advancement is the
# black-box signal that the retry actually fired.
errortime() { vol_field "$1" ErrorTime; }

# ensure_gate_open: converge one warmup volume so volumemgr's clusterStorageReady
# cache flips true (it is sticky-true for the process lifetime). Required before a
# "transient past the gate" test, otherwise a fault just keeps the gate closed and
# the volume DEFERS (no error) instead of failing-then-retrying.
ensure_gate_open() {
    local warm="${1:-voltest-warmup}"
    log "warmup: deploying $warm to open the storage-ready gate"
    $EDEN pod deploy -n "$warm" "${WARM_IMG:-docker://nginx:latest}" >/dev/null
    wait_vol "$warm" "$CREATED_VOLUME" "$SUBSTATE_CREATED" "${WARM_TIMEOUT:-900}"
    pass "warmup: $warm converged; gate cache is now open"
}

# wait_upload_down: block until cdi-uploadproxy has no ready replicas. `kubectl
# scale --replicas=0` returns before the pod finishes its termination grace
# period, so a volume deployed immediately after can still reach a live proxy and
# upload successfully. Gating deploy on this makes the upload fault deterministic.
wait_upload_down() {
    local t=0 rr=""
    while (( t < 120 )); do
        rr=$(kctl "-n cdi get deploy cdi-uploadproxy -o jsonpath={.status.readyReplicas}" | tr -d '[:space:]')
        [[ -z "$rr" || "$rr" == "0" ]] && return 0
        sleep 3; t=$((t+3))
    done
    return 0   # best-effort: proceed even if it lingers
}
