// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

// EVE-kvm <-> EVE-k boot-disk conversion driven from baseosmgr.
//
// A cross-flavor base-OS update is allowed (only when the device has no
// volumes; see the IsHVTypeKube/IsVersionHVTypeKube seam in handlebaseos.go)
// but the EVE-k partition geometry — a 2 GB ESP plus two 10 GB IMG partitions —
// may not yet exist on an older device. maybeConvert drives the standalone
// storage-resizer binary (via the diskconvert library) to repartition the boot
// disk before the A/B install, recording progress on BaseOsStatus so zedagent
// reports it as ZDEVICE_STATE_CONVERTING + sub_state.

package baseosmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/diskconvert"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/lf-edge/eve/pkg/pillar/zboot"
)

// convertResizerBinary is where the storage-resizer binary lives in the pillar
// container; it is built into both the pillar and storage-init images (the
// eve-storage-resizer linuxkit package is COPYd to this path).
const convertResizerBinary = "/usr/bin/storage-resizer"

// maybeConvert runs one boot-disk conversion step for a cross-flavor update and
// reports whether the A/B install may proceed.
//
// It returns true when the boot disk already has, or now has, the EVE-k
// geometry and the caller should continue with the install. It returns false
// when the install must wait: a reboot was requested for the offline shrink, or
// the conversion cannot proceed (insufficient space, or an error). In the false
// case it has already updated and published BaseOsStatus, so the caller should
// treat status as changed and return.
func maybeConvert(ctx *baseOsMgrContext, status *types.BaseOsStatus) bool {
	bootDisk, err := bootDiskFromCurrentPartition()
	if err != nil {
		errStr := fmt.Sprintf("conversion: cannot determine boot disk: %s", err)
		log.Error(errStr)
		status.Converting = false
		status.ConvertSubState = types.DEVICE_SUBSTATE_UNSPECIFIED
		status.SetErrorNow(errStr)
		publishBaseOsStatus(ctx, status)
		return false
	}

	wasConverting := status.Converting

	// The backup and the shrink flag file must be written to the CONFIG partition
	// itself: the runtime /config is a read-only tmpfs RAM copy that storage-init
	// remounts read-only, so a write there is lost on the reboot the shrink relies
	// on. Mount the partition read-write and point the resizer at it (the same
	// approach the monitor agent uses to update /config). The check/grow steps do
	// not write /config, but mounting unconditionally keeps the one /config-writing
	// step (backup) covered without first probing the decision.
	var res diskconvert.Result
	var runErr error
	mountErr := withConfigPartitionRW(func(mountDir string) error {
		c := &diskconvert.Converter{
			Runner: diskconvert.BinaryRunner{
				Binary:    convertResizerBinary,
				BackupDir: filepath.Join(mountDir, "backup-persist"),
				FlagFile:  filepath.Join(mountDir, "shrink-persist"),
			},
			PersistLabel: "P3",
		}
		res, runErr = c.Run(bootDisk)
		return nil
	})
	if mountErr != nil {
		errStr := fmt.Sprintf("conversion: cannot access CONFIG partition: %s", mountErr)
		log.Error(errStr)
		status.Converting = false
		status.ConvertSubState = types.DEVICE_SUBSTATE_UNSPECIFIED
		status.SetErrorNow(errStr)
		publishBaseOsStatus(ctx, status)
		return false
	}
	log.Functionf("maybeConvert(%s): decision=%s outcome=%s reason=%q target=%q err=%v",
		bootDisk, res.Decision, res.Outcome, res.Reason, res.ShrinkTarget, runErr)

	switch res.Outcome {
	case diskconvert.OutcomeProceed:
		// Geometry already EVE-k, or the online grow just completed. Only
		// publish if we were mid-conversion or carrying an error, to avoid
		// flapping CONVERTING on every status update of an already-EVE-k disk.
		if wasConverting || status.HasError() {
			status.Converting = false
			status.ConvertSubState = types.DEVICE_SUBSTATE_UNSPECIFIED
			status.ClearError()
			publishBaseOsStatus(ctx, status)
		}
		return true

	case diskconvert.OutcomeRebootForShrink:
		// The /config shrink flag + backup are written. Advance the sub-state so
		// nodeagent (subscribed to BaseOsStatus) performs a graceful reboot into
		// the offline shrink; the conversion re-evaluates on the next boot.
		status.Converting = true
		status.ConvertSubState = types.DEVICE_SUBSTATE_CONVERT_REBOOTING_TO_RESIZE
		status.ClearError()
		publishBaseOsStatus(ctx, status)
		return false

	case diskconvert.OutcomeInsufficient:
		errStr := fmt.Sprintf("conversion not possible: %s", res.Reason)
		log.Error(errStr)
		status.Converting = false
		status.ConvertSubState = types.DEVICE_SUBSTATE_UNSPECIFIED
		status.SetErrorNow(errStr)
		publishBaseOsStatus(ctx, status)
		return false

	default:
		errStr := fmt.Sprintf("conversion failed: %v", runErr)
		log.Error(errStr)
		status.Converting = false
		status.ConvertSubState = types.DEVICE_SUBSTATE_UNSPECIFIED
		status.SetErrorNow(errStr)
		publishBaseOsStatus(ctx, status)
		return false
	}
}

