# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## Unreleased

### Added
- Discovery of machines from Tailscale and `~/.ssh/config` (connects to nothing), per-machine
  consent, strict host-key trust (`hosts`, `trust`), and `doctor`.
- Session listing over SSH/SFTP with titles, last prompt, last activity, live status, git
  remote, branch and worktree state, grouped by repository (`ls`, `show`).
- `pull`: repository matching or cloning, worktree recreation, path rewriting (including
  JSON-escaped and Windows paths, with separator conversion between Windows and
  macOS/Linux), secret scan and optional redaction, verified install, undo journal (`undo`),
  and a start prompt that tells the resumed session it was moved and asks it to check its
  environment. `import` installs a transcript copied by hand.
- Remote Control and old-session notification, with an eligibility check from
  `claude auth status`; signed reasoning blocks are dropped automatically across accounts.
- Interactive terminal UI (`hopsesh` with no arguments).
- Native macOS app (Wails), including macOS local network privacy handling.
- Optional, hash-pinned remote helper (`hosts helper install|remove`).
- `update` with checksum, release-key signature and macOS code-signature checks.
- Release pipeline: signed checksums, Sigstore signatures, SBOMs, build provenance, signed and
  notarized macOS binaries and app, install scripts, Homebrew/Scoop/winget and Linux packages.
