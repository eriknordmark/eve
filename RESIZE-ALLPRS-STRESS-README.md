# resize-allprs-stress — resize-allprs + watchdog fault-injection

Integration vehicle for testing the EVE-kvm↔EVE-k boot-disk resize/conversion
work together with its dependency chain and the relevant independent PRs. **Not
for upstream merge** — this branch merges open PRs so a build/test sees the
combined diff.

Base: `upstream/master` @ `47cc591da`.

## Included (resize chain up to and including fork#4)

| PR | branch | head SHA at merge | role |
|----|--------|-------------------|------|
| lf-edge/eve#6063 | kvm-to-k-resize | e18f31fd0 | boot-disk repartition feature (vendors eve-api#149) |
| lf-edge/eve#6036 | kvm-k-baseos-upgrade-blob-reuse | c7670f80c | kvm↔k BaseOs upgrade w/ blob reuse + ZFS vault fs→zvol migration |
| fork#4 | baseosmgr-grow-only-volumes | 5e72b8bb4 | gate cross-flavor update on shrink (base of this branch; contains #6063) |

## Included (independent, conversion-relevant)

| PR | branch | head SHA at merge | role |
|----|--------|-------------------|------|
| lf-edge/eve#6085 | volumemgr-persist-deferred-content-delete | 022ae3401 | persist deferred content-tree deletes (blob reuse across conversion reboot) |
| lf-edge/eve#6121 | volumemgr-cdi-retry | e15b4d05c | CDI-retry cluster-storage volume-create robustness |
| lf-edge/eve#6108 | longhorn-preflight-checks | c1d80badb | surface insufficient disk/memory for Longhorn |
| lf-edge/eve#6120 | domainmgr-reap-shutdown-5916 | 373b25040 | promptly reap powered-off kvm domains (#5916) |
| lf-edge/eve#6068 | hw-watchdog-bootreason | ee96ff5b3 | detect hardware watchdog resets as a boot reason |
| lf-edge/eve#6122 | controllercerts-bak-on-create | 7eedd7d28 | create controllercerts.bak on first save |

## Not included (by design)

- fork#7 (`resize-watchdog-stress`) — lives only in the sibling `resize-allprs-stress` branch.
- fork#6 (`kvm-to-k-volmig`), fork#8 (`resize-bench-harness`) — different leaf / dev harness.
- eve-api#149 rides in vendored via #6063 (hand-carried `info.pb.go` until it merges).

## Conflict resolutions during assembly

Merging #6036 collided on the 4 shared base commits it duplicates with #6063:

- `upgradeconverter/containerd_namespace.go` — took #6036 (adds `stat` error handling).
- `volumemgr/handlevolume.go` — took #6036 (`restartCount == 1` guard on SignalRestarted).
- `baseosmgr/handlebaseos.go` — kept HEAD (fork#4 gate-on-shrink logic supersedes #6036's simple block-when-volumes-exist).

## Additionally included

| PR | branch | role |
|----|--------|------|
| fork#7 | resize-watchdog-stress | [DO NOT MERGE] storage-init no-pet fault-injection stress watchdog |
