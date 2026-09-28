// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"regexp"

	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

const githubIssuer = "https://token.actions.githubusercontent.com"

// releaseRef matches the release tags publish.yml and assets.yml sign on;
// docs/SIGNING.md is the source of this policy.
const releaseRef = `refs/tags/[0-9]+\.[0-9]+\.[0-9]+(-lts|-rc[0-9]+)?`

// policy is the certificate identity a signature must carry.
type policy struct {
	identity       string
	identityRegexp string
	issuer         string
	repository     string
}

// defaultPolicy returns the identity of the workflow that signs release
// artifacts of kind "image" (publish.yml) or "assets" (assets.yml) in the
// GitHub repository repo (owner/name).
func defaultPolicy(kind, repo string) policy {
	workflow := "publish"
	if kind == "assets" {
		workflow = "assets"
	}
	return policy{
		identityRegexp: fmt.Sprintf(`^https://github\.com/%s/\.github/workflows/%s\.yml@%s$`,
			regexp.QuoteMeta(repo), workflow, releaseRef),
		issuer:     githubIssuer,
		repository: "https://github.com/" + repo,
	}
}

func (p policy) certificateIdentity() (verify.CertificateIdentity, error) {
	san, err := verify.NewSANMatcher(p.identity, p.identityRegexp)
	if err != nil {
		return verify.CertificateIdentity{}, err
	}
	issuer, err := verify.NewIssuerMatcher(p.issuer, "")
	if err != nil {
		return verify.CertificateIdentity{}, err
	}
	return verify.NewCertificateIdentity(san, issuer,
		certificate.Extensions{SourceRepositoryURI: p.repository})
}
