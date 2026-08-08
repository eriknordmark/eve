# newgo-allprs2 — multi-PR integration branch

Integration vehicle for testing the EVE-kvm↔EVE-k boot-disk resize/conversion work
on top of the **Go `kube-init` daemon** (#5971) rather than the shell
`cluster-init.sh`. **Not for upstream merge** — this branch merges open PRs so a
build/test sees the combined diff.

Content is `resize-allprs` plus #5971 and the readiness work on top of it. The
`resize-allprs` line stays shell-based, so the two lines diverge in exactly the
EVE-k cluster bring-up code and their test results must be kept separate.

Base: `upstream/master` @ `992c28d2a` (2026-08-04). Every source PR sits on that same
master, so each merge below contributes only its own commits and drags in no
intervening master history.

## Stack shape

```
master (992c28d2a)
  └─ #6036 ──> #6063 ──> fork#6            (conversion chain; merged as fork#6's tip)
  ├─ #6197   zedkube stuck kubelet mounts  (independent)
  ├─ #6240   stray-DS block + truthful readiness
  ├─ #6259   longhorn instance-manager gate (pillar half only)
  ├─ #6190   build-tagged CSI volume-delete fault injection
  └─ #5971 ──> rucoder/eve#3   Go kube-init daemon + readiness fixes
```

## Included

| PR | branch | head at merge | role |
|----|--------|---------------|------|
| fork#6 | kvm-to-k-volmig | `87fca3ec4` | the whole conversion chain: #6036 (`d18d3bd76`, 5 commits — kvm↔k BaseOs upgrade, blob reuse, ZFS vault fs→zvol) + #6063 (`3a5f10434`, 10 own commits — boot-disk repartition, `diskconvert`, CONVERTING state) + 4 volmig commits (gate on shrink, relocate kvm volumes, migrate to PVC, descheduler on schedulable disk) |
| lf-edge/eve#6197 | kubelet-mount-wedge-detector | `bff6975e4` | zedkube: detect and recover stuck kubelet volume mounts, with server-side pod LIST filtering (2 commits) |
| lf-edge/eve#6240 | evek-storage-readiness | `dc8490cab` | ignore stray `longhorn-system` daemonsets; report `VolumeMgrStatus.Initialized` truthfully with an `UnmetCondition`; diag surfaces it (6 commits) |
| lf-edge/eve#6259 | lh-instance-manager-ready | `041e860e1` | gate cluster-storage readiness on a running Longhorn instance-manager for this node, and widen the ready budget (2 commits). Its `pkg/kube/longhorn-utils.sh` hunk is **not** here: #5971 deletes that script, and the equivalent gate lives in rucoder#3's `kube-init: wait for Longhorn's instance-manager` |
| lf-edge/eve#6190 | fault-inject-volume-delete | `58a60cba0` | build-tagged CSI volume-delete fault injection. **Compiled in more often than intended:** the top Makefile's `lk-extra-opt/%` arms a build arg on non-emptiness, so `FAULT_INJECTION=n` writes the build-arg file exactly as `y` does. Dormant either way — each gate is an `os.Stat` on a `/tmp` marker — so runtime behavior matches a production image. Per-build check: the log should say `FAULT_INJECTION: n` and the `eve-pillar:` tag should carry no `-dirty` |
| lf-edge/eve#6271 | kubevirt-graceful-stop | `484cbdb59` | keep domain bookkeeping until Cleanup; stop ignoring the scheduling lookup error on Delete; name the replicaset in the delete error. Its `log a successful VMIRS delete correctly` commit is **not** here — #6257 fixes that same bug with a three-way `switch`. **If #6257 is ever dropped from this branch, that commit must be restored** (it is on `bak-kubevirt-graceful-stop-20260805` as `8fba79bff`) |
| lf-edge/eve#6257 | andrewd-zededa:eve-k-purge-lost-delete | `df8ae7c12` | draft: purge leaving stale VMIRS generations and volume refs. Derives `Info`'s DomainId from a live VMIRS `Get` rather than `vmiList`, sweeps stale purge generations, and stops zedmanager waiting forever on a volume-ref removal (2 commits). Force-pushes often — three tips in one day |
| lf-edge/eve#5971 | rucoder:rucoder/kube-init-go | `7a501392a` | ports `cluster-init.sh` to a Go `kube-init` daemon under `pkg/kube/kube-init` (25 commits). Deletes the shell library files, including `cluster-init.sh` and `longhorn-utils.sh` |
| rucoder/eve#3 | kubeinit-readiness-snapshot | `2b2aa9c3a` | 5 readiness/robustness commits on top of #5971: bound readiness by progress, wait for Longhorn's instance-manager, log what the progress probe saw, establish the NAD CRD before its instance (the Go port of #6242's race fix), import images on every k3s start |

## Branch-local (never upstream)

| commit | why |
|--------|-----|
| `resize-allprs: stub the instance-manager gate in #6240's daemonset tests` | #6259 makes `checkLonghornReady` call `instanceManagerReady`, which builds a Longhorn client from the on-device kubeconfig; #6240's two positive daemonset tests drive that function with a fake clientset. Neither PR sees this alone — **whichever merges second upstream must carry the stub.** Both PRs carry a comment describing it |
| `resize-allprs: bump kvm rootfs cap to 291MB` | this branch's kvm rootfs lands at ~290.3MB, just over the upstream 290 cap; still far under the 300MB pre-10.2.0 hard limit |
| `kube-init: gate cluster join on valid status` | an `EdgeNodeClusterStatus` that exists but does not yet validate (e.g. empty `ClusterInterface`) wedges k3s mid-transition; the gate stays single-node until `GetClusterStatus()` succeeds. Not proposed on #5971 yet |
| `pkg/kube: fix external-boot-image tarball path` | #5971's port looks for the tarball on the `/images/` volume, but `pkg/kube/Dockerfile` copies it to `/etc/`, so the import silently no-ops and every app stops at BOOTING with `ErrImageNeverPull` |
| `kube-init: wait indefinitely for the etcd zvol` | vaultmgr formats this zvol and on a slow or contended pool its arrival is unbounded; the upstream 10-minute ceiling leaves k3s unstarted with nothing retrying the step. Waits without a deadline, warning every 10 minutes |
| `volumemgr: publish readiness during the wait` | proposed follow-up to #6240, not yet pushed to that PR. Splits `VolumeMgrStatus` publishing out of the disk-metrics task into its own goroutine started before the EVE-k readiness waits, so a node stuck on those waits reports which gate is outstanding instead of publishing nothing for tens of minutes |

## Pinned source PRs (deliberate, not drift)

`#5971` sits at `7a501392a` and `rucoder#3` at `2b2aa9c3a` — the pair the
`0.0.0-newgo-allprs2-dc05be7e` image took the 7-leg matrix 7/7 with. #5971 has since
been force-pushed to `ea4e1c402`: rebased onto a newer master, one new test commit
(`evetest: add a cluster-to-single conversion test`), the `state:` commit split in two,
and the idle-CPU logic relocated into `kube-init/prereqs`. Catching up means rebuilding
the 25-commit segment *and* re-applying rucoder#3 on top, which would change the
bring-up baseline at the same time as the changes under test. Held fixed on purpose so
results stay comparable; revisit when rucoder#3 is itself rebased onto the new #5971.

## Dependency pins

`pkg/storage-resizer` pins `diskfs/partitionresizer v1.0.1-0.20260804072839-b362ce3c37c3`
(main tip `b362ce3`), which pulls `go-diskfs v1.9.5-0.20260803113315-a6099fdbe455` — the
revision carrying the ext4 extent-tree fixes (#412, #420), directory-read error
propagation (#421), and the fat32 zero-length / dot-leading filename fix (#419). No
`replace` directive: both are released pseudo-versions.

`pkg/storage-resizer` is content-hash pinned by **two** consumers — `pkg/pillar/Dockerfile`
and `pkg/storage-init/Dockerfile`, both at `9f59a9b09184ba4e2cd6cf92b8607beb5326be9b`
here. Recompute with `linuxkit pkg show-tag pkg/storage-resizer` **on this branch** after
any change under that directory; never copy a pin from a sibling branch.

## Before you build or test from this branch

Re-verify it still tracks the current PR tips — those PRs keep moving. Recipe in the
`allprs-branch-workflow` skill: `git log --oneline --grep="^Merge #"` for the claimed
set, then compare each PR's current tip by content with
`git diff --name-status <tip> -- $(git diff --name-only <base> <tip>)`. Never hand-roll
that comparison with `git hash-object` — it reports every file a PR *deletes* as drift.
