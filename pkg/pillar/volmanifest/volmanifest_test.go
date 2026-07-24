// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package volmanifest

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// reasons returns a path→reason map for easy assertions.
func reasons(cs []Corruption) map[string]string {
	m := make(map[string]string)
	for _, c := range cs {
		m[c.Path] = c.Reason
	}
	return m
}

func TestWriteVerifyClean(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "vol-a.qcow2"), []byte("aaaa"))
	writeFile(t, filepath.Join(dir, "vol-b.qcow2"), []byte("bbbbbb"))
	if err := Write(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ManifestName)); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	cs, err := Verify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 0 {
		t.Fatalf("expected clean, got %+v", cs)
	}
}

func TestVerifyHashMismatch(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "vol.qcow2")
	writeFile(t, victim, []byte("original"))
	if err := Write(dir); err != nil {
		t.Fatal(err)
	}
	// Corrupt the content in place (same length): a torn/zeroed data block.
	writeFile(t, victim, []byte("CORRUPTX"))
	cs, _ := Verify(dir)
	if r := reasons(cs); r[victim] != "hash-mismatch" {
		t.Fatalf("expected hash-mismatch for %s, got %+v", victim, cs)
	}
}

func TestVerifyNoManifestAllSuspect(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "v1"), []byte("x"))
	writeFile(t, filepath.Join(dir, "v2"), []byte("y"))
	if err := Write(dir); err != nil {
		t.Fatal(err)
	}
	// Simulate the shrink clearing the manifest along with data.
	if err := os.Remove(filepath.Join(dir, ManifestName)); err != nil {
		t.Fatal(err)
	}
	cs, _ := Verify(dir)
	if len(cs) != 2 {
		t.Fatalf("no-manifest: expected all 2 suspect, got %+v", cs)
	}
	for _, c := range cs {
		if c.Reason != "no-manifest" {
			t.Fatalf("expected no-manifest reason, got %q", c.Reason)
		}
	}
}

func TestVerifyGarbledManifestAllSuspect(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "v1"), []byte("x"))
	if err := Write(dir); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ManifestName), []byte("this is not a manifest"))
	cs, _ := Verify(dir)
	if len(cs) != 1 || cs[0].Reason != "no-manifest" {
		t.Fatalf("garbled manifest should make all suspect, got %+v", cs)
	}
}

func TestVerifyUnexpectedFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "v1"), []byte("x"))
	if err := Write(dir); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(dir, "v2-unexpected")
	writeFile(t, extra, []byte("appeared during resize"))
	cs, _ := Verify(dir)
	if r := reasons(cs); r[extra] != "not-in-manifest" {
		t.Fatalf("expected not-in-manifest for %s, got %+v", extra, cs)
	}
}

func TestVerifyAbsentObjectNotReported(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep")
	gone := filepath.Join(dir, "gone")
	writeFile(t, keep, []byte("k"))
	writeFile(t, gone, []byte("g"))
	if err := Write(dir); err != nil {
		t.Fatal(err)
	}
	// A volume fsck moved out / removed: absent objects self-heal via recreate,
	// so Verify must not flag them.
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	cs, _ := Verify(dir)
	if len(cs) != 0 {
		t.Fatalf("absent object should not be reported, got %+v", cs)
	}
}

func TestWriteSkipsMissingDir(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist")
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(real, "v"), []byte("z"))
	// A missing dir (e.g. no clear/volumes) must be silently skipped, not error.
	if err := Write(missing, real); err != nil {
		t.Fatalf("Write should skip a missing dir: %v", err)
	}
	cs, err := Verify(missing, real)
	if err != nil || len(cs) != 0 {
		t.Fatalf("verify after skip: cs=%+v err=%v", cs, err)
	}
}
