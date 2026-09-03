# resize-allprs-stress — multi-PR integration branch (fault-injection line)

Integration vehicle: it combines still-open PRs so one build and test run sees
their combined diff. **Never PR'd upstream.**

Base: `upstream/master` @ `f94089001`, 46 commits on top.

The fault-injection twin of `resize-allprs`: an identical PR contribution
plus fork#7. The two are **parallel lines**, not stacked — verify parity by
content (`git diff --name-status <a> <b>` must list only the stress surface),
never by SHA.

## Included

| source | ref | tip when written | role |
|--------|-----|-----|------|
| fork#6 | `kvm-to-k-volmig` | `e02b06973` | the conversion chain: #6036 + #6063 + the volmig commits |
| fork#7 | `resize-watchdog-stress` | `301518b82` | the fault-injection stress harness: no-pet watchdog, watchdog-during-GPT-write chaos, the chaos storage-resizer pin. **STRESS ONLY — never merge** |
| lf-edge/eve#6271 | `eriknordmark:kubevirt-graceful-stop` | `6df3bfbb8` | VMIRS/domain delete-path fixes; keep domain bookkeeping until Cleanup |
| lf-edge/eve#6443 | `andrewd-zededa:eve-k-log-cleanup` | `6435a5cb1` | drops the per-poll csihandler log lines that bury everything else in a test log |
| lf-edge/eve#6442 | `andrewd-zededa:eve-k-purge-pvc-partial-annotation` | `7ef13c34b` | a stale volume ref a domain still holds no longer deadlocks the purge |
| lf-edge/eve#6441 | `eriknordmark:kvm-honour-force-shutdown` | `2ad60b3d8` | KVM `Stop(force)` terminates the process instead of re-issuing the same ACPI request. Fixes issue #6452; explicitly **not** a fix for #6440 |
| lf-edge/eve#6453 | `eriknordmark:bound-graceful-shutdown` | `9e65beaa6` | bounds the graceful stop wait for guests that leave the virtualization mode unset. Fixes #6440. Complementary to #6441; neither subsumes the other |
| lf-edge/eve#6314 | `andrewd-zededa:eve-k-skip-ct-for-pvc` | `331f2c7fb` | accept a cluster PVC instead of re-downloading its source; kubeapi rollout log + upload timeout; domainmgr disk-format retry |
| lf-edge/eve#6406 | `andrewd-zededa:eve-k-purge-cleanup-part2` | `679921bd4` | the `gcPVCs` reclaim of a swept stale generation's PVC |
| lf-edge/eve#6318 | `eriknordmark:gcpvcs-phase2` | `783418b32` | **only its two evetest commits** — see below |
| lf-edge/eve#6280 | `eriknordmark:purge-fix-validation` | `6499f3918` | evetest: hold the purge end state across a reboot, plus the on-the-spot VM app image |

fork#6 stacks the two conversion PRs, so they are not replayed separately:

| PR | ref | tip |
|----|-----|-----|
| lf-edge/eve#6036 | `eriknordmark:kvm-k-baseos-upgrade-blob-reuse` | `446a0af67` |
| lf-edge/eve#6063 | `eriknordmark:kvm-to-k-resize` | `53db34ff4` |

Each PR is replayed as its own commits rather than merged at its tip, so no
unrelated master history rides along.

## #6318 contributes two commits, not four

#6318's `initialvolumestatus.go` and `volumemgr.go` are **byte-identical** to
#6406's, and its `evetest: assert the purged PVC is reclaimed` is carried by
#6406 with a refined comment. Only two of its commits are unique, and both are
here: `evetest: purge with the designated node down`
(`TestVMAppPurgeDuringFailover`) and `evetest: work around a Longhorn
CSI-provisioner stall`, which that test needs.

The workaround commit was written before #6406 promoted the local
`kubectlListItems` helper to `EdgeDevice.KubectlListItems`, so it is adapted
here: `kubectlEvents` goes through `RunKubectl`, `restartCSIProvisioner` keeps
`RunShellScript` because it targets `longhorn-system` rather than the app
namespace, and the workaround file calls the promoted method.

## Branch-local (never upstream)

| change | why |
|--------|-----|
| `volumemgr: adapt #6406's test to the pointer-returning initStatusCtx` | #6406's reclaim test takes the context's address at 11 sites while `initStatusCtx` already returns `*volumemgrContext`, so `cmd/volumemgr` does not compile. Belongs on #6406 once it rebases |
| (no rootfs-cap commit) | this line gets 295MB from fork#7's own pin commit, so the branch-local bump the non-stress line carries would be a duplicate |

The `gcPVCs` reclaim is **no longer branch-local** — #6406 carries it, at
identical content. Do not re-apply it by hand.

## Notes

- `TestCreateReplicaPodConfig` fails under a non-root `go test` on every branch
  and on master alike — it writes to the real `/run/.kube`. See lf-edge/eve#6290.
- `pkg/pillar` compiles the EVE-k paths only under `-tags k`; a plain
  `go build ./...` skips `hypervisor/kubevirt.go` and `kubeapi/vitoapiserver.go`,
  which is where most of #6271 and half of #6314 live. Verify with
  `go build -tags k ./...` / `go test -tags k ...`, or `make HV=k pkg/pillar`.
- `pkg/storage-resizer` is content-hash pinned by both `pkg/pillar/Dockerfile`
  and `pkg/storage-init/Dockerfile`. fork#7's chaos instrumentation changes that
  package's content, so its pin commit's value is stale on every rebuild and the
  hash must be **recomputed, not merged**: this branch carries
  `3eae973e5e7fafb83094673bde803bed16c5f187` from
  `build-tools/bin/linuxkit pkg show-tag pkg/storage-resizer` on this branch.
  Never copy a pin from the non-stress twin.
- **Every build of this branch must pass `FAULT_INJECTION=y`** — the gates are
  `k && faultinjection`, so EVE-k only, and a stale `lk-build-arg-FAULT_INJECTION`
  is deleted rather than honored. `watch-build-package.sh --fault-injection`
  handles it; verify from the Makefile's `Passing`/`Removing file` line, not the
  Dockerfile's echo, which is absent on a pillar cache hit.

## Before building or testing from this branch

Re-verify it still tracks the current PR tips; they move. Recipe in the
`allprs-branch-workflow` skill: compare each PR's current tip by content with
`git diff --name-status <tip> -- $(git diff --name-only <base> <tip>)`. Never
hand-roll that with `git hash-object` — it reports every file a PR deletes as drift.
