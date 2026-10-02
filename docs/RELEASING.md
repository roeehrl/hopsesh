# Releasing hopsesh

Releases are built by `.github/workflows/release.yml` when a `v*` tag is pushed. The job
creates a **draft** release; a maintainer checks it and publishes it.

## What a release contains

| File | Made by | Checked by |
|---|---|---|
| `hopsesh_<ver>_<os>_<arch>.tar.gz` / `.zip` | GoReleaser | `checksums.txt` |
| `checksums.txt` | GoReleaser | `checksums.txt.sig`, `checksums.txt.sigstore.json` |
| `checksums.txt.sig` | `openssl dgst -sha256 -sign` with the release key | `hopsesh update`, install scripts |
| `checksums.txt.sigstore.json` | cosign keyless (this workflow's identity) | `cosign verify-blob` |
| SBOMs (`*.sbom.json`) | syft | |
| Build provenance | `actions/attest-build-provenance` | `gh attestation verify` |
| `hopsesh-<ver>-macos-universal.dmg` | `scripts/build-macos-app.sh` | Developer ID signature, notarization |

## One-time setup

### Release signing key

`hopsesh update` and the install scripts trust `checksums.txt` only when it is signed by the
release key. Create it once, offline:

```sh
openssl ecparam -name prime256v1 -genkey -noout -out hopsesh-release.pem
openssl ec -in hopsesh-release.pem -pubout -outform DER | base64   # public key, one line
```

- Store `hopsesh-release.pem` in the repository secret `HOPSESH_RELEASE_KEY` and in a safe
  place offline. Never commit it.
- Store the one-line public key in the repository **variable** `HOPSESH_RELEASE_PUBKEY`
  (it is embedded into release binaries), save the PEM form as `packaging/release-key.pub`,
  and paste it into `RELEASE_PUBKEY` in `scripts/install.sh` (PEM form:
  `openssl ec -in hopsesh-release.pem -pubout`) and `$ReleasePubKey` in
  `scripts/install.ps1` (the base64 DER line).

The current key is in `packaging/release-key.pub`.

Rotating the key means a release whose binaries embed the new key, signed by the old one.

### macOS signing and notarization

Repository secrets:

- `MACOS_SIGN_P12`: base64 of the Developer ID Application certificate and key (`.p12`)
- `MACOS_SIGN_PASSWORD`: its password
- `MACOS_NOTARY_KEY`, `MACOS_NOTARY_KEY_ID`, `MACOS_NOTARY_ISSUER_ID`: an App Store Connect
  API key with the Developer role

GoReleaser signs and notarizes the darwin CLI binaries with them; the `macos-app` job signs
the app with hardened runtime, notarizes and staples it.

Locally, the same script builds a signed app:

```sh
SIGN_IDENTITY="Developer ID Application: …" NOTARY_PROFILE=<profile> VERSION=0.1.0 scripts/build-macos-app.sh
```

(`xcrun notarytool store-credentials <profile>` creates the profile once.)

### Package repositories

`TAP_GITHUB_TOKEN` (a fine-grained token with write access to `roeehrl/homebrew-tap`,
`roeehrl/scoop-bucket` and a fork of `microsoft/winget-pkgs`) lets GoReleaser publish the
Homebrew cask, Scoop manifest and winget pull request. Without it those steps are skipped.

## Cutting a release

1. Update `CHANGELOG.md` (move items from Unreleased to the new version).
2. `git tag -s v0.1.0 -m "hopsesh 0.1.0" && git push origin v0.1.0`
3. Wait for both jobs, review the draft release, then publish it.

## Verifying a release by hand

```sh
sha256sum -c checksums.txt --ignore-missing
openssl dgst -sha256 -verify packaging/release-key.pub -signature checksums.txt.sig checksums.txt
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/roeehrl/hopsesh/.github/workflows/release.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
gh attestation verify hopsesh_0.1.0_linux_amd64.tar.gz --repo roeehrl/hopsesh
```
