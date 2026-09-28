// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

// eve-sigverify checks the Sigstore keyless signatures EVE's release
// workflows put on an EVE image and on the release assets, offline, against
// a trusted_root.json. docs/SIGNING.md describes what is signed and by whom.
//
// Exit status: 0 verified, 1 verification failed, 2 usage or input error.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

const usage = `usage:
  eve-sigverify image  -trusted-root FILE [policy flags] [-digest sha256:...] OCI_LAYOUT_DIR
  eve-sigverify assets -trusted-root FILE [policy flags] DIR SHA256SUMS
  eve-sigverify trusted-root -o FILE     (online: fetch Sigstore's trusted root via TUF)

policy flags:
  -repo OWNER/NAME         GitHub repository that signed (default lf-edge/eve); sets
                           the default identity regexp and source repository
  -identity URI            exact certificate identity (workflow@ref)
  -identity-regexp RE      certificate identity regexp
  -repository URI          certificate source repository (default https://github.com/OWNER/NAME)
  -issuer URL              OIDC issuer (default ` + githubIssuer + `)
`

// errUsage marks input errors, which exit 2 rather than 1.
var errUsage = errors.New("usage error")

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "image", "assets":
		err = runVerify(os.Args[1], os.Args[2:])
	case "trusted-root":
		err = runTrustedRoot(os.Args[2:])
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
	if errors.Is(err, errUsage) {
		os.Exit(2)
	}
	os.Exit(1)
}

func runVerify(kind string, args []string) error {
	fs := flag.NewFlagSet(kind, flag.ContinueOnError)
	trustedRoot := fs.String("trusted-root", "", "Sigstore trusted_root.json")
	repo := fs.String("repo", "lf-edge/eve", "GitHub repository that signed")
	identity := fs.String("identity", "", "exact certificate identity")
	identityRegexp := fs.String("identity-regexp", "", "certificate identity regexp")
	issuer := fs.String("issuer", githubIssuer, "OIDC issuer")
	repository := fs.String("repository", "", "source repository URI (default https://github.com/<repo>)")
	digest := fs.String("digest", "", "image digest to verify (image mode)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if *trustedRoot == "" {
		return fmt.Errorf("%w: -trusted-root is required", errUsage)
	}
	p := defaultPolicy(kind, *repo)
	p.issuer = *issuer
	if *repository != "" {
		p.repository = *repository
	}
	if *identity != "" || *identityRegexp != "" {
		p.identity, p.identityRegexp = *identity, *identityRegexp
	}
	v, err := newVerifier(*trustedRoot, p)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if kind == "image" {
		if fs.NArg() != 1 {
			return fmt.Errorf("%w: image takes one OCI layout directory", errUsage)
		}
		return v.verifyImage(fs.Arg(0), *digest)
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("%w: assets takes a directory and a sha256sums file", errUsage)
	}
	return v.verifyAssets(fs.Arg(0), fs.Arg(1))
}
