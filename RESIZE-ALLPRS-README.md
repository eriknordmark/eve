# resize-allprs — multi-PR integration branch

Integration vehicle: it combines still-open PRs so one build and test run sees
their combined diff. **Never PR'd upstream.**

Base: `upstream/master` @ `3440b3311`, 52 commits on top.

## Included

| source | ref | tip when written | role |
|--------|-----|-----|------|
| fork#6 | `kvm-to-k-volmig` | `feb92426e` | the conversion chain: #6530 + #6063 + the volmig commits |
| lf-edge/eve#6442 | `andrewd-zededa:eve-k-purge-pvc-partial-annotation` | `7ef13c34b` | a stale volume ref a domain still holds no longer deadlocks the purge |
| lf-edge/eve#6406 | `andrewd-zededa:eve-k-purge-cleanup-part2` | `679921bd4` | the `gcPVCs` reclaim of a swept stale generation's PVC, and its evetest coverage |
| lf-edge/eve#6478 | `eriknordmark:purge-during-failover` | `dac605926` | purge with the designated node down, the kubectl framework promotion it rests on, and its Longhorn CSI workaround |
| lf-edge/eve#6505 | `andrewd-zededa:eve-k-backup-dnid` | `e4688995f` | a healthy peer acts for a downed node's designated app: the eve-app-op lease, the node-health cache and threshold, the kubevirt/volumemgr work that lets the peer actually build and delete, and its evetest coverage |
| lf-edge/eve#6451 | `eriknordmark:vault-mode-recovery` | `386955027` | vaultmgr recovers a vault whose key mode does not match the one the device would derive now |
| lf-edge/eve#6590 | `rene:fix-sriov-pr` | `b06957a35` | the SR-IOV device plugin is deployed only when network VFs exist, and one wedged system pod no longer pins the node out of RUNNING |
| lf-edge/eve#6599 | `eriknordmark:kube-init-k3s-deadlines` | `89c8283e9` | k3s start deadlines raised to 30 minutes |

fork#6 stacks the two conversion PRs, so they are not replayed separately:

| PR | ref | tip |
|----|-----|-----|
| lf-edge/eve#6530 | `eriknordmark:vault-zvol-migration-cleanup` | `263af5b10` |
| lf-edge/eve#6063 | `eriknordmark:kvm-to-k-resize` | `65b51421a` |

Each PR is replayed as its own commits rather than merged at its tip, so no
unrelated master history rides along.

**#6529 `eve-k-halted-state` is here as #6505's second commit**, not replayed
separately. #6505 carries the same change one refactor further on — the pod
lookup behind `dependentsPresent` is extracted into `anyPodMatches`, and
`TestDependentsPresent` is folded into the surrounding table tests. Replaying
#6529 on top would apply the halted-state change twice.

The base carries #6036, #6271, #6280, #6240, #6443, #6314, #6441, #6453, #6489
(master's rootfs ceiling is now 290 MiB), and the merged evetest work of #6539
and #6540. `git cherry upstream/master` scores all of them `-`; replaying any
of them re-applies content master already has.

## Where two PRs collide

**#6406 and #6478 each promote the tests/apps kubectl plumbing into
`EdgeDevice`, with incompatible shapes.** #6406 bakes the app namespace into
`RunKubectl` and brings a `KubeItemList` rich enough for a PVC's phase; #6478
takes an explicit namespace and a per-call timeout, which its Longhorn
workaround needs to reach `longhorn-system`. This branch keeps #6478's runner
as the only one and moves `KubectlListItems` onto it. Both PRs also pre-date
master's `VolumeGenerationPolicy` argument on `PurgeApplication`, so their two
cluster call sites pass `BumpVolumeGeneration`.

**#6478 and #6505 each declare `dnidOutageThresholdKey`** in `tests/cluster`;
#6505's declaration is kept.

**#6451 and #6530 both rewrite vaultmgr's startup key-mode block.** #6451
replaces `checkAndPublishVaultConfig` with `vaultKeyMode` / `vaultSupported` /
`keyDerivationOf` while #6530 adds `CurrentPartitionCommitted` to the handler
options; the merged form keeps both.

**#6406 and the base collide additively in `purge_helpers_test.go`**
(`fastStorageReclaimTimeout` against the `deviceRebootTimeout` /
`postRebootEndStateTimeout` pair #6280 landed) — both sides are kept.

## Branch-local (never upstream)

| change | why |
|--------|-----|
| `volumemgr: adapt #6406's test to the pointer-returning initStatusCtx` | #6406's reclaim test takes the context's address at 11 sites while `initStatusCtx` returns `*volumemgrContext`, so `cmd/volumemgr` does not compile. Belongs on #6406 once it rebases |
| `evetest: reconcile #6406's and #6478's kubectl` | the collisions above. Belongs on whichever of the two rebases last |

This line carries no rootfs-cap change; master's 290 MiB ceiling applies. Note
the check multiplies `ROOTFS_MAXSIZE_MB` by 1024*1024, so the ceiling is MiB.

## Notes

- `pkg/storage-resizer` is content-hash pinned by **both** `pkg/pillar/Dockerfile`
  and `pkg/storage-init/Dockerfile`; the value here is
  `8ecc244734737f52b97e89a9f2362973bded8059`, inherited from #6063. Recompute with
  `build-tools/bin/linuxkit pkg show-tag pkg/storage-resizer` rather than copying
  it from another branch — the stress line's value differs.
- `TestCreateReplicaPodConfig` fails under a non-root `go test` on every branch
  and on master alike — it writes to the real `/run/.kube`. See lf-edge/eve#6290.
- `go build ./...` does not compile the EVE-k paths; use `-tags k`.
