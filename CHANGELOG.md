# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## Unreleased

### Fixed
- App: Back to sessions has one fixed upper-left toolbar location on every secondary
  screen and in the terminal window, including loading, errors and completed transfers.
  Late page loads no longer replace Sessions after returning. Resizing a terminal keeps
  its selected tab visible.
- Machines: added machines scan automatically, with queued/running status and a disabled
  per-machine Scan button. Failures explain what happened and offer Retry; successful
  rows refresh every five minutes while Machines is active.
- App: Move and the hand-off picker only offer enabled clouds. Other clouds can be set up
  in Machines; enabled destinations that are temporarily unavailable still say why.
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

### Added
- App: the end of the selected session's conversation (the last two exchanges, a line for
  each turn's tool calls, Load earlier, Open transcript), read on its machine and never
  during a scan. Markdown is rendered locally, with distinct user and agent cards and
  Show more for long messages. Raw HTML stays inert and images never load;
  Settings → General → Show conversation previews turns it off.
- App: where a session is open: its hopsesh tabs, iTerm2, Terminal, Windows Terminal, an
  editor, tmux, ssh or the Claude app, from the agents' own registries and the process table
  (no Apple Events: those run only when you click Show), every few seconds while the window
  is in front. Two processes on one session say "Open twice".
- App: ⋯ → Rename… gives a Claude Code or Codex session a title in the agent's own data (as
  `/rename` does); Activity undoes it. ⋯ also reveals the session's file and copies its
  resume command or id.
- Module SDK: `Previewer` (`preview`) and `Renamer` (`rename`), `PreviewText` and
  `BuildPreview`; `LiveInfo.Procs` lists every process that has a session open, and
  `LiveInfo.Name` the name the running agent gives it.
- Your terminal app: sessions, teleports and hand-off steps the app opens go to a new tab in
  iTerm2's front window when iTerm2 is installed (else a Terminal window; Windows Terminal on
  Windows), through a terminal-adapter layer whose only required verb is Open. Every launch
  runs only hopsesh's own `terminal-open <ticket>` (or `terminal-step <id>`), so what goes to
  the terminal app carries hopsesh's path and an id, never a title or a prompt. That verb
  labels an iTerm2 tab (a badge and `user.hopsesh_title`, `_agent`, `_machine` variables,
  sanitised and base64-encoded, wrapped for tmux), records the tab and the agent's process
  while it runs, and keeps the tab open with the exit code shown.
- Show a running session instead of a second copy: **Show in ‹terminal app›** in the app and
  `hopsesh open <session>` find the iTerm2 or Terminal tab from the agent's own process id
  (Claude Code's registry) or hopsesh's launch record (Codex) and the process table, and
  bring it forward. Resume refuses a session that already runs.
- `hopsesh open <session>` resumes a session on this machine in your terminal app
  (`--terminal <id>`, or `--here`); `hopsesh terminals` lists the terminal apps and sets
  `terminal.app` and `terminal.resume` (`--use`, `--resume`); `hopsesh pull --run
  --terminal <id>` starts the moved session in a new tab. The command line and the terminal
  UI label and record the sessions they run in place too.
- When macOS denies hopsesh control of iTerm2, the launch opens in Terminal and the app says
  so; when Terminal is denied too, you get the command to copy. hopsesh never turns on
  iTerm2's Python API, installs its Claude Code integration, writes profiles, or types into
  or reads a tab; the AppleScript it can run is a fixed list of lines, checked before each
  run. The macOS app's Automation permission text now names your terminal app, not only
  Terminal.
- **The hopsesh Terminal window**: the app now has a terminal of its own, a window with
  tabs. **Resume in hopsesh Terminal** runs a session in a tab there, and hand-offs to Claude
  Code cloud, bringing a session back, cloud sign-ins and **Open a shell in its folder** run there too.
  Each tab has a status chip (running, waiting for you, exited with its code), says what it
  runs and where, and keeps **Open in my terminal**, which ends the tab and runs the same
  command in your terminal app after asking. It uses xterm.js 6.0.0 (shipped inside the
  app, MIT), with copy, find, clear, links that open only after you confirm the whole
  address, Shift+Return for a new line in Claude Code, a screen reader mode, and colours
  that follow the app's light or dark look. Ctrl+` moves between the terminal and your
  sessions.
- In the hand-off's terminal step, a banner says when Claude Code asks whether it trusts
  hopsesh's hand-off folder: you answer it in the tab, and hopsesh never does. Bringing a
  session back says to send one message, and the done screen says when the tab ended
  before Claude Code saved a copy.
- Sign in from the app: the Claude Code cloud, Codex cloud and Copilot cards under
  **Machines** have **Sign in**, which runs `claude auth login`, `codex login
  --device-auth` or `gh auth login --web` in a tab that records nothing; when it ends well,
  the card checks the login again. Modules name their cloud's sign-in command
  (`Cloud.SignIn`) and the variables their programs get in the app's tabs
  (`Spec.TerminalEnv`; Claude Code gets `CLAUDE_CODE_FORCE_SYNC_OUTPUT=1`).
- The sessions list shows **In a tab** and **Waiting for you** for sessions running in the
  app's terminal, and they count in **Needs you**; the sidebar and the title bar show the
  terminal and how many tabs wait. A session runs in one tab at most: Resume shows its tab
  instead. On macOS a tab you can't see that waits for you raises a notification (hopsesh's
  own words, at most one every 10 seconds) and a Dock badge; on Windows the terminal's
  taskbar button flashes.
