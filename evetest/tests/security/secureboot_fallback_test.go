// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package security

import (
	"strings"
	"testing"
	"time"

	// revive:disable:dot-imports
	. "github.com/onsi/gomega"

	eveinfo "github.com/lf-edge/eve-api/go/info"
	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/evetest/netmodels"
	pillartypes "github.com/lf-edge/eve/pkg/pillar/types"
)

const (
	// fallbackCommitWindow keeps the upgraded image uncommitted for longer than
	// the test needs to cut power, so the cut cannot race the commit.
	fallbackCommitWindow = 3600
	fallbackBootTimeout  = 15 * time.Minute
)

// TestSecureBootSignedFallback upgrades a device running a signed image to
// another signed image with firmware Secure Boot on, and cuts power while the
// new image is still on trial. Stage-1 on the ESP must then fall back to the
// signed image on IMGA, which must boot verified and report the trial image
// as failed.
//
// Inputs are those of TestSecureBootSignedUpgrade: EVETEST_EVE_VERSION is the
// initial local build, SIGNED_UPGRADE_VERSION the one to upgrade to.
//
// Phases / assertions
// -------------------
//  1. onboarded: the initial image on IMGA with verified boot on; the commit
//     window (timer.test.baseimage.update) set to its maximum.
//  2. on-trial: the target image runs from IMGB, verified, with IMGB
//     inprogress.
//  3. fallen-back: after a power cut, the initial image runs from IMGA with
//     verified boot on and stage-1 unchanged, IMGB is still inprogress, and
//     the controller sees the target image FAILED.
//  4. rebooted: the initial image is still running from IMGA.
func TestSecureBootSignedFallback(test *testing.T) {
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

	config := vaultMgmtPortConfig()
	props := pillartypes.NewConfigItemValueMap()
	props.SetGlobalValueInt(pillartypes.MintimeUpdateSuccess, fallbackCommitWindow)
	config.SetConfigProperties(props)
	device.ApplyConfig(config, true, true)
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
	restarts := device.GetDeviceInfo().GetRestartCounter()

	device.UpgradeEVE(targetVersion, evetest.HypervisorKVM, evetest.BaseOSDatastoreHTTP,
		false, false)
	t.Eventually(func(g Gomega) {
		out, _, err := device.RunShellScript("cat /run/eve-release", shellCmdTimeout, 0)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(strings.TrimSpace(out)).To(HavePrefix(targetVersion))
	}, fallbackBootTimeout, pollingInterval).Should(Succeed(),
		"the device must boot the target image")
	// The harness counts reboots from the restart counter in info messages, and
	// a power cycle resets its baseline: the trial boot must be reported first.
	t.Eventually(func() uint32 {
		return device.GetDeviceInfo().GetRestartCounter()
	}, fallbackBootTimeout, pollingInterval).Should(BeNumerically(">", restarts),
		"the target image must report its boot to the controller")
	evetest.Checkpoint("on-trial")
	t.Expect(sbCurpart(t, device)).To(Equal("IMGB"))
	t.Expect(sbPartState(t, device, "IMGB")).To(Equal("inprogress"),
		"the target image must still be on trial when power is cut")
	expectSBVerifiedBoot(t, device, "on the image on trial")

	log.Infof("power cut while %s is on trial", targetVersion)
	device.PowerOff()
	device.PowerOn(false)
	waitForVaultOpen(t, device, evetest.FilesystemEXT4, "after the fallback")
	device.ApplyConfig(device.GetConfig(), true, true)
	evetest.Checkpoint("fallen-back")
	t.Expect(sbShell(t, device, "cat /run/eve-release")).To(Equal(initial))
	t.Expect(sbCurpart(t, device)).To(Equal("IMGA"))
	expectSBVerifiedBoot(t, device, "after the fallback")
	t.Expect(sbStage1Sum(t, device)).To(Equal(stage1))
	t.Expect(sbPartState(t, device, "IMGB")).To(Equal("inprogress"))
	t.Eventually(func(g Gomega) {
		var found bool
		for _, sw := range device.GetDeviceInfo().GetSwList() {
			if !strings.HasPrefix(sw.GetShortVersion(), targetVersion) {
				continue
			}
			found = true
			g.Expect(sw.GetUserStatus()).To(Equal(eveinfo.BaseOsStatus_FAILED))
			g.Expect(sw.GetSwErr().GetDescription()).To(
				ContainSubstring("power failure or crash"))
		}
		g.Expect(found).To(BeTrue(), "no SwList entry for %s", targetVersion)
	}, 5*time.Minute, pollingInterval).Should(Succeed(),
		"the controller must see the target image as failed")

	device.RequestReboot(false, true)
	waitForVaultOpen(t, device, evetest.FilesystemEXT4, "after a reboot on the initial image")
	evetest.Checkpoint("rebooted")
	t.Expect(sbShell(t, device, "cat /run/eve-release")).To(Equal(initial))
	t.Expect(sbCurpart(t, device)).To(Equal("IMGA"))
	expectSBVerifiedBoot(t, device, "after a reboot on the initial image")
}
