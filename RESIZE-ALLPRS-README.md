# appvol-allprs-stress — multi-PR integration branch (fault-injection line)

Integration vehicle: it combines still-open PRs so one build and test run sees
their combined diff. **Never PR'd upstream.**

Base: the `resize-allprs-stress` branch (`upstream/master` @ `1ddb9ac3a` + 38
commits), with 27 commits of app-volume work on top.

This is the fault-injection line. `appvol-allprs` is the **parallel** branch
with the same app-volume contribution on top of `resize-allprs-stress`'s
non-stress twin; the two are siblings off sibling bases, not stacked. Verify
parity by content, never by SHA.

## Inherited from resize-allprs-stress

The conversion chain, the fault-injection harness and the robustness set arrive
with the base — fork#6, fork#7, #6271, #6442, #6406, #6478's two unique evetest
commits and #6280 — as does that branch's own branch-local commit. See its
`RESIZE-ALLPRS-README.md`. Nothing here re-applies any of it.

## Added on top

| source | ref | tip when replayed | role |
|--------|-----|-----|------|
| lf-edge/eve#6267 | `eriknordmark:appvol-verify` | `606de462e` | WIP draft: kvm→k boot-disk conversion tests + the volverify data-volume app. All 20 of its commits are replayed |

Each PR is replayed as its own commits rather than merged at its tip, so no
unrelated master history rides along.

#6267 has been rebased onto current master, so the five commits earlier
assemblies had to exclude — two that merged upstream, one content-identical to
the base, and two superseded by master's reorganization of
`evetest/tests/apps/` into `*_helpers_test.go` — are no longer on its tip. The
whole tip replays, and the `evetest/broker/provider/qemu.go` conflict earlier
assemblies had to resolve by hand is gone with it: the PR now carries master's
`q35,kernel-irqchip=split` machine line alongside its own watchdog args.

## Branch-local (never upstream)

| change | why |
|--------|-----|
| `baseosmgr: allow shrink with volumes (TEST)` | **TEST ONLY, must never merge.** Fault injection for the app-volume corruption soak: production refuses a cross-flavor conversion that would shrink `/persist` while app volumes exist, and this relaxes that gate so the shrink proceeds, logging a WARNING that names the at-risk volumes. The "cannot determine the decision" case still blocks, and so does the EVE-k→kvm direction, which is refused whether or not volumes exist |
| `baseosmgr: allow keeping a corrupt volume for analysis` | Marker-gated on `/persist/volmanifest-keep-corrupt`: renames a mismatching volume aside with a `.corrupt` suffix instead of unlinking it, keeping it in the same fscrypt directory so it stays a rename rather than a multi-GiB copy. The marker is absent in the field, leaving the delete unchanged |
| `pillar: recreate app volumes torn by the resize` | The manifest mechanism: nodeagent records a sha256 manifest of the vault and clear volume directories once the app domains are halted, and baseosmgr verifies each volume on the post-resize boot and removes any not provably intact. New `pkg/pillar/volmanifest` package |
| `zedagent: keep apps stopped while a conversion verifies volumes` | Some conversion reboots reach userspace on the pre-conversion flavor; starting an app mounts its volume read-write, ext4 rewrites the superblock, and the whole-file hash then condemns a volume nothing damaged |
| `baseosmgr: log post-resize volume verify coverage` | Reports how many objects the check examined next to how many it condemned, so a clean run is distinguishable from one that measured nothing |
| `storage-resizer: weight stress watchdog to shrink` | Stress-only. The no-pet ladder ramped evenly from 5s to 300s, putting only two rungs under the ~130s shrink; the first eight rungs now sit below 65s so their resets land inside the shrink, which is the only step that relocates data and so the only one that can tear an app volume. The last two stay long enough to clear shrink+grow. This is what makes this line's `pkg/storage-resizer` pin differ from `resize-allprs-stress`'s |

The `gcPVCs` reclaim is **not** branch-local here — #6406 carries it at identical
content. Do not re-apply it. No rootfs-cap change is needed either: master's
291 MiB ceiling applies (the check multiplies `ROOTFS_MAXSIZE_MB` by 1024*1024)
and the kvm rootfs measures 274.8 MiB, 16.2 MiB under it.

The shrink weighting changes `pkg/storage-resizer`, so its content-hash pin is
recomputed on this line: `50eec94a0db9fb5c7d49372d1eed47c7d1d6a8a4`, against the
stress base's `bc26c95c…` and the non-stress line's `8ecc2447…`. Recompute with
`build-tools/bin/linuxkit pkg show-tag pkg/storage-resizer` rather than copying
across branches. Both `pkg/pillar/Dockerfile` and `pkg/storage-init/Dockerfile`
pin it.

Build with `FAULT_INJECTION=y`; leaving the flag off passes nothing, and
`FAULT_INJECTION=n` arms the gates exactly as `y` does.

## Notes

- `TestCreateReplicaPodConfig` fails under a non-root `go test` on every branch
  and on master alike — it writes to the real `/run/.kube`. See lf-edge/eve#6290.
- `GOWORK=off go build ./...` under `evetest/` fails on this host for want of
  libvirt development headers, on every branch and on master alike. The
  documented check is `GOWORK=off go vet ./tests/...`.
- `go build ./...` does not compile the EVE-k paths; use `-tags k`.
