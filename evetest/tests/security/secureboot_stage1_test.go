// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package security

import (
	"os"
	"strings"
	"testing"

	// revive:disable:dot-imports
	. "github.com/onsi/gomega"

	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/evetest/netmodels"
)

const (
	stage1InitialVersionParamKey = "INITIAL_EVE_VERSION"
	stage1EFIParamKey            = "STAGE1_EFI"

	stage1DevicePath = "/persist/evetest-stage1.efi"
)

// TestSecureBootStage1Swap moves a fielded device toward verified boot with
// firmware Secure Boot still off: it installs a released EVE, upgrades it to a
// signed image (tools/secureboot/sign.sh), then replaces the stage-1 GRUB on the
// ESP with the signed stage-1. The upgrade has to come first: the signed stage-1
// enforces GPG signatures with or without Secure Boot, so on an unsigned rootfs
// it finds no BOOTX64.EFI.sig and reboots forever. Enabling Secure Boot in
// firmware is the step after this one.
//
// The signed image comes as a local live build (EVETEST_EVE_LIVE_IMAGE,
// EVETEST_EVE_VERSION). STAGE1_EFI names its stage1.efi as the test sees it,
// e.g. /artifacts/stage1.efi for a copy in EVETEST_COLLECT_ARTIFACTS.
//
// Phases / assertions
// -------------------
//  1. onboarded: the released version is running, firmware Secure Boot off,
//     vault open on the local TPM seal.
//  2. upgrade-complete: the signed image is running, root on dm-verity without
//     signatures required.
//  3. stage1-swapped: EFI/BOOT/BOOTX64.EFI on ESP-A replaced over SSH, then a
//     reboot. The device must come back running the signed image with its vault
//     open, and the local seal must settle again within maxSettleReboots.
func TestSecureBootStage1Swap(test *testing.T) {
	stage1Swap(test, false)
}

// TestSecureBootStage1SwapPowerCut is TestSecureBootStage1Swap with a hard power
// cut, not a reboot, after each step: once the signed image is committed and
// once stage-1 is on disk. The two steps cannot be made atomic in the field, so
// the device must come up after an outage at either boundary.
func TestSecureBootStage1SwapPowerCut(test *testing.T) {
	stage1Swap(test, true)
}

