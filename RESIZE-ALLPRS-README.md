# resize-allprs-stress — multi-PR integration branch (fault-injection line)

Integration vehicle: it combines still-open PRs so one build and test run sees
their combined diff. **Never PR'd upstream.**

Base: `upstream/master` @ `1ddb9ac3a`, 38 commits on top.

The fault-injection twin of `resize-allprs`: an identical PR contribution
plus fork#7. The two are **parallel lines**, not stacked — verify parity by
content (`git diff --name-only <a> <b>` must list only the stress surface),
never by SHA.

## Included

| source | ref | tip when written | role |
|--------|-----|-----|------|
| fork#6 | `kvm-to-k-volmig` | `9b5fa8eae` | the conversion chain: #6036 + #6063 + the volmig commits |
| fork#7 | `resize-watchdog-stress` | `1e9ea2bab` | the fault-injection stress harness: no-pet watchdog, watchdog-during-GPT-write chaos, the chaos storage-resizer pin. **STRESS ONLY — never merge** |
| lf-edge/eve#6271 | `eriknordmark:kubevirt-graceful-stop` | `6df3bfbb8` | VMIRS/domain delete-path fixes; keep domain bookkeeping until Cleanup |
| lf-edge/eve#6442 | `andrewd-zededa:eve-k-purge-pvc-partial-annotation` | `7ef13c34b` | a stale volume ref a domain still holds no longer deadlocks the purge |
| lf-edge/eve#6406 | `andrewd-zededa:eve-k-purge-cleanup-part2` | `679921bd4` | the `gcPVCs` reclaim of a swept stale generation's PVC |
| lf-edge/eve#6478 | `eriknordmark:purge-during-failover` | `537df2103` | **only its two evetest commits** — see below |
| lf-edge/eve#6280 | `eriknordmark:purge-fix-validation` | `e37c36485` | evetest: hold the purge end state across a reboot, plus the on-the-spot VM app image |

fork#6 stacks the two conversion PRs, so they are not replayed separately:

| PR | ref | tip |
|----|-----|-----|
| lf-edge/eve#6036 | `eriknordmark:kvm-k-baseos-upgrade-blob-reuse` | `a823eebc6` |
| lf-edge/eve#6063 | `eriknordmark:kvm-to-k-resize` | `d77c54d89` |

Each PR is replayed as its own commits rather than merged at its tip, so no
unrelated master history rides along.

**Four PRs this branch used to replay have merged upstream** and now arrive with
the base: #6443 (csihandler idle logging, 09-04), #6314 (accept a cluster PVC
instead of re-downloading its source, 09-04), #6441 (kvm honours the force flag,
09-07), #6453 (bound the graceful stop for default-mode guests, 09-07). Do not
re-add them.

## #6478 contributes two commits

`evetest: purge with the designated node down` (`TestVMAppPurgeDuringFailover`)
and `evetest: work around a Longhorn CSI-provisioner stall`, which that test
needs.

The workaround commit was written before #6406 promoted the local
`kubectlListItems` helper to `EdgeDevice.KubectlListItems`, so it is adapted
here: `kubectlEvents` goes through `RunKubectl`, `restartCSIProvisioner` keeps
`RunShellScript` because it targets `longhorn-system` rather than the app
namespace, and the workaround file calls the promoted method.

## Branch-local (never upstream)

| change | why |
|--------|-----|
| `volumemgr: adapt #6406's test to the pointer-returning initStatusCtx` | #6406's reclaim test takes the context's address at 11 sites while `initStatusCtx` already returns `*volumemgrContext`, so `cmd/volumemgr` does not compile. Belongs on #6406 once it rebases |

This line carries no rootfs-cap change; master's 291MB ceiling applies. The kvm
rootfs measures ~288MB under it, with master's `40fb331bc rootfs: use 1MB
squashfs blocks on amd64/kvm` setting the block size that gets it there.

## Notes

- `pkg/storage-resizer` is content-hash pinned by **both** `pkg/pillar/Dockerfile`
  and `pkg/storage-init/Dockerfile`; the value here is the chaos build's
  `bc26c95c64a2d652cff93f0ac7a5c91ea74f899a`, which differs from the non-stress
  line's. Recompute with `build-tools/bin/linuxkit pkg show-tag pkg/storage-resizer`
  rather than copying it across branches.
- Build with `FAULT_INJECTION=y`; leaving the flag off passes nothing, and
  `FAULT_INJECTION=n` arms the gates exactly as `y` does.
- `make check-docker-hashes-consistency` fails on
  `pkg/external-boot-image/Dockerfile` naming `eve-xen-tools`. That mismatch is
  upstream's and reproduces on an untouched master checkout.
- `TestCreateReplicaPodConfig` fails under a non-root `go test` on every branch
  and on master alike — it writes to the real `/run/.kube`. See lf-edge/eve#6290.
- `go build ./...` does not compile the EVE-k paths; use `-tags k`.
