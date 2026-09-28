# eve-sigverify

Verifies, offline, the Sigstore keyless signatures described in
[docs/SIGNING.md](../../../docs/SIGNING.md) on an EVE image and on the release
assets. It ships in eve-debug as `/usr/bin/eve-sigverify`.

Each run prints one `OK:` or `FAIL:` verdict line. Exit status is 0 when
verified, 1 when verification fails, and 2 on a usage or input error.

```text
eve-sigverify image  -trusted-root FILE [policy flags] [-digest sha256:...] OCI_LAYOUT_DIR
eve-sigverify assets -trusted-root FILE [policy flags] DIR SHA256SUMS
eve-sigverify trusted-root -o FILE
```

The examples below verify the `0.0.1-rc2` release of the `eriknordmark/eve`
fork, hence `-repo eriknordmark/eve`. Drop `-repo` for an `lf-edge/eve`
release.

## Trusted root

Verification needs Sigstore's public-good `trusted_root.json` (Fulcio CA,
Rekor and CT log keys, timestamp authorities). `trusted-root` is the only
mode that goes online; it fetches the file through Sigstore's TUF repository:

```console
$ eve-sigverify trusted-root -o trusted_root.json
OK: wrote trusted_root.json
```

The file changes only when Sigstore rotates keys, so a copy can be carried to
an offline host.

## Image

The input is an OCI image layout holding the image and its referrers, where
cosign 3 stores the signature and SBOM attestation bundles. `oras` copies
both; `cosign save` does not include referrers.

```console
$ oras copy --recursive --to-oci-layout \
    ghcr.io/eriknordmark/eve@sha256:1e4d6d3ce9042e5b03d4e8fd1fac2b729d58ecb0bfb8b80df6f9d170407afdd4 ./eve-layout
$ eve-sigverify image -trusted-root trusted_root.json -repo eriknordmark/eve ./eve-layout
info: verified https://spdx.dev/Document attestation by https://github.com/eriknordmark/eve/.github/workflows/publish.yml@refs/tags/0.0.1-rc2 at 2026-09-28T05:32:30Z (Tlog)
OK: image sha256:1e4d6d3ce9042e5b03d4e8fd1fac2b729d58ecb0bfb8b80df6f9d170407afdd4 (11 blobs) signed by https://github.com/eriknordmark/eve/.github/workflows/publish.yml@refs/tags/0.0.1-rc2 at 2026-09-28T05:32:12Z (Tlog)
```

Every blob of the image is hashed against its manifest, and at least one
referrer must be a `cosign sign` signature over the manifest digest by the
release workflow; an SBOM attestation alone does not count. Use `-digest`
when the layout holds more than one image.

The same image fails the default `lf-edge/eve` policy:

```console
$ eve-sigverify image -trusted-root trusted_root.json ./eve-layout
rejected bundle sha256:71e407b2…: failed to verify certificate identity: no matching CertificateIdentity found, last error: expected SAN value to match regex "^https://github\.com/lf-edge/eve/\.github/workflows/publish\.yml@refs/tags/[0-9]+\.[0-9]+\.[0-9]+(-lts|-rc[0-9]+)?$", got "https://github.com/eriknordmark/eve/.github/workflows/publish.yml@refs/tags/0.0.1-rc2"
rejected bundle sha256:9d6dac05…: (same)
FAIL: image sha256:1e4d6d3ce9042e5b03d4e8fd1fac2b729d58ecb0bfb8b80df6f9d170407afdd4: no cosign signature matching identity ^https://github\.com/lf-edge/eve/\.github/workflows/publish\.yml@refs/tags/[0-9]+\.[0-9]+\.[0-9]+(-lts|-rc[0-9]+)?$, repository https://github.com/lf-edge/eve (2 bundles checked)
```

## Release assets

Each release variant has a `<arch>.<hv>.<platform>.sha256sums` asset and its
signature bundle `<sums>.sigstore.json`. The sums file is verified against the
bundle, then every listed asset present in `DIR` against the signed sums.
Listed assets that were not downloaded are counted, not failed.

```console
$ eve-sigverify assets -trusted-root trusted_root.json -repo eriknordmark/eve rc2 rc2/amd64.kvm.generic.sha256sums
OK: amd64.kvm.generic.sha256sums signed by https://github.com/eriknordmark/eve/.github/workflows/assets.yml@refs/tags/0.0.1-rc2 at 2026-09-28T17:19:46Z (Tlog); 4 assets match, 4 listed but not present
```

The `images.txt` asset names the `eve` and `eve-sources` image digests the
assets were extracted from, so the asset signature also binds those images.
A changed asset fails:

```console
$ eve-sigverify assets -trusted-root trusted_root.json -repo eriknordmark/eve tampered tampered/amd64.kvm.generic.sha256sums
FAIL: amd64.kvm.generic.images.txt: sha256 e50c635eb7e5f6987a0e6740ad1f9db5c785784b2343d7fccfef9c413945bb88, signed sums say d721d36bc2c016c19c8201886a212217266a930342fd7ffe839498880d47d3f9
```

## Policy

By default a signature must come from `publish.yml` (images) or `assets.yml`
(assets) of `lf-edge/eve` on a release tag, issued by GitHub Actions OIDC.
`-repo owner/name` switches the repository, for example to verify a fork's
release; `-identity`, `-identity-regexp`, `-repository` and `-issuer`
override individual parts.
