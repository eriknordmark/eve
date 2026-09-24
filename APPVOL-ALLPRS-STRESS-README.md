# appvol-allprs-stress

Fault-injection line of `appvol-allprs`: the app-volume work and its
evetest coverage, built on `resize-allprs-stress` so the shrink can be
interrupted while an app volume is live. This is the branch the
app-volume corruption soak runs from. It is never PR'd upstream.

Base: `upstream/master` `adb213cdf`, plus 75 commits.

`appvol-allprs` is the parallel line, not a parent — the two share an
identical PR contribution and differ only on the stress surface below.

## Inherited

From `resize-allprs-stress`: the conversion chain including
eriknordmark/eve#7 `8c1dfe534`, the robustness set, and fork#7's stress
layer — see `RESIZE-ALLPRS-STRESS-README.md`, carried on this branch.

From `appvol-allprs`: the five branch-local app-volume commits, the
branch-local `evetest: take the newer resize-fault accounting`, and the
test PRs #6267 `cae2961ec` and #6347 `d1ec8a290`.

## The stress surface

Everything this branch carries beyond `appvol-allprs`. A rebuild that
produces any other difference has gone wrong.

| file | what the stress layer does to it |
|---|---|
| `pkg/storage-resizer/watchdog.go`, `pkg/storage-resizer/watchdog_test.go` | the no-pet ladder, weighted so its resets land inside the shrink |
| `pkg/storage-resizer/vendor/github.com/diskfs/partitionresizer/delaybackend_chaos.go` | delay a GPT write so the watchdog fires mid-resize |
| `pkg/storage-init/storage-resize.sh` | stop petting the watchdog under fault injection |
| `pkg/storage-resizer/Dockerfile` | build the resizer with `-tags chaos` |
| `pkg/pillar/Dockerfile`, `pkg/storage-init/Dockerfile` | pin the chaos build |
| the READMEs | this file and `RESIZE-ALLPRS-STRESS-README.md` in place of the non-stress pair |

## Watchdog weighting

`storage-resizer: weight stress watchdog to shrink` packs the first
eight rungs of the no-pet ladder below 65s, so their resets land inside
the ~130s shrink rather than after it, and leaves the last two long
enough to clear shrink+grow within `RESIZE_MAX_REBOOTS`. The shrink is
the only step that relocates data, so it is the only one that can tear
an app volume; an evenly-ramped ladder put just two rungs under it and
capped the soak's sensitivity regardless of how long it ran.

## storage-resizer pin

`pkg/pillar/Dockerfile` and `pkg/storage-init/Dockerfile` both pin
`lfedge/eve-storage-resizer:086d38048472a4bd69493d432508443285a2d2df`.
This differs from `resize-allprs-stress`'s
`745e7ec921718d35fa2a11cc6393a639ee3bf732` because the weighting commit
changes `watchdog.go`, and from `appvol-allprs`'s
`af6f8349494145cec64da95bb4399bc8c39f68ea`, which has no chaos build at
all. Recompute with `build-tools/bin/linuxkit pkg show-tag
pkg/storage-resizer` on a clean tree, and re-pin both Dockerfiles in the
same commit that changes the package — otherwise the build succeeds and
the device keeps running the previous binary.

## Building

`FAULT_INJECTION=y` on every build of this branch. The Makefile tests
the variable for being non-empty rather than for `y`, so
`FAULT_INJECTION=n` turns it *on*; leave it unset to disable. The build
arg is written to an untracked `pkg/pillar/lk-build-arg-FAULT_INJECTION`,
which dirties the tree and adds `-dirty` to the pillar package tag —
remove it before computing any package hash.

## Verification

On this tip, from a clean tree:

- `git diff --name-status appvol-allprs HEAD` lists only the stress
  surface above
- `gofmt -l` over the branch's non-vendor `.go` files: clean
- `pkg/pillar`: `go vet -tags k ./...` and `-tags k,faultinjection` rc=0
- `evetest`: `GOWORK=off go vet . ./tests/...` rc=0
- `make check-docker-hashes-consistency` rc=0, once the gitignored
  generated Dockerfiles left over from earlier builds are excluded
- `go test -tags k,faultinjection` over `cmd/{baseosmgr,nodeagent,zedagent}`,
  `volmanifest`, `diskconvert`, `hypervisor`: pass, except
  `TestCreateReplicaPodConfig`, which fails `mkdir /run/.kube: permission
  denied` under a non-root `go test` on master too (#6290)
- `pkg/storage-resizer`: `go test ./...` rc=0
