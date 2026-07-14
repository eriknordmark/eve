# volumemgr EVE-k volume defer/retry — fault-injection harness (PR #6121)

Isolated tests for the typed-verdict defer/retry machinery in PR #6121: the
permanent-no-retry, transient-retry-converge, and reboot-reverify branches that a
happy-path soak cannot reach (`01`–`03`, `stress.sh`), plus fault-characterization
tests (`04`–`07`) that measure *where* the create worker actually parks vs is absorbed
by virtctl's internal retry, to inform whether the gc-tick retry is needed. Validated
on EVE-k (k3s v1.34.2+k3s1), 2026-07-13/14.

## What it checks

| Script            | Invariant |
|-------------------|-----------|
| `01-permanent.sh` | A permanent create failure (403 Forbidden via ResourceQuota) parks the volume with `ClusterStorageTransientErr=false` and is **never retried** (ErrorTime frozen). |
| `02-transient.sh` | A transient failure past the gate (upload backend down) parks with `ClusterStorageTransientErr=true` and **converges** once the fault clears. Convergence-after-restore is the retry proof: an error-parked volume is only ever re-driven by `retryFailedClusterVolumeCreate`. |
| `03-reboot.sh`    | A reboot with an incomplete upload **re-verifies and re-drives** (converges) rather than skipping an empty PVC. Best-effort black-box; use the injector for determinism. |
| `04-retry-necessity.sh` | Sweeps sustained upload-path downtime to find `D*`, the minimum outage that forces a worker return. Each trial classifies ABSORBED (virtctl's internal retry rode it out → the gc retry never fired) vs PARKED (worker returned → gc retry needed). Answers "is `retryFailedClusterVolumeCreate` needed, or do the gate + virtctl's internal retries suffice?" |
| `05-upload-sustained-park.sh` | Holds a *sustained* upload outage (operator down first so the proxy can't self-heal) and logs `uploadproxy_ready` each sample to prove the fault stays applied. Records that the worker parks transient only after virtctl's internal budget exhausts (~310s for a small volume), then recovers via the gc retry once the fault clears. |
| `06-attach-cordon-absorb.sh` | Cordons the node so the CDI upload pod can't schedule (attach cannot happen). Shows the stall is **absorbed** by virtctl's `--wait-secs` budget (importer pod Pending, no park within 420s) — i.e. attach/scheduling faults park *later* than upload-proxy faults, not faster. |
| `07-attach-allowsched-ineffective.sh` | Sets Longhorn node/disk `allowScheduling=false` and confirms it is **not** an effective attach fault — the volume binds and converges anyway (see the caveat below). Reads the Longhorn `Schedulable` state via `-o json` + `jq` so the kubectl jsonpath filter isn't parsed by the remote shell under `eve ssh`. |
| `stress.sh`       | Soak: N-volume batches cycling through baseline / transient-storm / permanent-storm phases, asserting each phase's invariant every cycle. |

## Prerequisites

- **An isolated eden — NOT the host eden slot.** Use `eden-vm-sandbox` (Multipass VM
  with its own docker + ports) or a dedicated server. EVE-k sizing: ≥80G disk / 16G
  RAM / 8 vCPU. App guests need not fully boot — the volume converges to
  `CREATED_VOLUME` independent of guest boot.
- EVE-k image built from PR #6121 (confirmed tag:
  `lfedge/eve:0.0.0-volumemgr-cdi-retry-typed-a24d2545-k`).
- `jq` on the host running the scripts.

## Required setup before running

1. **Speed up the retry cadence.** The retry fires on volumemgr's gc ticker =
   `timer.gc.vdisk / 10`. Default `timer.gc.vdisk` is 3600s → a **6-minute** retry
   period. Set it to the minimum (60s → ~6s period) via your controller/eden global
   config:

   ```
   timer.gc.vdisk = 60
   ```

   (Exact push mechanism depends on your controller; with eden it is a device global
   config item.) Without this, the transient windows in the scripts must be lengthened
   to minutes.

2. **Point the scripts at your eden.** They call `eden` by default; override with
   `EDEN=/path/to/eden` or an eden wrapper.

## Two behaviours the scripts depend on (read before editing)

- **The gate cache is sticky-true.** `clusterStorageReady` caches `true` for the
  volumemgr process lifetime once storage first comes up. So a fault applied *after*
  first-ready does **not** re-close the gate — the create attempt proceeds and *fails*
  (the retry path), rather than deferring. `lib.sh:ensure_gate_open` deploys a warmup
  volume first to flip the cache; the transient/reboot tests rely on it.
- **Shared-infra faults are cluster-wide.** Scaling `cdi-uploadproxy` or applying a
  namespace `ResourceQuota` affects *every* volume, not one — so profiles are applied
  per phase across a batch, not randomized per volume. Per-volume randomized profiles
  need the code-level injector (`fault-injector/`).

## Environment assumptions (confirmed on EVE-k v1.34.2+k3s1, 2026-07-13)

- `kctl()` runs `eve exec kube kubectl …`, which works out of the box on this image.
  If kubectl isn't on PATH in the kube container or KUBECONFIG isn't set on yours,
  add `--kubeconfig=/run/.kube/k3s/k3s.yaml`.
- `03-reboot.sh` reboots via `eden controller edge-node reboot` (through the
  controller, so the reboot is deferred/graceful) and detects the reboot by a change
  in `/proc/sys/kernel/random/boot_id`, then waits for the k3s API to return.
- The transient/soak faults break the upload PATH (scale `cdi-operator` to 0 first so
  it cannot reconcile, then `cdi-uploadproxy` to 0, gated on the proxy actually being
  down). A scheduling fault (e.g. cordon) is absorbed inside virtctl's own
  `--wait-secs`/`--retry` budget and never surfaces to the classifier in-window.

## Running

```sh
export EDEN=eden                       # or your wrapper
./01-permanent.sh                      # permanent-no-retry (highest value; soak can't hit it)
./02-transient.sh                      # transient retry + converge
./03-reboot.sh                         # reboot reverify (best-effort)
./04-retry-necessity.sh                # duration sweep -> find D*
./05-upload-sustained-park.sh          # sustained upload outage -> park at ~D*, recover
./06-attach-cordon-absorb.sh           # attach/scheduling fault -> absorbed, no park
./07-attach-allowsched-ineffective.sh  # allowScheduling=false -> ineffective, converges
N=4 CYCLES=20 ./stress.sh              # soak
```

Common overrides: `APP`, `IMG`, `CONVERGE_TIMEOUT`, `NORETRY_WINDOW`, `N`, `CYCLES`,
`PHASES` (force a deterministic soak phase set, e.g. `PHASES="phase_transient"`);
`HOLD`/`INT`/`DURATIONS` for `04`–`06`; `NODE=<k8s-node>` for `06` on a named device.

## Findings (2026-07-14)

Measured with `04`–`06` on single-node EVE-k. See the analysis write-up for detail.

- **Gate covers the motivating cases.** First-boot / kvm→k (storage 10–30 min coming
  up) are handled by the readiness gate deferring *before* the worker runs — not the
  retry.
- **The worker absorbs outages internally for ~5 min.** A sustained upload-path outage
  sits quietly in `CREATING_VOLUME/PrepareDone` (no error) until virtctl's
  `--retry 10 --wait-secs 600` + outer loop exhausts at **`D*`≈310s** (small volume),
  only *then* parking transient. Realistic self-healing blips (cdi-operator reconciles
  a killed proxy in ~10s) are fully absorbed and never park.
- **Attach/scheduling faults park later, not sooner.** A cordoned node (upload pod
  Pending) is absorbed by virtctl's `--wait-secs` for >420s without parking. So there
  is no *fast*-park path via Longhorn attach; the only fast park is a `CreatePVC`
  API-level typed error (see `01`).
- **A reboot re-drives a parked/incomplete upload** (`CreateVolume`'s `IsPVCUploadComplete`
  guard re-runs the upload against the existing PVC), so a reboot is a second recovery
  path alongside the gc retry.

Net: `retryFailedClusterVolumeCreate` only adds value for a sustained (>~5 min)
post-gate outage that later heals — a narrow tail also covered by a reboot.

### Caveat: `allowScheduling=false` is NOT a usable attach fault on single-node

`07-attach-allowsched-ineffective.sh` demonstrates this: setting `allowScheduling=false`
on the Longhorn node+disk does **not** block a new PVC — it binds in ~9s and the volume
converges. Single-node Longhorn schedules to the only node regardless, and the disk
`Schedulable` *condition* tracks capacity, not the admin toggle. Use the cordon fault
(`06`) for an attach/scheduling stall.

## Deterministic alternative

Black-box timing can't reliably force a *bounded* number of transient failures, and
can't make the permanent branch reachable per-volume. For that, `fault-injector/` adds
an env-gated hook (`EVE_KUBE_FAULT`) to `kubeapi` on a **throwaway scratch branch**
(never committed) that fails `CreatePVC`/`RolloutDiskToPVC` a chosen number of times
with a chosen typed error. See `fault-injector/README.md`.
