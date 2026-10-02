# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## Unreleased

### Added
- Round trips: after a handoff the copy left behind is titled "↪ moved to <machine> · <title>",
  so Claude Code's resume list there shows it moved (a running session is marked once it
  stops). Listings show one row per session with the newest copy, and `pull` without a
  machine name brings back the newest copy. Moving back refuses when the copy here changed
  too, unless you pass `--keep-both` (a separate session) or `--replace` (kept for undo);
  `--stop-local` quits a copy running here.
- Code follows the session: the checkout here is brought to the session's commit (fetched
  from origin, or straight from the other machine when it was not pushed) and fast-forwarded
  only when clean; `--push` optionally pushes on the other machine first.
- `hopsesh plan`: the read-only version of `pull --dry-run`.
- The hopsesh skill for Claude Code (`hopsesh skill install`): Claude can list your sessions
  and move one, always showing the plan and asking first. hopsesh detects whether it is
  installed, out of date or edited by you, and never overwrites your edits.
- The app can put the hopsesh command on your PATH (a link into the app, no password), and
  has a Settings screen for the skill, the command, moving defaults, updates and folders.
- Password login for machines that don't take keys: `hosts add <name> <dest> --password`,
  `hosts auth <machine> key|password`, and the app's Login button on the Machines screen.
  ssh asks hopsesh, which answers from the macOS Keychain (on by default, per machine), a
  hidden terminal prompt, a dialog in the app, or `--password-stdin`. The password is never
  written to hopsesh's files or a command line. `hosts setup-key <machine>` (Set up key
  login in the app) adds your key there and switches the machine to key login.

### Fixed
- `install.sh` names the right profile file for your shell and no longer replaces a
  command linked by the app.

## [0.1.0] - 2026-10-02

First public release.

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
- Release pipeline: Linux and Windows builds with build provenance and SBOMs in CI; macOS
  binaries and app signed and notarized on the maintainer's Mac; `checksums.txt` signed with
  a release key that never leaves that Mac; install scripts; Scoop/winget and Linux packages.

[0.1.0]: https://github.com/roeehrl/hopsesh/releases/tag/v0.1.0
