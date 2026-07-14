# volumemgr EVE-k volume defer/retry — fault-injection harness (PR #6121)

Isolated tests for the typed-verdict defer/retry machinery in PR #6121: the
permanent-no-retry, transient-retry-converge, and reboot-reverify branches that a
happy-path soak cannot reach. Validated on EVE-k (k3s v1.34.2+k3s1) on 2026-07-13.

## What it checks

| Script            | Invariant |
|-------------------|-----------|
| `01-permanent.sh` | A permanent create failure (403 Forbidden via ResourceQuota) parks the volume with `ClusterStorageTransientErr=false` and is **never retried** (ErrorTime frozen). |
| `02-transient.sh` | A transient failure past the gate (upload backend down) parks with `ClusterStorageTransientErr=true` and **converges** once the fault clears. Convergence-after-restore is the retry proof: an error-parked volume is only ever re-driven by `retryFailedClusterVolumeCreate`. |
| `03-reboot.sh`    | A reboot with an incomplete upload **re-verifies and re-drives** (converges) rather than skipping an empty PVC. Best-effort black-box; use the injector for determinism. |
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
N=4 CYCLES=20 ./stress.sh              # soak
```

Common overrides: `APP`, `IMG`, `CONVERGE_TIMEOUT`, `NORETRY_WINDOW`, `N`, `CYCLES`,
`PHASES` (force a deterministic soak phase set, e.g. `PHASES="phase_transient"`).

## Deterministic alternative

Black-box timing can't reliably force a *bounded* number of transient failures, and
can't make the permanent branch reachable per-volume. For that, `fault-injector/` adds
an env-gated hook (`EVE_KUBE_FAULT`) to `kubeapi` on a **throwaway scratch branch**
(never committed) that fails `CreatePVC`/`RolloutDiskToPVC` a chosen number of times
with a chosen typed error. See `fault-injector/README.md`.
