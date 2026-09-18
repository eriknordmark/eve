# appvol-allprs-stress — multi-PR integration branch

Integration vehicle: it combines still-open PRs so one build and test run sees
their combined diff. **Never PR'd upstream.**

Base: the `resize-allprs-stress` branch (`upstream/master` @ `3440b3311` + 56
commits), with 15 commits of app-volume work on top.

This is the stress line. `appvol-allprs` is the **parallel** branch with the
same app-volume contribution on top of `resize-allprs`; the two are siblings
off sibling bases, not stacked on each other. Build this one with
`FAULT_INJECTION=y`.

## Inherited from resize-allprs-stress

The conversion chain, the fault-injection harness and the robustness set arrive
with the base — fork#7 (#6530 + #6063 + fork#6 + the watchdog chaos commits),
#6442, #6406, #6478, #6505 (which carries #6529), #6451, #6590 and #6599 — as
do that branch's own branch-local `initStatusCtx` adapter and its
reconciliation of #6406's and #6478's kubectl helpers. See its
`RESIZE-ALLPRS-README.md`. Nothing here re-applies any of it.

## Added on top

| source | ref | tip when replayed | role |
|--------|-----|-----|------|
| lf-edge/eve#6267 | `eriknordmark:appvol-verify` | `5858620fb` | kvm→k boot-disk conversion tests, the ZFS-vault migration and power-cut tests, and the volverify data-volume app. All 8 of its commits are replayed. Its base is master past #6539 and #6540, so it carries every evetest change master has |

Each PR is replayed as its own commits rather than merged at its tip, so no
unrelated master history rides along.

## Branch-local (never upstream)

| change | why |
|--------|-----|
| `baseosmgr: allow shrink with volumes (TEST)` | **TEST ONLY, must never merge.** Fault injection for the app-volume corruption soak: production refuses a cross-flavor conversion that would shrink `/persist` while app volumes exist, and this relaxes that gate so the shrink proceeds, logging a WARNING that names the at-risk volumes. The "cannot determine the decision" case still blocks, and so does the EVE-k→kvm direction, which is refused whether or not volumes exist |
| `pillar: recreate app volumes torn by the resize` | The manifest mechanism: nodeagent records a sha256 manifest of the vault and clear volume directories once the app domains are halted, and baseosmgr verifies each volume on the post-resize boot and removes any not provably intact. New `pkg/pillar/volmanifest` package |
| `baseosmgr: allow keeping a corrupt volume for analysis` | Marker-gated on `/persist/volmanifest-keep-corrupt`: renames a mismatching volume aside with a `.corrupt` suffix instead of unlinking it, keeping it in the same fscrypt directory so it stays a rename rather than a multi-GiB copy. The marker is absent in the field, leaving the delete unchanged |
| `zedagent: keep apps stopped while a conversion verifies volumes` | Some conversion reboots reach userspace on the pre-conversion flavor; starting an app mounts its volume read-write, ext4 rewrites the superblock, and the whole-file hash then condemns a volume nothing damaged |
| `baseosmgr: log post-resize volume verify coverage` | Reports how many objects the check examined next to how many it condemned, so a clean run is distinguishable from one that measured nothing |
| `storage-resizer: weight stress watchdog to shrink` | Biases fork#7's fault injection toward the shrink phase, which is the one that can tear an app volume |

No rootfs-cap change is needed: master's 290 MiB ceiling applies (the check
multiplies `ROOTFS_MAXSIZE_MB` by 1024*1024) and the app-volume layer adds no
rootfs content beyond the base's.

The shrink weighting changes `pkg/storage-resizer`, so this line carries its own
content-hash pin, `50eec94a0db9fb5c7d49372d1eed47c7d1d6a8a4`, in **both**
`pkg/pillar/Dockerfile` and `pkg/storage-init/Dockerfile`. Recompute it with
`build-tools/bin/linuxkit pkg show-tag pkg/storage-resizer` on this branch;
never copy a pin from another line.

## Notes

- `TestCreateReplicaPodConfig` fails under a non-root `go test` on every branch
  and on master alike — it writes to the real `/run/.kube`. See lf-edge/eve#6290.
- `GOWORK=off go build ./...` under `evetest/` fails on this host for want of
  libvirt development headers, on every branch and on master alike. The
  documented check is `GOWORK=off go vet ./tests/...`.
- `go build ./...` does not compile the EVE-k paths; use `-tags k`.
