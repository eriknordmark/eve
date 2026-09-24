# resize-allprs

Integration branch for testing the in-field EVE-kvm ↔ EVE-k boot-disk
conversion together with the EVE-k robustness fixes it has to survive.
It is never PR'd upstream; it exists so one build and one eden run see
the combined diff.

Base: `upstream/master` `adb213cdf`, plus 43 commits.

`resize-allprs-stress` is the parallel line: same PR contribution, built
on the fork#7 fault-injection layer instead of fork#6. `appvol-allprs`
and `appvol-allprs-stress` stack the app-volume work on these two.

## Conversion chain

The chain is rebased onto this branch's base, so fork#6's tip
`776632278` is an ancestor here rather than a set of cherry-picks. Each
PR builds on the one above it, and fork#6 contains all of #6530 and
#6063.

| PR | head | title |
|---|---|---|
| #6530 | `6a230c123` | vault: clean up after a failed kvm→EVE-k vault migration |
| #6063 | `b530b9c36` | Boot-disk repartition for in-field EVE-kvm → EVE-k conversion |
| eriknordmark/eve#6 | `776632278` | kvm↔k conversion: allow and preserve app volumes |

24 commits.

## Robustness set

Open PRs touching what the conversion exercises — domainmgr, kubevirt,
k3s, Longhorn, CSI, vault. Replayed as cherry-picks onto the chain.

| PR | head | commits | title |
|---|---|---|---|
| #6442 | `7ef13c34b` | 2 | zedmanager: do not let a domain-held stale volume ref block a purge |
| #6406 | `679921bd4` | 2 | eve-k volumemgr: reclaim orphaned PVCs after a stale generation is swept |
| #6478 | `3fdcf53ba` | 4 | evetest: purge with the designated node down |
| #6589 | `96101a167` | 2 | domainmgr: fix node reboot on IoBundle not ours |
| #6602 | `f255ae289` | 2 | zedrouter: don't crash on a deleted network instance |
| #6644 | `1a94072b0` | 1 | Give HV=k guests the full poweroff budget |
| #6451 | `1000210ed` | 3 | vaultmgr: recover a wrong vault key mode |

#6451 is based on #6530's tip `6a230c123`, so its three commits apply
onto the chain with nothing to reconcile: it rewrites the vaultmgr
startup block on top of #6530's `CurrentPartitionCommitted` rather than
against it.

## Branch-local commits

Not in any PR. A replay selects PR content by construction, so these are
invisible to it and have to be re-applied by hand on every rebuild.

- `volumemgr: adapt #6406's test to the pointer-returning initStatusCtx` —
  master's `initStatusCtx` returns a pointer, #6406's reclaim test still
  takes its address.
- `evetest: reconcile #6406's and #6478's kubectl` — both PRs add a
  kubectl runner to `EdgeDevice`; #6478's wins and #6406's
  `KubectlListItems` moves onto it. Also brings #6406's call sites up to
  master's `SeparateClusterPort` and `PurgeApplication` signatures.

Both belong on #6406 once it rebases onto current master.

## storage-resizer pin

`pkg/pillar/Dockerfile` and `pkg/storage-init/Dockerfile` both pin
`lfedge/eve-storage-resizer:af6f8349494145cec64da95bb4399bc8c39f68ea`,
owned by #6063's `storage-init,pillar: run storage-resizer during the
offline resize`. The hash follows `pkg/storage-resizer/`, including the
eve-alpine base that #6063's `storage-resizer: boot-disk repartition
tool + offline-resize watchdog` pins — bump one and the other has to be
recomputed, or the build succeeds and the device keeps running the
previous binary.

The value is branch-specific. fork#7 adds chaos instrumentation to the
package, so the stress line computes its own
(`745e7ec921718d35fa2a11cc6393a639ee3bf732`). Recompute with
`build-tools/bin/linuxkit pkg show-tag pkg/storage-resizer` on a clean
tree; a dirty tree yields a `-dirty-…` suffix rather than the real hash.

## Deliberately excluded

Recorded so the next rebuild does not rediscover them as omissions.

| PR | why not |
|---|---|
| #6529 hypervisor/kubevirt: base halted state on the whole workload | in master via #6505. `dependentsPresent`, `confirmedAbsent` and all three call sites are there, and `domainmgr.go` is byte-identical. Only `TestDependentsPresent` is unmerged, and master covers the same truth table through `Info()`. Replaying it reverts master's later `anyPodMatches` refactor. |
| #6600 evetest: list parameters declared through a helper | tooling; changes no runtime behavior |
| #6643 domainmgr: always detach the host framebuffer for an assigned iGPU | iGPU passthrough, unrelated subsystem |
| #6421 kernel: move amd64 to eve-kernel 6.18.46, hwe flavour for HV=k | a kernel change would confound a soak result |
| #6338 kube-images: ship the k8s images with EVE and mount them as EROFS | reshapes the k image layout the conversion measures |
| #6516, #6565 Xen removal | large unrelated diff |
| #6335 SMT/NUMA-aware CPU placement | unrelated subsystem |

## Verification

On this tip, from a clean tree:

- `gofmt -l` over the branch's non-vendor `.go` files: clean
- `pkg/pillar`: `go vet ./...` with no tags, `-tags k`, `-tags
  faultinjection`, `-tags k,faultinjection` — all rc=0
- `evetest`: `GOWORK=off go vet . ./tests/...` rc=0 (the module cannot be
  vetted whole on a host without libvirt headers)
- `make check-docker-hashes-consistency` rc=0, once the gitignored
  generated `pkg/eve/Dockerfile` left over from an earlier build is
  excluded
- `go test -tags k,faultinjection` over `cmd/{volumemgr,zedmanager,zedkube,baseosmgr,vaultmgr,nodeagent}`,
  `kubeapi`, `vault`, `diskconvert`, `types`, `hypervisor`: pass, except
  `TestCreateReplicaPodConfig`, which fails `mkdir /run/.kube: permission
  denied` under a non-root `go test` on master too (#6290) — the branch
  does not touch `kubevirt_test.go`
- `pkg/kube/kube-init`: `go test ./...` rc=0
