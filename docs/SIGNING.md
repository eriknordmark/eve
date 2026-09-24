# Signed EVE releases

EVE's release workflows sign their outputs with [Sigstore](https://www.sigstore.dev/)
keyless signing. No long-lived key exists: each signature carries a short-lived
certificate issued to the GitHub Actions workflow that produced it, and is
recorded in the public Rekor transparency log. Checking that identity is what
distinguishes an LF Edge build from anyone else's, including a fork that signs
its own builds the same way.

## What is signed

| Artifact | Signed by | When |
|---|---|---|
| `lfedge/eve:<tag>[-<platform>]-<hv>-<arch>` and its multi-arch indexes | `publish.yml` | release tags |
| SPDX SBOM attestation on `lfedge/eve:<tag>…-<arch>` | `publish.yml` | release tags, except riscv64 |
| `lfedge/eve-sources:<tag>…-<arch>` | `publish.yml` | release tags, except riscv64 |
| `lfedge/eve-<pkg>` platform manifests | `publish.yml`, `sign-backfill.yml` | every `publish.yml` run (pushes to `master`, `N.N`, `N.N-stable`, and release tags) |
| `<arch>.<hv>.<platform>.sha256sums` release asset | `assets.yml` | release tags |

Each asset set's `sha256sums` also covers `<arch>.<hv>.<platform>.images.txt`,
which names the image digests the assets were extracted from. The signature
for `sha256sums` is the `….sha256sums.sigstore.json` bundle next to it.

Pull requests never reach these workflows, so nothing unmerged is signed.

## Verifying a release

With [cosign](https://docs.sigstore.dev/cosign/system_config/installation/) 3.x:

```sh
TAG=17.6.0
ISSUER=https://token.actions.githubusercontent.com

cosign verify \
  --certificate-oidc-issuer "$ISSUER" \
  --certificate-identity "https://github.com/lf-edge/eve/.github/workflows/publish.yml@refs/tags/$TAG" \
  lfedge/eve:$TAG-kvm-amd64

cosign verify-attestation --type spdxjson \
  --certificate-oidc-issuer "$ISSUER" \
  --certificate-identity "https://github.com/lf-edge/eve/.github/workflows/publish.yml@refs/tags/$TAG" \
  lfedge/eve:$TAG-kvm-amd64

cosign verify-blob \
  --bundle amd64.kvm.generic.sha256sums.sigstore.json \
  --certificate-oidc-issuer "$ISSUER" \
  --certificate-identity "https://github.com/lf-edge/eve/.github/workflows/assets.yml@refs/tags/$TAG" \
  amd64.kvm.generic.sha256sums
sha256sum --ignore-missing -c amd64.kvm.generic.sha256sums
```

Pin the full identity, not only the repository. A signature from a fork names
that fork's repository in its certificate, and a signature from any other
workflow or ref names that workflow and ref.

cosign cannot currently check the certificate's immutable repository-owner ID,
so these checks rely on the `lf-edge` organization name.

## Verification inside the build

`publish.yml` verifies what it reuses before it pushes anything built from it:

- every `lfedge/eve-*` platform manifest in the linuxkit cache that the
  registry already served, after `make pkgs` and again after `make eve`
- the `FROM lfedge/eve-*` base images of every package the job built

It signs the package images it built only after pushing them. Verification
checks the digest in the linuxkit cache rather than the tag, so a tag moved in
the registry cannot slip past it. `tools/eve-cosign.sh` implements all of this.

A failed check is a warning until the repository variable `EVE_COSIGN_ENFORCE`
is `true`, and fails the job after that.

Not signed by EVE's workflows, and therefore not verified: `lfedge/eve-kernel`
(published by the eve-kernel repository), `linuxkit/*` images, and external
`FROM` images. The build pins them by tag or digest as before.

## Enabling on a branch

Package images pushed before signing existed carry no signature, and unchanged
packages are reused by content hash indefinitely. After the signing change lands
on a branch:

1. Run the **Sign backfill** workflow (`sign-backfill.yml`) on that branch. It
   builds every variant without pushing and signs the digests those builds
   reused, plus the base images of every package.
2. Once backfill has run on every branch that carries the change, set
   `EVE_COSIGN_ENFORCE` to `true`.

A package pushed by one branch's build and pulled by another branch's build
before the first build has signed it fails verification; re-running the job
resolves it.
