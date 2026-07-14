//go:build k

// Reference fault injector for the cluster-volume-create-failure harness. It lives
// under tests/ (outside the pillar module) and is NOT compiled here. To exercise
// the deterministic fault path, copy this file into pkg/pillar/kubeapi/ on a
// scratch branch, add the two call sites described in README.md, build HV=k, and
// drive it with the EVE_KUBE_FAULT env var (unset => every call is a no-op).
// Must NOT be merged into pkg/pillar — it would ship a fault injector in product.
//
// EVE_KUBE_FAULT syntax: comma-separated SITE=KIND[:N]
//
//	SITE = createpvc | rollout
//	KIND = transient | permanent
//	N    = fail the first N calls at this site, then pass (default: unlimited)
//
// Examples:
//
//	EVE_KUBE_FAULT="rollout=transient:3"      // fail 3 uploads, then succeed
//	EVE_KUBE_FAULT="createpvc=permanent"      // every PVC create is Forbidden
package kubeapi

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type faultSpec struct {
	kind  string
	limit int // -1 = unlimited
	count int
}

var (
	faultOnce  sync.Once
	faultMu    sync.Mutex
	faultSpecs map[string]*faultSpec
)

func faultParse() {
	faultSpecs = map[string]*faultSpec{}
	for _, tok := range strings.Split(os.Getenv("EVE_KUBE_FAULT"), ",") {
		if tok = strings.TrimSpace(tok); tok == "" {
			continue
		}
		site, rest, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		kind, nStr, hasN := strings.Cut(rest, ":")
		limit := -1
		if hasN {
			if v, err := strconv.Atoi(nStr); err == nil {
				limit = v
			}
		}
		faultSpecs[site] = &faultSpec{kind: kind, limit: limit}
	}
}

// faultInject returns a pre-classified synthetic error for SITE when EVE_KUBE_FAULT
// asks for it, else nil. transient => a TransientError wrapping 503 ServiceUnavailable
// (IsTransient true); permanent => a raw 403 Forbidden (IsTransient false). Call sites
// return the value directly so the verdict rides through unchanged.
func faultInject(site string) error {
	faultOnce.Do(faultParse)
	faultMu.Lock()
	defer faultMu.Unlock()
	s := faultSpecs[site]
	if s == nil || (s.limit >= 0 && s.count >= s.limit) {
		return nil
	}
	s.count++
	if s.kind == "permanent" {
		gr := schema.GroupResource{Resource: "persistentvolumeclaims"}
		return k8serrors.NewForbidden(gr, "fault-inject", errors.New("EVE_KUBE_FAULT"))
	}
	return &TransientError{Err: k8serrors.NewServiceUnavailable("EVE_KUBE_FAULT")}
}
