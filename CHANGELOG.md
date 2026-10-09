# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## Unreleased

### Added

- Link a logical cloud task to a verified saved handoff in Settings or the CLI.
  Original ancestry and briefing context survive later checkpoints and agent
  changes; explicit cloud forks keep independent branches and trip counts.
  Identity associations appear in journey history without counting a transfer.

- Paired native machines publish approved inventory changes through the shared
  encrypted relay connection. Idle renewals replace repeated remote scans;
  source timestamps, expiry, pause state and current approvals remain explicit.

- Review and install cloud startup files from Settings or the CLI. Claude's
  cloud-only hook prepares fresh keys quietly; Codex setup includes reviewed
  repository guidance and optional environment Start skill instructions with an
  explicit unqualified-startup status. Missing task identity remains unsupported.
  Existing settings and unrelated repository instructions are preserved, modified files are
  refused, and stale previews cannot overwrite concurrent edits.

- Cloud session invitations in CLI and Settings: admit one fresh provider/session
  incarnation, check provisional claim status and revoke its delivery lease.
  Independent fingerprint approval still controls sharing; rebuilds and forks
  use fresh invitations. Retry preserves the original scoped credential.

- Optional relay browser approval: desktop uses an external browser with S256 PKCE
  and a temporary loopback callback; headless machines use `hopsesh relay login`
  with a short-lived code. Approval grants delivery only, separate from local
  conversation sharing and receiving. Private credentials stay in local state.
- 0.5 foundation: `hopsesh runtime observe` reads local sessions without initializing
  accounts, probing login, adopting imports or applying movement marks. `--watch`
  coalesces filesystem notifications with bounded fallback reconciliation and reports
  source freshness, errors and profile-scoped session identities, without requiring a GUI.
- Explicit logical cloud task continuity across approved resumes and rebuilds.
  Fresh keys and owner-signed generations supersede old connector access while
  verified native prefixes preserve checkpoint lineage. Checkpoint forks remain
  separate, and private change notifications shut down superseded connectors.

- Saved session metadata appears immediately in GUI, Quick access, TUI and
  `hopsesh ls --cached`, with progressive discovery, independent source retries,
  private summary caching and reconciled local file notifications.
- Session account labels and grouping across GUI, TUI and CLI. Account groups
  include retained source copies after a move and keep same-email profiles separate.

### Fixed

- Active internet requests wake the shared HTTP fallback receiver to collect
  replies promptly. Completed, canceled and expired requests return to idle
  reconciliation; healthy WebSocket delivery keeps using notifications.

- Concurrent transfers from separate machines preserve both source lineage
  receipts, even when their local journal IDs match. Recovery of the same journal
  cannot take over a receipt lock held by another active recovery process.

- Moving an original after creating a fork preserves the fork's ancestry and
  return destinations. Retrying the first transfer keeps its original receipt
  instead of reporting a conflicting causal history.

- Background GUI and TUI scans keep independent settings snapshots. Cloud consent
  and environment edits cannot alter an in-flight scan or crash catalog hashing;
  copied settings retain their revision for conflict-safe saves.

- Cloud admission preserves the signed absolute lease across relay objects;
  network latency cannot extend a connector's authority.
- Progressive startup preserves pressed controls. Shared observation watches
  Claude's process registry, retries concurrent account registration, and avoids
  creating an unused browsing database merely to invalidate it.

- Background inventory notifications no longer replay an already displayed scan
  or interrupt splitter dragging, keyboard resizing and form editing. Public
  pairing fingerprints wrap within compact enrollment dialogs.
- Cloud observations accept canonical Windows workspace paths when inspected
  from another operating system; remote metadata never grants local file access.

- Relay receiving starts independently of the first local inventory scan, so a
  slow scan no longer delays connections after a runtime restart.
- Reading large private-state registries on Windows avoids repeatedly propagating
  unchanged directory permissions. Every read still verifies ownership and the
  exact protected access list before accepting the file.

- Installing a missing macOS app cannot replace another app or filesystem entry
  that appears while the signed download is being verified. Publication refuses
  replacement atomically; existing installations keep their updater workflow.

- After explicitly forking rewritten cloud history, later checkpoints continue
  the accepted branch. Source acknowledgment recovers after interruption; undo
  restores the previous task ledger without changing cloud vendor files.

- Claude/Codex sign-in detection, Codex initialization ordering, and account
  refresh when the vendor sign-in terminal exits. Remote discovery retains public
  owner-machine identity metadata and explains SSH/Keychain check limitations.
- Background refreshes preserve selected conversations, focus, menus and scroll;
  early summaries retain provisional repository metadata during enrichment.
  Large session lists render bounded rows while preserving keyboard navigation.
