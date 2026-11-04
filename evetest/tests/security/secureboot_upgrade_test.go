// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package security

import (
	"strings"
	"testing"

	// revive:disable:dot-imports
	. "github.com/onsi/gomega"

	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/evetest/netmodels"
)

const signedUpgradeVersionParamKey = "SIGNED_UPGRADE_VERSION"

func signedUpgradeVersionParameter() evetest.TestParameterDefinition {
	return evetest.TestParameterDefinition{
		Key:          signedUpgradeVersionParamKey,
		DefaultValue: "",
		Description: evetest.TestParameterDescription{
			Summary: "Local build (dist directory name) of the signed image to upgrade to",
		},
	}
}

// sbShell runs cmd over the device's management SSH and returns its trimmed
// stdout.
func sbShell(t *WithT, device *evetest.EdgeDevice, cmd string) string {
	out, stderr, err := device.RunShellScript(cmd, shellCmdTimeout, 0)
	t.Expect(err).ToNot(HaveOccurred(), "%q failed (stderr: %s)", cmd, stderr)
	return strings.TrimSpace(out)
}

func sbCurpart(t *WithT, device *evetest.EdgeDevice) string {
	return strings.TrimSpace(runInPillar(t, device, "zboot curpart"))
}

func sbPartState(t *WithT, device *evetest.EdgeDevice, part string) string {
	return strings.TrimSpace(runInPillar(t, device, "zboot partstate "+part))
}

// sbStage1Sum returns the SHA-256 of stage-1 on ESP-A. ESP-B shares the "EFI
// System" label, so ESP-A is found as partition 1 of the disk holding CONFIG.
func sbStage1Sum(t *WithT, device *evetest.EdgeDevice) string {
	out := runInPillar(t, device, "sh -c 'set -e; C=$(/sbin/findfs PARTLABEL=CONFIG); "+
		"E=${C%[0-9]*}1; M=$(mktemp -d); mount -t vfat -o ro $E $M; "+
		"sha256sum $M/EFI/BOOT/BOOTX64.EFI; umount $M'")
	f := strings.Fields(out)
	t.Expect(f).ToNot(BeEmpty(), "no checksum of stage-1 on ESP-A")
	return f[0]
}

// expectSBVerifiedBoot checks firmware Secure Boot, lockdown and a dm-verity
// root with signatures required on the running boot.
func expectSBVerifiedBoot(t *WithT, device *evetest.EdgeDevice, when string) {
	t.Expect(sbShell(t, device, "dmesg | grep -c 'Secure boot enabled' || true")).ToNot(
		Equal("0"), "firmware must report Secure Boot enabled "+when)
	t.Expect(sbShell(t, device, "cat /sys/kernel/security/lockdown")).To(
		ContainSubstring("[integrity]"), "lockdown "+when)
	// SSH lands in the debug container, which shares the host PID namespace.
	t.Expect(sbShell(t, device, "awk '$2 == \"/\" {print $1}' /proc/1/mounts | tail -1")).To(
		Equal("/dev/mapper/eve-root"), "root device "+when)
	t.Expect(sbShell(t, device, "cat /sys/module/dm_verity/parameters/require_signatures")).To(
		Equal("Y"), "dm-verity signatures required "+when)
}

// TestSecureBootSignedUpgrade upgrades a device running a signed image
// (tools/secureboot/sign.sh) to another image signed with the same keys, with
// firmware Secure Boot on. Stage-1 on the ESP stays as installed and must accept
// the new stage-2 on the other partition.
//
// Both images come as local live builds in EVETEST_EVE_DIST_DIR: the initial one
// is EVETEST_EVE_VERSION, the target SIGNED_UPGRADE_VERSION. The firmware
// (EVETEST_EVE_FIRMWARE_DIR or the initial build's installer/firmware) must
// enforce Secure Boot with the signer's certificate in db.
//
// Phases / assertions
// -------------------
//  1. onboarded: the initial image on IMGA, verified boot on (Secure Boot,
//     lockdown, dm-verity root with signatures required).
//  2. upgrade-complete: the target image runs from IMGB with verified boot on,
//     stage-1 on the ESP is unchanged, and the vault opens.
//  3. rebooted: the target image is still running from IMGB.
func TestSecureBootSignedUpgrade(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	evetest.DefineTestParameters(
		evetest.EVEVersionParameter(),
		signedUpgradeVersionParameter(),
	)
	targetVersion := evetest.GetTestParameter[string](signedUpgradeVersionParamKey)
	if targetVersion == "" {
		evetestT.Fatalf("%s must name the signed image to upgrade to",
			signedUpgradeVersionParamKey)
	}

	evetest.Setup(
		evetest.RequireEdgeDevice{
			Name:              devName,
			WithHypervisor:    evetest.HypervisorKVM,
			WithTPM:           true,
			WithFilesystem:    evetest.FilesystemEXT4,
			DeviceReusePolicy: evetest.CreateFromScratchWithLiveImage,
		},
		evetest.RequireNetworkModel{
			NetworkModel: netmodels.SingleEthWithDHCP,
		},
	)
	device := evetest.GetEdgeDevice(devName)
	log := evetest.Logger()

	device.ApplyConfig(vaultMgmtPortConfig(), true, true)
	waitForVaultOpen(t, device, evetest.FilesystemEXT4, "after onboarding")
	settleVaultSeal(t, device, evetest.FilesystemEXT4)
	evetest.Checkpoint("onboarded")
	initial := sbShell(t, device, "cat /run/eve-release")
	log.Infof("running %s", initial)
	t.Expect(initial).ToNot(HavePrefix(targetVersion),
		"the device must start on a different image than the upgrade target")
	t.Expect(sbCurpart(t, device)).To(Equal("IMGA"))
	expectSBVerifiedBoot(t, device, "on the initial image")
	stage1 := sbStage1Sum(t, device)
	log.Infof("stage-1 on ESP-A: %s", stage1)

	device.UpgradeEVE(targetVersion, evetest.HypervisorKVM, evetest.BaseOSDatastoreHTTP,
		true, false)
	evetest.Checkpoint("upgrade-complete")
	waitForVaultOpen(t, device, evetest.FilesystemEXT4, "after the upgrade")
	upgraded := sbShell(t, device, "cat /run/eve-release")
	log.Infof("running %s", upgraded)
	t.Expect(upgraded).To(HavePrefix(targetVersion))
	t.Expect(sbCurpart(t, device)).To(Equal("IMGB"))
	expectSBVerifiedBoot(t, device, "after the upgrade")
	t.Expect(sbStage1Sum(t, device)).To(Equal(stage1), "the upgrade must leave stage-1 as installed")
	st := readVaultStatus(t, device)
	log.Infof("vault after the upgrade: unlockMethod=%d mismatchingPCRs=%v",
		st.UnlockMethod, st.MismatchingPCRs)
	log.Infof("partitions: IMGA=%s IMGB=%s",
		sbPartState(t, device, "IMGA"), sbPartState(t, device, "IMGB"))

	settleVaultSeal(t, device, evetest.FilesystemEXT4)
	evetest.Checkpoint("rebooted")
	t.Expect(sbShell(t, device, "cat /run/eve-release")).To(Equal(upgraded))
	t.Expect(sbCurpart(t, device)).To(Equal("IMGB"))
	expectSBVerifiedBoot(t, device, "after a reboot on the upgraded image")
}
