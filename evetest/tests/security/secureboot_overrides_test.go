// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package security

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	// revive:disable:dot-imports
	. "github.com/onsi/gomega"

	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/evetest/netmodels"
)

// overridesGolden is pkg/grub/test/eve_overrides/cases/golden.cfg; keep the
// two identical, so the host case table and this test check the same file.
const overridesGolden = `# eve_overrides golden file, shared with evetest TestSecureBootGrubOverrides
set_global hv_dom0_mem_settings "dom0_mem=900M,max:900M"
set_global dom0_extra_args "$dom0_extra_args getty"
set_global dom0_console "console=tty0 console=ttyS0,115200n8"
set dom0_extra_args="${dom0_extra_args} log_buf_len=4M"
set_global initrd "/initrd.img"
set_global dom0_platform_tweaks "$dom0_platform_tweaks pcie_port_pm=off"
set_no_dirsync
set_global dom0_extra_args "$dom0_extra_args eve_blackbox; set check_signatures=no"
set_global dom0_extra_args "$dom0_extra_args eve_root=/dev/sda9"
export dom0_extra_args
set_global timeout 3
set_global dom0_extra_args "$dom0_extra_args nohz=off"
set_global eve_flavor kvm
`

var (
	// overridesGoldenRejected are the golden file's lines eve_overrides
	// must reject (cases/golden.out).
	overridesGoldenRejected = []string{"3", "6", "9", "10", "13"}
	// overridesGoldenApplied are kernel arguments only the golden file's
	// accepted lines add.
	overridesGoldenApplied = []string{"dom0_mem=900M,max:900M",
		"console=ttyS0,115200n8", "log_buf_len=4M", "eve_no_dirsync",
		"pcie_port_pm=off"}
	// overridesGoldenRefused are kernel arguments only its rejected lines
	// carry, and the defaults its accepted lines replace.
	overridesGoldenRefused = []string{"getty", "eve_blackbox",
		"eve_root=/dev/sda9", "nohz=off", "check_signatures=no",
		"dom0_mem=640M,max:800M", "console=ttyS0,115200"}
)

// overridesOver16 has 20 lines eve_overrides rejects.
func overridesOver16() string {
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&b, "bad %d\n", i)
	}
	return b.String()
}

// rejectedLines returns the line numbers of this boot's eve_overrides
// rejection events in the TPM event log, sorted numerically.
func rejectedLines(t *WithT, device *evetest.EdgeDevice) []string {
	out := runInPillar(t, device, "sh -c 'grep -aoE "+
		"\"eve_overrides: rejected line [0-9]+:\" "+
		"/hostfs/sys/kernel/security/tpm0/binary_bios_measurements "+
		"| tr -dc \"0-9\\n\" | sort -n'")
	return strings.Fields(out)
}

// cmdlineArgs returns the running kernel's arguments.
func cmdlineArgs(t *WithT, device *evetest.EdgeDevice) []string {
	return strings.Fields(sbShell(t, device, "cat /proc/cmdline"))
}

// expectGoldenApplied checks that exactly the golden file's accepted lines
// reached the kernel.
func expectGoldenApplied(t *WithT, device *evetest.EdgeDevice, when string) {
	args := cmdlineArgs(t, device)
	for _, a := range overridesGoldenApplied {
		t.Expect(args).To(ContainElement(a), "%s: accepted line not applied", when)
	}
	for _, a := range overridesGoldenRefused {
		t.Expect(args).ToNot(ContainElement(a), "%s: %s on the command line", when, a)
	}
	t.Expect(args).To(ContainElement(HavePrefix("eve_root=PARTUUID=")),
		"%s: the root must still come from the signed config", when)
	t.Expect(rejectedLines(t, device)).To(Equal(overridesGoldenRejected),
		"%s: the event log must hold one event per rejected line", when)
}

// rebootWithConfigFile writes name on the CONFIG partition (removes it if
// content is empty) and reboots.
func rebootWithConfigFile(t *WithT, device *evetest.EdgeDevice, name, content string) {
	writeConfigPartitionFile(t, device, name, content)
	device.SyncDisks()
	device.RequestReboot(false, true)
}