- Where your terminal app can say when a tab ends (iTerm2 with its Python API on), the app
  shows how a session or a hand-off step you opened there ended: "exited N" beside the
  session, or "closed" when the tab was closed first, and a notice.
- Quitting hopsesh while programs run in its tabs asks first and lists them. Closing the
  window hides it while programs run (**Keep tabs when the window closes**).
- **Settings → Terminal**: where sessions, hand-offs and bring-backs open (**In this
  window**, the default; **In my terminal**; **Ask each time**), your terminal app, font,
  size, scrollback (kept in memory only; 5,000 lines by default), keeping tabs when the
  window closes, notifications, the screen reader mode, on Windows the bundled console host,
  and what the terminal never does. The choices are saved under `[terminal]` in the
  configuration.
- On Windows a tab runs the program behind an npm command shim (such as `codex.cmd`) itself,
  node with the package's script or its own `.exe`, since tabs never run batch files. The
  installer and the app's update carry Microsoft's ConPTY (`conpty.dll` and
  `OpenConsole.exe`, MIT) in a `conpty` folder, which the tabs use instead of the older one
  built into Windows. Programs in tabs take input only from what you type and keep their
  output in memory only.
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
- `hopsesh followup <cloud>:<id> "<text>"` sends a cloud session a message, for a cloud
  whose command line can send one (Claude Code's and Codex's cannot; see Fixed).
- In the app: **Hand off ▸** on a session (every cloud, a disabled one with its reason),
  the hand-off sheet (the editable briefing, the branch, what stays on this Mac, the
  options), the steps as they run (and which one failed), and a done screen with the link
  and Undo; *Hand off to…* in the command palette; hand-offs in Activity. In the terminal
  UI: `c` hands the selected session off.
- The skill handles "hand this off to Claude Code cloud" (it plans first and asks), and its
  approval rules let `clouds --json` and `clouds test` run without asking while `handoff`
  and `followup` always ask.
- Four cloud-only agents: the GitHub Copilot cloud agent (through `gh agent-task` and
  `gh pr`), Jules (`jules remote`), Devin (`devin list --format json`) and Amp
  (`amp threads`). Once allowed, each lists its sessions in `hopsesh clouds`, `hopsesh ls
  --cloud` and the app's Clouds group, and `hopsesh clouds test` checks it read-only.
  Bringing one back brings its code into a new worktree (`hopsesh pull <cloud>:<id>
  --code-only`, or **Get the code** in the app): Copilot's and Devin's pull request branch,
  or Jules's patch committed on a `hopsesh/from/jules/<id>` branch; `hopsesh undo` takes it
  back. An Amp orb's code (`amp sync`) is not brought. These CLIs' output is mostly undocumented: hopsesh reads it defensively,
  and was tested against stand-ins only.

