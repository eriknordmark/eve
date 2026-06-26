// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package upgradeconverter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStageKvmVolumes_HappyPath(t *testing.T) {
	initTestLog()
	root := t.TempDir()
	volumes := filepath.Join(root, "vault", "volumes")
	holding := filepath.Join(root, "vault", kvmVolumesHoldingDirName)
	sentinel := filepath.Join(root, "vault", ".volumes-relocated-for-kube")

	// A disk-volume file and a container-volume directory.
	disk := "11111111-1111-1111-1111-111111111111#0.qcow2"
	ctr := "22222222-2222-2222-2222-222222222222#0"
	writeFile(t, filepath.Join(volumes, disk), "disk-bytes")
	writeFile(t, filepath.Join(volumes, ctr, "rootfs", "f"), "ctr-bytes")

	assert.NoError(t, stageKvmVolumes(volumes, holding, sentinel, false))

	// Both relocated to the holding dir...
	got, err := os.ReadFile(filepath.Join(holding, disk))
	assert.NoError(t, err)
	assert.Equal(t, "disk-bytes", string(got))
	assert.FileExists(t, filepath.Join(holding, ctr, "rootfs", "f"))
	// ...volumes dir left empty for Longhorn, and the sentinel written.
	assert.NoFileExists(t, filepath.Join(volumes, disk))
	assert.NoDirExists(t, filepath.Join(volumes, ctr))
	assert.DirExists(t, volumes)
	assert.FileExists(t, sentinel)
}

func TestStageKvmVolumes_SentinelShortCircuits(t *testing.T) {
	initTestLog()
	root := t.TempDir()
	volumes := filepath.Join(root, "vault", "volumes")
	holding := filepath.Join(root, "vault", kvmVolumesHoldingDirName)
	sentinel := filepath.Join(root, "vault", ".volumes-relocated-for-kube")

	// Sentinel present (already staged) — a live Longhorn file must be left alone.
	writeFile(t, sentinel, "")
	live := "pvc-abc"
	writeFile(t, filepath.Join(volumes, live), "longhorn")

	assert.NoError(t, stageKvmVolumes(volumes, holding, sentinel, false))

	assert.FileExists(t, filepath.Join(volumes, live))
	assert.NoDirExists(t, holding)
}

func TestStageKvmVolumes_FreshInstallNoVolumes(t *testing.T) {
	initTestLog()
	root := t.TempDir()
	volumes := filepath.Join(root, "vault", "volumes")
	holding := filepath.Join(root, "vault", kvmVolumesHoldingDirName)
	sentinel := filepath.Join(root, "vault", ".volumes-relocated-for-kube")

	// No volumes dir at all (fresh EVE-k install).
	assert.NoError(t, stageKvmVolumes(volumes, holding, sentinel, false))

	// Nothing to relocate; volumes dir ensured present, sentinel written,
	// holding dir never created.
	assert.DirExists(t, volumes)
	assert.NoDirExists(t, holding)
	assert.FileExists(t, sentinel)
}

func TestStageKvmVolumes_DryRun(t *testing.T) {
	initTestLog()
	root := t.TempDir()
	volumes := filepath.Join(root, "vault", "volumes")
	holding := filepath.Join(root, "vault", kvmVolumesHoldingDirName)
	sentinel := filepath.Join(root, "vault", ".volumes-relocated-for-kube")

	disk := "33333333-3333-3333-3333-333333333333#0.qcow2"
	writeFile(t, filepath.Join(volumes, disk), "disk-bytes")

	assert.NoError(t, stageKvmVolumes(volumes, holding, sentinel, true))

	// noFlag: nothing moved, no sentinel.
	assert.FileExists(t, filepath.Join(volumes, disk))
	assert.NoDirExists(t, holding)
	assert.NoFileExists(t, sentinel)
}

// Round-trip: relocate (EVE-k) then restore (rollback to EVE-kvm) returns the
// volumes to their original location.
func TestStageThenRestoreRoundTrip(t *testing.T) {
	initTestLog()
	root := t.TempDir()
	volumes := filepath.Join(root, "vault", "volumes")
	holding := filepath.Join(root, "vault", kvmVolumesHoldingDirName)
	sentinel := filepath.Join(root, "vault", ".volumes-relocated-for-kube")

	disk := "44444444-4444-4444-4444-444444444444#0.qcow2"
	writeFile(t, filepath.Join(volumes, disk), "disk-bytes")

	assert.NoError(t, stageKvmVolumes(volumes, holding, sentinel, false))
	assert.FileExists(t, filepath.Join(holding, disk))

	assert.NoError(t, restoreKvmVolumes(holding, volumes, false))
	got, err := os.ReadFile(filepath.Join(volumes, disk))
	assert.NoError(t, err)
	assert.Equal(t, "disk-bytes", string(got))
	assert.NoDirExists(t, holding)
}
