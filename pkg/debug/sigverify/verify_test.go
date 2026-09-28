// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The testdata sums, bundle and images.txt are the amd64 kvm generic assets
// of eriknordmark/eve 0.0.1-rc2, signed by that fork's assets.yml.
const (
	sumsName      = "amd64.kvm.generic.sha256sums"
	rc2AssetsID   = "https://github.com/eriknordmark/eve/.github/workflows/assets.yml@refs/tags/0.0.1-rc2"
	signedAsset   = "amd64.kvm.generic.images.txt"
	trustedRootTD = "testdata/trusted_root.json"
)

func stageAssets(t *testing.T, sums []byte, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	if sums == nil {
		var err error
		if sums, err = os.ReadFile(filepath.Join("testdata", sumsName)); err != nil {
			t.Fatal(err)
		}
	}
	copyFile(t, filepath.Join("testdata", sumsName+".sigstore.json"), filepath.Join(dir, sumsName+".sigstore.json"))
	if err := os.WriteFile(filepath.Join(dir, sumsName), sums, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		copyFile(t, filepath.Join("testdata", f), filepath.Join(dir, f))
	}
	return dir
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyAssets(t *testing.T) {
	sums, err := os.ReadFile(filepath.Join("testdata", sumsName))
	if err != nil {
		t.Fatal(err)
	}
	tamperedSums := []byte(strings.Replace(string(sums), "d721d36b", "d721d36c", 1))

	fork := defaultPolicy("assets", "eriknordmark/eve")
	exact := fork
	exact.identity, exact.identityRegexp = rc2AssetsID, ""
	rc1 := fork
	rc1.identity, rc1.identityRegexp = strings.Replace(rc2AssetsID, "rc2", "rc1", 1), ""
	otherRepo := fork
	otherRepo.identityRegexp = ".*"
	otherRepo.repository = "https://github.com/lf-edge/eve"
	publishWorkflow := defaultPolicy("image", "eriknordmark/eve")

	tests := []struct {
		name    string
		policy  policy
		sums    []byte
		files   []string
		tamper  bool
		wantErr string
	}{
		{name: "fork release policy", policy: fork, files: []string{signedAsset}},
		{name: "exact identity", policy: exact, files: []string{signedAsset}},
		{name: "upstream policy", policy: defaultPolicy("assets", "lf-edge/eve"), files: []string{signedAsset}, wantErr: "expected SAN value to match regex"},
		{name: "other ref", policy: rc1, files: []string{signedAsset}, wantErr: "expected SAN value"},
		{name: "other repository", policy: otherRepo, files: []string{signedAsset}, wantErr: "expected SourceRepositoryURI"},
		{name: "other workflow", policy: publishWorkflow, files: []string{signedAsset}, wantErr: "expected SAN value to match regex"},
		{name: "tampered sums", policy: fork, sums: tamperedSums, files: []string{signedAsset}, wantErr: "failed to verify signature"},
		{name: "tampered asset", policy: fork, files: []string{signedAsset}, tamper: true, wantErr: "signed sums say"},
		{name: "no listed asset present", policy: fork, wantErr: "none of the 8 assets"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := stageAssets(t, tc.sums, tc.files...)
			if tc.tamper {
				f, err := os.OpenFile(filepath.Join(dir, signedAsset), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				f.Write([]byte{0})
				f.Close()
			}
			v, err := newVerifier(trustedRootTD, tc.policy)
			if err != nil {
				t.Fatal(err)
			}
			err = v.verifyAssets(dir, filepath.Join(dir, sumsName))
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("verified, want error containing %q", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("error %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseSumsRejectsPaths(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	for _, name := range []string{"../etc/passwd", "sub/file", "/abs", ".."} {
		if _, err := parseSums([]byte(sum + "  " + name + "\n")); err == nil {
			t.Errorf("parseSums accepted %q", name)
		}
	}
	got, err := parseSums([]byte(sum + " *binary-mode.img\n"))
	if err != nil || got["binary-mode.img"] != sum {
		t.Errorf("binary-mode line: got %v, %v", got, err)
	}
}

// writeLayout builds an OCI layout holding one image with a config and a
// layer, and returns its directory and the image manifest digest.
func writeLayout(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	put := func(data []byte) descriptor {
		d := descriptor{Digest: "sha256:" + sha256Hex(data), Size: int64(len(data))}
		p := filepath.Join(dir, "blobs", "sha256", sha256Hex(data))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return d
	}
	cfg := put([]byte(`{"architecture":"amd64"}`))
	layer := put([]byte("layer"))
	m, _ := json.Marshal(manifest{MediaType: "application/vnd.oci.image.manifest.v1+json", Config: &cfg, Layers: []descriptor{layer}})
	md := put(m)
	idx, _ := json.Marshal(manifest{Manifests: []descriptor{md}})
	if err := os.WriteFile(filepath.Join(dir, "index.json"), idx, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, md.Digest
}

func TestLayoutContent(t *testing.T) {
	dir, digest := writeLayout(t)
	l, err := openLayout(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := l.target(""); err != nil || got != digest {
		t.Fatalf("target: got %s, %v; want %s", got, err, digest)
	}
	if n, err := l.checkContent(digest); err != nil || n != 3 {
		t.Fatalf("checkContent: %d blobs, %v", n, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blobs", "sha256", sha256Hex([]byte("layer"))), []byte("LAYER"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.checkContent(digest); err == nil {
		t.Fatal("checkContent accepted a tampered layer")
	}
}

func TestUnsignedImage(t *testing.T) {
	dir, _ := writeLayout(t)
	v, err := newVerifier(trustedRootTD, defaultPolicy("image", "lf-edge/eve"))
	if err != nil {
		t.Fatal(err)
	}
	err = v.verifyImage(dir, "")
	if err == nil || errors.Is(err, errUsage) || !strings.Contains(err.Error(), "no Sigstore bundle referrers") {
		t.Fatalf("got %v, want a verification failure for missing signatures", err)
	}
}
