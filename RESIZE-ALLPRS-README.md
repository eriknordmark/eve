# newgo-allprs3 — multi-PR integration branch

Integration vehicle: it combines still-open PRs so one build and test run sees
their combined diff. **Never PR'd upstream.**

Base: `upstream/master` @ `38e378177`. 40 replayed commits plus the branch-local
Makefile cap.

## Why this branch exists

It carries the **smaller #6257** — the version after its PVC-reclaim work moved
to a phase 2 — so a stress/soak run can show whether that reclaim is needed in
practice. The pair to compare against is `newgo-allprs2*`, which carries the
larger #6257 and therefore `gcPVCs`.

The one intended difference from `newgo-allprs2` is five files:

| absent here | what it is |
|-------------|------------|
| `pkg/pillar/cmd/volumemgr/initialvolumestatus.go` | `gcPVCs` / `volumesToReap` |
| `pkg/pillar/cmd/volumemgr/initialvolumestatus_test.go` | their unit tests |
| `pkg/pillar/cmd/volumemgr/volumemgr.go` (the GC wiring) | the periodic-GC call |
| `evetest/tests/apps/longhorn_provisioner_workaround_test.go` | CSI-provisioner stall mitigation |
| `evetest/tests/apps/purge_during_failover_test.go` | `TestVMAppPurgeDuringFailover` |

`sweepStaleGenerations` **is** present, so this branch deletes a stale purge
generation's VMIRS and pod while leaving its PVC behind. That is the behavior
under test, not a defect in the assembly.

## Included

| source | ref | tip when written | role |
|--------|-----|-----|------|
| lf-edge/eve#6197 | `eriknordmark:kubelet-mount-wedge-detector` | `a7a7d7873` | zedkube: detect and recover stuck kubelet volume mounts |
| lf-edge/eve#6240 | `eriknordmark:evek-storage-readiness` | `5a1d2a82a` | ignore stray longhorn-system daemonsets; report VolumeMgrStatus.Initialized truthfully with an UnmetCondition; diag surfaces it; gate cluster-storage readiness on a running Longhorn instance-manager |
| lf-edge/eve#6190 | `eriknordmark:fault-inject-volume-delete` | `5213b7986` | build-tagged CSI volume-delete fault injection |
| lf-edge/eve#6257 | `andrewd-zededa:eve-k-purge-lost-delete` | `c2c2a0ff8` | purge leaving stale VMIRS generations; the 2-commit form, phase-2 work excluded |
| lf-edge/eve#6271 | `eriknordmark:kubevirt-graceful-stop` | `0282bd830` | **its four `kubevirt:` commits only** — VMIRS/domain delete-path fixes |
| fork#6 | `kvm-to-k-volmig` | `87fca3ec4` | the conversion chain: #6036 + #6063 + the volmig commits |

Each PR is replayed as its own commits rather than merged at its tip, so no
unrelated master history rides along.

#6271 is **not** replayed whole. It still contains the older, larger #6257 as
its base, so replaying its tip would reintroduce exactly the phase-2 code this
branch exists to exclude. Only its own four `kubevirt:` commits are taken, on
top of #6257's current head. `allprs-apply-base-segment.sh` reports the
`6257 -> 6271` chain as BROKEN for this reason; here that break is deliberate.

## Not included — already upstream

| source | landed |
|--------|--------|
| lf-edge/eve#5971 (`pkg/kube`: cluster-init.sh → Go daemon) | merged 2026-08-09; all 35 commits present in master by patch-id |
| lf-edge/eve#6291 (evetest blank-volume encryption) | merged 2026-08-10 |

Because #5971 is in master, the shell library files under `pkg/kube/` are gone
from the baseline, so every branch now needs the Go form of any `pkg/kube`
change — there is no longer a shell-vs-Go split between this line and
`resize-allprs*`.

## Branch-local (never upstream)

| change | why |
|--------|-----|
| Makefile: raise integration rootfs cap to 295MB | the integration content exceeds the upstream 290 cap; still under the 300MB pre-10.2.0 hard limit |

`newgo-allprs2`'s other branch-local — adapting #6257's volumemgr test to
#6240's pointer-returning `initStatusCtx` — is not needed here: the test file it
patched belongs to the phase-2 reclaim commit and is absent.

## Testing this branch

**The orphan-PVC detector is disabled in the code this branch carries.**
`purge_after_power_cycle_test.go` has `assertNoOrphanedPVCs` commented out on the
KubeVirt path, and `assertKubePurgeEndState` deliberately does not check PVC
reclaim, so the only live call is in `purge_baseline_test.go` — a healthy purge,
which does not go through `sweepStaleGenerations`.

A soak run against this branch's own evetest tree therefore reports green whether
or not PVCs leak. To get a falsifiable result, **supply the tests from the
`newgo-allprs2` lineage**, whose `purge_after_power_cycle_test.go` has the
assertion armed. evetest is host-side, so pairing an allprs2 test tree with an
allprs3 image is a one-variable comparison: `gcPVCs` present vs absent.

`TestCreateReplicaPodConfig` fails under a non-root `go test` on this branch, on
`newgo-allprs2`, and on master alike — it writes to the real `/run/.kube`. See
lf-edge/eve#6290.

## Before building or testing from this branch

Re-verify it still tracks the current PR tips; they move. Recipe in the
`allprs-branch-workflow` skill: compare each PR's current tip by content with
`git diff --name-status <tip> -- $(git diff --name-only <base> <tip>)`. Never
hand-roll that with `git hash-object` — it reports every file a PR deletes as
drift.
