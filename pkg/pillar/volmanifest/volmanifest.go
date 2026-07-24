// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package volmanifest records and verifies a sha256 manifest of the application
// volume objects across the EVE-kvm→EVE-k offline filesystem shrink.
//
// An interrupted shrink can leave a volume present but with wrong/zeroed content
// (relocated data blocks torn) while the filesystem metadata stays consistent —
// invisible to both e2fsck and qemu-img. A content hash is the only detector that
// catches it. Write is called during the conversion reboot preparation, once the
// application domains are halted (volumes quiescent) and while the vault is still
// mounted; Verify runs on the post-resize boot, before the volumes are relocated
// or rolled out to PVCs, and reports every object that is not provably intact.
//
// The manifest lives in-directory on /persist (never on /config, which is measured
// into PCR14 and would break the TPM-sealed vault unseal). Being small it usually
// survives the shrink in low block groups, but correctness never depends on that:
// a missing/short/unparseable manifest makes every present object suspect
// (degrade-safe), so anything not proven intact is recreated rather than served.
package volmanifest

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ManifestName is the per-directory manifest file. It is skipped when hashing.
const ManifestName = ".sha256"

// Corruption identifies a volume object that failed verification, with why.
type Corruption struct {
	Path   string // absolute path of the suspect volume object
	Reason string // "hash-mismatch" | "not-in-manifest" | "no-manifest"
}

// hashFile streams the file at path through sha256 and returns the hex digest.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// volumeObjects lists the regular files in dir that are volume objects (i.e. every
// regular file except the manifest itself), sorted for deterministic output.
func volumeObjects(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.Type().IsRegular() || e.Name() == ManifestName {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// Write hashes every volume object in each dir and writes dir/.sha256 (one
// "sha256hex  name" line per object, sha256sum format). A dir that does not exist
// is skipped (no volumes of that flavor). It must be called with the volumes
// quiescent (apps halted) and the vault mounted.
func Write(dirs ...string) error {
	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		names, err := volumeObjects(dir)
		if err != nil {
			return fmt.Errorf("list %s: %w", dir, err)
		}
		var b strings.Builder
		for _, name := range names {
			sum, err := hashFile(filepath.Join(dir, name))
			if err != nil {
				return fmt.Errorf("hash %s/%s: %w", dir, name, err)
			}
			fmt.Fprintf(&b, "%s  %s\n", sum, name)
		}
		tmp := filepath.Join(dir, ManifestName+".tmp")
		if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", tmp, err)
		}
		if err := os.Rename(tmp, filepath.Join(dir, ManifestName)); err != nil {
			return fmt.Errorf("rename %s: %w", tmp, err)
		}
	}
	return nil
}

// Exists reports whether any of the dirs holds a manifest — i.e. a pre-resize hash
// was recorded. The post-resize check runs only when this is true; its absence
// means no destructive conversion recorded one, so the volumes must be left alone.
func Exists(dirs ...string) bool {
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, ManifestName)); err == nil {
			return true
		}
	}
	return false
}

// Remove deletes the manifest from each dir, consuming it so the post-resize check
// runs once. A missing manifest is not an error.
func Remove(dirs ...string) {
	for _, dir := range dirs {
		_ = os.Remove(filepath.Join(dir, ManifestName))
	}
}

// readManifest parses dir/.sha256 into name→hash. ok is false when the manifest is
// absent or unparseable — the caller then treats every present object as suspect.
func readManifest(dir string) (map[string]string, bool) {
	f, err := os.Open(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	m := make(map[string]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 64 {
			return nil, false // malformed → unverifiable
		}
		m[fields[1]] = fields[0]
	}
	if sc.Err() != nil {
		return nil, false
	}
	return m, true
}

// Verify re-hashes each dir's present volume objects and compares them to the
// dir's manifest. It returns every object that is not provably intact:
//   - manifest missing/unparseable → every present object (reason "no-manifest");
//   - object present but absent from the manifest → "not-in-manifest";
//   - object present with a different hash → "hash-mismatch".
//
// Objects listed in the manifest but now absent are NOT reported: an absent volume
// already self-heals via EVE's recreate-on-missing path. A non-existent dir is
// skipped.
func Verify(dirs ...string) ([]Corruption, error) {
	var out []Corruption
	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		names, err := volumeObjects(dir)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", dir, err)
		}
		manifest, ok := readManifest(dir)
		for _, name := range names {
			path := filepath.Join(dir, name)
			if !ok {
				out = append(out, Corruption{Path: path, Reason: "no-manifest"})
				continue
			}
			want, present := manifest[name]
			if !present {
				out = append(out, Corruption{Path: path, Reason: "not-in-manifest"})
				continue
			}
			got, err := hashFile(path)
			if err != nil {
				return nil, fmt.Errorf("hash %s: %w", path, err)
			}
			if got != want {
				out = append(out, Corruption{Path: path, Reason: "hash-mismatch"})
			}
		}
	}
	return out, nil
}