- Scanning one machine does not rescan every peer. Remote reconciliation runs
  independently of local updates; Tailscale CLI failures are visible and peers
  without MagicDNS remain discoverable.

### Security

- Build with Go 1.27.2, which fixes the standard-library vulnerabilities reported
  against the previous pinned toolchain.

## [0.4.0] - 2026-10-08

- Imported conversations now end with a clearly attributed Hopsesh import notice, rather
  than a fabricated agent promise to start working. GUI, CLI and TUI explain when the
  destination still needs your first message; opening the desktop app only shows the chat.

hopsesh now works with the coding agents' clouds: it hands a session to Claude Code on the
web, Codex cloud, the GitHub Copilot cloud agent, Jules, Devin or Amp, brings their sessions
home, and hands one cloud's session on to another, always through the vendors' own
command-line tools, signed in as you. The desktop app gets a terminal of its own, the
hopsesh Terminal window, and sessions you open elsewhere go to a tab of iTerm2 or your other
terminal app. Codex cloud, Copilot, Jules, Devin and Amp support is experimental: Copilot,
Jules, Devin and Amp have been tested against stand-ins of their tools only, and Codex cloud
works only with older Codex cloud environments, because the `codex` command can't see
environments made in today's Codex cloud. The configuration format changed: after upgrading,
start fresh and add your machines again.

### Added

- **Terminal panels in the main window.** Choose Separate window, Bottom panel or
  Right panel in Settings → Terminal. Resize, maximize, hide, dock and detach the
  terminal while preserving its programs, screen, scrollback and input state.
  A failed move keeps the original terminal view usable.
- **Conversation families.** Group sessions and terminal tabs by verified forks and
  transfers in the GUI, TUI and CLI. Name and collapse families, distinguish branches
  from copies, and associate existing shells without changing conversation lineage.
  Native Codex forks require inherited-history evidence; unverified sessions stay separate.

- Choose System, Light or Dark in Settings → General → Appearance. The saved choice
  updates Hopsesh, Quick access and open terminal tabs immediately; System follows
  live OS changes. macOS native window chrome follows the same choice.

- Durable movement notices and branch-aware return destinations. Transfers report
  preparation until new agent work is observed; forks keep the original available.
  Notices can be disabled without losing lineage or returns. Receipts now require
  `lineage/5` and peers protocol 5; older versions are refused without migration.
  See [movement and return](docs/movement-return.md).


- **Menu bar and system tray Quick access.** Choose the supported Dock/taskbar/tray
  placement in Settings → Desktop, independently of login startup and window-close
  behavior. Search sessions, preview conversations, open exact session details and focus
  existing terminal tabs from the compact window. Linux detects tray support and keeps
  normal app access when the host is unavailable. See [Desktop presence](docs/desktop-presence.md).
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
  Code saves its copy only after you send a message in it: send one (even "ok") and
  hopsesh picks the copy up, checks it against the briefing when hopsesh
  started that cloud session (otherwise it says how many messages came), keeps the cloud's
  `claude/…` branch as `hopsesh/from/claude-cloud/…` and records it all for undo. `--in
  codex` continues it in Codex, and `--code-only` brings the branch alone. An agent running
  hopsesh has no terminal to answer in, so the plan says you have to run these steps
  yourself.
