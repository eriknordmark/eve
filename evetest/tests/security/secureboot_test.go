// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package security

import (
	"encoding/base64"
	"strings"
	"testing"

	// revive:disable:dot-imports
	. "github.com/onsi/gomega"

	"github.com/lf-edge/eve/evetest"
	"github.com/lf-edge/eve/evetest/netmodels"
)

const (
	// sbInjectMarker is the kernel argument the injected /config/grub.cfg
	// tries to add on a line eve_overrides must reject.
	sbInjectMarker = "evetest_sb_inject"
	// sbAllowedArg is a kernel argument on the allow-list, distinct from the
	// image's own log_buf_len=2M.
	sbAllowedArg = "log_buf_len=4M"
)

// TestSecureBootOnboarded checks a Secure Boot image (tools/secureboot/sign.sh)
// on an onboarded device whose vault seal has settled, and that an unsigned
// /config/grub.cfg is applied only as far as its allow-list permits.
//
// It needs the signed image delivered as a local live image
// (EVETEST_EVE_LIVE_IMAGE) and UEFI firmware with Secure Boot enforcing and the
// signer's certificate in db (EVETEST_EVE_FIRMWARE_DIR).
//
// Phases / assertions
// -------------------
//  1. onboarded: config applied, vault open on the local TPM seal.
//  2. On that boot: firmware Secure Boot on, lockdown integrity, root on
//     dm-verity with signatures required.
//  3. grub-cfg-injected: an unsigned /config/grub.cfg with an allow-listed
//     kernel argument and one that is not, then a reboot. Only the first must
//     reach the kernel, the second must appear as rejected in the TPM event
//     log, and the local seal must fail, since /config content is measured
//     into PCR 14.
func TestSecureBootOnboarded(test *testing.T) {
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
	evetest.Checkpoint("onboarded")

	sh := func(cmd string) string {
		out, stderr, err := device.RunShellScript(cmd, shellCmdTimeout, 0)
		t.Expect(err).ToNot(HaveOccurred(), "%q failed (stderr: %s)", cmd, stderr)
		return strings.TrimSpace(out)
	}
	t.Expect(sh("dmesg | grep -c 'Secure boot enabled' || true")).ToNot(Equal("0"),
		"firmware must report Secure Boot enabled")
	t.Expect(sh("cat /sys/kernel/security/lockdown")).To(ContainSubstring("[integrity]"))
	// SSH lands in the debug container, which shares the host PID namespace.
	t.Expect(sh("awk '$2 == \"/\" {print $1}' /proc/1/mounts | tail -1")).To(
		Equal("/dev/mapper/eve-root"))
	t.Expect(sh("cat /sys/module/dm_verity/parameters/require_signatures")).To(Equal("Y"))
	cmdline := sh("cat /proc/cmdline")
	t.Expect(cmdline).To(ContainSubstring("dm_verity.require_signatures=1"))
	t.Expect(cmdline).To(ContainSubstring("eve_root=PARTUUID="))
	t.Expect(device.GetDeviceInfo().GetMaintenanceMode()).To(BeFalse())

	// The runtime /config is a read-only tmpfs copy; boot reads the CONFIG
	// partition.
	log.Infof("Writing grub.cfg to the CONFIG partition and rebooting...")
	grubCfg := "set_global dom0_extra_args \"$dom0_extra_args " + sbAllowedArg + "\"\n" +
		"set_global dom0_extra_args \"$dom0_extra_args " + sbInjectMarker + "\"\n"
	written := runInPillar(t, device, "sh -c 'M=$(mktemp -d) && "+
		"mount -t vfat $(/sbin/findfs PARTLABEL=CONFIG) $M && "+
		"echo "+base64.StdEncoding.EncodeToString([]byte(grubCfg))+" | base64 -d > $M/grub.cfg && "+
		"cat $M/grub.cfg; umount $M'")
	t.Expect(written).To(ContainSubstring(`"$dom0_extra_args `+sbInjectMarker+`"`),
		"the injected grub.cfg must be in place before the reboot")
	device.SyncDisks()
	device.RequestReboot(false, true)
	evetest.Checkpoint("grub-cfg-injected")

	waitForVaultOpen(t, device, evetest.FilesystemEXT4, "after the grub.cfg injection")
	cmdline = sh("cat /proc/cmdline")
	t.Expect(cmdline).To(ContainSubstring(sbAllowedArg),
		"eve_overrides must apply the allow-listed line")
	t.Expect(cmdline).ToNot(ContainSubstring(sbInjectMarker),
		"eve_overrides must reject a parameter off the allow-list")
	t.Expect(runInPillar(t, device, "grep -ac 'eve_overrides: rejected line 2' "+
		"/hostfs/sys/kernel/security/tpm0/binary_bios_measurements")).To(
		HavePrefix("1"), "the rejected line must be in the TPM event log")
	st := readVaultStatus(t, device)
	log.Infof("vault after injection: unlockMethod=%d mismatchingPCRs=%v",
		st.UnlockMethod, st.MismatchingPCRs)
	t.Expect(st.UnlockMethod).ToNot(Equal(unlockTPMLocalSealed),
		"a changed /config must move PCR 14 and break the local seal")
}