// withConfigPartitionRW mounts the CONFIG partition read-write at a temp dir
// under /run, calls fn with that dir, then unmounts. The conversion backup and
// the shrink flag file have to be written here, on the real partition, because
// the runtime /config is a read-only tmpfs RAM copy whose writes do not survive
// the reboot into the offline shrink. Mirrors cmd/monitor/monitor.go, which
// writes the server file the same way. Returns the mount error (fn not run), or
// fn's error, or the unmount error; the caller here lets fn always succeed and
// captures the resizer result via closure variables.
func withConfigPartitionRW(fn func(mountDir string) error) error {
	out, err := base.Exec(log, "/sbin/findfs", "PARTLABEL=CONFIG").Output()
	if err != nil {
		return fmt.Errorf("findfs PARTLABEL=CONFIG: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	devicePath := strings.TrimSpace(string(out))
	if devicePath == "" {
		return fmt.Errorf("no CONFIG partition found")
	}
	mountDir, err := os.MkdirTemp("/run", "convert-config-")
	if err != nil {
		return fmt.Errorf("create temp mount dir: %w", err)
	}
	if err := syscall.Mount(devicePath, mountDir, "vfat", 0, "iocharset=iso8859-1"); err != nil {
		_ = os.RemoveAll(mountDir) // never mounted, safe to remove
		return fmt.Errorf("mount CONFIG partition %s: %w", devicePath, err)
	}
	fnErr := fn(mountDir)
	// Only remove the mountpoint once it is actually unmounted; RemoveAll on a
	// still-mounted dir would delete files on the CONFIG partition.
	if umountErr := syscall.Unmount(mountDir, 0); umountErr != nil {
		if fnErr != nil {
			return fnErr
		}
		return fmt.Errorf("unmount CONFIG partition %s: %w", mountDir, umountErr)
	}
	_ = os.RemoveAll(mountDir)
	return fnErr
}

// bootDiskFromCurrentPartition returns the whole-disk device (e.g. /dev/sda)
// holding the current root partition.
func bootDiskFromCurrentPartition() (string, error) {
	partDev := zboot.GetPartitionDevname(zboot.GetCurrentPartition())
	if partDev == "" {
		return "", fmt.Errorf("empty devname for current partition")
	}
	return parentDisk(partDev)
}

// parentDisk maps a partition device (e.g. /dev/sda2, /dev/mmcblk0p3) to its
// whole-disk device (e.g. /dev/sda, /dev/mmcblk0) using lsblk's PKNAME, which
// is robust across sd*/mmcblk*/nvme*/vd* naming conventions.
func parentDisk(partDev string) (string, error) {
	out, err := base.Exec(log, "lsblk", "-ndo", "PKNAME", partDev).Output()
	if err != nil {
		return "", fmt.Errorf("lsblk PKNAME %s: %w", partDev, err)
	}
	pk := strings.TrimSpace(string(out))
	if pk == "" {
		return "", fmt.Errorf("no parent disk for %s", partDev)
	}
	return "/dev/" + pk, nil
}
