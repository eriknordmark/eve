# appvol-allprs-stress — multi-PR integration branch

Integration vehicle: it combines still-open PRs so one build and test run sees
their combined diff. **Never PR'd upstream.**

Base: `upstream/master` @ `6fe649bd8`. Rebuilt 2026-08-13.

Content is the matching `appvol-allprs` line plus the stress fault-injection
gates (fork#7), which is what makes this the branch the watchdog soaks run on.

## Included

| source | ref | tip when replayed | role |
|--------|-----|-----|------|
| lf-edge/eve#6190 | `eriknordmark:fault-inject-volume-delete` | `5213b7986` | build-tagged CSI volume-delete fault injection |
| lf-edge/eve#6197 | `eriknordmark:kubelet-mount-wedge-detector` | `a7a7d7873` | zedkube: detect and recover stuck kubelet volume mounts |
| lf-edge/eve#6240 | `eriknordmark:evek-storage-readiness` | `5a1d2a82a` | ignore stray longhorn-system daemonsets; report VolumeMgrStatus.Initialized truthfully with an UnmetCondition; diag surfaces it; gate cluster-storage readiness on a running Longhorn instance-manager |
| lf-edge/eve#6257 | `andrewd-zededa:eve-k-purge-lost-delete` | `b594693b5` | purge leaving stale VMIRS generations and volume refs. **Only 2 of its 3 commits are replayed** — see "The #6257 / #6267 purge-test fork" below |
| lf-edge/eve#6271 | `eriknordmark:kubevirt-graceful-stop` | `b4b27231a` | draft: VMIRS/domain delete-path fixes. **Only its four `kubevirt:` commits are replayed** — see below |
| lf-edge/eve#6298 | `eriknordmark:zedagent-maintmode-config-refetch` | `49ec898e9` | zedagent re-reads the configuration after maintenance mode clears |
| lf-edge/eve#6267 | `eriknordmark:appvol-verify` | `b506ec194` | WIP: kvm→k boot-disk conversion tests + volverify app. Its tail commit is `evetest/`-only, so it does **not** invalidate an already-built image |
| fork#7 | `resize-watchdog-stress` | `e303cf4de` | DO NOT MERGE. The conversion chain (#6036 + #6063 + the volmig commits, via fork#6 `87fca3ec4`) plus the `--no-pet` escalating watchdog in storage-init, watchdog-during-GPT-write chaos instrumentation in storage-resizer, and the pins that ship the `-tags chaos` binary |

Each PR is replayed as its own commits rather than merged at its tip, so no
unrelated master history rides along.

**#5971 and #6291 are gone from this list because they merged upstream** (2026-08-09
and 2026-08-10); their content now arrives with the base. Do not re-add them —
`git cherry` reports every one of their commits as already present.

## Branch-local (never upstream)

| change | why |
|--------|-----|
| Makefile: rootfs cap 295MB | the integration content exceeds the upstream 290 cap; still under the 300MB pre-10.2.0 hard limit. Arrives with fork#7 |
| `volumemgr: reclaim orphaned PVCs on EVE-k` (`gcPVCs`) | **no PR carries this any more.** #6257 moved its PVC-reclaim work to a phase 2 and its current head has none; #6240 deliberately never had it. The branch keeps it because the soaks depend on it |
| baseosmgr: allow shrink with volumes (TEST) | test-only relaxation of the shrink gate |
| pillar: recreate app volumes torn by the resize | |
| baseosmgr: allow keeping a corrupt volume for analysis | |
| zedagent: keep apps stopped while a conversion verifies volumes | |
| baseosmgr: log post-resize volume verify coverage | |
| storage-resizer: weight stress watchdog to shrink | stress-line only |
| built with `FAULT_INJECTION=y` | the fault gates are compiled in only when the top Makefile sees the variable **non-empty**; omitting it passes nothing and yields a normal image that builds and runs identically, so a missed arm is silent. Drive it with `watch-build-package.sh --fault-injection` — never `FAULT_INJECTION=n`, which arms it just as `y` does. The generated `pkg/pillar/lk-build-arg-FAULT_INJECTION` is untracked on purpose: it is what makes the pillar tag `...-dirty-<hash>` rather than collide with the clean image's cache slot |

## The #6257 / #6267 purge-test fork

#6257 and #6267 each carry a version of `evetest: add purge tests for the kube
path`, and they are **mutually exclusive**: 23 helper functions
(`purgeCounter`, `assertPurgeCompleted`, `assertNoOrphanedPVCs`, …) are defined
in both under different filenames, so taking both does not compile.

This branch takes **#6267's** fork — `purge_assertions_test.go`,
`purge_baseline_test.go`, `purge_during_failover_test.go`, `fixtures_test.go`,
`deviceaccess_test.go` — because #6267's own `8d6499c32` extends those files.
#6257's competing `evetest: add purge tests for the kube path` (`20fb99f9c`) is
therefore **not replayed**; its other two commits are.

Separately, the #6257 → #6271 containment chain is broken: #6271 still carries a
stale copy of #6257 as its base. Only #6271's four `kubevirt:` commits are
replayed. Its `pillar: keep purges…` is patch-identical to #6257's
(`00b0bb8c9149`), so nothing is lost by taking #6257's.

## Notes

- `TestCreateReplicaPodConfig` fails under a non-root `go test` on every branch and
  on master alike — it writes to the real `/run/.kube`. See lf-edge/eve#6290.

- #6257 was force-pushed twice on 2026-08-13 (`07d69268d` then `b594693b5`).
  `refs/pull/6257/head` on lf-edge lagged behind the PR's real head for a while;
  when they disagree, trust `gh pr view --json headRefOid`.

## Before building or testing from this branch

Re-verify it still tracks the current PR tips; they move. Recipe in the
`allprs-branch-workflow` skill: compare each PR's current tip by content with
`git diff --name-status <tip> -- $(git diff --name-only <base> <tip>)`. Never
hand-roll that with `git hash-object` — it reports every file a PR deletes as drift.
