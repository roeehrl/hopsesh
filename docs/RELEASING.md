# Releasing hopsesh

A release is built in two places, and no signing key or Apple credential is ever stored
in GitHub:

1. **GitHub Actions** (`.github/workflows/release.yml`), on a `v*` tag: builds the Linux and
   Windows files with GoReleaser, records build provenance for each of them, and creates a
   **draft** release.
2. **The maintainer's Mac** (`scripts/release-sign.sh`): verifies that provenance, builds
   the macOS CLI and app from the tagged commit, signs and notarizes them, writes
   `checksums.txt` for every file, signs it with the release key, and uploads the result to
   the draft. A person then reviews and publishes it.

## What a release contains

| File | Made by | Checked by |
|---|---|---|
| `hopsesh_<ver>_linux_*.tar.gz`, `hopsesh_<ver>_windows_*.zip`, `.deb`/`.rpm`/`.apk` | GoReleaser in CI | build provenance (`gh attestation verify`), `checksums.txt` |
| `*.sbom.json` | syft in CI | `checksums.txt` |
| `hopsesh_<ver>_darwin_{amd64,arm64}.tar.gz` | `release-sign.sh` | Developer ID signature, notarization, `checksums.txt` |
| `hopsesh-<ver>-macos-universal.dmg` | `release-sign.sh` → `build-macos-app.sh` | Developer ID signature, notarization (stapled), `checksums.txt` |
| `checksums.txt`, `checksums.txt.sig` | `release-sign.sh` | the release key (`packaging/release-key.pub`) |

`hopsesh update` and the install scripts accept a download only when `checksums.txt` is
signed by the release key and the file matches it.

## One-time setup on the Mac

**Release key.** The private key stays on the Mac (and in an offline backup):

```sh
mkdir -p ~/.hopsesh-release && chmod 700 ~/.hopsesh-release && cd ~/.hopsesh-release
(umask 077; openssl ecparam -name prime256v1 -genkey -noout -out hopsesh-release.pem)
openssl ec -in hopsesh-release.pem -pubout > hopsesh-release.pub                 # PEM
openssl ec -in hopsesh-release.pem -pubout -outform DER | base64 | tr -d '\n'     # one line
```

The public half is in `packaging/release-key.pub` (PEM), `RELEASE_PUBKEY` in
`scripts/install.sh` (PEM), `$ReleasePubKey` in `scripts/install.ps1` (one line), and the
repository **variable** `HOPSESH_RELEASE_PUBKEY` (one line; CI embeds it in the Linux and
Windows binaries). Rotating the key means changing all four; copies that embed the old key
must then be reinstalled by hand.

**Signing identity.** A "Developer ID Application" certificate in the login keychain
(`security find-identity -v -p codesigning`).

**Notarization.** An App Store Connect API key (Developer role), stored once as a keychain
profile:

```sh
xcrun notarytool store-credentials hopsesh-notary \
  --key ~/.appstoreconnect/private_keys/AuthKey_<id>.p8 --key-id <id> --issuer <issuer-id>
```

The issuer id is shown above the key list in App Store Connect → Users and Access →
Integrations → App Store Connect API.

**Optional:** the repository secret `TAP_GITHUB_TOKEN` (a fine-grained token with write
access to `roeehrl/scoop-bucket` and a fork of `microsoft/winget-pkgs`) lets CI publish the
Scoop manifest and a winget pull request. Without it those steps are skipped.

## Cutting a release

1. Move the `CHANGELOG.md` entries from "Unreleased" to the new version, commit, push.
2. Tag and push: `git tag -s v0.1.0 -m "hopsesh 0.1.0" && git push origin v0.1.0`
   (only the repository admin can create `v*` tags).
3. Wait for the release workflow to finish (`gh run watch`).
4. On the Mac: `scripts/release-sign.sh v0.1.0`. A rehearsal that notarizes nothing and
   uploads nothing: `DRY_RUN=1 scripts/release-sign.sh v0.1.0`.
5. Review the draft on GitHub, then publish: `gh release edit v0.1.0 --draft=false`.

To test the whole pipeline without publishing, use a pre-release tag such as
`v0.1.0-rc.1`, then delete the draft and the tag
(`gh release delete v0.1.0-rc.1 --cleanup-tag`).

## Verifying a release by hand

```sh
openssl dgst -sha256 -verify packaging/release-key.pub -signature checksums.txt.sig checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
gh attestation verify hopsesh_0.1.0_linux_amd64.tar.gz --repo roeehrl/hopsesh   # Linux/Windows files
spctl -a -vv -t install hopsesh-0.1.0-macos-universal.dmg                         # macOS app
```
