# Code-level fault injector (THROWAWAY — never commit to the PR)

Deterministic control over the defer/retry state machine, for cases black-box timing
can't force reliably: a *bounded* number of transient failures (retry-N-then-converge),
and the *permanent* branch on demand.

Gated entirely on `EVE_KUBE_FAULT`; with the var unset it is a no-op, so the build is
inert unless explicitly enabled.

## Apply (on a scratch branch off the PR head)

```sh
git checkout -b volumemgr-cdi-retry-faultinject a24d2545   # PR head; throwaway
cp fault-injector/faultinject_k.go pkg/pillar/kubeapi/
```

Then add one call site at the top of each function in
`pkg/pillar/kubeapi/vitoapiserver.go` (both return the pre-classified error directly,
so the verdict rides through unchanged):

```go
func CreatePVC(pvcName string, size uint64, log *base.LogObject, storageClass string) error {
	if e := faultInject("createpvc"); e != nil {
		return e
	}
	// ... existing body ...
```

```go
func RolloutDiskToPVC(ctx context.Context, log *base.LogObject, exists bool,
	diskfile string, pvcName string, filemode bool, pvcSize uint64, storageClass string) error {
	if e := faultInject("rollout"); e != nil {
		return e
	}
	// ... existing body ...
```

Build (`~/bin/eve-build.sh <ws> k`) and deploy this image to the isolated eden.

## Drive it

Set `EVE_KUBE_FAULT` in the running pillar's environment (e.g. inject into the pillar
service env on the test image, or export before the volume-create path runs).

```
EVE_KUBE_FAULT="rollout=transient:3"   # 3 uploads fail transient, 4th succeeds
EVE_KUBE_FAULT="createpvc=permanent"   # every PVC create is 403 Forbidden
EVE_KUBE_FAULT="rollout=permanent:1"   # one permanent rollout failure
```

Syntax: comma-separated `SITE=KIND[:N]` — `SITE` ∈ {`createpvc`,`rollout`},
`KIND` ∈ {`transient`,`permanent`}, `N` = fail the first N calls then pass
(default unlimited).

## What it proves that black-box can't

- **retry-N-then-converge:** `rollout=transient:3` → volume parks transient, ErrorTime
  advances exactly across the 3 retries, then converges — a bounded, deterministic
  retry count rather than "until the operator restores the backend".
- **permanent no-loop, on demand:** `createpvc=permanent` or `rollout=permanent` →
  `ClusterStorageTransientErr=false`, ErrorTime frozen, without needing a ResourceQuota.
- **per-volume mix:** because it's driven in-process, different volumes can be faulted
  independently (extend the spec with a PVC-name predicate if you need true per-volume
  profiles under concurrency).

## Reminder

This never lands in the PR. Keep it on the throwaway branch; do not push it to
`volumemgr-cdi-retry`. (`go.mod` unaffected — it uses only already-vendored k8s deps.)