// TestSecureBootGrubOverrides checks how a Secure Boot image (tools/secureboot/sign.sh)
// applies an unsigned /config/grub.cfg through GRUB's eve_overrides command
// (docs/GRUB-OVERRIDES.md), on an onboarded device with a TPM. The host case
// table in pkg/grub/test/eve_overrides covers the grammar; this test covers
// what only a boot shows: the real GRUB, rootfs.cfg, the kernel command line,
// the TPM event log and the vault seal.
//
// It needs the signed image delivered as a local live image
// (EVETEST_EVE_LIVE_IMAGE) and UEFI firmware with Secure Boot enforcing and the
// signer's certificate in db (EVETEST_EVE_FIRMWARE_DIR).
//
// Phases / assertions
// -------------------
//  1. onboarded: the vault seal has settled on the local TPM key.
//  2. golden: the golden file (9 accepted lines, 5 rejected). Exactly the
//     accepted lines reach /proc/cmdline; the event log holds a rejection
//     event for lines 3, 6, 9, 10 and 13 only; the local seal fails, since the
//     file moves PCR 8, 9 and 14.
//  3. golden-again: the same file, another reboot. The same command line and
//     events, and the local seal holds: the measurements are deterministic.
//  4. over-16: 20 rejected lines. Exactly 16 rejection events (lines 1-16),
//     and none of the golden file's settings remain.
//  5. removed: no grub.cfg. No golden setting and no rejection event; the
//     local seal fails again.
func TestSecureBootGrubOverrides(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	evetest.Setup(
		evetest.RequireEdgeDevice{
			Name:              devName,
			WithHypervisor:    evetest.HypervisorKVM,
			WithTPM:           true,
			WithFilesystem:    evetest.FilesystemEXT4,
			DeviceReusePolicy: evetest.ResetDeviceConfig,
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
	t.Expect(rejectedLines(t, device)).To(BeEmpty(), "no grub.cfg yet")
	evetest.Checkpoint("onboarded")

	vaultAfter := func(when string) vaultStatus {
		waitForVaultOpen(t, device, evetest.FilesystemEXT4, when)
		st := readVaultStatus(t, device)
		log.Infof("vault %s: unlockMethod=%d mismatchingPCRs=%v",
			when, st.UnlockMethod, st.MismatchingPCRs)
		return st
	}

	rebootWithConfigFile(t, device, "grub.cfg", overridesGolden)
	evetest.Checkpoint("golden")
	st := vaultAfter("after the golden grub.cfg")
	expectGoldenApplied(t, device, "golden")
	t.Expect(st.UnlockMethod).ToNot(Equal(unlockTPMLocalSealed),
		"a new grub.cfg must move the sealed PCRs")

	device.RequestReboot(false, true)
	evetest.Checkpoint("golden-again")
	st = vaultAfter("after rebooting with the same grub.cfg")
	expectGoldenApplied(t, device, "golden-again")
	t.Expect(st.UnlockMethod).To(Equal(unlockTPMLocalSealed),
		"the same grub.cfg must measure the same (mismatchingPCRs=%v)",
		st.MismatchingPCRs)

	rebootWithConfigFile(t, device, "grub.cfg", overridesOver16())
	evetest.Checkpoint("over-16")
	vaultAfter("after the over-16 grub.cfg")
	var first16 []string
	for i := 1; i <= 16; i++ {
		first16 = append(first16, fmt.Sprint(i))
	}
	t.Expect(rejectedLines(t, device)).To(Equal(first16),
		"only the first 16 rejected lines are measured")
	args := cmdlineArgs(t, device)
	for _, a := range overridesGoldenApplied {
		t.Expect(args).ToNot(ContainElement(a), "over-16: %s outlived the golden file", a)
	}

	rebootWithConfigFile(t, device, "grub.cfg", "")
	evetest.Checkpoint("removed")
	st = vaultAfter("after removing grub.cfg")
	t.Expect(rejectedLines(t, device)).To(BeEmpty())
	args = cmdlineArgs(t, device)
	for _, a := range overridesGoldenApplied {
		t.Expect(args).ToNot(ContainElement(a), "removed: %s still applied", a)
	}
	t.Expect(st.UnlockMethod).ToNot(Equal(unlockTPMLocalSealed),
		"removing grub.cfg must move the sealed PCRs")
}

// TestSecureBootGrubOverridesNoTPM checks that under Secure Boot, without a
// TPM to measure it into, an unsigned /config/grub.cfg is not applied at all:
// rootfs.cfg runs eve_overrides with --require-tpm.
//
// Same image and firmware requirements as TestSecureBootGrubOverrides.
//
// Phases / assertions
// -------------------
//  1. onboarded: device without a TPM, firmware Secure Boot on.
//  2. golden: the golden file, then a reboot. None of its settings reach the
//     kernel, and the default memory and console settings remain.
func TestSecureBootGrubOverridesNoTPM(test *testing.T) {
	evetestT := evetest.Init(test)
	t := NewGomegaWithT(evetestT)
	defer evetest.Close()

	evetest.Setup(
		evetest.RequireEdgeDevice{
			Name:              devName,
			WithHypervisor:    evetest.HypervisorKVM,
			WithTPM:           false,
			WithFilesystem:    evetest.FilesystemEXT4,
			DeviceReusePolicy: evetest.ResetDeviceConfig,
		},
		evetest.RequireNetworkModel{
			NetworkModel: netmodels.SingleEthWithDHCP,
		},
	)
	device := evetest.GetEdgeDevice(devName)

	device.ApplyConfig(vaultMgmtPortConfig(), true, true)
	t.Expect(sbShell(t, device, "dmesg | grep -c 'Secure boot enabled' || true")).ToNot(
		Equal("0"), "firmware must report Secure Boot enabled")
	t.Expect(sbShell(t, device, "test -e /dev/tpm0 && echo tpm || echo none")).To(
		Equal("none"))
	before := cmdlineArgs(t, device)
	evetest.Checkpoint("onboarded")

	rebootWithConfigFile(t, device, "grub.cfg", overridesGolden)
	evetest.Checkpoint("golden")
	after := cmdlineArgs(t, device)
	for _, a := range overridesGoldenApplied {
		t.Expect(after).ToNot(ContainElement(a), "%s applied without a TPM", a)
	}
	sort.Strings(before)
	sort.Strings(after)
	t.Expect(after).To(Equal(before), "the command line must not change")
}
