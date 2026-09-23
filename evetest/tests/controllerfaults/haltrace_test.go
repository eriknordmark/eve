// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

// Instrument for lf-edge/eve#6440. Not for upstream.

package controllerfaults_test

import (
	"testing"
	"time"

	// revive:disable:dot-imports
	. "github.com/onsi/gomega"

	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/evetest/netmodels"
)

// TestHaltAfterImmediateDeactivate measures how long an application takes to be
// reported HALTED when it is deactivated in the same second it first reports
// RUNNING, which is the lf-edge/eve#6440 trigger. Nothing is placed between the
// two, so the gap does not drift out of the window the way it does in a test
// that samples metrics first.
func TestHaltAfterImmediateDeactivate(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)

	evetest.DefineTestParameters(
		evetest.HypervisorParameter(),
	)

	hypervisor := evetest.GetHypervisorParameterValue()
	evetest.Setup(
		evetest.RequireEdgeDevice{
			Name:              devName,
			WithHypervisor:    hypervisor,
			DeviceReusePolicy: evetest.ResetDeviceConfig,
		},
		evetest.RequireNetworkModel{NetworkModel: netmodels.SingleEthWithDHCP},
	)
	device := evetest.GetEdgeDevice(devName)
	log := evetest.Logger()

	devConfig, appUUID := appDeviceConfig()
	appUpdates, stopAppWatch := device.WatchAppInfo(appUUID)
	defer stopAppWatch()
	device.ApplyConfig(devConfig, true, true)

	runningAt := time.Now()
	device.WaitUntilAppIsRunning(appUUID, 5*time.Minute)
	runningAt = time.Now()

	halt := watchHalt(appUpdates)
	device.DeactivateApplication(appUUID, false, 0)
	log.Infof("GSB-E2E gap RUNNING->deactivate %v",
		halt.deactivatedAt.Sub(runningAt).Round(time.Millisecond))

	settleApp(t, halt)
}
