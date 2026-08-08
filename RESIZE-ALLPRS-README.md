# newgo-allprs2 — multi-PR integration branch

Integration vehicle: it combines still-open PRs so one build and test run sees
their combined diff. **Never PR'd upstream.**

Base: `upstream/master` @ `664756969`. Head `53e36f5d8`, 79 commits on top.

## Included

| source | ref | tip when written | role |
|--------|-----|-----|------|
| lf-edge/eve#5971 | `rucoder:rucoder/kube-init-go` | `d6eff7335` | ports cluster-init.sh to a Go kube-init daemon under pkg/kube/kube-init. Deletes the shell library files |
| lf-edge/eve#6197 | `eriknordmark:kubelet-mount-wedge-detector` | `b210464ad` | zedkube: detect and recover stuck kubelet volume mounts |
| lf-edge/eve#6240 | `eriknordmark:evek-storage-readiness` | `cad6c2c69` | ignore stray longhorn-system daemonsets; report VolumeMgrStatus.Initialized truthfully with an UnmetCondition; diag surfaces it; gate cluster-storage readiness on a running Longhorn instance-manager |
| lf-edge/eve#6190 | `eriknordmark:fault-inject-volume-delete` | `5213b7986` | build-tagged CSI volume-delete fault injection |
| lf-edge/eve#6271 | `eriknordmark:kubevirt-graceful-stop` | `5fafb5335` | VMIRS/domain delete-path fixes |
| lf-edge/eve#6257 | `andrewd-zededa:eve-k-purge-lost-delete` | `480a04296` | draft: purge leaving stale VMIRS generations and volume refs |
| lf-edge/eve#6291 | `eriknordmark:evetest-blank-volume-cleartext` | `090c40275` | draft: make blank-volume encryption an explicit AddBlankVolume parameter |
| fork#6 | `kvm-to-k-volmig` | `87fca3ec4` | the conversion chain: #6036 + #6063 + the volmig commits |

Each PR is replayed as its own commits rather than merged at its tip, so no
unrelated master history rides along.

The tips above are what each PR pointed at when this file was written, which is
not a claim about what the branch carries — several were assembled from earlier
tips and #6197 and #6190 have moved since. Verify by content before building.

## Branch-local (never upstream)

| change | why |
|--------|-----|
| Makefile: raise integration rootfs cap to 295MB | the integration content exceeds the upstream 290 cap; still under the 300MB pre-10.2.0 hard limit |
| volumemgr: adapt #6257's test to the pointer-returning helper | #6240 makes initStatusCtx return *volumemgrContext so the subscriptions it registers and the caller share one context. #6257's new test is written against master, where the helper returns a value, so it takes the address at every call site. Belongs in #6257 once it rebases onto #6240 |

## Notes

- #6259 is closed unmerged; its instance-manager gate lives in #6240, which also
  carries the `stubInstanceManagerGate` helper its daemonset tests need.

- #5971 deletes `pkg/kube/*.sh`, so #6240's `longhorn-utils.sh` hunk is not here;
  the equivalent gate lives in the Go kube-init code.

- `TestCreateReplicaPodConfig` fails under a non-root `go test` on every branch and
  on master alike — it writes to the real `/run/.kube`. See lf-edge/eve#6290.

## Before building or testing from this branch

Re-verify it still tracks the current PR tips; they move. Recipe in the
`allprs-branch-workflow` skill: compare each PR's current tip by content with
`git diff --name-status <tip> -- $(git diff --name-only <base> <tip>)`. Never
hand-roll that with `git hash-object` — it reports every file a PR deletes as drift.
