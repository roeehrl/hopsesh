# hopsesh

**Find your Claude Code sessions on your other machines and continue one here.**

> Status: alpha. It works end to end on macOS and Linux; Windows support is built but less
> tested. Expect rough edges and please report them.
>
> hopsesh is an independent open-source project. It works with Claude Code but is **not
> affiliated with, endorsed by or sponsored by Anthropic**. "Claude" and "Claude Code" are
> trademarks of Anthropic, PBC.

You started something in Claude Code on your desktop, and now you're on your laptop. hopsesh:

1. **Finds your machines.** It reads Tailscale and your `~/.ssh/config`, and connects only to
   the machines you allow, using your own `ssh`, keys and agent.
2. **Lists every Claude Code session on them**, grouped by repository: the path on each
   machine, the git remote, the branch (and whether it is in a worktree), the title, when it
   was last active, your last prompt, and whether it is running right now.
3. **Moves the one you pick to this machine.** It finds the repository here or offers to clone
   it, recreates the worktree if the session used one, warns about unpushed or uncommitted
   work on the other machine, copies the session, rewrites its paths for this machine, and
   gives you the command to resume it. The resumed session's first message tells Claude it
   was moved and asks it to check that nothing is missing. Optionally it starts with Remote
   Control on and tells the old session where the work went.

```sh
hopsesh                                   # interactive: machines → repos → sessions
hopsesh ls --json                         # every allowed machine, grouped by repo
hopsesh pull studio:"fix flaky tests"     # plan, confirm, move, print the resume command
hopsesh pull studio:7f3c2a1e --clone --rc --notify --yes
hopsesh doctor laptop                     # SSH, host trust, Claude version, Remote Control
hopsesh undo 7f3c2a1e
```

There is also a native app for macOS (Windows next) built on the same engine.

## Install

No release is published yet; until then, build from source (below). From the first release
on:

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/roeehrl/hopsesh/main/scripts/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/roeehrl/hopsesh/main/scripts/install.ps1 | iex
```

Both check the download against the release's `checksums.txt` and its signature. Releases
also list a Homebrew cask, Scoop and winget manifests, `.deb`/`.rpm`/`.apk` packages and the
signed, notarized macOS app. `hopsesh update` installs new releases with the same checks.

From source (Go 1.26+):

```sh
go install github.com/roeehrl/hopsesh/cmd/hopsesh@latest
```

The other machines need nothing but an SSH server (Remote Login on macOS, OpenSSH on Windows)
and the sessions themselves.

## How it treats your data

- **Your machines only.** Sessions travel over your own SSH. There's no hopsesh server, no
  cloud relay and no telemetry. The app can check GitHub once a day for new versions, only
  if you say yes.
- **Read-only until you say go.** Discovery connects to nothing. Each machine needs your
  permission, and its SSH host key is checked strictly. Every move is shown as a plan first,
  and can be undone with `hopsesh undo`.
- **Never moves credentials.** Logins, keys, live sockets and account data stay where they
  are; each machine stays signed in on its own.
- **Transcripts can hold secrets.** hopsesh scans for likely secrets before moving and can
  redact the copy (`--redact`). Every remote action is written to a local audit log.
- **Only Claude Code itself talks to the model.** hopsesh never calls the API.

## macOS: Local Network permission

macOS 15 and later ask before an app may reach devices on your local network. The hopsesh
app asks at the moment it first connects to such a machine, explains why, and waits for your
answer. If you chose Don't Allow, it says so and links to System Settings → Privacy &
Security → Local Network. Machines reached over Tailscale are not affected, and the command
line tool run from Terminal or over SSH never needs this permission.

## Optional helper

`hopsesh hosts helper install <machine>` places a copy of hopsesh on a macOS or Linux machine
so that listing its sessions takes one SSH command instead of many small file reads. hopsesh
pins its SHA-256 and checks it before every run; it listens on nothing and is removed with
`hopsesh hosts helper remove <machine>`. Without it everything works the same, only slower
on machines with long histories.

## Documentation

- [docs/design.md](docs/design.md): how discovery, transport, path rewriting and linking work
- [docs/RELEASING.md](docs/RELEASING.md): how releases are built, signed and verified

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report security issues privately as described in
[SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE)
