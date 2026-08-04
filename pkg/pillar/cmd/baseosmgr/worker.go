// Copyright (c) 2020 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package baseosmgr

import (
	"fmt"
	"os"
	"time"

	"github.com/lf-edge/eve/pkg/pillar/volmanifest"
	"github.com/lf-edge/eve/pkg/pillar/worker"
	"github.com/lf-edge/eve/pkg/pillar/zboot"
)

const (
	workInstall       = "install"
	workVerifyVolumes = "verifyVolumes"
	// verifyVolumesWorkKey is the single work key for the post-resize volume
	// content verification — only one conversion runs at a time.
	verifyVolumesWorkKey = "verify-volumes"
	// quarantineMarkerPath, when present, makes the post-resize check rename a
	// volume it found corrupt instead of deleting it. Absent in the field, where
	// deleting is correct: the volume is unusable and EVE recreates it. A test
	// measuring what the interrupted resize DID to the volume needs the bytes,
	// and they are gone by the time any check outside pillar can look.
	// The suffix keeps the rename inside the same fscrypt directory, so it stays a
	// rename and never a multi-GiB copy.
	quarantineMarkerPath = "/persist/volmanifest-keep-corrupt"
	quarantineNameSuffix = ".corrupt"
)

// installWorkDescription install work we feed into the worker go routine
type installWorkDescription struct {
	key    string
	ref    string
	target string
}

// AddWorkInstall create a Work job to install the provided image to the target path
func AddWorkInstall(ctx *baseOsMgrContext, key, ref, target string) {
	d := installWorkDescription{
		key:    key,
		ref:    ref,
		target: target,
	}
	// Don't fail on errors to make idempotent (Submit returns an error if
	// the work was already submitted)
	done, err := ctx.worker.TrySubmit(worker.Work{Key: key, Kind: workInstall,
		Description: d})
	if err != nil {
		log.Errorf("TrySubmit %s failed: %s", key, err)
	} else if !done {
		log.Fatalf("Failed to submit work due to queue length for %s", key)
	}
	log.Functionf("AddWorkInstall(%s) done", key)
}

// installWorker implementation of work.WorkFunction that installs an image to a particular location
func installWorker(ctxPtr interface{}, w worker.Work) worker.WorkResult {
	d := w.Description.(installWorkDescription)

	result := worker.WorkResult{
		Key:         w.Key,
		Description: d,
	}

	if d.target == "" {
		result.Error = fmt.Errorf("installWorker: unassigned destination partition for %s", d.ref)
		result.ErrorTime = time.Now()
		return result
	}

	log.Functionf("installWorker to install %s to %s", d.ref, d.target)
	err := zboot.WriteToPartition(log, d.ref, d.target)
	log.Functionf("installWorker DONE install %s to %s: err %v",
		d.ref, d.target, err)

	if err != nil {
		result.Error = err
		result.ErrorTime = time.Now()
	}
	return result
}

// processInstallWorkResult handle the work result that was an installation
func processInstallWorkResult(ctxPtr interface{}, res worker.WorkResult) error {
	ctx := ctxPtr.(*baseOsMgrContext)
	d := res.Description.(installWorkDescription)
	baseOsHandleStatusUpdateUUID(ctx, d.key)
	return nil
}

// verifyVolumesWorkDescription carries the BaseOsStatus key to re-evaluate once
// the post-resize volume verification finishes.
type verifyVolumesWorkDescription struct {
	baseOsKey string
}

// AddWorkVerifyVolumes queues the post-resize volume content verification. It runs
// on the worker, not in a pubsub handler, because hashing tens of GiB of volume
// data would otherwise stall the agent past its watchdog window.
func AddWorkVerifyVolumes(ctx *baseOsMgrContext, baseOsKey string) {
	d := verifyVolumesWorkDescription{baseOsKey: baseOsKey}
	done, err := ctx.worker.TrySubmit(worker.Work{Key: verifyVolumesWorkKey,
		Kind: workVerifyVolumes, Description: d})
	if err != nil {
		log.Errorf("TrySubmit %s failed: %s", verifyVolumesWorkKey, err)
	} else if !done {
		log.Fatalf("Failed to submit verify-volumes work due to queue length")
	}
}

// verifyVolumesWorker verifies each application volume against the pre-resize
// content manifest, removes any that is not provably intact (so the EVE-k boot
// recreates it blank / from its content tree), then consumes the manifest so the
// conversion proceeds on the next evaluation.
func verifyVolumesWorker(ctxPtr interface{}, w worker.Work) worker.WorkResult {
	d := w.Description.(verifyVolumesWorkDescription)
	result := worker.WorkResult{Key: w.Key, Description: d}

	dirs := volumeManifestDirs()
	checked, corruptions, err := volmanifest.Verify(dirs...)
	if err != nil {
		result.Error = err
		result.ErrorTime = time.Now()
		return result
	}
	// Report the coverage even when nothing is wrong. Without it the only evidence
	// this check produces is a warning per corrupt volume, so a conversion that
	// hashed nothing at all is indistinguishable from one where every volume was
	// intact — and "no corruption found" then cannot be told from "not measured".
	log.Noticef("post-resize: verified %d volume objects against the pre-resize manifest in %v; %d not intact",
		checked, dirs, len(corruptions))
	_, quarantine := os.Stat(quarantineMarkerPath)
	for _, c := range corruptions {
		if quarantine == nil {
			dst := c.Path + quarantineNameSuffix
			if mvErr := os.Rename(c.Path, dst); mvErr != nil {
				log.Errorf("post-resize: quarantine %s failed: %v; removing instead",
					c.Path, mvErr)
			} else {
				log.Warnf("post-resize: volume %s not intact (%s); quarantined as %s",
					c.Path, c.Reason, dst)
				continue
			}
		}
		log.Warnf("post-resize: volume %s not intact (%s); removing so it is recreated",
			c.Path, c.Reason)
		if rmErr := os.Remove(c.Path); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Errorf("post-resize: remove corrupt volume %s: %v", c.Path, rmErr)
		}
	}
	volmanifest.Remove(dirs...)
	return result
}

// processVerifyVolumesWorkResult re-evaluates the BaseOsStatus once verification
// is done; the manifest is now consumed, so maybeConvert proceeds with the install.
func processVerifyVolumesWorkResult(ctxPtr interface{}, res worker.WorkResult) error {
	ctx := ctxPtr.(*baseOsMgrContext)
	d := res.Description.(verifyVolumesWorkDescription)
	baseOsHandleStatusUpdateUUID(ctx, d.baseOsKey)
	return nil
}
