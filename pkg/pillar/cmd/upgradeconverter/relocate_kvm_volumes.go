// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgradeconverter

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/types"
	fileutils "github.com/lf-edge/eve/pkg/pillar/utils/file"
	"github.com/lf-edge/eve/pkg/pillar/utils/persist"
)

// kvmVolumesStagedSentinelPath returns the marker recording that the
// carried-over EVE-kvm volumes have already been relocated for Longhorn on this
// EVE-k device. It lives in the vault so it shares the vault's lifecycle, and it
// is what makes the relocation run exactly once.
func kvmVolumesStagedSentinelPath(persistDir string) string {
	return filepath.Join(persistDir, "vault", ".volumes-relocated-for-kube")
}

// relocateKvmVolumesForKube moves carried-over EVE-kvm app volumes out of
// /persist/vault/volumes — which Longhorn must own on EVE-k (data path set in
// pkg/kube/longhorn-utils.sh) — into /persist/vault/volumes-kvm, leaving a clean
// empty /persist/vault/volumes for Longhorn. The relocated sources are later
// converted to Longhorn PVCs, lazily, by volumemgr once the cluster is up; the
// counterpart restoreKvmVolumesOnDowngrade moves them back if the device falls
// back to EVE-kvm.
//
// Why a sentinel and why the timing matters. This runs in the post-vault phase,
// before k3s/Longhorn start, so on the first EVE-k boot /persist/vault/volumes
// still holds the kvm volumes and Longhorn has not yet created its own layout.
// That is the only safe window to tell carried-over volumes apart from Longhorn
// data. After this runs once the directory belongs to Longhorn, so the sentinel
// guarantees we never relocate again — including after the lazy converter has
// drained and removed volumes-kvm.
//
// ext4 only. On ZFS /persist the EVE-k vault is a zvol-backed ext4 filesystem
// that differs structurally from the EVE-kvm encrypted-dataset vault; that
// migration (and the zvol->file staging of each app volume) is handled by the
// fs->zvol vault conversion, not here.
//
// No-op on EVE-kvm, on ZFS persist, and once the sentinel exists. A fresh EVE-k
// install (no carried-over volumes) simply relocates nothing and writes the
// sentinel. Idempotent.
func relocateKvmVolumesForKube(ctxPtr *ucContext) error {
	if !base.IsHVTypeKube() {
		log.Functionf("relocateKvmVolumesForKube: not EVE-k, skipping")
		return nil
	}
	if persist.ReadPersistType() == types.PersistZFS {
		log.Functionf("relocateKvmVolumesForKube: ZFS persist handled by the fs->zvol vault migration, skipping")
		return nil
	}
	return stageKvmVolumes(
		filepath.Clean(ctxPtr.volumesDir()),
		kvmVolumesHoldingDir(ctxPtr.persistDir),
		kvmVolumesStagedSentinelPath(ctxPtr.persistDir),
		ctxPtr.noFlag)
}

// stageKvmVolumes is the testable core. If the sentinel exists it is a no-op.
// Otherwise it moves every entry of volumesDir into holdingDir (creating
// holdingDir on demand, skipping any name already present there), ensures
// volumesDir exists and is empty for Longhorn, and writes the sentinel. A
// missing/empty volumesDir (fresh EVE-k install) is fine — nothing is moved and
// the sentinel is still written so we don't probe every boot.
func stageKvmVolumes(volumesDir, holdingDir, sentinelPath string, noFlag bool) error {
	if fileutils.FileExists(log, sentinelPath) {
		log.Functionf("stageKvmVolumes: sentinel %s exists, skipping", sentinelPath)
		return nil
	}
	var entries []os.DirEntry
	if fileutils.DirExists(log, volumesDir) {
		var err error
		entries, err = os.ReadDir(volumesDir)
		if err != nil {
			return fmt.Errorf("stageKvmVolumes: read %s: %w", volumesDir, err)
		}
	}
	if noFlag {
		log.Noticef("stageKvmVolumes: dryrun, would relocate %d entry(ies) from %s to %s",
			len(entries), volumesDir, holdingDir)
		return nil
	}
	if len(entries) > 0 {
		if err := os.MkdirAll(holdingDir, 0700); err != nil {
			return fmt.Errorf("stageKvmVolumes: mkdir %s: %w", holdingDir, err)
		}
	}
	var moved, skipped int
	for _, e := range entries {
		oldPath := filepath.Join(volumesDir, e.Name())
		newPath := filepath.Join(holdingDir, e.Name())
		info, err := os.Stat(oldPath)
		if err != nil {
			log.Errorf("stageKvmVolumes: stat %s: %v", oldPath, err)
			continue
		}
		if _, err := os.Stat(newPath); err == nil {
			// Already staged (e.g. a re-run after an interrupted relocation) —
			// don't overwrite the holding copy.
			log.Warnf("stageKvmVolumes: %s already exists; not moving %s", newPath, oldPath)
			skipped++
			continue
		}
		// maybeMove handles the file/dir distinction, the fscrypt copy-vs-rename
		// nuance, and the container snapshot-id file.
		maybeMove(oldPath, info.ModTime(), newPath, noFlag)
		moved++
	}
	// Longhorn needs the directory to exist and be empty.
	if err := os.MkdirAll(volumesDir, 0700); err != nil {
		return fmt.Errorf("stageKvmVolumes: mkdir %s: %w", volumesDir, err)
	}
	log.Noticef("stageKvmVolumes: relocated %d, skipped %d, from %s to %s",
		moved, skipped, volumesDir, holdingDir)
	return writeSentinel(sentinelPath)
}
