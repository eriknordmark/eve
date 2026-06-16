// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// the critical files a real EVE /persist would hold (verified paths/names)
var persistFixture = map[string]string{
	"checkpoint/lastconfig":                       "edgedevconfig-with-ssh-keys",
	"checkpoint/lastconfig.bak":                   "edgedevconfig-backup",
	"checkpoint/controllercerts":                  "controller-signing-certs",
	"certs/ecdh.key.pem":                          "ECDH-PRIVATE-KEY",
	"certs/ecdh.cert.pem":                         "ECDH-CERT",
	"certs/attest.cert.pem":                       "ATTEST-CERT",
	"certs/ek.cert.pem":                           "EK-CERT",
	"status/nim/DevicePortConfigList/global.json": `{"dpc":"cellular-fallback"}`,
	// a file that must NOT be backed up (not in the patterns)
	"vault/volumes/app1.qcow2": "big-app-volume",
	"newlog/keep/log.gz":       "logs",
}

func writePersist(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

// backedUpRels is the set of fixture paths the patterns should select.
var backedUpRels = []string{
	"checkpoint/lastconfig", "checkpoint/lastconfig.bak", "checkpoint/controllercerts",
	"certs/ecdh.key.pem", "certs/ecdh.cert.pem", "certs/attest.cert.pem", "certs/ek.cert.pem",
	"status/nim/DevicePortConfigList/global.json",
}

func TestBackupSelectsTheRightFiles(t *testing.T) {
	persist := t.TempDir()
	backup := filepath.Join(t.TempDir(), "backup-persist")
	writePersist(t, persist, persistFixture)

	n, err := backupPersistFiles(persist, backup, defaultBackupPatterns)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if n != len(backedUpRels) {
		t.Errorf("backed up %d files, want %d", n, len(backedUpRels))
	}
	for _, rel := range backedUpRels {
		if _, err := os.Stat(filepath.Join(backup, rel)); err != nil {
			t.Errorf("missing from backup: %s (%v)", rel, err)
		}
	}
	// the non-pattern files must NOT be backed up
	for _, rel := range []string{"vault/volumes/app1.qcow2", "newlog/keep/log.gz"} {
		if _, err := os.Stat(filepath.Join(backup, rel)); err == nil {
			t.Errorf("%s should not have been backed up", rel)
		}
	}
}

func TestRestoreIntoWipedPersist(t *testing.T) {
	persist := t.TempDir()
	backup := filepath.Join(t.TempDir(), "backup-persist")
	writePersist(t, persist, persistFixture)
	if _, err := backupPersistFiles(persist, backup, defaultBackupPatterns); err != nil {
		t.Fatalf("backup: %v", err)
	}

	// simulate fsck failure: /persist recreated empty
	wiped := t.TempDir()

	restored, err := restorePersistFiles(backup, wiped)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored != len(backedUpRels) {
		t.Errorf("restored %d, want %d", restored, len(backedUpRels))
	}
	for _, rel := range backedUpRels {
		got, err := os.ReadFile(filepath.Join(wiped, rel))
		if err != nil {
			t.Errorf("not restored: %s (%v)", rel, err)
			continue
		}
		if string(got) != persistFixture[rel] {
			t.Errorf("%s content = %q, want %q", rel, got, persistFixture[rel])
		}
	}
}

func TestRestoreOnlyMissingOrChanged(t *testing.T) {
	persist := t.TempDir()
	backup := filepath.Join(t.TempDir(), "backup-persist")
	writePersist(t, persist, persistFixture)
	if _, err := backupPersistFiles(persist, backup, defaultBackupPatterns); err != nil {
		t.Fatalf("backup: %v", err)
	}

	// fsck passed but left one file truncated and removed another
	if err := os.WriteFile(filepath.Join(persist, "checkpoint/lastconfig"), []byte("corrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(persist, "certs/ecdh.key.pem")); err != nil {
		t.Fatal(err)
	}

	restored, err := restorePersistFiles(backup, persist)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored != 2 {
		t.Errorf("restored %d, want 2 (the changed + the missing)", restored)
	}
	if got, _ := os.ReadFile(filepath.Join(persist, "checkpoint/lastconfig")); string(got) != persistFixture["checkpoint/lastconfig"] {
		t.Errorf("lastconfig not restored to original")
	}
	if got, _ := os.ReadFile(filepath.Join(persist, "certs/ecdh.key.pem")); string(got) != persistFixture["certs/ecdh.key.pem"] {
		t.Errorf("ecdh.key.pem not restored")
	}
}

func TestRestoreNoBackupDirIsNoop(t *testing.T) {
	persist := t.TempDir()
	n, err := restorePersistFiles(filepath.Join(t.TempDir(), "absent"), persist)
	if err != nil || n != 0 {
		t.Errorf("restore with no backup dir: n=%d err=%v, want 0/nil", n, err)
	}
}

func TestCmdRestoreGCWhenFlagAbsent(t *testing.T) {
	persist := t.TempDir()
	backup := filepath.Join(t.TempDir(), "backup-persist")
	if err := os.MkdirAll(filepath.Join(backup, "certs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "certs/ek.cert.pem"), []byte("EK"), 0o600); err != nil {
		t.Fatal(err)
	}
	flagFile := filepath.Join(t.TempDir(), "shrink-persist") // absent

	if rc := cmdRestore([]string{"--persist", persist, "--backup-dir", backup, "--flag-file", flagFile}); rc != 0 {
		t.Fatalf("cmdRestore rc=%d", rc)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Error("flag absent: leftover backup dir must be GC'd")
	}
	if _, err := os.Stat(filepath.Join(persist, "certs/ek.cert.pem")); err == nil {
		t.Error("flag absent: must NOT restore into /persist")
	}
}

func TestCmdCleanupRemovesBackupWhenFlagGone(t *testing.T) {
	backup := filepath.Join(t.TempDir(), "backup-persist")
	if err := os.MkdirAll(filepath.Join(backup, "certs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "certs/ek.cert.pem"), []byte("EK"), 0o600); err != nil {
		t.Fatal(err)
	}
	flagFile := filepath.Join(t.TempDir(), "shrink-persist") // absent

	if rc := cmdCleanup([]string{"--backup-dir", backup, "--flag-file", flagFile}); rc != 0 {
		t.Fatalf("cmdCleanup rc=%d", rc)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Error("cleanup must remove the leftover backup dir when the flag file is gone")
	}
}

func TestCmdCleanupIsIdempotent(t *testing.T) {
	backup := filepath.Join(t.TempDir(), "absent")    // never created
	flagFile := filepath.Join(t.TempDir(), "no-flag") // absent

	if rc := cmdCleanup([]string{"--backup-dir", backup, "--flag-file", flagFile}); rc != 0 {
		t.Fatalf("cmdCleanup on absent dir rc=%d, want 0 (no-op)", rc)
	}
}

func TestCmdCleanupRefusesWhileFlagPresent(t *testing.T) {
	backup := filepath.Join(t.TempDir(), "backup-persist")
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "marker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	flagFile := filepath.Join(t.TempDir(), "shrink-persist")
	if err := os.WriteFile(flagFile, []byte("78G\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if rc := cmdCleanup([]string{"--backup-dir", backup, "--flag-file", flagFile}); rc == 0 {
		t.Fatal("cmdCleanup must refuse while the flag file is still present")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Error("cleanup must NOT remove the backup dir while the flag file is present")
	}
}

func TestCmdRestoreCleanupRemovesFlagFirstThenBackup(t *testing.T) {
	persist := t.TempDir() // simulate wiped /persist
	backup := filepath.Join(t.TempDir(), "backup-persist")
	if err := os.MkdirAll(filepath.Join(backup, "certs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "certs/ek.cert.pem"), []byte("EK"), 0o600); err != nil {
		t.Fatal(err)
	}
	flagFile := filepath.Join(t.TempDir(), "shrink-persist")
	if err := os.WriteFile(flagFile, []byte("78G\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if rc := cmdRestore([]string{"--persist", persist, "--backup-dir", backup, "--flag-file", flagFile, "--cleanup"}); rc != 0 {
		t.Fatalf("cmdRestore rc=%d", rc)
	}
	if got, _ := os.ReadFile(filepath.Join(persist, "certs/ek.cert.pem")); string(got) != "EK" {
		t.Error("flag present: must restore the backed-up file")
	}
	if _, err := os.Stat(flagFile); !os.IsNotExist(err) {
		t.Error("cleanup must remove the flag")
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Error("cleanup must remove the backup dir")
	}
}
