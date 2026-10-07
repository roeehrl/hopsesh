<div align="center">

<img src="docs/assets/logo.svg" width="96" height="96" alt="hopsesh logo">

# hopsesh

**Move coding-agent sessions between your machines, between Claude Code and Codex, and to and from their clouds.**

[![Release](https://img.shields.io/github/v/release/roeehrl/hopsesh)](https://github.com/roeehrl/hopsesh/releases/latest)
[![CI](https://github.com/roeehrl/hopsesh/actions/workflows/ci.yml/badge.svg)](https://github.com/roeehrl/hopsesh/actions/workflows/ci.yml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/roeehrl/hopsesh/badge)](https://scorecard.dev/viewer/?uri=github.com/roeehrl/hopsesh)
[![Go Reference](https://pkg.go.dev/badge/github.com/roeehrl/hopsesh.svg)](https://pkg.go.dev/github.com/roeehrl/hopsesh)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
![macOS · Linux · Windows](https://img.shields.io/badge/platforms-macOS%20%C2%B7%20Linux%20%C2%B7%20Windows-0b6b62)

[Install](#install) · [Quick start](#quick-start) · [Another agent](#continue-in-another-agent) · [Round trips](#round-trips) · [Push](#send-a-session-to-another-machine) · [Cloud sessions](#cloud-sessions) · [Ask your agent](#use-it-from-your-agent) · [Why not…?](#why-not) · [FAQ](#faq)

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/hero-dark.gif">
  <img src="docs/assets/hero-light.gif" alt="The hopsesh macOS app lists Claude Code and Codex sessions from two machines grouped by repository; a Claude Code session is continued in Codex after a plan shows what carries over, what changes and what's left out" width="880">
</picture>

<sub>The macOS app, recorded with made-up machines and sessions (<a href="demo/README.md">demo/</a>).</sub>

</div>

You started something in Claude Code on your desktop, and now you're on your laptop, or you
want Codex to take over. hopsesh shows every session of every agent on all your machines and
brings the one you pick here: in the same agent, or continued in the other one, with the
repository, worktree and paths fixed up. Then it gives you the command to continue.

- **Finds your machines** from Tailscale and `~/.ssh/config`, and connects only to the ones you
  allow, with your own `ssh`, keys and agent. Nothing to install on the other machines.
  Machines without SSH keys can log in with a password.
- **Lists every session of every agent** (Claude Code and Codex) by repository: machine, agent,
  path, branch, worktree, last prompt, last activity, whether it's running, and where else
  it has been.
- **Moves a session safely**: finds the repo here or clones it, recreates the worktree, brings
  the code to the session's commit, rewrites paths, and shows a plan first. `hopsesh undo`
  reverses it.
- **Continues it in another agent**: `--in codex` or `--in claude`, on another machine or this
  one. The plan shows exactly what carries over and what doesn't, plus the briefing the other
  agent gets.
- **Tracks round trips and forks**: verified compatible returns can add only new work.
  Across account profiles, Hopsesh creates a portable conversation and preserves the original
  protected state. If both copies changed, it offers a separate branch; it never silently merges.
- **Works with the agents' clouds**: hands a session to Claude Code on the web, Codex cloud,
  Copilot, Jules, Devin or Amp, brings their sessions home, and hands one cloud's session on
  to another, all through the vendors' own commands, signed in as you.
- **Works from inside your agent**: ask Claude Code or Codex "bring my laptop session here" and
  it plans with hopsesh, then moves only after you say yes.
- **Multiple accounts**: name and tag independent Claude Code and Codex profiles, discover
  known roots locally and on allowed machines, and review account-to-account transfers.
  See [Accounts](docs/accounts.md) for setup and vendor limitations.
- **Open a Codex conversation in its desktop app** from the Resume menu, with the exact
  thread selected. Custom account roots use a terminal because desktop links cannot select them.
- **CLI, TUI and a desktop app for macOS and Windows** on one engine, with `--json` output
  everywhere it matters. The apps update themselves.

> Status: alpha. It works end to end on macOS and Linux, and the Windows app and installer are
> tested in CI on Windows Server 2025, including the real window. hopsesh is an independent
> project, not affiliated with Anthropic or OpenAI.

## Install

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/roeehrl/hopsesh/main/scripts/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/roeehrl/hopsesh/main/scripts/install.ps1 | iex
```

Both check the download against the release's `checksums.txt` and its signature, and
`hopsesh update` installs later releases with the same checks.

- **macOS app:** [download the signed, notarized `.dmg`](https://github.com/roeehrl/hopsesh/releases/latest/download/hopsesh-macos-universal.dmg)
  (macOS 13+, Apple silicon and Intel).
- **Windows app:** [download the installer](https://github.com/roeehrl/hopsesh/releases/latest/download/hopsesh-windows-amd64-setup.exe)
  (Windows 10/11; [arm64](https://github.com/roeehrl/hopsesh/releases/latest/download/hopsesh-windows-arm64-setup.exe)).
  It installs for your user only, no administrator rights, and adds the `hopsesh` command to
  your PATH. The installer isn't code-signed yet, so Windows SmartScreen says "Windows
  protected your PC": click **More info**, then **Run anyway**. A portable `.zip` of the app
  is on the [release page](https://github.com/roeehrl/hopsesh/releases/latest).
  On Windows 11 with **Smart App Control** turned on (mostly on new PCs), Windows blocks
  programs that aren't code-signed, with no "Run anyway" option. hopsesh isn't code-signed
  yet, so it won't run on those PCs until it is.
- **Linux packages:** `.deb`, `.rpm` and `.apk` for amd64 and arm64 are on the
  [release page](https://github.com/roeehrl/hopsesh/releases/latest), for example
  `sudo apt install ./hopsesh_<version>_linux_amd64.deb`.
- **From source** (Go 1.26+): `go install github.com/roeehrl/hopsesh/cmd/hopsesh@latest`

The other machines need only an SSH server (Remote Login on macOS, OpenSSH on Windows) and
their sessions. To continue in another agent, that agent must be installed here.

## Quick start

```sh
hopsesh hosts                 # machines found in Tailscale and ~/.ssh/config (connects to nothing)
hopsesh hosts allow studio    # let hopsesh connect to one
hopsesh trust studio          # confirm its SSH host key
hopsesh                       # browse sessions and move one here
```

<img src="docs/assets/demo.gif" alt="The hopsesh terminal UI lists Claude Code and Codex sessions on two machines grouped by repository, then brings a Claude Code session to this machine and prints the resume command" width="880">

<sub>Recorded with <a href="https://github.com/charmbracelet/vhs">VHS</a> from <a href="demo/hopsesh.tape">demo/hopsesh.tape</a>.</sub>

A session is named `[<machine>:][<agent>/]<id-or-title>`. Leave out the machine to take the
newest copy anywhere, and leave out the agent when the title or id is unambiguous.

```sh
hopsesh agents                                 # supported agents and what's installed here
hopsesh ls --agent codex --json                # sessions of one agent, grouped by repo
hopsesh plan studio:"fix flaky tests"          # read-only preview of a move
hopsesh pull studio:"fix flaky tests"          # plan, confirm, move, print the command
hopsesh pull studio:claude/7f3c2a1e --in codex # continue a Claude Code session in Codex
hopsesh pull 7f3c2a1e                          # no machine name: bring back the newest copy
hopsesh push 7f3c2a1e laptop                   # send it to a machine that runs hopsesh
hopsesh hosts add nas alice@192.168.1.20 --password   # a machine without SSH keys
hopsesh doctor studio                          # agents, SSH, host trust and the skill
hopsesh runtime observe                        # passive local snapshot; no state changes
hopsesh runtime observe --watch                # watch changes as JSON lines, without a GUI
hopsesh undo                                   # undo the newest move (--list shows more)
```

### The app

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/app-dark.png">
  <img src="docs/assets/app-light.png" alt="The hopsesh app listing Claude Code and Codex sessions from two machines, grouped by repository, with live state and the last prompt" width="900">
</picture>

Same engine, with a plan sheet for each move: repository, worktree mode, what gets
rewritten, and what's left behind on the other machine. Sessions are grouped by repository,
machine, agent, status or age, with filters you can see as chips. Each session has one row of
actions: **Resume in ‹place›** (or **Show in ‹place›** while it runs, or **Bring to this
Mac…**), **Move** (**Continue with ‹agent›…** with a preview of the conversion, **Send to
‹machine›…**, **Hand off to ‹cloud›…**) and **⋯**, beside the end of its conversation and
where it is open now. Sending plans on the other machine, carries it out, and one Undo
reverses both sides. Settings has per-agent options and a **Receive sessions**
switch. It can also put the `hopsesh` command on your PATH (no administrator password) and
install the skill into your agents.

The app is the same on macOS and Windows; on Windows it says "this PC", uses Ctrl where macOS
uses ⌘, opens sessions in Windows Terminal (or PowerShell) and keeps passwords in Windows
Credential Manager. It updates itself: **Settings → Updates → Install and restart** checks
the release's signature and checksum (on macOS also the Apple developer and notarization),
installs it and reopens.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/windows-app-dark.png">
  <img src="docs/assets/windows-app-light.png" alt="The hopsesh app on Windows listing Claude Code and Codex sessions from this PC and from studio, a Linux machine, grouped by repository" width="900">
</picture>

### The hopsesh Terminal

The app has a terminal of its own: a **hopsesh Terminal** window with tabs. **Resume in
hopsesh Terminal** runs a session there, and so do the steps Claude Code needs a terminal for
(a hand-off to its cloud, bringing a session back), cloud sign-ins and **Open a shell in its
folder**. hopsesh starts the
command; you do all the typing, and it never answers a question for you, Claude Code's trust
question included. Each tab shows whether its program runs, waits for you or ended (with its
exit code), and keeps **Open in my terminal**, which ends it and runs the same command in your
own terminal app. Ctrl+` moves between the terminal and your sessions. Quitting hopsesh ends
the programs in its tabs, so it asks first; for work that must outlive the app, use your
terminal app.

Settings → Terminal chooses where sessions and steps open (**In this window** or **In my
terminal**; a session's Resume menu picks another place, remembered for that agent), the font, its size and how much scrollback each tab keeps
(in memory only). Programs in a tab can never read your clipboard, links open only after you
confirm the whole address, and nothing a tab shows is written to disk or a log. Sign-in and
shell tabs are recorded nowhere: hopsesh doesn't watch, match or keep what appears in them.

### Your terminal app

Sessions, teleports and hand-off steps you open outside the app go to your own terminal
app: on macOS a new tab in iTerm2's front window when iTerm2 is installed (else a Terminal
window), on Windows Windows Terminal. `hopsesh open <session>` does the same from the command line (`--here` runs it in
this terminal), and `hopsesh terminals` lists the apps and picks one (`--use terminal-app`).
In iTerm2 the tab gets a badge with the session's title, agent and machine (and
`user.hopsesh_*` variables for your own title or status bar); hopsesh leaves the tab's
title to the agent.

A session that's already running is shown, not opened twice: the app's **Show in iTerm2**
or **Show in Terminal** and `hopsesh open` bring its tab forward. hopsesh finds the tab
from the agent's own process and the terminal it runs on (for Codex, from hopsesh's record of
the launch), never from anything the tab shows. It never types into a tab or reads one, and
never changes iTerm2's settings: it doesn't turn on the Python API or install iTerm2's
Claude Code integration.

## Continue in another agent

```sh
hopsesh plan studio:"fix flaky tests" --in codex   # see what carries over, change nothing
hopsesh pull studio:"fix flaky tests" --in codex   # then do it
```

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/plan-dark.png">
  <img src="docs/assets/plan-light.png" alt="The plan for continuing a Claude Code session in Codex: carried over, changed and left out, the briefing Codex gets, and how the repository and code will be matched" width="900">
</picture>

hopsesh converts the conversation into the other agent's own session format, on another
machine or this one:

- **`--fidelity history`** (the default) brings the conversation as text. Tool activity is
  labelled as the previous agent's, long outputs are shortened, and the oldest steps are
  summarised when the history is long.
- **`--fidelity note`** brings only a briefing.
- **`--native`** (experimental) replays exact shell calls as the target agent's own, where it
  can.
- **`--via import`** lets Codex's own importer convert a Claude Code session; hopsesh adds its
  briefing and keeps it undoable.

Every plan shows a **loss report** (what was kept, what was shortened or summarised, which paths
were mapped; reasoning from the other vendor is never carried). It also shows the **briefing**
the other agent gets: where the session came from, what to verify first (git status, the
commit at transfer time), the open plan, and instruction files only the previous agent read.
`--note-file` adds your own handoff note, `--carry-rules` adds your instructions for every
project, and `--go` starts the session with "Continue." Nothing is sent to a model unless you
start it. Details: [docs/design.md §8](docs/design.md#8-continuing-in-another-agent).


## Round trips

Move a session to your laptop, or into Codex, work on it, and bring it back later:

- **The copy left behind is marked** in its agent's own list: `↪ moved to <machine> · <title>`
  or `↪ continued in Codex on <machine> · <title>`.
- **One row per session.** Listings combine the copies of a session across machines and
  agents. `hopsesh pull <id-or-title>` without a machine name brings back the newest one,
  into the folder it came from.
- **Coming back to the original agent adds only the new work.** The original session's own
  turns, including Claude Code's signed reasoning, stay byte for byte.
- **It never merges.** If both copies changed, the move stops until you choose `--keep-both`
  (a separate session) or `--replace` (the copy here is set aside; `hopsesh undo` brings it
  back).
- **The code follows.** hopsesh fetches the session's commit, straight from the other machine
  if it was never pushed, and fast-forwards a clean checkout. It never merges, rebases or
  stashes; otherwise it says exactly what's missing.
- **The history travels with the session.** A small `<session>.hopsesh.json` beside it records
  its copies and hops, so the next move works from any machine.

Details: [docs/design.md §10](docs/design.md#10-round-trips-and-lineage) and
[§12](docs/design.md#12-code-follows-the-conversation).

## Send a session to another machine

`pull` needs only SSH on the other machine. When that machine runs hopsesh too, you can also
send a session from here:

```sh
hopsesh receive on               # on the machine that receives (off by default)
hopsesh push 7f3c2a1e laptop     # on the machine that has the session
```

The receiver plans with its own agents and settings against a read-only copy of that
session's files; it can't read or run anything else on the sender. Nothing changes until you
confirm, and `hopsesh undo` on the sender reverses both machines. Both sides must speak the same
hopsesh protocol version; otherwise hopsesh asks you to update. Details:
[docs/design.md §11](docs/design.md#11-peers-working-with-hopsesh-on-the-other-machine).

## Cloud sessions

hopsesh works with the agents' own clouds: Claude Code on the web, Codex cloud, the GitHub
Copilot cloud agent, Jules, Devin and Amp. It hands a session on this machine (or on another
of yours) to a cloud, brings a cloud session home into Claude Code or Codex, and hands one
cloud's session on to another vendor's cloud through this machine. It does this with the
vendors' own commands (`claude --cloud`, `claude --teleport`, `codex cloud`, `gh agent-task`,
`jules remote`, `devin`, `amp`), run here, signed in as you: the cloud sessions it starts
are yours, on your plan, and hopsesh never reads a login or calls a vendor's servers. Each
cloud is off until you allow it.

```sh
hopsesh clouds                                        # the clouds, and what a read-only look found
hopsesh clouds allow claude-cloud                     # each is off until you allow it
hopsesh clouds test claude-cloud                      # read-only: the login and the commands hopsesh uses
hopsesh plan claude/<id> --to codex-cloud --env acme-api   # what goes and what stays; changes nothing
hopsesh handoff claude/<id> --to codex-cloud --env acme-api
hopsesh ls --cloud                                    # cloud sessions hopsesh knows of
hopsesh pull claude-cloud:session_01… --run           # bring one home (or paste its link); --in codex
hopsesh handoff claude-cloud:session_01… --to codex-cloud --env acme-api   # cloud to cloud
hopsesh clouds cleanup                                # branches whose work is merged, deleted when you say so
```

In the app: **Clouds** in the sidebar, **Move → Hand off to ‹cloud›…** on a session (on a
cloud session too), **Bring to this Mac…** on a cloud session, and the cards under
**Machines**. In the terminal UI, `c`
hands off and `enter` brings a cloud session here.

### What goes up: a briefing and the code

No vendor's cloud takes a conversation, so a cloud session starts from a **briefing**: about
2,000 tokens on what was done, what is open and your latest requests, written by hopsesh
(never by a model), with likely secrets masked. Tool calls, tool output and hidden reasoning
stay here. The plan shows the briefing, and you can edit it.

The code goes on a branch the cloud clones. A branch that is clean and already on GitHub
goes as it is (so does a cloud's own branch that a session was brought home on). Otherwise
hopsesh pushes a `hopsesh/handoff/<date>-<id>` branch with a snapshot of the unpushed commits
and changed files, without touching your checkout, index or branch. Codex cloud can take a
few changes on an already pushed branch as a starting diff instead (`--starting-diff`), and
Claude Code can take a repository that isn't on GitHub as its own upload (`--bundle`; the
cloud then can't push its work back). Jules, Devin and Amp can't be told which branch to
start from, so their briefing asks the cloud agent to check it out first.

### What comes back

| Cloud | Through | The conversation comes back | The code comes back |
|---|---|---|---|
| Claude Code cloud | `claude --teleport`, in your terminal | Whole, as Claude Code's own copy | Its `claude/…` branch, kept here as `hopsesh/from/claude-cloud/…` |
| Codex cloud (legacy tasks) | `codex cloud status`, `codex cloud diff` | The task's title and what came of it, written as a session | Its diff, committed on `hopsesh/from/codex-cloud/<id>` |
| GitHub Copilot cloud agent | `gh agent-task view --log` | Its session log, as text | Its pull request's branch |
| Amp | `amp threads markdown` | Its thread, as text | Stays in the orb |
| Jules | `jules remote pull` | Stays in Jules | Its patch, committed on `hopsesh/from/jules/<id>` |
| Devin | `devin list` | Stays in Devin | Its pull request's branch |

The code always comes into a new worktree beside your checkout, which stays as it is. Text
is written as a new session of the agent it was handed off from (else Claude Code; `--in
codex` picks Codex), or, with `--append`, added to the session it was handed off from when
that is as you left it. Only older Codex cloud environments work: the codex command can't
use environments made in today's Codex cloud (chatgpt.com) yet. Codex cloud runs every task
in an **environment** you made on the web: name it with `--env`; the plan suggests the ones
your recent tasks used, and hopsesh remembers your pick for the repository (`hopsesh clouds
env codex-cloud`).

### Claude Code's steps in your terminal

Claude Code starts a cloud session, and copies one home, only in a terminal you can answer,
so both steps run in one you see: a tab of the app's hopsesh Terminal window (or your own
terminal app, if you chose it in Settings → Terminal), or the terminal UI's own terminal,
which it hands over and comes back to. Starting one runs `claude --cloud "<briefing>"` in hopsesh's
hand-off folder for the repository. The first time, Claude Code asks whether you trust that
folder: you answer it, once per repository, and hopsesh never answers it or changes Claude
Code's settings to skip it. hopsesh only reads the session's link Claude Code prints, and asks
you to paste it if it sees none. Bringing one home runs `claude --teleport <id>` in a new
worktree. **Claude Code saves its copy only after you send a message in it**: send one (even
"ok") and hopsesh picks the copy up; keep working in it if you like. It checks the copy against the
briefing hopsesh sent, when hopsesh started that cloud session; otherwise it says how many
messages came, since Claude Code gives no count to check them against.

### Cloud to cloud

A cloud session can go on to another vendor's cloud. No cloud hands one to another, so
hopsesh brings it here first (the copy stays here, whole for Claude Code), then hands that
copy off: `hopsesh handoff claude-cloud:<id> --to codex-cloud` starts the Codex cloud task
from the `claude/…` branch as it is, and `hopsesh handoff codex-cloud:<id> --to claude-cloud`
commits the task's diff here and pushes it on a handoff branch for Claude Code. The plan
shows both legs and what the trip loses; the copy's lineage records both; one undo takes
both back.

### Undo and branch clean-up

`hopsesh undo` takes back what a hand-off did here: the handoff branch (only while the cloud
hasn't pushed to it, and not when you chose to keep it) and the mark on the session. The
cloud session itself stays in the vendor's list, for you to archive there: no vendor's
command line can archive one. Undoing a bring-back removes the worktree, the copy and the
branches it made. Once a hand-off's work is merged into the default branch,
`hopsesh clouds cleanup` (or **Activity → Look for merged branches** in the app) offers to
delete the handoff branch and the cloud's own `claude/…` or `copilot/…` branch: it asks the
remote, read-only, and deletes only what you confirm, each only while it is where hopsesh
saw it. Undo pushes them back. `delete_branch = "never"` or `"on-undo"` under
`[clouds.<name>]` keeps them.

### Privacy

- A cloud gets the briefing and the branch, nothing else. Credential-like files (`.env*`,
  `*.tfvars`, `id_rsa*` and other SSH keys, `*.pem`, `*.key`, `*.p12`, `.npmrc`), Git LFS
  files and files over 50 MB never go, whatever you pick. Untracked files go only when you
  name them (`--untracked docs/plan.md`).
- The conversation itself goes on the branch only if you ask (`--history-file`, which
  commits `.hopsesh/handoff.md`): anyone who can see the branch can read it.
- Secret masking is best effort, not a guarantee; the plan shows what it masked.
- What a cloud session holds is kept under that vendor's terms. Most of these commands'
  output is undocumented: hopsesh reads it defensively, and has been tested against
  stand-ins of them and by hand.

## Use it from your agent

```sh
hopsesh skill install              # your agent asks before each hopsesh command
hopsesh skill install --add-rules  # read-only hopsesh commands run without asking
```

The skill goes into every installed agent (Claude Code and Codex) as identical files. Then ask
things like "bring my laptop session here", "continue this in Codex" or "which sessions are
running on the studio?". The agent plans first and **moves only after you say yes**.
`--add-rules` lets the read-only commands (`ls`, `show`, `plan`, `agents`, `clouds`, `doctor`, …)
run without asking, while `pull`, `push`, `handoff`, `followup` and `undo` **always ask**, even
in auto mode. "Hand this off to Claude Code cloud" (or to Codex cloud, Copilot, Jules, Devin
or Amp) works the same way: the agent shows the plan and what stays behind, asks you for the
environment Codex cloud needs, then hands it off after your yes (to Claude Code cloud, it
gives you the command to run in your own terminal, where Claude Code starts the session). The skill
never starts another agent and never handles passwords: for a machine that logs in with a
password, the agent asks you to run `hopsesh hosts setup-key` yourself. `hopsesh skill remove`
takes it out again. Details: [docs/design.md §13](docs/design.md#13-the-hopsesh-skill-for-every-agent)
and [§14](docs/design.md#14-the-command-line-tool-from-the-app).

## How it works

1. **Discovery** reads `tailscale status --json` and your `~/.ssh/config`. It connects to
   nothing until you allow a machine.
2. **Listing** connects with your system `ssh` (strict host-key checking, ControlMaster) and
   reads the head and tail of each transcript over SFTP. One batched `git` probe per machine
   adds branch, worktree and unpushed/uncommitted counts.
3. **Moving** builds a plan, then copies the session's files into a staging folder. It
   rewrites paths in one pass, so ids, signatures and encrypted content are never touched.
   It checks the result, installs it where the agent looks for it, and keeps an undo
   journal.
4. **Continuing in another agent** reads the session into a neutral form (a chain of
   content-hashed steps), then writes it in the other agent's format with the loss report
   and briefing.

Each agent is a module behind a small SDK, so adding one doesn't touch the core. The details,
including the file-format traps hopsesh handles, are in [docs/design.md](docs/design.md).

### How it treats your data

- **Your machines only.** Sessions travel over your own SSH. There's no hopsesh server, no
  relay and no telemetry. A session goes to a vendor's cloud only when you hand it off. The app can check GitHub once a day for new versions, only
  if you say yes.
- **Read-only until you say go.** Each machine needs your permission, and every move is shown
  as a plan first.
- **Never moves credentials.** Logins, keys, live sockets and account data stay where they
  are; each machine stays signed in on its own. A machine's SSH password is kept in
  the macOS Keychain or Windows Credential Manager, or asked for each time, and never written
  to hopsesh's files or a command line.
- **Transcripts can hold secrets.** hopsesh scans for likely secrets while moving and can
  redact the copy (`--redact`). Every remote action goes to a local audit log.
- **Only your agents talk to their models.** hopsesh never calls the Anthropic or OpenAI API.
- **Clouds only through your agents.** A vendor's cloud is reached only through that agent's
  own command, signed in as you, and only once you allow it; hopsesh never reads its login
  or calls its servers. A hand-off sends a briefing (secrets masked, best effort) and a
  branch; credential-like files never go, and the snapshot commit names nothing but an
  opaque id.

## Why not…?

- **Codex's own import of Claude Code sessions?** It converts on one machine. hopsesh uses it
  when you ask (`--via import`) and adds machines, the repository, a loss report, a briefing
  and a way home that keeps the original session intact.
- **`claude --teleport`, `claude --cloud`, Codex cloud?** hopsesh uses them. They move a
  session between a vendor's cloud and the machine you're on, in that vendor's agent.
  hopsesh runs them for you and adds the rest: a session on another machine, the other
  vendor's agent or cloud, a briefing and a loss report, the branches, lineage and undo.
- **Remote Control?** Remote Control lets you drive a session that keeps running where it
  is. hopsesh moves the session, so it continues on the machine you're at, with that
  machine's files and tools. It can turn Remote Control on for the moved session.
- **Copying the `.jsonl` and running `claude --resume`?** It works only if every path is the
  same on both machines, the project folder name matches Claude Code's naming rules, and
  there's no second copy of the session. hopsesh handles those cases, plus the repository,
  worktree, side files and undo.
- **Syncing all of `~/.claude` between machines?** Sync tools copy everything, all the time.
  hopsesh moves the one session you pick, when you pick it, and fixes it up for the new
  machine.

Related projects, with different trade-offs:
- [parcels](https://github.com/0xSero/parcels) ships a repository together with a live agent
  session over Tailscale.
- [go-claude-teleport](https://github.com/mithro/go-claude-teleport) moves a session, its git
  worktree and its tmux window over SSH.
- [claude-nomad](https://github.com/funkadelic/claude-nomad) syncs your setup and history
  with path remapping.
- [chronicle](https://github.com/geekmuse/chronicle) syncs history through git.
- [sessport](https://www.npmjs.com/package/sessport) converts sessions between agents on one
  machine.

## FAQ

<details>
<summary><b>Does anything leave my machines?</b></summary>

Only what you hand off to a cloud. Sessions go directly between your machines over SSH.
hopsesh has no server and no telemetry, and it never calls the Anthropic or OpenAI API. A
hand-off sends a briefing and a branch to the cloud you chose, through that vendor's own
command, signed in as you (see [Cloud sessions](#cloud-sessions)).
</details>

<details>
<summary><b>Do I need Tailscale?</b></summary>

No. Any machine you can `ssh` to works, including aliases, ProxyJump and ProxyCommand from
your `~/.ssh/config`. Tailscale just makes machines easy to find. When an alias's LAN name
doesn't resolve, hopsesh falls back to the machine's Tailscale name.
</details>

<details>
<summary><b>One of my machines logs in with a password, not an SSH key</b></summary>

Add it with `hopsesh hosts add <name> <destination> --password`, or switch an
existing one with `hopsesh hosts auth <machine> password`. In the app, use the "Login" button
on the Machines screen, or tick "This machine logs in with a password" when adding it.

hopsesh asks for the password when it connects:
- **On macOS and Windows** it remembers it in the Keychain or Windows Credential Manager by
  default (`--keychain=false` to be asked each time).
- **Otherwise** it asks with a hidden prompt in the terminal, or a dialog in the app.
- **In scripts**, `--password-stdin` reads it from standard input.

It's never written to hopsesh's files or put on a command line.

Better still, `hopsesh hosts setup-key <machine>` (in the app: "Set up key login") logs in once
with the password and adds your SSH key there. It then checks that key login works, switches
the machine to keys and forgets the password. This works for macOS and Linux machines.
`hopsesh hosts` shows each machine's login method. Details:
[docs/design.md §16](docs/design.md#16-password-login).
</details>

<details>
<summary><b>Does the other machine need hopsesh installed?</b></summary>

No. `pull` only needs an SSH server there. hopsesh on the other machine is needed only to
`push` from here to there (it must also run `hopsesh receive on`).
</details>

<details>
<summary><b>What gets lost when a session continues in another agent?</b></summary>

The plan tells you before anything is written. Usually the conversation arrives as text, tool
calls appear as the previous agent's activity, long outputs are shortened, and reasoning
from the other vendor is left out. When you bring the session back, only the new work is
added to the original, so nothing in the original is lost.
</details>

<details>
<summary><b>What if the session is still running on the other machine?</b></summary>

By default hopsesh copies it as it is and hands it off: the copy left behind is marked when
that session ends. With `--fork`, both copies continue independently. hopsesh refuses to
replace a session that's open on this machine, unless `--stop-local` quits it first. For
Codex that works only between turns, because Codex has no graceful way to stop mid-turn: if
it's working, hopsesh asks you to let it finish. Listings show running sessions as "live
working" or "live idle".
</details>

<details>
<summary><b>I moved a session and kept using the old copy too. What happens?</b></summary>

Moving it again stops and explains: both copies changed, and hopsesh never merges them. Choose
`--keep-both` to bring the incoming copy in as a separate session, or `--replace` to set the
copy here aside (`hopsesh undo` restores it).
</details>

<details>
<summary><b>Different usernames, home folders or operating systems?</b></summary>

That's the point. Paths are rewritten for the new machine, including JSON-escaped Windows
paths, and separators are converted between Windows and macOS/Linux. Paths it can't map are
left as they are and listed in the plan.
</details>

<details>
<summary><b>Different accounts on the two machines?</b></summary>

hopsesh asks each agent which account it's signed in to: `claude auth status` for Claude Code,
and Codex itself for Codex (its `auth.json` is never read). If the accounts differ, it leaves
out what only the original account can use: Claude Code's signed thinking blocks, and Codex's
encrypted reasoning and compaction records. These are removed whole, never edited, and the
conversation itself stays.
</details>

<details>
<summary><b>Why does macOS ask about "devices on your local network"?</b></summary>

macOS 15 and later ask before an app may reach machines on your LAN. The hopsesh app asks
when it first connects to such a machine, explains why, and if you chose Don't Allow, links to
System Settings → Privacy & Security → Local Network. Tailscale connections aren't affected,
and the command-line tool run from Terminal never needs this permission.
</details>

<details>
<summary><b>Why does macOS ask whether hopsesh may control iTerm2 or Terminal?</b></summary>

Opening a tab in another app, or bringing one forward, takes macOS's Automation permission,
asked once per app. hopsesh uses it only to open tabs that run its own launch command and to
select a session's tab. If you choose Don't Allow for iTerm2, hopsesh opens Terminal
instead; if Terminal is refused too, it gives you the command to copy. You can change it
later in System Settings → Privacy & Security → Automation.
</details>

<details>
<summary><b>Does hopsesh use iTerm2's Python API?</b></summary>

Only if you have turned it on yourself (iTerm2 → Settings → General → Magic → Enable Python
API); hopsesh never turns it on and never changes iTerm2's or Claude Code's settings. With it
on, a hand-off step opens in a split beside the session you are in, the app learns at once
when you close a step's tab, and "Show" finds a running session's tab directly. It asks iTerm2 for an API cookie once per
connection (macOS asks you once whether hopsesh may control iTerm2) and keeps it in memory
only. hopsesh's client cannot type into a session, inject output or read what is on screen:
those requests are not in it. With the API off, hopsesh uses AppleScript as before.
</details>

<details>
<summary><b>How do I undo a move?</b></summary>

`hopsesh undo` reverses the newest move or continuation, and `hopsesh undo <id>` a specific
one. It removes new files, restores replaced ones, cuts off appended records and brings back
set-aside copies, on other machines too. `hopsesh undo --list` shows what can be undone.
</details>

## Contributing

Contributions are welcome. Issues labelled
[good first issue](https://github.com/roeehrl/hopsesh/contribute) are a good place to start.
You don't need two machines: `demo/record.sh shell` starts two made-up machines in Docker and
drops you on one with hopsesh ready (see [demo/README.md](demo/README.md)).

See [CONTRIBUTING.md](CONTRIBUTING.md) for the 60-second dev setup, and use
[Discussions](https://github.com/roeehrl/hopsesh/discussions) for questions and ideas.
Report security issues privately as described in [SECURITY.md](SECURITY.md).

### Add your agent

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/agents-dark.png">
  <img src="docs/assets/agents-light.png" alt="Settings, Agents: Claude Code and Codex with the capabilities each module provides" width="720">
</picture>

Every agent is a module behind a small SDK (`sdk/agent`): implement listing, bundling,
planning and resuming, add the capabilities your agent supports, and run the conformance
kit. Claude Code and Codex are the two templates. See
[Add an agent module](CONTRIBUTING.md#add-an-agent-module); OpenCode is an open
[help wanted](https://github.com/roeehrl/hopsesh/issues/10) issue.

## License

[Apache License 2.0](LICENSE). "Claude" and "Claude Code" are trademarks of Anthropic, PBC;
"Codex" and "OpenAI" are trademarks of OpenAI. hopsesh is not affiliated with, endorsed by or
sponsored by Anthropic or OpenAI.
