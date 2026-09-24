# appvol-allprs

App-volume line of `resize-allprs`: the conversion chain and robustness
set, plus the work that lets an app's data volume survive the boot-disk
repartition and the evetest coverage that proves it. It is never PR'd
upstream.

Base: `upstream/master` `adb213cdf`, plus 64 commits.

`appvol-allprs-stress` is the parallel fault-injection line, built on
`resize-allprs-stress` with an identical PR contribution.

## Inherited from resize-allprs

The conversion chain (#6530 `6a230c123` → #6063 `b530b9c36` →
eriknordmark/eve#6 `776632278`), the robustness set (#6442, #6406,
#6478, #6589, #6602, #6644) and two branch-local #6406 adaptations —
40 commits. `RESIZE-ALLPRS-README.md` on that branch carries the per-PR
table and the excluded list, including why #6451 is out.

## App-volume layer

Branch-local, not in any PR. A replay selects PR content by
construction, so these are invisible to it and have to be re-applied by
hand on every rebuild.

| commit | what it does |
|---|---|
| `baseosmgr: allow shrink with volumes (TEST)` | drops the refusal so a shrink with app volumes present can be exercised at all |
| `pillar: recreate app volumes torn by the resize` | the `volmanifest` package and its use across baseosmgr/nodeagent |
| `baseosmgr: allow keeping a corrupt volume for analysis` | keeps a failed volume instead of deleting the evidence |
| `zedagent: keep apps stopped while a conversion verifies volumes` | holds app activation until the post-resize verify finishes |
| `baseosmgr: log post-resize volume verify coverage` | reports what the verify actually covered |

The first is a test relaxation and must not reach a PR.

## Test PRs

| PR | head | commits | title |
|---|---|---|---|
| #6267 | `c9bedfbfe` | 16 | evetest: kvm→k boot-disk conversion tests + volverify app |
| #6642 | `c22e17681` | 1 | evetest: count reboots from RestartCounter |
| #6347 | `d1ec8a290` | 1 | evetest: port eden shutdown_test.txt to TestDeviceShutdownAndRecovery |

#6642 collides with #6267's `evetest infra: opt out of reboot
accounting` in `evetest/setup.go`: both rewrite the post-reuse reboot
expectation. The resolution keeps #6267's `rebootAccountingOff` reset
and #6642's `pendingReboot` condition, which is derived from
`RestartCounter` rather than from boot time alone.

## storage-resizer pin

Inherited unchanged from `resize-allprs`:
`lfedge/eve-storage-resizer:af6f8349494145cec64da95bb4399bc8c39f68ea`.
The app-volume layer does not touch `pkg/storage-resizer/`, so the hash
is the same on both branches. `appvol-allprs-stress` carries the chaos
build's own value.

## Verification

On this tip, from a clean tree:

- `gofmt -l` over the branch's non-vendor `.go` files: clean
- `pkg/pillar`: `go vet -tags k ./...` and `-tags k,faultinjection` rc=0
- `evetest`: `GOWORK=off go vet . ./tests/...` rc=0
- `make check-docker-hashes-consistency` rc=0, once the gitignored
  generated Dockerfiles left over from earlier builds are excluded
- `go test -tags k,faultinjection` over `cmd/{volumemgr,zedmanager,zedkube,baseosmgr,vaultmgr,nodeagent}`,
  `kubeapi`, `vault`, `volmanifest`, `diskconvert`, `types`,
  `hypervisor`: pass, except `TestCreateReplicaPodConfig`, which fails
  `mkdir /run/.kube: permission denied` under a non-root `go test` on
  master too (#6290)
