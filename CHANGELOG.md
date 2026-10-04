# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## Unreleased

### Added
- Bring a session from Claude Code cloud: `hopsesh pull claude-cloud:<id>` (or the session's
  link) makes a new worktree of its repository and runs `claude --teleport` there, in your
  terminal (`--run`, or the command to paste). Once the copy appears, hopsesh checks its
  message count (complete, partial or empty, with the known Claude Code problem), keeps the
  cloud's `claude/…` branch as `hopsesh/from/claude-cloud/…`, records lineage, and `hopsesh
  undo` takes it all back. `--in codex` continues it in Codex, `--code-only` brings the
  branch alone.
- `hopsesh clouds` lists the agents' clouds; `clouds allow` and `deny` record whether hopsesh
  may use one, and `clouds test` checks one read-only. `hopsesh ls --cloud` (or
  `ls claude-cloud:`) shows cloud sessions and the local sessions Remote Control mirrors.
- In the app: a **Clouds** group and an **In the cloud** scope, cloud sessions with **Bring
  here**, a plan sheet and a done screen for bringing one back, a card per cloud under
  Machines (Allow, Test), and **Paste a cloud link** and **Find in Claude Code**. In the
  terminal UI: clouds in the header and as rows; enter brings one here.
- Hand a session off to Claude Code cloud: `hopsesh handoff <session> --to claude-cloud`
  (`hopsesh plan <session> --to claude-cloud` shows the plan first) starts a cloud session
  whose first prompt is a briefing of about 2,000 tokens, with likely secrets masked. A
  clean branch already on GitHub goes as it is; otherwise hopsesh pushes a
  `hopsesh/handoff/<date>-<id>` branch with a snapshot of the unpushed commits and changed
  files, leaving your checkout, index and branch as they were. Untracked files go only when
  you name them (`--untracked`); files that look like credentials never go. `--bundle`
  lets Claude Code upload the repository instead (for one that isn't on GitHub);
  `--history-file` also commits the conversation as `.hopsesh/handoff.md`. The session here
  is marked, the hop is recorded in its lineage, and `hopsesh undo` deletes the branch (only
  while the cloud hasn't pushed to it) and the mark. Works for sessions on your other
  machines too: the snapshot and the push happen there, over SSH.
- `hopsesh followup claude-cloud:<id> "<text>"` sends a cloud session a message.
- In the app: **Hand off ▸** on a session (every cloud, a disabled one with its reason),
  the hand-off sheet (the editable briefing, the branch, what stays on this Mac, the
  options), the steps as they run (and which one failed), and a done screen with the link,
  Undo and **Send a follow-up**; *Hand off to…* in the command palette; hand-offs in
  Activity. In the terminal UI: `c` hands the selected session off.
- The skill handles "hand this off to Claude Code cloud" (it plans first and asks), and its
  approval rules let `clouds --json` and `clouds test` run without asking while `handoff`
  and `followup` always ask.
- Four cloud-only agents: the GitHub Copilot cloud agent (through `gh agent-task` and
  `gh pr`), Jules (`jules remote`), Devin (`devin list --format json`) and Amp
  (`amp threads`). Once allowed, each lists its sessions in `hopsesh clouds`, `hopsesh ls
  --cloud` and the app's Clouds group, and `hopsesh clouds test` checks it read-only.
  Bringing one back brings its code into a new worktree (`hopsesh pull <cloud>:<id>
  --code-only`, or **Get the code** in the app): Copilot's and Devin's pull request branch,
  or Jules's patch committed on a `hopsesh/from/jules/…` branch; `hopsesh undo` takes it
  back. Their conversations stay in the cloud for now, and an Amp orb's code (`amp sync`)
  is not brought. These CLIs' output is mostly undocumented: hopsesh reads it defensively,
  and was tested against stand-ins only.

### Fixed
- Reading a Windows machine with many sessions could stall for 30 seconds and then fail:
  the long script hopsesh sent there over standard input sometimes never arrived, because
  PowerShell with redirected input can read it first. Such a script is now uploaded over
  SFTP, run from the file and removed.

### Changed
- The macOS app's self-update opens the new disk image with `diskutil image attach` (on
  macOS 27, which deprecates `hdiutil attach`), and with hdiutil on earlier systems.
- The configuration format is now schema 4, with room for the cloud sessions to come
  (`[clouds.<name>]`: whether hopsesh may use a vendor's cloud, and how code goes up). A
  configuration file from an earlier version is refused, never converted: the app offers
  to set it aside and start fresh (the old file stays next to the new one), the command
  line names the file to move aside, and you add your machines again. Sessions are not
  affected.
- Lineage manifests (the `.hopsesh.json` file beside each session hopsesh moved) have a new
  format that can record cloud copies. Manifests from earlier versions are not read: a
  session moved with 0.3 is treated as if hopsesh had not moved it before.

## [0.3.1] - 2026-10-04

Sessions from Windows machines now come over with their repository, the installers are
friendlier, and a move stops instead of guessing when the other machine's checkout cannot
be read.

### Changed
- The macOS disk image opens on a window: drag hopsesh to Applications.
- The Windows installer has a welcome page that says what it will do, a progress page, and
  a finish page that opens hopsesh and links to the getting-started guide. The licence page
  is gone; the LICENSE file is still installed.

### Fixed
- A Windows machine with more than a few dozen sessions lost its git details: the
  script that reads them, passed on the command line, outgrew cmd.exe's 8191-character
  limit. Long PowerShell scripts now go through standard input, and sessions from that
  machine come over with their repository again.
- When hopsesh cannot read the session's git checkout on the other machine (a dropped
  connection, say), it now tries once more and then stops and says so. Before, it took
  the folder for one without a repository: where the same path existed here (the same
  home folder on both machines), the session arrived without its branch or unpushed work.
- A Codex session that went to another machine and came back no longer keeps its "moved"
  mark in Codex's own list: the mark is the thread's name in Codex's shared index, which
  outlived the copy it marked.
- Unpushed commits and never-pushed worktree branches now come along from Windows machines
  too, through a git bundle (git over ssh does not work against Windows' OpenSSH, which
  runs commands through cmd.exe); before, hopsesh asked you to push them first.
- Moving or continuing a session from a Windows machine now rewrites its paths when its
  folder is a git repository: git reports `C:/Users/…`, the agents record `C:\Users\…`,
  and the two never matched, so the copy kept the other machine's paths.
- A session that ran in an agent worktree whose branch was never pushed now comes over
  with its branch, fetched straight from the other machine like unpushed commits on the
  checked-out branch (before, hopsesh stopped with "branch … exists neither locally nor on
  origin").

## [0.3.0] - 2026-10-03

hopsesh now works with more than one coding agent: Claude Code and Codex, each a module
behind a small SDK, and continues a session in the other agent. The desktop app comes to
Windows, and both apps update themselves. The configuration format changed; an older file
is refused and set aside (the app offers this), and machines are added again.

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

[0.3.0]: https://github.com/roeehrl/hopsesh/releases/tag/v0.3.0
