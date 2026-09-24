# resize-allprs-stress

Fault-injection line of `resize-allprs`: the same conversion chain and
the same robustness set, built on fork#7's stress layer so the offline
boot-disk resize can be interrupted on purpose. It is never PR'd
upstream.

Base: `upstream/master` `adb213cdf`, plus 46 commits.

`resize-allprs` is the parallel line, not a parent — the two share an
identical PR contribution and differ only on the stress surface below.
`appvol-allprs-stress` stacks the app-volume work on this branch.

## Conversion chain

The chain is rebased onto this branch's base, so fork#7's tip
`8c1dfe534` is an ancestor here rather than a set of cherry-picks. Each
PR builds on the one above it.

| PR | head | title |
|---|---|---|
| #6530 | `6a230c123` | vault: clean up after a failed kvm→EVE-k vault migration |
| #6063 | `b530b9c36` | Boot-disk repartition for in-field EVE-kvm → EVE-k conversion |
| eriknordmark/eve#6 | `776632278` | kvm↔k conversion: allow and preserve app volumes |
| eriknordmark/eve#7 | `8c1dfe534` | resize watchdog fault-injection stress test |

27 commits.

## Robustness set

Open PRs touching what the conversion exercises — domainmgr, kubevirt,
k3s, Longhorn, CSI. Replayed as cherry-picks onto the chain, identical
to `resize-allprs`.

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

Not in any PR, and invisible to a replay, which selects PR content by
construction. Both belong on #6406 once it rebases onto current master.

- `volumemgr: adapt #6406's test to the pointer-returning initStatusCtx`
- `evetest: reconcile #6406's and #6478's kubectl`

## The stress surface

Everything this branch carries beyond `resize-allprs`. A rebuild that
produces any other difference has gone wrong.

| file | what fork#7 does to it |
|---|---|
| `pkg/storage-resizer/watchdog.go`, `pkg/storage-resizer/vendor/github.com/diskfs/partitionresizer/delaybackend_chaos.go` | delay a GPT write so the watchdog fires mid-resize |
| `pkg/storage-init/storage-resize.sh` | stop petting the watchdog under fault injection |
| `pkg/storage-resizer/Dockerfile` | build the resizer with `-tags chaos` |
| `pkg/pillar/Dockerfile`, `pkg/storage-init/Dockerfile` | pin the chaos build |
| `RESIZE-ALLPRS-STRESS-README.md` | this file |

## Building

`FAULT_INJECTION=y` on every build of this branch — the stock binary
makes the GPT-write delay a silent no-op. The Makefile tests the
variable for being non-empty rather than for `y`, so `FAULT_INJECTION=n`
turns it *on*; leave it unset to disable.

That build arg is written to an untracked `pkg/pillar/lk-build-arg-FAULT_INJECTION`,
which dirties the tree and adds `-dirty` to the pillar package tag. Remove
it before computing any package hash.

## storage-resizer pin

`pkg/pillar/Dockerfile` and `pkg/storage-init/Dockerfile` both pin
`lfedge/eve-storage-resizer:745e7ec921718d35fa2a11cc6393a639ee3bf732`,
the chaos build. `resize-allprs` computes a different value
(`af6f8349494145cec64da95bb4399bc8c39f68ea`) because it lacks the chaos
instrumentation. Recompute with `build-tools/bin/linuxkit pkg show-tag
pkg/storage-resizer` on a clean tree; a dirty tree yields a `-dirty-…`
suffix rather than the real hash.

## Deliberately excluded

Identical to `resize-allprs` — see `RESIZE-ALLPRS-README.md` on that
branch for the table and the reason per PR.

## Verification

On this tip, from a clean tree:

- `git diff --name-status resize-allprs HEAD` lists only the stress
  surface above
- `gofmt -l` over the branch's non-vendor `.go` files: clean
- `pkg/pillar`: `go vet ./...` with no tags, `-tags k`, `-tags
  faultinjection`, `-tags k,faultinjection` — all rc=0
- `evetest`: `GOWORK=off go vet . ./tests/...` rc=0
- `make check-docker-hashes-consistency` rc=0, once the gitignored
  generated Dockerfiles left over from earlier builds are excluded
- `go test -tags k,faultinjection` over `cmd/{volumemgr,zedmanager,zedkube,baseosmgr,vaultmgr,nodeagent}`,
  `kubeapi`, `vault`, `diskconvert`, `types`, `hypervisor`: pass, except
  `TestCreateReplicaPodConfig`, which fails `mkdir /run/.kube: permission
  denied` under a non-root `go test` on master too (#6290)
- `pkg/storage-resizer`: `go test ./...` rc=0
