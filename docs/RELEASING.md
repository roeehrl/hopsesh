# Releasing hopsesh

A release is built in two places, and no signing key or Apple credential is ever stored
in GitHub:

1. **GitHub Actions** (`.github/workflows/release.yml`), on a `v*` tag: builds the Linux and
   Windows files with GoReleaser, the Windows app with `scripts/build-windows-app.sh` and the
   test bundle with `internal/devtools/testbundle`, records build provenance for each of
   them, and creates a **draft** release.
2. **The maintainer's Mac** (`scripts/release-sign.sh`): verifies that provenance, builds
   the macOS CLI and app from the tagged commit, signs and notarizes them, writes
   `checksums.txt` for every file, signs it with the release key, and uploads the result to
   the draft. A person then reviews and publishes it.

## What a release contains

| File | Made by | Checked by |
|---|---|---|
| `hopsesh_<ver>_linux_*.tar.gz`, `hopsesh_<ver>_windows_*.zip`, `.deb`/`.rpm`/`.apk` | GoReleaser in CI | build provenance (`gh attestation verify`), `checksums.txt` |
| `hopsesh-<ver>-windows-{amd64,arm64}-setup.exe` | `build-windows-app.sh` in CI (NSIS) | build provenance, `checksums.txt`; not code-signed yet (SmartScreen asks) |
| `hopsesh-windows-{amd64,arm64}-setup.exe` | CI (a copy of the above) | same; a stable link: `releases/latest/download/hopsesh-windows-amd64-setup.exe` |
| `hopsesh-<ver>-windows-{amd64,arm64}-app.zip` | `build-windows-app.sh` in CI | build provenance, `checksums.txt`; what the Windows app updates itself from (both programs, and Microsoft's ConPTY in `conpty/`: `conpty.dll`, `x64/OpenConsole.exe`, `arm64/OpenConsole.exe`, `LICENSE-conpty.txt`) |
| `*.sbom.json` | syft in CI | `checksums.txt` |
| `hopsesh-testbundle-<ver>.tar.gz`, `.sha256` | `internal/devtools/testbundle` in CI: the stand-in agents for six platforms, the agents' fixtures, the modules' specs and the contributed payloads ([testbundle/README.md](../testbundle/README.md)) | build provenance (the archive), `checksums.txt`; `release-sign.sh` checks the `.sha256` against the archive |
| `hopsesh_<ver>_darwin_{amd64,arm64}.tar.gz` | `release-sign.sh` | Developer ID signature, notarization, `checksums.txt` |
| `hopsesh-<ver>-macos-universal.dmg` | `release-sign.sh` → `build-macos-app.sh` | Developer ID signature, notarization (stapled), `checksums.txt` |
| `hopsesh-macos-universal.dmg` | `release-sign.sh` (a copy of the above) | same; a stable link for web pages: `releases/latest/download/hopsesh-macos-universal.dmg` |
| `checksums.txt`, `checksums.txt.sig` | `release-sign.sh` | the release key (`packaging/release-key.pub`) |

`hopsesh update`, the apps' own updates and the install scripts accept a download only when
`checksums.txt` is signed by the release key and the file matches it. The macOS app also
requires the same Apple Developer ID team and a notarized app before it replaces itself.
They all read GitHub's "latest release", which never points to a draft or a pre-release.

### Third-party files in the apps

- **Microsoft's ConPTY** (Windows app and installer). `build-windows-app.sh` runs
  `internal/devtools/conptyfetch`, which downloads the `Microsoft.Windows.Console.ConPTY`
  package from NuGet at build time and refuses it unless its SHA-512 matches the version and
  hash pinned in `conptyfetch`. So a release build needs nuget.org, and a bump means changing
  that version and hash (CI's Windows jobs fetch the same pinned package for the tests).
- **xterm.js** (the hopsesh Terminal window, both apps). It is vendored in
  `internal/ui/gui/assets/terminal/vendor` with its licence and a `VERSIONS` manifest, and
  built into the binary, so a release fetches nothing. A bump is a normal pull request: change
  the pinned versions and integrities in `internal/devtools/xtermfetch`, run it, and commit
  the folder (`TestVendoredXterm` checks the files against `VERSIONS`).

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

Before tagging:

- `main` is green in `ci`, and the latest `nightly` run on `main` is green. The nightly runs
  the app's terminal checks on macOS and Windows; they are the release gate for the hopsesh
  Terminal window. If they are not green, ship with `config.AppResumeDefault = ResumeTerminal`
  (the app then resumes sessions in the user's terminal app until they choose otherwise).
- The manual checks the stand-ins can't replace, on the Mac, for what the release touches:
  `scripts/cloud-smoke.sh` (real Claude Code cloud and Codex cloud; it starts paid turns),
  `scripts/iterm-smoke.sh` and `scripts/iterm-api-smoke.sh` (iTerm2), and the app's
  terminal window by hand.

Then:

1. Move the `CHANGELOG.md` entries from "Unreleased" to a `## [X.Y.Z] - date` section
   (leave an empty `## Unreleased` above it, and add the `[X.Y.Z]:` link at the bottom),
   commit, push. That section becomes the release notes (the workflow fails without it).
2. Tag and push: `git tag -a v0.4.0 -m "hopsesh 0.4.0" && git push origin v0.4.0`
   (use `-s` instead of `-a` if git has a signing key configured; only the repository admin
   can create `v*` tags). The tag also starts `release-tests` (the scenario matrix at
   triple coverage).
3. Wait for the release workflow to finish (`gh run watch`).
4. On the Mac: `scripts/release-sign.sh v0.4.0`. A rehearsal that notarizes nothing and
   uploads nothing: `DRY_RUN=1 scripts/release-sign.sh v0.4.0`. It also checks the test
   bundle's `.sha256` and that the draft is a pre-release exactly when the tag has a suffix.
5. Review the draft on GitHub, then publish: `gh release edit v0.4.0 --draft=false --latest`.
6. The website: bump `softwareVersion` in the hopsesh entry of `src/data/apps.ts` in
   [codonic-site](https://github.com/roeehrl/codonic-site) and merge it. It's the only place
   the site writes the version; its download links use `releases/latest/download/…` and
   follow the release by themselves.

To test the whole pipeline without publishing, use a pre-release tag such as
`v0.5.0-rc.1`. A tag with a pre-release suffix (`-rc.N`, `-beta.N`) makes a draft marked as
a **pre-release** (GoReleaser's `release.prerelease: auto`), gets placeholder notes unless
`CHANGELOG.md` has a section for that exact version, and never publishes the Scoop manifest or a winget pull
request. GitHub's "latest release", which `hopsesh update`, the apps and the install scripts
read, never points to a pre-release, so even a published one updates nobody. When you are
done, delete the draft and the tag (`gh release delete v0.5.0-rc.1 --cleanup-tag`).

**Never reuse a tag name, even a deleted test tag.** The Go module proxy and checksum
database cache every version they see, permanently. A
reused name with different contents breaks `go install` for everyone with a checksum
mismatch. Pick the next number instead.

## Verifying a release by hand

```sh
openssl dgst -sha256 -verify packaging/release-key.pub -signature checksums.txt.sig checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
gh attestation verify hopsesh_0.4.0_linux_amd64.tar.gz --repo roeehrl/hopsesh   # Linux/Windows files
gh attestation verify hopsesh-testbundle-0.4.0.tar.gz --repo roeehrl/hopsesh     # the test bundle
spctl -a -vv -t install hopsesh-0.4.0-macos-universal.dmg                         # macOS app
```
