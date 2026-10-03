# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## Unreleased

hopsesh now works with more than one coding agent: Claude Code and Codex, each a module
behind a small SDK. The configuration format changed; an older file is refused and set
aside (the app offers this), and machines are added again.

### Added
- The desktop app on Windows (amd64 and arm64): a per-user installer (no administrator
  rights) with a Start menu entry, the hopsesh command on your PATH and an uninstaller.
  Passwords can be remembered in Windows Credential Manager, sessions open in Windows
  Terminal (or PowerShell), and the window uses Ctrl shortcuts. Not code-signed yet, so
  Windows SmartScreen asks before the first run.
- Both apps update themselves: Settings → Updates → Install and restart, after the same
  checks as `hopsesh update` (release signature and checksum; on macOS also the same
  Apple developer and notarization). `hopsesh update` run from inside an app updates
  the whole app.
- Codex sessions: listing, live state, moving between machines (paths rewritten,
  encrypted content untouched), marks in Codex's own list, and Codex lists what hopsesh
  installs right away.
- Continuing a session in another agent, on another machine or the same one:
  `pull --in codex` / `--in claude`. The conversation arrives as text history (oldest steps
  summarised when long) or only a briefing (`--fidelity note`), with exact shell calls
  replayed natively where the target can (`--native`), or through Codex's own importer
  (`--via import`). Every plan shows a loss report and the briefing the other agent gets;
  `--note-file` adds a handoff note and `--carry-rules` your instructions for every project.
- Round trips across agents: going back adds only the new work to the original session,
  whose earlier turns (and their signed reasoning) stay byte for byte. Continuing on
  another machine also keeps the source agent's own copy there for that purpose.
- Lineage beside each session (`<session>.hopsesh.json`) that travels with it, so any
  machine knows a session's copies and hops.
- `hopsesh push <session> <machine>` sends a session to another machine's hopsesh, which
  receives it only after `hopsesh receive on` there; one `undo` reverses both sides.
- Codex: hopsesh tells whether Codex is working or idle, can quit an idle Codex session
  open here (`--stop-local`), and knows which account a machine's Codex is signed in to
  (from Codex itself; `auth.json` is never read). Moving a Codex session to another
  account drops the encrypted reasoning and compaction that only the old one can use.
- Sending to and from Windows machines with `push`.
- Windows: paths a session names by their 8.3 short form (`C:\Users\LIHUA~1\…`) move
  with their folder; hopsesh starts on a machine whose user name is in Japanese or Chinese
  through its short path; machines with Git for Windows on their PATH are recognised as
  Windows.
- The app can send a session on this machine to another one ("Send to…").
- `hopsesh agents` lists the supported agents and what each can do; `ls --agent` filters.
- The skill is written to every installed agent (Claude Code and Codex) as identical
  files, with per-copy drift detection and approval rules for each agent.
- A redesigned app: scopes (Needs you, each machine, each agent), sessions with their
  state and copies, a details pane with the session's history, the plan as a sheet with
  a summary and one-click fixes, a result page with the loss report, an Activity page
  with undo, a Machines page (receiving, login, host keys, discovery), tabbed settings,
  a ⌘K palette and keyboard control.
- Agents are pictured by their installed desktop app's icon (read on this machine; turn it
  off in Settings), else a mark the module draws, else their initials. Modules declare both
  in `Spec.Icon`.
- `[agents.<id>] import = true` sends continuations into that agent through its own
  importer by default.
- `HOPSESH_MACHINE` sets the name this machine has in marks and lineage.

### Changed
- `--desktop` is now `--app` (open in the agent's desktop app, where it has one).
- Undoing a mark or a line in an agent's shared index removes only hopsesh's own bytes,
  so lines written afterwards stay.
- `undo` refuses when a session it would change was used since (that work would be
  lost), including a session that came back from another agent and was continued; `undo
  --force`, or "Undo anyway" in the app, does it anyway.

### Removed
- The optional remote helper (`hosts helper`, `hopsesh agent`) and `hopsesh import`.
- The `live_policy` and top-level `remote_control` settings (remote control is now per
  agent).

### Fixed
- On Windows, `hopsesh trust` (and adding a machine in the app) works with servers whose
  host keys Windows' `ssh-keyscan` cannot read, such as Ubuntu 24.04's OpenSSH: the key is
  then read through one ssh connection and still shown for confirmation.
- Continuing a session in another agent on the same machine uses the session's own
  checkout wherever it is, instead of asking to clone it into the repos folder.
- A session is recognised as already being in a folder reached through a symlink (such as
  macOS's `/var`), instead of being moved onto itself.
- The terminal UI no longer carries out a plan you pressed `y` on while a changed one was
  still being worked out.

## [0.2.0] - 2026-10-02

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
- The text `pull` prints for pasting into the old session named the new session instead of
  this machine.
- The app opened from Finder now uses the `CLAUDE_CONFIG_DIR` set in your shell for
  scanning and moving too, not only for the skill.

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

[0.2.0]: https://github.com/roeehrl/hopsesh/releases/tag/v0.2.0
[0.1.0]: https://github.com/roeehrl/hopsesh/releases/tag/v0.1.0