func stage1Swap(test *testing.T, powerCut bool) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	evetest.DefineTestParameters(
		evetest.EVEVersionParameter(),
		evetest.TestParameterDefinition{
			Key:          stage1InitialVersionParamKey,
			DefaultValue: "17.12.0",
			Description: evetest.TestParameterDescription{
				Summary: "Released EVE version to install before swapping stage-1",
				Default: "17.12.0",
			},
		},
		evetest.TestParameterDefinition{
			Key:          stage1EFIParamKey,
			DefaultValue: "",
			Description: evetest.TestParameterDescription{
				Summary: "Path to the signed stage1.efi inside the evetest container",
			},
		},
	)
	initialVersion := evetest.GetTestParameter[string](stage1InitialVersionParamKey)
	signedVersion := evetest.GetEVEVersionParameterValue()
	stage1Path := evetest.GetTestParameter[string](stage1EFIParamKey)
	stage1, err := os.ReadFile(stage1Path)
	if err != nil {
		evetestT.Fatalf("reading %s %q: %v", stage1EFIParamKey, stage1Path, err)
	}

	evetest.Setup(
		evetest.RequireEdgeDevice{
			Name:              devName,
			WithEVEVersion:    initialVersion,
			WithHypervisor:    evetest.HypervisorKVM,
			WithTPM:           true,
			WithFilesystem:    evetest.FilesystemEXT4,
			DeviceReusePolicy: evetest.CreateFromScratchWithInstaller,
		},
		evetest.RequireNetworkModel{
			NetworkModel: netmodels.SingleEthWithDHCP,
		},
	)
	device := evetest.GetEdgeDevice(devName)
	log := evetest.Logger()

	sh := func(cmd string) string {
		out, stderr, err := device.RunShellScript(cmd, shellCmdTimeout, 0)
		t.Expect(err).ToNot(HaveOccurred(), "%q failed (stderr: %s)", cmd, stderr)
		return strings.TrimSpace(out)
	}
	// An outage does not flush the guest, so no SyncDisks. PowerOn cannot wait
	// for the reboot signal after an external power cycle; the vault opening
	// is the signal, and the config round trip settles the reboot audit.
	cutPower := func(when string) {
		log.Infof("power cut %s", when)
		device.PowerOff()
		device.PowerOn(false)
		waitForVaultOpen(t, device, evetest.FilesystemEXT4, "after the power cut "+when)
		device.ApplyConfig(vaultMgmtPortConfig(), true, true)
	}

	device.ApplyConfig(vaultMgmtPortConfig(), true, true)
	waitForVaultOpen(t, device, evetest.FilesystemEXT4, "after onboarding")
	settleVaultSeal(t, device, evetest.FilesystemEXT4)
	evetest.Checkpoint("onboarded")
	version := sh("cat /run/eve-release")
	log.Infof("running %s", version)
	t.Expect(version).To(HavePrefix(initialVersion))
	t.Expect(sh("dmesg | grep -c 'Secure boot disabled' || true")).ToNot(Equal("0"),
		"firmware must report Secure Boot disabled")

	device.UpgradeEVE(signedVersion, evetest.HypervisorKVM, evetest.BaseOSDatastoreHTTP,
		true, false)
	evetest.Checkpoint("upgrade-complete")
	waitForVaultOpen(t, device, evetest.FilesystemEXT4, "after the upgrade")
	version = sh("cat /run/eve-release")
	log.Infof("running %s", version)
	t.Expect(sh("awk '$2 == \"/\" {print $1}' /proc/1/mounts | tail -1")).To(
		Equal("/dev/mapper/eve-root"))
	t.Expect(sh("cat /sys/module/dm_verity/parameters/require_signatures")).To(Equal("N"))
	if powerCut {
		cutPower("after the upgrade")
		t.Expect(sh("cat /run/eve-release")).To(Equal(version))
	}
	settleVaultSeal(t, device, evetest.FilesystemEXT4)
	log.Infof("partitions before the swap: %s", sh(
		"eve exec pillar sh -c 'echo cur=$(zboot curpart) "+
			"IMGA=$(zboot partstate IMGA) IMGB=$(zboot partstate IMGB)'"))

	// ESP-B shares the "EFI System" label, so ESP-A is found as partition 1 of
	// the disk holding CONFIG.
	device.WriteFile(stage1DevicePath, stage1)
	swapped := runInPillar(t, device, "sh -c 'set -e; C=$(/sbin/findfs PARTLABEL=CONFIG); "+
		"E=${C%[0-9]*}1; M=$(mktemp -d); mount -t vfat $E $M; "+
		"ls -l $M/EFI/BOOT; cp $M/EFI/BOOT/BOOTX64.EFI /persist/evetest-bootx64.orig; "+
		"cp "+stage1DevicePath+" $M/EFI/BOOT/BOOTX64.EFI; sync; "+
		"sha256sum "+stage1DevicePath+" $M/EFI/BOOT/BOOTX64.EFI; umount $M'")
	log.Infof("stage-1 swap:\n%s", swapped)
	sums := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(swapped), "\n") {
		if f := strings.Fields(line); len(f) == 2 && len(f[0]) == 64 {
			sums[f[0]] = true
		}
	}
	t.Expect(sums).To(HaveLen(1), "BOOTX64.EFI on ESP-A must now be the signed stage-1")
	if powerCut {
		cutPower("after the stage-1 swap")
	} else {
		device.SyncDisks()
		device.RequestReboot(false, true)
		waitForVaultOpen(t, device, evetest.FilesystemEXT4, "after the stage-1 swap")
	}
	evetest.Checkpoint("stage1-swapped")
	st := readVaultStatus(t, device)
	log.Infof("vault after stage-1 swap: unlockMethod=%d mismatchingPCRs=%v",
		st.UnlockMethod, st.MismatchingPCRs)
	t.Expect(sh("cat /run/eve-release")).To(Equal(version))
	t.Expect(sh("awk '$2 == \"/\" {print $1}' /proc/1/mounts | tail -1")).To(
		Equal("/dev/mapper/eve-root"))
	settleVaultSeal(t, device, evetest.FilesystemEXT4)
}
