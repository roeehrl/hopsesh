# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## Unreleased

## [0.4.0] - 2026-10-05

hopsesh now works with the coding agents' clouds: it hands a session to Claude Code on the
web, Codex cloud, the GitHub Copilot cloud agent, Jules, Devin or Amp, brings their sessions
home, and hands one cloud's session on to another, always through the vendors' own
command-line tools, signed in as you. The desktop app gets a terminal of its own, the
hopsesh Terminal window, and sessions you open elsewhere go to a tab of iTerm2 or your other
terminal app. Copilot, Jules, Devin and Amp support is experimental: it has been tested
against stand-ins of their tools only. The configuration format changed: after upgrading,
start fresh and add your machines again.

### Added

- **Cloud sessions.** `hopsesh clouds` lists the clouds hopsesh can reach: Claude Code cloud
  (`claude-cloud`), Codex cloud (`codex-cloud`), the GitHub Copilot cloud agent
  (`copilot-cloud`), Jules (`jules`), Devin (`devin`) and Amp (`amp`). Each is off until you
  allow it (`hopsesh clouds allow <cloud>`, `clouds deny` to turn it off again), and
  `hopsesh clouds test` checks its login and the commands hopsesh uses, read-only. hopsesh
  reaches a cloud only through the vendor's own tool (`claude`, `codex`, `gh`, `jules`,
  `devin`, `amp`), run on this machine and signed in as you: it never reads a login or calls
  a vendor's servers, and the cloud sessions it starts run on your plan. `hopsesh ls --cloud`
  (or `hopsesh ls <cloud>:`) lists cloud sessions, with the local sessions Claude Code's
  Remote Control mirrors.
