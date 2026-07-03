// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package volumemgr

import (
	"strings"
	"time"

	"github.com/lf-edge/eve/pkg/pillar/kubeapi"
	"github.com/lf-edge/eve/pkg/pillar/types"
)

// Cache for the EVE-k cluster-storage readiness probe (single-threaded volumemgr
// main loop, so no lock needed). Once ready it stays ready (longhorn/CDI don't
// un-deploy); while not ready we re-probe at most every clusterStorageProbeTTL.
var (
	clusterStorageReadyCache   bool
	clusterStorageReadyCheckAt time.Time
)

const clusterStorageProbeTTL = 15 * time.Second

// clusterStorageReady reports whether EVE-k longhorn+CDI are ready to create app
// volumes, caching the kube-API probe. The volume-create path calls this so
// volumemgr DEFERS (quietly, no error) until the cluster storage stack is up,
// instead of attempting and failing (storageclass-not-found / no-upload-pod-
// annotation) then relying on the error-retry. Always true off EVE-k.
func clusterStorageReady(ctx *volumemgrContext) bool {
	if !ctx.hvTypeKube {
		return true
	}
	if clusterStorageReadyCache {
		return true
	}
	if time.Since(clusterStorageReadyCheckAt) < clusterStorageProbeTTL {
		return false
	}
	clusterStorageReadyCheckAt = time.Now()
	clusterStorageReadyCache = kubeapi.ClusterStorageReadyForVolumes(log)
	return clusterStorageReadyCache
}

// clusterStorageRetrySubstrings are (lower-cased) substrings of volume-create
// errors that originate in the EVE-k (kubevirt) cluster storage stack --
// longhorn PVC provisioning, the CDI image-upload path, or the k8s API server
// itself. These are transient while the cluster is still coming up, which is
// common right after a kvm->k conversion: longhorn/CDI may not be ready when
// volumemgr first tries to roll the volume out to a PVC, e.g. the CDI upload pod
// has not been created/annotated yet ("no upload pod annotation"). A volume
// parked on one of these should be retried rather than left in a permanent
// error state with no further attempts.
var clusterStorageRetrySubstrings = []string{
	"no upload pod annotation",
	"attempts to upload image failed",
	"rolloutdisktopvc",
	"to pvc",             // "Error converting <file> to PVC <name>: ..."
	"error creating pvc", // CreatePVC failure
	"cdi-upload",
	"uploadproxy",
	"failed to get clientset",
	"getsupportedreplicacountforcluster",
	"storageclass", // "storageclass \"longhorn\" not found"
	"longhorn",
	"dial tcp", // k8s api / uploadproxy not reachable yet
	":6443",    // k3s api server
	"connection refused",
}

// isRetryableClusterStorageError reports whether a volume-create error came from
// the EVE-k cluster storage stack and is worth retrying (longhorn/CDI/k8s-API
// not ready yet). Matching is substring-based and case-insensitive. False
// positives are benign: a retry just re-runs CreateVolume, which re-fails and
// re-parks the volume (self-throttled to one attempt per gc tick).
func isRetryableClusterStorageError(errStr string) bool {
	l := strings.ToLower(errStr)
	for _, s := range clusterStorageRetrySubstrings {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// retryFailedClusterVolumeCreate re-drives volumes parked in a CREATING_VOLUME
// error that was caused by a transient EVE-k cluster storage issue. It clears
// the error and resubmits the create work; if the create fails again the error
// (with a fresh ErrorTime) is set anew, so this self-throttles to at most one
// retry per gc tick. Called from the periodic gc handler.
func retryFailedClusterVolumeCreate(ctx *volumemgrContext) {
	if !ctx.hvTypeKube {
		// PVC/longhorn/CDI volume creation only happens on EVE-k.
		return
	}
	for _, st := range ctx.pubVolumeStatus.GetAll() {
		status := st.(types.VolumeStatus)
		// Only the create stage rolls a volume out to a PVC, so a retryable
		// cluster-storage failure parks the volume here.
		if status.State != types.CREATING_VOLUME ||
			status.SubState != types.VolumeSubStatePrepareDone {
			continue
		}
		if !status.HasError() || !isRetryableClusterStorageError(status.Error) {
			continue
		}
		log.Noticef("retryFailedClusterVolumeCreate: retrying volume %s (%s) "+
			"after transient cluster-storage error: %s",
			status.Key(), status.DisplayName, status.Error)
		status.ClearErrorWithSource()
		publishVolumeStatus(ctx, &status)
		AddWorkCreate(ctx, &status)
	}
}
