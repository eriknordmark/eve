// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

// /config backup + restore for the persist shrink (design-doc requirements 3 & 4):
// if the shrink ever recreates /persist empty (fsck failure), the device must
// still come up on cellular, keep its ssh access, and retain its device-identity
// certs/keys in /persist/certs/ (attestation + credential decryption) so it can
// re-attest and recover its vault key from the controller. Those keys cannot be
// re-derived once the filesystem is wiped, so the critical files are copied to a
// small directory on the CONFIG partition before the destructive work, and
// restored into /persist afterwards if missing or changed.
//
// IMPORTANT: the --backup-dir/--flag-file paths must point at the CONFIG
// partition mounted READ-WRITE (find PARTLABEL=CONFIG, mount it rw, sync,
// unmount). At runtime EVE's /config is a read-only tmpfs RAM copy of that
// partition, so writes to the runtime /config land in RAM and are lost on the
// very reboot the shrink depends on. The caller owns that mount (see
// pkg/pillar/docs/diskconvert.md); these subcommands just read/write the paths
// they are given.
//
// Timing is also owned by the caller, because the files can only be read/written
// when /persist is mounted:
//   - `backup`  runs ONLINE (baseosmgr), /persist mounted, before the reboot.
//              It writes the backups first and the shrink-persist flag file last.
//   - `shrink`  runs with /persist UNMOUNTED (storage-init), does the shrink.
//   - `restore` runs after /persist is mounted again. If the flag file is gone
//              it garbage-collects any leftover backup dir (so stray /config
//              files don't perturb the measure-config PCR). If the flag file is
//              present it copies back missing/changed files, then (with
//              --cleanup) removes the flag file FIRST and the backup dir second,
//              so a crash mid-cleanup is safe.
//   - `cleanup` is the idempotent end-of-conversion sweep the caller runs after
//              ANY backup, independent of whether a restore ran. A crash during
//              restore's --cleanup can clear the flag file but leave the backup
//              dir behind; once the device reaches the steady `proceed` state
//              nothing re-runs restore to GC it, so the leftover dir would linger
//              and keep perturbing the measure-config PCR. cleanup removes the
//              backup dir, but only once the flag file is gone (while it is
//              present a shrink is still pending and the dir is the only copy of
//              the device-identity files).

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// defaultBackupPatterns are globs relative to the persist root. They cover the
// state needed to reach the controller over cellular, keep ssh access, and
// preserve the device-identity certs/keys (attestation + credential decryption)
// even if /persist is recreated empty.
var defaultBackupPatterns = []string{
	"checkpoint/lastconfig*",          // saved EdgeDevConfig: ConfigItemValueMap (ssh keys) + network
	"checkpoint/controllercerts*",     // controller signing certs
	"certs/ecdh.*.pem",                // ecdh key+cert: decrypt credentials
	"certs/attest.*.pem",              // attestation key+cert
	"certs/ek.*.pem",                  // endorsement cert
	"status/nim/DevicePortConfigList", // persisted DPC list: lastresort/cellular fallback
}

