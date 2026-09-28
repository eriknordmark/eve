// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// signPredicate is the in-toto predicate type of a `cosign sign` signature,
// as opposed to an attestation such as the SPDX SBOM.
const signPredicate = "https://sigstore.dev/cosign/sign/v1"

type verifier struct {
	sv       *verify.Verifier
	identity verify.CertificateIdentity
	policy   policy
}

// newVerifier requires every signature to carry a transparency log entry and
// a verified timestamp from the trusted root, which is what cosign's keyless
// signing produces.
func newVerifier(trustedRootPath string, p policy) (*verifier, error) {
	tr, err := root.NewTrustedRootFromPath(trustedRootPath)
	if err != nil {
		return nil, fmt.Errorf("loading trusted root: %w", err)
	}
	sv, err := verify.NewVerifier(tr, verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1))
	if err != nil {
		return nil, err
	}
	id, err := p.certificateIdentity()
	if err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	return &verifier{sv: sv, identity: id, policy: p}, nil
}

func (v *verifier) describePolicy() string {
	id := v.policy.identity
	if id == "" {
		id = v.policy.identityRegexp
	}
	return fmt.Sprintf("identity %s, repository %s", id, v.policy.repository)
}

func signer(r *verify.VerificationResult) string {
	s := r.Signature.Certificate.SubjectAlternativeName
	for _, ts := range r.VerifiedTimestamps {
		s += fmt.Sprintf(" at %s (%s)", ts.Timestamp.UTC().Format("2006-01-02T15:04:05Z"), ts.Type)
		break
	}
	return s
}

// verifyImage checks that the layout holds the complete image and that at
// least one of its referrers is a cosign signature over its manifest digest
// matching the policy.
func (v *verifier) verifyImage(dir, digest string) error {
	l, err := openLayout(dir)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	target, err := l.target(digest)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	blobs, err := l.checkContent(target)
	if err != nil {
		return fmt.Errorf("image %s content: %v", target, err)
	}
	bundles, err := l.bundlesFor(target)
	if err != nil {
		return err
	}
	if len(bundles) == 0 {
		return fmt.Errorf("image %s has no Sigstore bundle referrers in %s", target, dir)
	}
	digestBytes, err := hex.DecodeString(strings.TrimPrefix(target, "sha256:"))
	if err != nil {
		return err
	}
	var signed *verify.VerificationResult
	var failures []string
	for _, rb := range bundles {
		r, err := v.verifyBundle(rb.json, verify.WithArtifactDigest("sha256", digestBytes))
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", rb.manifestDigest, err))
			continue
		}
		if r.Statement == nil {
			failures = append(failures, fmt.Sprintf("%s: not a DSSE statement", rb.manifestDigest))
			continue
		}
		if r.Statement.PredicateType != signPredicate {
			fmt.Printf("info: verified %s attestation by %s\n", r.Statement.PredicateType, signer(r))
			continue
		}
		signed = r
	}
	for _, f := range failures {
		fmt.Fprintf(os.Stderr, "rejected bundle %s\n", f)
	}
	if signed == nil {
		return fmt.Errorf("image %s: no cosign signature matching %s (%d bundles checked)",
			target, v.describePolicy(), len(bundles))
	}
	fmt.Printf("OK: image %s (%d blobs) signed by %s\n", target, blobs, signer(signed))
	return nil
}

func (v *verifier) verifyBundle(data []byte, artifact verify.ArtifactPolicyOption) (*verify.VerificationResult, error) {
	var b bundle.Bundle
	if err := b.UnmarshalJSON(data); err != nil {
		return nil, fmt.Errorf("parsing bundle: %w", err)
	}
	return v.sv.Verify(&b, verify.NewPolicy(artifact, verify.WithCertificateIdentity(v.identity)))
}

// verifyAssets checks the sha256sums file against its <sums>.sigstore.json
// bundle, then every listed asset present in dir against the signed sums.
// Listed assets missing from dir are counted, not failed, since users
// usually download only the assets they need.
func (v *verifier) verifyAssets(dir, sums string) error {
	sumsData, err := os.ReadFile(sums)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	bundleData, err := os.ReadFile(sums + ".sigstore.json")
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	r, err := v.verifyBundle(bundleData, verify.WithArtifact(strings.NewReader(string(sumsData))))
	if err != nil {
		return fmt.Errorf("%s: signature does not match %s: %v", filepath.Base(sums), v.describePolicy(), err)
	}
	want, err := parseSums(sumsData)
	if err != nil {
		return fmt.Errorf("%s: %v", filepath.Base(sums), err)
	}
	var checked, missing int
	for _, name := range sortedKeys(want) {
		got, err := fileSHA256(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			missing++
			continue
		}
		if err != nil {
			return err
		}
		if got != want[name] {
			return fmt.Errorf("%s: sha256 %s, signed sums say %s", name, got, want[name])
		}
		checked++
	}
	if checked == 0 {
		return fmt.Errorf("%w: signature OK but none of the %d assets listed in %s is in %s",
			errUsage, len(want), filepath.Base(sums), dir)
	}
	fmt.Printf("OK: %s signed by %s; %d assets match, %d listed but not present\n",
		filepath.Base(sums), signer(r), checked, missing)
	return nil
}

// parseSums reads sha256sum(1) output. Names must be plain file names: the
// release workflow runs sha256sum in the assets directory.
func parseSums(data []byte) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" {
			continue
		}
		sum, name, ok := strings.Cut(line, " ")
		name = strings.TrimPrefix(strings.TrimPrefix(name, " "), "*")
		if _, err := hex.DecodeString(sum); !ok || err != nil || len(sum) != 64 {
			return nil, fmt.Errorf("line %d: not a sha256sum line", n)
		}
		if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
			return nil, fmt.Errorf("line %d: unexpected file name %q", n, name)
		}
		out[name] = strings.ToLower(sum)
	}
	if len(out) == 0 {
		return nil, errors.New("no checksums")
	}
	return out, sc.Err()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func fileSHA256(path string) (string, error) {
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

// runTrustedRoot fetches Sigstore's public-good trusted root through TUF,
// anchored on the TUF root embedded in sigstore-go.
func runTrustedRoot(args []string) error {
	fs := flag.NewFlagSet("trusted-root", flag.ContinueOnError)
	out := fs.String("o", "trusted_root.json", "output file")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	c, err := tuf.New(tuf.DefaultOptions().WithDisableLocalCache())
	if err != nil {
		return err
	}
	data, err := c.GetTarget("trusted_root.json")
	if err != nil {
		return err
	}
	if _, err := root.NewTrustedRootFromJSON(data); err != nil {
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("OK: wrote %s\n", *out)
	return nil
}