- **Codex cloud, both ways (experimental).** It works with older Codex cloud environments
  only: the `codex` command can't see environments made in today's Codex cloud, and hopsesh
  says so. `hopsesh handoff <session> --to codex-cloud --env
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
- **Clouds in the app:** a **Clouds** group in the sidebar; **Move → Hand off to ‹cloud›…**
  on every session, local or in a cloud (a cloud hopsesh can't use is listed with the reason);
  the hand-off sheet with the editable briefing, the branch, what stays on this machine and
  the options; the steps as they run, and a done screen with the link and Undo; **Bring to
  this Mac…** and **Get the code…** on cloud sessions; **Paste a cloud link** and **Find in Claude
  Code**; a card per cloud under **Machines** with Turn on, Test, the Codex cloud environment
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
  **Resume in hopsesh Terminal** runs a session in a tab, and so do Claude Code's hand-off
  and bring-back steps, cloud sign-ins and **Open a shell in its folder**. hopsesh starts the command and you do all
  the typing: it never writes into a program or answers a question for you. Each tab shows
  whether its program runs, waits for you or has exited (with its code), and has **Open in
  my terminal**, which ends the tab and runs the same command in your terminal app after
  asking. Banners say when Claude Code asks whether it trusts the hand-off folder (you
  answer it in the tab) and, when bringing a session back, to send one message, then that
  the copy is saved; the done screen says when the tab ended before Claude Code saved a copy.
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
  window**, the default, or **In my terminal**; a session's Resume menu picks another place,
  remembered for that agent), your terminal app, the
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
- **A running session is shown, not started twice:** **Show in ‹terminal app›** in the app
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

- **The sessions screen, redesigned.** The sidebar lists places: Needs you, All sessions,
  This Mac and your other machines, and the clouds that are on. The list groups by
  repository, location, agent, status or last active (or not at all), sorts within the
  groups, has comfortable and compact rows (compact on its own for 150 sessions or more, or
  when the list is narrower than 600px), and its groups collapse (⌥-click for all). The
  Filter menu (⇧⌘F: status, location, agent, repository, last active, has; is / is not)
  shows as chips, beside a text filter (⌘F) and the Display popover (⌘J, also in the View
  menu); all of it but the text is kept in `[list]` and `[list.filter]`. The sidebar and
  the details panel can be resized and hidden (dividers, the title bar's buttons, the View
  menu: ⌃⌘S and ⌥⌘I, Ctrl+B and Ctrl+I on Windows), kept in `[window]`; on macOS the title
  bar is 52px and the sidebar button sits where AppKit puts its own.
- **One set of actions:** Resume in ‹place›, Show in ‹place›, Bring to this Mac…, Send to
  ‹machine›…, Continue with ‹agent›…, Hand off to ‹cloud›…. The details panel puts them in
  one row: the main action as a split button whose menu lists the other places (the one
  you pick becomes that agent's default, `[agents.<id>] place`), Move ▾ (an action that
  can't run now stays, with its reason) and ⋯ (Rename…, which writes the title into the
  agent's own data and can be undone, reveal the file, copy the resume command or id).
- **What a session is and where it is open:** the details panel shows the end of the
  conversation (the last two exchanges, a line for each turn's tool calls, Load earlier,
  Open transcript), read on its machine and never during a scan; markdown is rendered
  locally, raw HTML stays inert and images never load (Settings → General → Show
  conversation previews turns it off). Rows and the panel show where a session is open:
  hopsesh tabs, iTerm2, Terminal, Windows Terminal, an editor, tmux, ssh or the Claude app,
  from the agents' own registries and the process table every few seconds while the
  window is in front (Apple Events only when you click Show); two processes on one session
  say "Open twice". Titles never need a model: the title given, the name the Claude app
  gives, the agent's own, the first prompt or reply, else "Untitled · folder (branch)".
- **More in the app:** setup is one line on All sessions; the list refreshes this machine
  every minute while the window is in front; in the palette, Enter shows a session in the
  list and ⌘Enter runs its action; cloud sessions carry a cloud badge; "In the cloud" leaves
  out Remote Control mirrors, which run on their machine; added machines are scanned
  straight away, with Retry when a scan fails; Move offers only the clouds that are on.
- **Account profiles:** name and tag independent Claude Code and Codex accounts (Settings →
  Accounts, `a` in the terminal UI, `hopsesh accounts`, one store), find their folders here
  and on allowed machines, and sign in with the vendor's own command. A transfer plan names
  its destination profile, and plans refuse edited profiles or a login that changed. A
  return across profiles becomes a portable copy and leaves the original as it was; if both
  copies changed, hopsesh offers a separate branch and never merges. See
  [Accounts](docs/accounts.md).
- **Codex sessions open in the Codex app** with the exact thread selected (the Resume menu,
  `hopsesh open <session> --app`); an account in a custom folder opens in a terminal,
  because the app's links can't select it.
- Module SDK: `Previewer` (`preview`) and `Renamer` (`rename`), `PreviewText` and
  `BuildPreview`; `LiveInfo.Procs` lists every process that has a session open, and
  `LiveInfo.Name` the name the running agent gives it.
- A cloud hand-off's tab runs in the background and comes forward when Claude Code asks
  something or the step fails. Tabs whose program ended well close on their own (Settings →
  Terminal → Close a tab when its program ends).

### Changed

- **Upgrading from 0.3: add your machines again.** The configuration format is now schema 4,
  with `[clouds.<name>]` and `[terminal]`. A configuration file from 0.3 is refused, never
  converted: the app offers to set it aside and start fresh (the old file stays next to the
  new one as `config.toml.old-<date>`), and the command line names the file to move aside.
  Then add and allow your machines again. Your sessions are not affected. A configuration
  file written by a newer hopsesh is reported as newer: update hopsesh.
- Lineage manifests (the `.hopsesh.json` file beside each session hopsesh moved) have a new
  format (`lineage/4`) that records cloud copies, account profiles and forks. Manifests written by 0.3 are not read: a session
  moved with 0.3 is treated as if hopsesh had not moved it before. Update hopsesh on every
  machine where you run it, so they read each other's manifests.
- The peer protocol is now 4: a push between hopsesh 0.3 and 0.4 stops at hello and names
  the machine to update to 0.4.0 or later.
- The app now resumes sessions in its hopsesh Terminal window by default. To open them in
  your terminal app as before, choose **In my terminal** in Settings → Terminal, or pick
  the place from a session's Resume menu. The command line still runs sessions in your terminal.
- The macOS app's Automation permission text names your terminal app (iTerm2 or Terminal)
  and says hopsesh only opens tabs and brings a session's tab forward.
- The macOS app's self-update opens the new disk image with `diskutil image attach` on
  macOS 27, which deprecates `hdiutil attach`, and with `hdiutil` on earlier systems.

### Fixed

- Keep bounded transfers readable: quote original conversation records and compaction
  summaries, preserve message formatting, fence tool output and retain the current
  user request separately. Very small context allowances block conversion instead of
  silently dropping the request; full portable archives remain available.
- Use the same detected-agent names under local and remote machine sidebar rows,
  including installed agents with no sessions; keep connection failures explicit.
- Wait for native window readiness before changing desktop placement or focusing
  early reopen and Quick access requests, preventing a Windows WebView2 startup crash.

- Preserve transfer-review scroll position when options change, explain unavailable
  account emails without repeating profile labels, match the close-behavior explanation
  to the selected setting, and label missing journey origins instead of showing `/`.

- Make window-close behavior default to keeping Hopsesh running in the background,
  independently of menu-bar or tray placement on macOS, Windows and Linux. Preserve
  explicit Quit preferences and reopen the hidden window when launching Hopsesh again.
- Show selectable instruction files and text previews in transfer review, distinguish
  account profiles from verified identity, explain live-source snapshots and deferred
  moved labels, and place a remembered launch choice beside Continue. Single-account
  destinations display their identity without a redundant Default selector.

- Use the main window’s agent icons in Quick access. Put this machine first in
  Accounts and give remote setup notices the same collapsible machine grouping.

- Explain remote account setup prerequisites on the Accounts screen instead of
  silently omitting connected machines without a Hopsesh identity. Replace the
  sun-shaped Settings icon with a recognizable gear.

- Show pending actions, screen loading, retryable read failures, and terminal
  connection status throughout the GUI and Quick access. Keep previous sessions
  visible during refresh, lock pending saves, prevent repeated session launches,
  ignore obsolete navigation/plan responses, and stop reporting failed copies as
  successful. Add delayed/failing-response browser scenarios to the CI GUI matrix.

- Show a startup screen immediately while the bridge and first scan load. Report
  module/initialization failures with a reload action; keep shortcuts from operating
  on an uninitialized inventory and offer recovery when startup is unusually slow.

- Open the selected Claude Code session in Desktop instead of only activating the
  app. Use the supported CLI for saved sessions, with a temporary PTY and visible
  errors; focus a currently owned Desktop session only on a verified app build.
  Gate unsupported versions/platforms/profiles. Replace the tray arrows with the
  official Hopsesh arch and dots, rendered as a macOS template image.

- Avoid false divergence on portable returns when the first account observation rotates
  a profile binding. Verify the original's native history while keeping native append
  restrictions and independent-work conflict checks intact.

- Prevent oversized working contexts in Claude/Codex conversions, vendor imports, cloud
  returns and repeated round trips. Count final briefings and existing active context;
  preserve portable text in journaled archives with bounded retrieval. Full destinations
  can roll over to a new native session on the same logical branch. Recovery is available
  in the GUI, TUI and CLI; originals remain available. See [context safety](docs/context-safety.md).
- Keep Codex sessions with large first messages visible. Report imported history and
  preparation status accurately; enforce edited cloud briefing limits and record cloud
  return conversion losses.


- Windows UI validation waits for the actual main window and uses the remote-session
  continuation label. Screenshot failures now fail CI and retain their own reports.
- The install script (`curl … | sh`) failed on macOS in a terminal with a UTF-8 locale
  ("arch…: unbound variable"): macOS's /bin/sh read the "…" after `$arch` as part of the
  name.
- With hopsesh's settings or state in other folders (`HOPSESH_CONFIG_DIR`,
  `HOPSESH_STATE_DIR`), a session or step opened in iTerm2 or Terminal couldn't find its
  launch: the terminal app doesn't pass on hopsesh's environment. hopsesh now passes the
  folders to the tab itself.
- A Claude Code session resumed in a terminal that a Claude Code session had started
  (directly or through the terminal app) inherited its CLAUDE_CODE_CHILD_SESSION marker,
  and Claude Code then saved nothing of it. hopsesh now removes Claude Code's session
  markers before it resumes a session.
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