- Codex cloud, both ways. `hopsesh handoff <session> --to codex-cloud --env <environment>`
  starts a Codex cloud task with the briefing and the code on a branch (`codex cloud exec`),
  or with a few changes as a starting diff on a branch already pushed (`--starting-diff`);
  `--attempts` asks for several attempts. The plan lists the environments your recent tasks
  used and waits for your pick ("If you have none, open `codex cloud` once to create one");
  the one you pick is remembered for the repository, and `hopsesh clouds env codex-cloud`
  shows and sets them. `hopsesh ls codex-cloud:` lists tasks (`codex cloud list --json`,
  `--env` to filter). `hopsesh pull codex-cloud:<id>` brings a task back: its diff committed
  on `hopsesh/from/codex-cloud/<id>` in a new worktree, and the task's title and what came
  of it written as a new Codex session there (`--in claude` writes a Claude Code session);
  undo removes the session, the branch and the worktree. hopsesh drives only your own
  `codex`, signed in with ChatGPT (`codex login status`), never reads its login and never
  calls the ChatGPT backend. Only Codex cloud (legacy) tasks are reachable: the new Codex
  Cloud has no command line, and hopsesh says so.
- A cloud whose driver brings a conversation back as text (or a task's words) is written
  into a local agent of your choice through the same pieces as a continuation, beside the
  cloud's code.
- In the app: Codex cloud under Clouds, in **Hand off ▸** (with what hopsesh cannot reach
  there), the environment picker and the starting-diff choice in the hand-off sheet, a done
  screen with the task's link, a bring-back sheet and done screen for a task, and an
  environment per repository on its card under Machines. In the terminal UI: `e` picks the
  environment, `S` the starting diff. The skill handles "hand this off to Codex cloud".
- Hand a session off to the GitHub Copilot cloud agent, Jules, Devin or Amp:
  `hopsesh handoff <session> --to copilot-cloud|jules|devin|amp`, the same briefing and
  handoff branch as the other clouds, through each vendor's own CLI signed in as you.
  Copilot: `gh agent-task create -F - --base <handoff branch> -R <owner/repo>`, the briefing
  on standard input; the agent starts from the branch and opens its pull request against
  it. Jules: `jules remote new --repo <owner/repo> --session <briefing>`. Devin: `devin
  --cloud -p` (its documented non-interactive start) in a worktree on the handoff branch.
  Amp: `amp -ox <briefing> --project <owner/repo>` in a new orb thread. The jules, devin and
  amp commands can't name the branch a session starts from, so the briefing asks the cloud
  agent to check it out first, and the plan says so. Refusals (not signed in, no plan, a
  repository the cloud can't take) are said in words; after a refused start, undo removes
  the branch.
- Copilot's session log and Amp's thread come home as text: **Bring here** (or `hopsesh pull
  copilot-cloud:<id>` / `amp:<id>`) writes them as a new session of the agent the session was
  handed off from, else Claude Code (`--in codex`, or **Bring here into ▸** in the app,
  picks another), in a new worktree with Copilot's pull request branch. Fidelity is text:
  tool calls and their output come only as the log's words, and review comments, an orb's
  code and Jules's and Devin's messages stay in their clouds. **Get the code only** works as
  before.
- SDK: `Cloud.BriefBranch` declares a cloud whose driver cannot name the branch a session
  starts from (the core's briefing then asks the cloud agent to check it out), and
  `agenttest.CloudOptions.Work` plays the cloud's agent between the conformance kit's send
  and fetch, and `agent.LinksIn` reads a driver's links on an exact host only.
- Hand a cloud session on to another cloud, through this machine: `hopsesh handoff
  claude-cloud:<id> --to codex-cloud` (or `codex-cloud:<id> --to claude-cloud`, and any cloud
  whose sessions come back as a session to any cloud that takes a hand-off) brings it here
  first, as `hopsesh pull` does, keeps that copy, and hands it off from here. Codex cloud
  starts from Claude Code cloud's `claude/…` branch as it is when the copy is still exactly
  that; a Codex cloud task's diff is committed here and goes up on a handoff branch. The plan
  (`hopsesh plan <cloud>:<id> --to <cloud>`) shows both legs and what the trip loses, the
  copy's lineage records both hops, and one `hopsesh undo` takes both back, the hand-off
  first. In the app, **Hand off ▸** on a cloud session opens the same plan and waits while
  Claude Code's teleport runs in a terminal; in the terminal UI, `c` on a cloud row.
  `hopsesh clouds continue <hop>` takes on a hop whose first leg ran in another terminal.
- Branch clean-up after merge: `hopsesh clouds cleanup` lists the handoff branches hopsesh
  pushed and the clouds' own branches it brought home, asks each remote (read-only) whether
  their work is in its default branch (by history, through the branch brought here, or a
  merged pull request where `gh` is installed), and deletes the merged ones only when you
  confirm, each only while it is where hopsesh saw it. Undo pushes them back. In the app:
  **Activity → Look for merged branches**, and a pointer to it on the bring-back's done
  screen. `delete_branch = "on-undo"` and `"never"` keep them.
- A cloud's text or task summary can be added to the session it was handed off from, when
  that session is as it was left (`hopsesh pull <cloud>:<id> --append`): a delta append, so
  the session's own turns stay byte for byte, and undo cuts the addition off again.
- `agent.LinkOn` reads a link the user pasted on an exact host only (https, no user info,
  no port).

- An opt-in client for iTerm2's Python API (`internal/core/termapp/iterm2api`), used only
  when you have enabled the API in iTerm2 yourself: session-terminated, new-session and
  focus events (with reconnection, and catching up on tabs that closed while disconnected),
  opening a tab or a split beside a session with a command and folder, finding a session
  by its TTY and focusing it, and `user.hopsesh_*` labels. A credential-free probe comes
  first, so nobody with the API off is prompted; the cookie comes from AppleScript and stays
  in memory. The client is hand-written (iTerm2's protocol file is GPLv2) and can send seven
  requests only: no typing, injecting or screen, buffer, selection or prompt reading, which
  a test enforces. With the API on, iTerm2 hand-off steps open in a split beside the
  session in front, the app stops waiting as soon as a step's tab is closed, and finding and
  focusing a session's tab go through the API; the exit code always comes from hopsesh's
  own records (`--hold` keeps the tab open after the agent ends). Any API error falls back
  to AppleScript, and a refusal is never asked again. `scripts/iterm-api-smoke.sh` checks it
  against a real iTerm2 by hand.
- `hopsesh agents --json` includes each module's spec as data (`spec`): its binaries, data
  folders, secrets it never opens, instruction files, features, desktop apps, and its clouds
  with their fidelity, needs and upstream watch lists.
- Each release has a test bundle, `hopsesh-testbundle-<version>.tar.gz` (in `checksums.txt`,
  with build provenance): the stand-in agents for six platforms, the agents' fixtures, the
  modules' specs, and scrubbed payloads other projects contribute, so a project that ships
  hopsesh can test against exactly that version. See `testbundle/README.md`.

### Fixed
- App: the hopsesh Terminal window has no separate title bar on macOS (its tabs sit beside
  the window buttons and drag the window), and many tabs shrink, then scroll, without
  covering "+" or Back to sessions.
- App: the details panel follows the list (a session the list no longer shows goes), the
  Hand off menu closes on Escape, a click elsewhere or a choice, row buttons keep their
  labels inside, sizes no longer break between number and unit, and paths break at slashes.
- App: a session open in the Claude app offers Show the Claude app instead of a terminal tab
  it doesn't have; a terminal tab hopsesh can't find says so.
- App: a session brought from Claude Code cloud is never resumed twice: while the tab that
  brought it runs it, Resume shows that tab, and the done screen offers the list's own
  actions (the agent's app too) once it ends. Nothing in the bring-back asks for `/exit`.
- App: "In the cloud" and each cloud's list leave out Remote Control mirrors (they run on
  their machine); cloud ids no longer show where people read (marks say "continued in
  Claude Code cloud").
- Codex cloud: when codex lists no environment, hopsesh says why: environments made in
  today's Codex cloud can't be used by the codex command yet.

- Bringing a session from Claude Code cloud adopts the copy the real Claude Code writes.
  Claude Code 2.1.289's teleport saves nothing until you send a message in the teleported
  session, then writes a new session with the cloud's conversation and a "continued from
  another machine" record, with no `teleported-from` record and no message count. hopsesh
  now finds the copy by that record (and still by a `teleported-from` record where one
  appears), counts only the cloud's messages, checks the copy against the briefing hopsesh
  sent when hopsesh started that cloud session, and otherwise says how many messages came
  with nothing to check them against, instead of calling it complete. Every bring-back plan
  and done screen (command line, terminal UI and app) says that Claude Code saves its copy
  only after you send a message in it.
- Codex cloud: the new task's link is read on chatgpt.com only. A link to any other host
  that ended in `/tasks/task_…` was taken for the task's. Pasted links of Claude Code,
  Codex, Amp and Devin sessions, and Devin's pull request links, are read on their exact
  hosts too.
- `delete_branch = "never"` now keeps the handoff branch through undo; undo deleted it
  whatever the setting said.
- Two operations started in the same millisecond could share a journal, so one's undo record
  overwrote the other's.
- Handing a session off to Claude Code cloud works with the real Claude Code. hopsesh ran
  `claude -p <briefing> --cloud --output-format json` through pipes, which Claude Code
  2.1.28x refuses ("--cloud cannot be combined with --print", and without a terminal
  "--cloud requires an interactive terminal"). The hand-off now runs `claude --cloud
  "<briefing>"` in your terminal, after the snapshot and the push, and reads the session's
  link Claude Code prints (`View: https://claude.ai/code/session_…`); the link and id go
  into the journal and the lineage as before. Claude Code may first ask whether you trust
  the folder: you answer it (hopsesh never does, and never changes Claude Code's settings to
  skip it). The folder is hopsesh's hand-off folder for the repository, the same each time
  (a worktree of your checkout, reset to the handoff branch for each hand-off, one hand-off
  at a time), so Claude Code asks once per repository; your checkout stays as it is. If
  hopsesh sees no link (you said no, or Claude Code stopped), it asks you to paste it, or
  stops at "Start cloud session" with a message that says what happened, and undo removes
  the pushed branch. The command line relays the terminal (with `--json`, on standard
  error); the terminal UI hands the terminal over for the step and comes back afterwards
  instead of quitting; the app opens a terminal window for the step, shows what happens
  there, waits for the link and also takes a pasted one. Without a terminal (an agent
  running hopsesh), the plan says you have to run the hand-off yourself.
- "Send a follow-up" to a Claude Code cloud session is gone: Claude Code has no command that
  sends one outside its own terminal session (its `-p … --cloud <id>` form is refused like
  the hand-off's was). The app's done screen, the command line and the skill say so and
  point to the session's page instead.
- `scripts/cloud-smoke.sh`: a hand-off or a bring-back that failed after it pushed a branch
  now leaves the journal for the cleanup, which undoes it (the branch had to be deleted by
  hand). The hand-off check runs `claude --cloud` in your terminal (answer its trust
  question; the script goes on when it exits) and no longer sends a follow-up.
- Reading a Windows machine with many sessions could stall for 30 seconds and then fail:
  the long script hopsesh sent there over standard input sometimes never arrived, because
  PowerShell with redirected input can read it first. Such a script is now uploaded over
  SFTP, run from the file and removed.
- One session folder that git could not read quickly, such as a folder in iCloud Drive
  whose files are not downloaded, made every scan wait for it, and with it `hopsesh ls`,
  `plan`, `pull` and `handoff`, sometimes for minutes. Each git call hopsesh makes to read a
  folder now gets 20 seconds to answer (30 seconds on another machine over SSH), on macOS,
  Linux and Windows; a folder that is slow but answers is still read in full. A folder
  where git does not answer in time is reported as such, not as one without a repository:
  moving or handing off that session stops and says why, and the other sessions are not
  affected. `pull`, `plan`, `handoff`, `push` and `show` now ask git only about the folders
  of the session they name.
- On Windows, a `claude.cmd` (or another npm or pnpm command shim) now runs as the program
  behind it in the command line's and terminal UI's terminal steps, launches in a terminal
  app or this terminal, and `pull --run`, as it already did in the app's tabs.
- A configuration file written by a newer hopsesh is reported as newer, not older: update
  hopsesh, or set it aside as a confirmed second choice (the app no longer offers to set it
  aside as outdated).

### Changed
- App: the Sessions screen is redesigned. The sidebar lists places (Needs you, All sessions,
  This Mac and your other machines, the clouds that are on); agents and "In the cloud"
  became filters. The list groups by repository, location, agent, status or last active (or
  not at all), sorts within the groups, has comfortable and compact rows (compact by itself
  for 150 sessions or more, and whenever the list is narrower than 600px), and its groups
  collapse (⌥-click for all), each choice kept. Live only and All agents gave way to the
  Filter menu (⇧⌘F: status, location, agent, repository, last active, has; is / is not),
  shown as chips, a text filter (⌘F) and the Display popover (⌘J, and the View menu). It
  is all kept in `[list]` and `[list.filter]`, except the text.
- App: one action vocabulary: Resume in ‹place›, Show in ‹place›, Bring to this Mac…, Send
  to ‹machine›…, Continue with ‹agent›…, Hand off to ‹cloud›…. The inspector's actions are
  one row: the primary as a split button whose menu lists the other places (the one you
  pick becomes that agent's default, `[agents.<id>] place`), Move ▾ (unavailable items stay,
  with their reason) and ⋯. "Ask each time" is gone from Settings → Terminal; Where sessions
  open is where they first resume.
- App: the inspector shows where it runs in its status line, a facts line, where the
  session is open (each place with Show), and collapsible Repository, Copies & history and
  Details sections (kept app-wide in `[inspector]`). It is 300 to 960px wide; by default 30%
  of the window beside the sidebar.
- App: titles never need a model: the title given, the name the running Claude app gives,
  the agent's own, the first prompt or reply, else "Untitled · folder (branch)".
- App on macOS: the title bar is 52px, as tall as AppKit's own, and the sidebar button sits
  where AppKit puts its own (17px clear of the window's buttons), in the Terminal window too.
- App: the sidebar and the inspector can be resized and hidden (dividers, the title bar's
  buttons, the new View menu: ⌃⌘S and ⌥⌘I, Ctrl+B and Ctrl+I on Windows); the layout is
  kept in `[window]`. Below 1000px the sidebar hides for now.
- App: the sidebar lists the clouds that are on (Turn on a cloud… opens Machines) and the
  agents with sessions; setup is one line on All sessions instead of three cards.
- App: the list refreshes this machine every minute while the window is in front, and
  everything when it comes back after five minutes.
- App: in the palette, Enter shows a session in the list (⌘Enter runs its action); rows
  show the repository, a cloud badge on cloud sessions, and a title for untitled ones.
- App: a cloud hand-off's step runs in the background and comes forward when it asks
  something or fails; tabs whose program ended well close (Settings → Terminal → Close a
  tab when its program ends, `keep_ended` in `[terminal]`).
- App: wording: "Turn on" for clouds throughout, "hand-off", "Colors", "The hopsesh
  command", "Sign in using …", ⌃` on macOS; Codex cloud is marked experimental.

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
- The README presents cloud sessions as working with the vendors' clouds (one "Cloud
  sessions" section: what goes up, what comes back from each cloud, Claude Code's terminal
  steps, cloud to cloud, undo and clean-up, privacy), and docs/design.md lists cloud
  sessions as built.
- The hand-off's branch choice reads "Offer to delete it once its work is merged (and on
  undo)", "Delete it only if I undo" and "Keep it, even on undo".
- The peer protocol is now 2 (lineage and cloud fields changed): a push between hopsesh 0.3
  and 0.4 stops at hello and names the machine to update to 0.4.0 or later.
- `hopsesh followup` is hidden (help, completions, the skill) and says that no cloud accepts
  a follow-up yet; the app shows no follow-up button.

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
