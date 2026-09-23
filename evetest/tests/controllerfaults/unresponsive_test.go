// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

// Instrument for lf-edge/eve#5916 / #6441. Not for upstream.

package controllerfaults_test

import (
	"encoding/base64"
	"testing"
	"time"

	// revive:disable:dot-imports
	. "github.com/onsi/gomega"

	eveconfig "github.com/lf-edge/eve-api/go/config"
	"github.com/lf-edge/eve-api/go/evecommon"
	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/evetest/netmodels"
	"github.com/lf-edge/eve/pkg/pillar/types"
)

// Pinned Alpine cloud images, copied from tests/security/vcom_test.go.
var unresponsiveImages = map[string]struct {
	relativePath string
	sha256       string
	sizeBytes    uint64
}{
	"amd64": {
		relativePath: "/alpine/v3.24/releases/cloud/generic_alpine-3.24.1-x86_64-bios-cloudinit-r0.qcow2",
		sha256:       "6e2e6fe0572b6632527f268d3659e8fccebda4e1ee470fafe2c4d7b85b6a4df6",
		sizeBytes:    183697408,
	},
	"arm64": {
		relativePath: "/alpine/v3.24/releases/cloud/generic_alpine-3.24.1-aarch64-uefi-cloudinit-r0.qcow2",
		sha256:       "3059a6280977c2122982632e0317c5ddbd39069d46ca1e60480de283091f720f",
		sizeBytes:    239271936,
	},
}

// TestHaltUnresponsiveGuest measures how long a guest which never acts on the
// ACPI poweroff request takes to be reported HALTED. acpid is what services the
// power button on Alpine, so a guest with it stopped and removed from the
// runlevel ignores the request indefinitely -- the condition lf-edge/eve#5916
// observed in CI, where four pods each took the full 60s + 600s budget.
//
// The mode is HVM, which already gets the 60s first wait, so the graceful bound
// does not apply and the whole difference between builds is whether the
// escalation terminates the domain or re-sends the same ignored request.
func TestHaltUnresponsiveGuest(test *testing.T) {
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
	image := unresponsiveImages[device.GetArch()]

	cloudConfig := `#cloud-config
runcmd:
  - rc-service acpid stop
  - rc-update del acpid default
`
	devConfig := evetest.NewEdgeDeviceConfig(devName)
	dhcpNet := devConfig.AddNetwork(evetest.DHCPNetworkConfig{
		NetworkType: evecommon.NetworkType_V4Only,
	})
	devConfig.AddNetworkAdapter(evetest.NetworkAdapterConfig{
		LogicalLabel:  portLogicalLabel,
		PhysicalLabel: portIfName,
		InterfaceName: portIfName,
		NetworkUUID:   dhcpNet,
		Usage:         evecommon.PhyIoMemberUsage_PhyIoUsageMgmtAndApps,
	})
	niUUID := devConfig.AddNetworkInstance(evetest.LocalNetworkInstanceConfig{
		DisplayName: "local-ni",
		Port:        portLogicalLabel,
		Subnet:      evetest.IPSubnet("10.11.12.0/24"),
		DHCPRange: types.IPRange{
			Start: evetest.IPAddress("10.11.12.2"),
			End:   evetest.IPAddress("10.11.12.254"),
		},
		Gateway: evetest.IPAddress("10.11.12.1"),
		MTU:     1500,
	})
	appUUID := devConfig.AddApplication(evetest.ApplicationInstanceConfig{
		DisplayName: "unresponsive-vm",
		Activate:    true,
		Image: evetest.HTTPStorage{
			ImageFormat:       eveconfig.Format_QCOW2,
			ImageSHA256:       image.sha256,
			MaxDownloadBytes:  image.sizeBytes,
			ImageRelativePath: image.relativePath,
			ServerAddress:     "dl-cdn.alpinelinux.org",
			UseHTTPS:          true,
		},
		VirtualizationMode: eveconfig.VmMode_HVM,
		CPUs:               1,
		MemoryBytes:        512 * evetest.MiB,
		UserData:           base64.StdEncoding.EncodeToString([]byte(cloudConfig)),
		NetworkAdapters: []evetest.AppNetworkAdapter{
			evetest.VirtualNetworkAdapter{
				LogicalLabel:        "vif0",
				NetworkInstanceUUID: niUUID,
				ACLAllowRules: []evetest.ACLAllowRule{
					{
						Protocol:     evetest.NetworkProtocolAny,
						RemoteSubnet: evetest.IPSubnet("0.0.0.0/0"),
					},
				},
			},
		},
	})

	appUpdates, stopAppWatch := device.WatchAppInfo(appUUID)
	defer stopAppWatch()
	device.ApplyConfig(devConfig, true, true)
	device.WaitUntilAppIsRunning(appUUID, 10*time.Minute)

	// Let cloud-init finish disabling acpid before the request is sent; the
	// point here is a guest which ignores it, not one which races it.
	time.Sleep(90 * time.Second)

	halt := watchHalt(appUpdates)
	device.DeactivateApplication(appUUID, false, 0)
	log.Info("Deactivated a guest which does not service ACPI poweroff")

	settleApp(t, halt)
}