func cmdBackup(args []string) int {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	persist := fs.String("persist", "/persist", "mounted persist root to back up from")
	backupDir := fs.String("backup-dir", "/config/backup-persist", "destination on /config")
	flagFile := fs.String("flag-file", "/config/shrink-persist", "shrink flag file to write last")
	target := fs.String("target", "", "shrink target size recorded in the flag file (e.g. 78G) (required)")
	_ = fs.Parse(args)

	if *target == "" {
		fmt.Fprintln(os.Stderr, "backup: --target is required")
		return 2
	}
	if _, err := parseSize(*target); err != nil {
		fmt.Fprintln(os.Stderr, "backup: bad --target:", err)
		return 2
	}

	n, err := backupPersistFiles(*persist, *backupDir, defaultBackupPatterns)
	if err != nil {
		fmt.Fprintln(os.Stderr, "backup failed:", err)
		return 1
	}
	// Write the flag file LAST: the read side treats an absent/empty flag file as
	// "not started" and ignores a partial backup dir.
	if err := writeFileSync(*flagFile, []byte(*target+"\n"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "backup: write flag file:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "backed up %d file(s) to %s; wrote %s=%s\n", n, *backupDir, *flagFile, *target)
	return 0
}

func cmdRestore(args []string) int {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	persist := fs.String("persist", "/persist", "mounted persist root to restore into")
	backupDir := fs.String("backup-dir", "/config/backup-persist", "backup source on /config")
	flagFile := fs.String("flag-file", "/config/shrink-persist", "shrink flag file (gates restore; removed first on --cleanup)")
	cleanup := fs.Bool("cleanup", false, "after restoring, remove the flag file (first) and the backup dir")
	_ = fs.Parse(args)

	// The flag file gates the backup dir. If it is absent (conversion finished,
	// never started, or a crash cleared it), garbage-collect any leftover backup dir
	// WITHOUT restoring: stray files left in /config would otherwise be measured
	// into PCR 14 by measure-config and break the vault unseal on the next boot.
	if _, present := readFlagFile(*flagFile); !present {
		if err := os.RemoveAll(*backupDir); err != nil {
			fmt.Fprintln(os.Stderr, "restore: GC leftover backup dir:", err)
			return 1
		}
		return 0
	}

	restored, err := restorePersistFiles(*backupDir, *persist)
	if err != nil {
		fmt.Fprintln(os.Stderr, "restore failed:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "restored %d file(s) into %s\n", restored, *persist)

	if *cleanup {
		// Remove the flag file FIRST (the reverse of backup's flag-file-last
		// order): once the flag file is gone the backup dir is ignored, so a crash
		// between the two removals is safe (the next boot GCs the leftover dir, above).
		if err := os.Remove(*flagFile); err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "restore: remove flag file:", err)
			return 1
		}
		if err := os.RemoveAll(*backupDir); err != nil {
			fmt.Fprintln(os.Stderr, "restore: remove backup dir:", err)
			return 1
		}
	}
	return 0
}

func cmdCleanup(args []string) int {
	fs := flag.NewFlagSet("cleanup", flag.ExitOnError)
	backupDir := fs.String("backup-dir", "/config/backup-persist", "backup dir on /config to remove")
	flagFile := fs.String("flag-file", "/config/shrink-persist", "shrink flag file that must already be gone")
	_ = fs.Parse(args)

	// The flag file gates the backup dir, so it MUST already be gone: while it is
	// present a shrink is still pending and the backup dir holds the only copy of
	// the device-identity files, so removing it would be data loss. Refuse.
	if _, present := readFlagFile(*flagFile); present {
		fmt.Fprintf(os.Stderr, "cleanup: flag file %s still present; shrink unfinished, refusing to remove %s\n",
			*flagFile, *backupDir)
		return 1
	}
	// Idempotent: RemoveAll on an absent dir is a no-op, so the caller may run
	// this after every backup cycle regardless of whether a restore happened.
	if err := os.RemoveAll(*backupDir); err != nil {
		fmt.Fprintln(os.Stderr, "cleanup: remove backup dir:", err)
		return 1
	}
	return 0
}

// backupPersistFiles copies every file matching the patterns (relative to
// persist) into backupDir, preserving the relative path. Returns the file count.
func backupPersistFiles(persist, backupDir string, patterns []string) (int, error) {
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return 0, err
	}
	count := 0
	for _, p := range patterns {
		matches, err := filepath.Glob(filepath.Join(persist, p))
		if err != nil {
			return count, fmt.Errorf("glob %q: %w", p, err)
		}
		for _, m := range matches {
			rel, err := filepath.Rel(persist, m)
			if err != nil {
				return count, err
			}
			n, err := copyTree(m, filepath.Join(backupDir, rel))
			if err != nil {
				return count, err
			}
			count += n
		}
	}
	return count, nil
}

// restorePersistFiles walks backupDir and copies each file into persist at the
// same relative path when it is missing or its content differs. Returns the
// number of files written.
func restorePersistFiles(backupDir, persist string) (int, error) {
	if _, err := os.Stat(backupDir); os.IsNotExist(err) {
		return 0, nil // nothing backed up
	}
	restored := 0
	err := filepath.Walk(backupDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(backupDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(persist, rel)
		same, err := sameContent(path, dst)
		if err != nil {
			return err
		}
		if same {
			return nil
		}
		if err := copyFileSyncMode(path, dst); err != nil {
			return err
		}
		restored++
		return nil
	})
	return restored, err
}

// copyTree copies a file, or recursively a directory, from src to dst preserving
// structure. Returns the number of regular files copied.
func copyTree(src, dst string) (int, error) {
	info, err := os.Stat(src)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return 1, copyFileSyncMode(src, dst)
	}
	count := 0
	err = filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if err := copyFileSyncMode(path, filepath.Join(dst, rel)); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}

// copyFileSyncMode copies src to dst (creating parent dirs), preserves the mode,
// and fsyncs dst.
func copyFileSyncMode(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// writeFileSync writes data to path and fsyncs it.
func writeFileSync(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

// sameContent reports whether a (must exist) and b have identical contents. A
// missing b means "not same" (needs restore).
func sameContent(a, b string) (bool, error) {
	bb, err := os.ReadFile(b)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	ab, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ab, bb), nil
}
