<div align="center">

<img src="docs/assets/logo.svg" width="96" height="96" alt="hopsesh logo">

# hopsesh

**Find your Claude Code sessions on your other machines and continue one here.**

[![CI](https://github.com/roeehrl/hopsesh/actions/workflows/ci.yml/badge.svg)](https://github.com/roeehrl/hopsesh/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/roeehrl/hopsesh)](https://goreportcard.com/report/github.com/roeehrl/hopsesh)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/roeehrl/hopsesh/badge)](https://scorecard.dev/viewer/?uri=github.com/roeehrl/hopsesh)
[![Go Reference](https://pkg.go.dev/badge/github.com/roeehrl/hopsesh.svg)](https://pkg.go.dev/github.com/roeehrl/hopsesh)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
![macOS · Linux · Windows](https://img.shields.io/badge/platforms-macOS%20%C2%B7%20Linux%20%C2%B7%20Windows-0b6b62)

[Install](#install) · [Quick start](#quick-start) · [How it works](#how-it-works) · [Why not…?](#why-not) · [FAQ](#faq) · [Contributing](#contributing)

<img src="docs/assets/demo.gif" alt="hopsesh lists Claude Code sessions on two machines grouped by repository, then moves one to this machine, cloning its repository and printing the resume command" width="900">

<sub>Recorded with <a href="https://github.com/charmbracelet/vhs">VHS</a> from <a href="demo/hopsesh.tape">demo/hopsesh.tape</a>, with made-up machines and sessions.</sub>

</div>

You started something in Claude Code on your desktop, and now you're on your laptop. hopsesh
shows every session on all your machines and moves the one you pick here: repository,
worktree, paths and all. Then it gives you the `claude --resume` command.

- **Finds your machines** from Tailscale and `~/.ssh/config`, and connects only to the ones you
  allow, with your own `ssh`, keys and agent. Nothing to install on the other machines.
- **Lists every session by repository**: machine, path, branch, worktree, last prompt, when it
  was last active, and whether it's running right now.
- **Moves a session safely**: finds the repo here or clones it, recreates the worktree, warns
  about unpushed or uncommitted work, rewrites paths, and shows a plan first. `hopsesh undo`
  reverses a move.
- **Picks up where you left off**: the resumed session's first message tells Claude it was moved
  and asks it to check that nothing is missing. It can start with Remote Control on and tell
  the old session where the work went.
- **CLI, TUI and a macOS app** on one engine. `--json` output everywhere it matters.

> Status: alpha. It works end to end on macOS and Linux; Windows support is built but less
> tested. hopsesh is an independent project, not affiliated with Anthropic.

## Install

No release is published yet; until then, build from source (Go 1.26+):

```sh
go install github.com/roeehrl/hopsesh/cmd/hopsesh@latest
```

From the first release on:

```sh
# macOS and Linux
curl -fsSL https://raw.githubusercontent.com/roeehrl/hopsesh/main/scripts/install.sh | sh
```

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/roeehrl/hopsesh/main/scripts/install.ps1 | iex
```

Both check the download against the release's `checksums.txt` and its signature. Releases
also include Scoop and winget manifests, `.deb`/`.rpm`/`.apk` packages and the signed,
notarized macOS app. `hopsesh update` installs new releases with the same checks.

The other machines need only an SSH server (Remote Login on macOS, OpenSSH on Windows) and
their Claude Code sessions.

## Quick start

```sh
hopsesh hosts                 # machines found in Tailscale and ~/.ssh/config (connects to nothing)
hopsesh hosts allow studio    # let hopsesh connect to one
hopsesh trust studio          # confirm its SSH host key
hopsesh                       # browse sessions and move one here
```

Or from scripts:

```sh
hopsesh ls --json                          # every allowed machine, grouped by repo
hopsesh pull studio:"fix flaky tests"      # plan, confirm, move, print the resume command
hopsesh pull studio:7f3c2a1e --clone --rc --notify --yes
hopsesh doctor studio                      # SSH, host trust, Claude version, Remote Control
hopsesh undo 7f3c2a1e
```

### The macOS app

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/app-dark.png">
  <img src="docs/assets/app-light.png" alt="The hopsesh macOS app listing sessions from two machines, grouped by repository, each with a Hop here button" width="900">
</picture>

Same engine, with a preflight screen for each move: repository, worktree mode, what gets
rewritten, and what's left behind on the other machine.

## How it works

1. **Discovery** reads `tailscale status --json` and your `~/.ssh/config`. It connects to
   nothing until you allow a machine.
2. **Listing** connects with your system `ssh` (strict host-key checking, ControlMaster) and
   reads the head and tail of each transcript over SFTP. One batched `git` probe per machine
   adds branch, worktree and unpushed/uncommitted counts.
3. **Moving** builds a plan, then copies the transcript and its side files into a staging
   folder. It rewrites paths in one pass, so ids and signed thinking blocks are never
   touched. It re-reads the result, installs it where Claude Code looks for it, and keeps an
   undo journal.

The details, including the Claude Code file-format traps hopsesh handles, are in
[docs/design.md](docs/design.md).

### How it treats your data

- **Your machines only.** Sessions travel over your own SSH. There's no hopsesh server, no
  cloud relay and no telemetry. The app can check GitHub once a day for new versions, only
  if you say yes.
- **Read-only until you say go.** Each machine needs your permission, and every move is shown
  as a plan first.
- **Never moves credentials.** Logins, keys, live sockets and account data stay where they
  are; each machine stays signed in on its own.
- **Transcripts can hold secrets.** hopsesh scans for likely secrets while moving and can
  redact the copy (`--redact`). Every remote action goes to a local audit log.
- **Only Claude Code itself talks to the model.** hopsesh never calls the API.

## Why not…?

- **`claude --teleport`?** Teleport brings a session from Claude Code on the web to your
  machine. hopsesh moves sessions between your own machines.
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

## FAQ

<details>
<summary><b>Does anything leave my machines?</b></summary>

No. Sessions go directly between your machines over SSH. hopsesh has no server and no
telemetry, and it never calls the Anthropic API.
</details>

<details>
<summary><b>Do I need Tailscale?</b></summary>

No. Any machine you can `ssh` to works, including aliases, ProxyJump and ProxyCommand from
your `~/.ssh/config`. Tailscale just makes machines easy to find. When an alias's LAN name
doesn't resolve, hopsesh falls back to the machine's Tailscale name.
</details>

<details>
<summary><b>Does the other machine need hopsesh installed?</b></summary>

No. It only needs an SSH server. There's an optional helper (`hopsesh hosts helper install
<machine>`) that makes listing faster on machines with long histories. It's a copy of
hopsesh pinned by SHA-256, checked before every run, listens on nothing, and is removed with
`hopsesh hosts helper remove <machine>`.
</details>

<details>
<summary><b>What if the session is still running on the other machine?</b></summary>

By default hopsesh copies it as it is and asks the old session to stop (handoff). With
`--fork`, both copies continue independently. hopsesh refuses to overwrite a session that's
running on this machine.
</details>

<details>
<summary><b>Different usernames, home folders or operating systems?</b></summary>

That's the point. Paths are rewritten for the new machine, including JSON-escaped Windows
paths, and separators are converted between Windows and macOS/Linux. Paths it can't map are
left as they are and listed in the plan.
</details>

<details>
<summary><b>Different Claude accounts on the two machines?</b></summary>

hopsesh reads `claude auth status` on both sides. If the accounts differ, it leaves out
thinking blocks, because they're signed for the original account.
</details>

<details>
<summary><b>Why does macOS ask about "devices on your local network"?</b></summary>

macOS 15 and later ask before an app may reach machines on your LAN. The hopsesh app asks
when it first connects to such a machine, explains why, and if you chose Don't Allow, links to
System Settings → Privacy & Security → Local Network. Tailscale connections aren't affected,
and the command-line tool run from Terminal never needs this permission.
</details>

<details>
<summary><b>How do I undo a move?</b></summary>

`hopsesh undo <session-id>` removes the copy and restores anything it set aside. `hopsesh undo`
with no arguments lists recent moves.
</details>

## Contributing

Contributions are welcome. Issues labelled
[good first issue](https://github.com/roeehrl/hopsesh/contribute) are a good place to start.
You don't need two machines: `demo/record.sh shell` starts two made-up machines in Docker and
drops you on one with hopsesh ready (see [demo/README.md](demo/README.md)).

See [CONTRIBUTING.md](CONTRIBUTING.md) for the 60-second dev setup, and use
[Discussions](https://github.com/roeehrl/hopsesh/discussions) for questions and ideas.
Report security issues privately as described in [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE). "Claude" and "Claude Code" are trademarks of Anthropic, PBC;
hopsesh is not affiliated with, endorsed by or sponsored by Anthropic.