- **Hand a session off to a cloud:** `hopsesh handoff <session> --to <cloud>` (`hopsesh plan
  <session> --to <cloud>` shows the plan first) starts a cloud session that continues one on
  this machine or on another of yours. No cloud takes a conversation, so the cloud agent
  gets a briefing of about 2,000 tokens as its first prompt (likely secrets masked; you can
  read and edit it) and the code on a branch. A clean branch already on GitHub goes as it
  is; otherwise hopsesh pushes a `hopsesh/handoff/<date>-<id>` branch with a snapshot of the
  unpushed commits and changed files, leaving your checkout, index and branch as they were.
  Untracked files go only when you name them (`--untracked`); credential-like files (`.env*`,
  SSH keys, `*.pem`, `*.key`, …), Git LFS files and files over 50 MB never go.
  `--history-file` also commits the conversation as `.hopsesh/handoff.md`, and `--bundle`
  lets Claude Code upload a repository that isn't on GitHub. The session here is marked, and
  `hopsesh undo` deletes the branch (while the cloud hasn't pushed to it) and the mark; the
  cloud session stays in the vendor's list for you to archive there.
- **Claude Code cloud, both ways.** Claude Code starts a cloud session, and copies one home,
  only in a terminal you can answer, so hopsesh runs both steps where you see them: in your
  terminal from the command line, in the terminal UI (which hands over its terminal and
  comes back), or in the app's hopsesh Terminal window (or your own terminal app). A
  hand-off runs `claude --cloud "<briefing>"` in a hand-off folder hopsesh keeps for each
  repository and reads the session's link Claude Code prints, or asks you to paste it. The
  first time, Claude Code asks whether you trust that folder: you answer it, once per
  repository, and hopsesh never answers it or changes Claude Code's settings to skip it.
  `hopsesh pull claude-cloud:<id>` (or the session's link) makes a new worktree of its
  repository and runs `claude --teleport` there (`--run`, or the command to paste). Claude
  Code saves its copy only after you send a message in it: send one (even "ok"), then type
  `/exit`, and hopsesh picks the copy up, checks it against the briefing when hopsesh
  started that cloud session (otherwise it says how many messages came), keeps the cloud's
  `claude/…` branch as `hopsesh/from/claude-cloud/…` and records it all for undo. `--in
  codex` continues it in Codex, and `--code-only` brings the branch alone. An agent running
  hopsesh has no terminal to answer in, so the plan says you have to run these steps
  yourself.
- **Codex cloud, both ways** (Codex cloud legacy tasks; the new Codex Cloud has no command
  line yet, and hopsesh says so). `hopsesh handoff <session> --to codex-cloud --env
  <environment>` starts a task with the briefing and the code on a branch, or with a few
  changes as a starting diff on a branch already pushed (`--starting-diff`); `--attempts`
  asks for several attempts. The plan suggests the environments your recent tasks used, and
  hopsesh remembers your pick for the repository (`hopsesh clouds env codex-cloud`).
  `hopsesh pull codex-cloud:<id>` brings a task back: its diff committed on
  `hopsesh/from/codex-cloud/<id>` in a new worktree, and its title and outcome written as a
  new Codex session there (`--in claude` writes a Claude Code session). Only the first
  attempt comes back. hopsesh needs your `codex` signed in with ChatGPT and never reads its
  login.
- **Copilot, Jules, Devin and Amp (experimental).** Hand a session off with `--to
  copilot-cloud`, `jules`, `devin` or `amp`, list their sessions, and bring their code into a
  new worktree: Copilot's and Devin's pull request branch, or Jules's patch committed on
  `hopsesh/from/jules/<id>`. Copilot's session log and Amp's thread come home as text,
  written as a new session of the agent the session was handed off from (else Claude Code;
  `--in` picks another). Jules's and Devin's messages and an Amp orb's code stay in their
  clouds. Jules, Devin and Amp can't be told which branch to start from, so the briefing
  asks the cloud agent to check the handoff branch out first, and the plan says so. Most of
  these tools' output is undocumented: hopsesh reads it defensively and has been tested
  against stand-ins of them only, not against the real services.
- **Cloud to cloud:** `hopsesh handoff <cloud>:<id> --to <other cloud>` brings a cloud
  session here first, as `hopsesh pull` does, keeps that copy, and hands it off from here. A
  Claude Code cloud session goes on to Codex cloud from its own `claude/…` branch as it is;
  a Codex cloud task's diff is committed here and pushed on a handoff branch. The plan shows
  both legs and what the trip loses, and one `hopsesh undo` takes both back. `hopsesh clouds
  continue <hop>` goes on with a hop whose first leg ran in another terminal.
- **Add a cloud's work to the original session:** `hopsesh pull <cloud>:<id> --append` adds
  what came back to the session it was handed off from, when that session is as you left
  it. Its own turns stay byte for byte, and undo cuts the addition off again.
- **Branch clean-up:** `hopsesh clouds cleanup` lists the handoff branches hopsesh pushed and
  the clouds' own branches it brought home (`claude/…`, `copilot/…`), asks each remote,
  read-only, whether their work is in its default branch (by history, or a merged pull
  request where `gh` is installed), and deletes the merged ones only when you confirm, each
  only while it is where hopsesh saw it. Undo pushes them back. `delete_branch = "on-undo"`
  or `"never"` under `[clouds.<name>]` keeps them.
- **Clouds in the app:** a **Clouds** group and an **In the cloud** scope; **Hand off ▸** on
  every session, local or in a cloud (a cloud hopsesh can't use is listed with the reason);
  the hand-off sheet with the editable briefing, the branch, what stays on this machine and
  the options; the steps as they run, and a done screen with the link and Undo; **Bring
  here** and **Get the code** on cloud sessions; **Paste a cloud link** and **Find in Claude
  Code**; a card per cloud under **Machines** with Allow, Test, the Codex cloud environment
  per repository and **Sign in**, which runs `claude auth login`, `codex login
  --device-auth` or `gh auth login --web` in a tab that records nothing; and **Activity →
  Look for merged branches**. In the terminal UI: clouds in the header and as rows, `enter`
  brings one here, `c` hands a session off, `e` picks the Codex cloud environment and `S`
  the starting diff.
- The skill handles "hand this off to Claude Code cloud" (or to any of the other clouds): the
  agent shows the plan and asks first. For Claude Code cloud it gives you the command to run
  in your own terminal. With `--add-rules`, `clouds` and `clouds test` run without asking,
  while `handoff`, `clouds cleanup` and `clouds continue` always ask.
- **The hopsesh Terminal window:** the app has a terminal of its own, a window with tabs.
  **Resume here** runs a session in a tab, and so do Claude Code's hand-off and bring-back
  steps, cloud sign-ins and **Open a shell here**. hopsesh starts the command and you do all
  the typing: it never writes into a program or answers a question for you. Each tab shows
  whether its program runs, waits for you or has exited (with its code), and has **Open in
  my terminal**, which ends the tab and runs the same command in your terminal app after
  asking. Banners say when Claude Code asks whether it trusts the hand-off folder (you
  answer it in the tab) and, when bringing a session back, to send one message and then
  type `/exit`; the done screen says when the tab ended before Claude Code saved a copy.
  Copy, find and clear, links that open only after you confirm the whole address,
  Shift+Return for a new line in Claude Code, a screen reader mode, and colours that follow
  the app's light or dark look. Ctrl+` moves between the terminal and your sessions. It
  uses xterm.js 6.0.0 (MIT), shipped inside the app.
- Sessions running in a tab show **In a tab** and **Waiting for you** and count in **Needs
  you**; the sidebar and the title bar show the terminal and how many tabs wait. A session
  runs in one tab at most: Resume shows its tab instead. On macOS a tab you can't see that
  waits for you raises a notification (at most one every 10 seconds) and a Dock badge; on
  Windows the terminal's taskbar button flashes. Quitting hopsesh while programs run in its
  tabs asks first and lists them, and closing the window hides it while they run (**Keep
  tabs when the window closes**).
- **Settings → Terminal:** where sessions, hand-offs and bring-backs open (**In this
  window**, the default; **In my terminal**; **Ask each time**), your terminal app, the
  font, its size, scrollback (5,000 lines by default), keeping tabs when the window closes,
  notifications, the screen reader mode and, on Windows, the bundled console host. The
  choices are saved under `[terminal]` in the configuration. What a tab shows is kept in
  memory only, never written to disk or a log.
- On Windows the app and its installer carry Microsoft's ConPTY (`conpty.dll` and
  `OpenConsole.exe`, MIT) in a `conpty` folder, which the tabs use instead of the older one
  built into Windows. Tabs never run batch files, so for an npm command shim (such as
  `codex.cmd`) a tab runs the program behind it directly, and so do the command line and
  the terminal UI.
- **Your terminal app:** sessions, teleports and hand-off steps that hopsesh opens outside
  its own window go to a new tab in iTerm2's front window when iTerm2 is installed (else a
  Terminal window; Windows Terminal on Windows). The tab runs hopsesh's own launcher with an
  id, never a title or a prompt. In iTerm2 the tab gets a badge with the session's title,
  agent and machine, and `user.hopsesh_title`, `user.hopsesh_agent` and
  `user.hopsesh_machine` variables for your own title or status bar; it keeps the tab open
  with the exit code shown when the agent ends.
- `hopsesh open <session>` resumes a session on this machine in your terminal app
  (`--terminal <id>` picks one, `--here` runs it in this terminal). `hopsesh terminals` lists
  the terminal apps, chooses one (`--use`) and sets where the app resumes sessions
  (`--resume`). `hopsesh pull --run --terminal <id>` starts the moved session in a new tab.
- **A running session is shown, not started twice:** **Show its terminal tab** in the app
  and `hopsesh open` bring its iTerm2 or Terminal tab forward, found from the agent's own
  process and the terminal it runs on, never from what the tab shows. Resuming a session
  that already runs is refused.
- When macOS denies hopsesh control of iTerm2, the launch opens in Terminal and the app says
  so; when Terminal is denied too, you get the command to copy. hopsesh only opens tabs and
  brings them forward, from a fixed list of AppleScript lines: it never types into or reads
  a tab, and never changes iTerm2's settings or installs its Claude Code integration.
- **iTerm2's Python API, when you have turned it on yourself:** hand-off steps open in a
  split beside the session you are in, the app learns at once when a step's tab is closed
  and shows how a session you opened there ended ("exited N", or "closed" when the tab was
  closed first), and finding and focusing a session's tab go through the API. hopsesh's
  client can open, split, find and focus tabs and set its own labels; it has no request
  that types into a session or reads its screen. With the API off, or on any error, hopsesh
  uses AppleScript, and it never asks again after you said no.
- `hopsesh agents --json` includes each module's spec: its programs, data folders, the
  secrets it never opens, instruction files, features, desktop apps, and its clouds with
  their fidelity, needs and the upstream changes the drift check watches.
- Each release has a test bundle, `hopsesh-testbundle-<version>.tar.gz` (with a `.sha256`,
  in `checksums.txt` and with build provenance): the stand-in agents for six platforms, the
  agents' fixtures, the modules' specs and scrubbed payloads other projects contribute, so a
  project that ships hopsesh can test against exactly that version. See
  `testbundle/README.md`.
- For module authors (the SDK is v0 and changes with the app): cloud capabilities
  (`CloudLister`, `CloudSender`, `CloudStepReader`, `CloudFetcher`, `CloudAdopter`,
  `CloudLinker`, `CloudTester`, `CloudFollower`, `CloudArchiver`), clouds declared in
  `Spec.Clouds` (with the cloud's sign-in command and `BriefBranch` for a cloud that can't
  name its starting branch), cloud-only modules (`agent.NoLocal`), `Spec.TerminalEnv` for
  variables a module's programs get in the app's tabs, `agent.LinksIn` and `agent.LinkOn`,
  which read links on an exact host only, and the cloud conformance kit
  (`agenttest.RunCloud`, `agenttest.RunCloudWith`).

### Changed

- **Upgrading from 0.3: add your machines again.** The configuration format is now schema 4,
  with `[clouds.<name>]` and `[terminal]`. A configuration file from 0.3 is refused, never
  converted: the app offers to set it aside and start fresh (the old file stays next to the
  new one as `config.toml.old-<date>`), and the command line names the file to move aside.
  Then add and allow your machines again. Your sessions are not affected. A configuration
  file written by a newer hopsesh is reported as newer: update hopsesh.
- Lineage manifests (the `.hopsesh.json` file beside each session hopsesh moved) have a new
  format that can record cloud copies. Manifests written by 0.3 are not read: a session
  moved with 0.3 is treated as if hopsesh had not moved it before. Update hopsesh on every
  machine where you run it, so they read each other's manifests.
- The peer protocol is now 2: a push between hopsesh 0.3 and 0.4 stops at hello and names
  the machine to update to 0.4.0 or later.
- The app now resumes sessions in its hopsesh Terminal window by default. To open them in
  your terminal app as before, choose **In my terminal** (or **Ask each time**) in Settings
  → Terminal. The command line still runs sessions in your terminal.
- The macOS app's Automation permission text names your terminal app (iTerm2 or Terminal)
  and says hopsesh only opens tabs and brings a session's tab forward.
- The macOS app's self-update opens the new disk image with `diskutil image attach` on
  macOS 27, which deprecates `hdiutil attach`, and with `hdiutil` on earlier systems.

### Fixed

- Reading a Windows machine with many sessions could stall for 30 seconds and then fail:
  the long script hopsesh sent there over standard input sometimes never arrived. Such a
  script is now uploaded over SFTP, run from the file and removed.
- One session folder that git could not read quickly, such as a folder in iCloud Drive or
  OneDrive whose files are not downloaded, made every scan wait for it, and with it
  `hopsesh ls`, `plan` and `pull`, sometimes for minutes. Each git call hopsesh makes to read
  a folder now gets 20 seconds to answer (30 seconds on another machine over SSH), on macOS,
  Linux and Windows; a folder that is slow but answers is still read in full. A folder where
  git does not answer in time is reported as such, not as one without a repository: moving
  that session stops and says why, and the other sessions are not affected. `pull`, `plan`,
  `push` and `show` now ask git only about the folders of the session they name.
- Two operations started in the same millisecond could share an undo journal, so one's undo
  record overwrote the other's.

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

[0.4.0]: https://github.com/roeehrl/hopsesh/releases/tag/v0.4.0
[0.3.1]: https://github.com/roeehrl/hopsesh/releases/tag/v0.3.1
[0.3.0]: https://github.com/roeehrl/hopsesh/releases/tag/v0.3.0
