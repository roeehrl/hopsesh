# hopsesh design

*hopsesh is unofficial and not affiliated with Anthropic. §12 lists what is built and what is planned.*

## 1. What it does, and what it deliberately doesn't

**Does.** With your permission, hopsesh:
- finds your machines (Tailscale and `~/.ssh/config`);
- checks SSH access to each one;
- lists every Claude Code session on them, grouped by repository, showing:
  - the absolute path on each machine and the git remote URL;
  - the branch, and whether the session ran in a worktree;
  - the title, last activity and the last prompt;
  - whether the session is live right now.

You pick a session. hopsesh then:
- finds the repo locally, or offers to clone it into your repos folder (default `~/git`, overridable). If cloning fails, it explains why and asks for a path or auto-detects one;
- recreates the worktree if the session used one (or uses the main checkout, as you choose);
- copies the session and rewrites its paths for this machine;
- prints, or runs, the exact `claude --resume` command, with a first message that tells the resumed session it was moved and asks it to check that nothing is missing.

Optionally it also:
- turns on Remote Control for the new session;
- has the new session tell the old one where the work went (both on Remote Control), after which either can message the other with Claude Code's own cross-session messaging.

**Doesn't:**
- No cloud relay of transcripts; files move machine to machine over your own SSH.
- No copying of login tokens or credentials. Each machine stays logged in on its own.
- No writes into another session's live socket using undocumented formats.
- No model calls. Only the unmodified `claude` binary talks to the model, under each machine's own login.
- No telemetry.

**Why it's worth building.** Anthropic's teleport covers only cloud → local. The existing community tools each cover a slice:
- parcels pushes repo plus session one way;
- cct and drev handle export/import bundles;
- claude-sync syncs `~/.claude` in bulk.

Two more (as of 2026-10):
- **CtxHop** (Go, MIT, 57★, created 2026-08-10): bind a project, sync its sessions through an encrypted storage backend you set up, and resume on authorized devices. That's a push-and-sync model with setup per project.
- **sessport** (npm, 2026-09-30): converts sessions between Claude Code, Codex and Gemini on one machine.

None of them discovers existing sessions across your machines with zero setup and pulls the one you pick, matches the repo by git identity (clone if missing, warn about unpushed work), and handles the Claude-specific traps. Those traps are thinking-block signatures, duplicate-ID lookup failures, the realpath-derived folder names, and 30-day cleanup.

## 2. Name, repo and license

The trademark constraint first: Anthropic's Claude Code legal page forbids "Claude Code" or "Anthropic" in a product name. We describe the tool in plain text as "works with Claude Code" and add an "unofficial, not affiliated" disclaimer.

**Name.** `hopsesh` ("hop a session") was free on GitHub, Homebrew, Scoop, winget, Debian, Arch, npm, PyPI and crates.io when checked (2026-10-02), and isn't tied to Claude, so a later Codex backend still fits.

**License: Apache-2.0.** It has an explicit patent grant, suits a security-sensitive tool, and is compatible with every dependency.

## 3. Principles (design rules every feature must pass)

1. **Consent-first and read-only by default.** Discovery connects to nothing. Each machine needs explicit opt-in. Reads are limited to `<configDir>/**` and `git` metadata until you confirm a move.
2. **Plan → confirm → apply.** Every write is a rendered plan first (a non-interactive `pull` without `--yes` shows the plan and stops), and it is reversible (`undo`).
3. **Use the user's own tools.** hopsesh drives system `ssh` (SFTP over it), `git`, `tailscale` and the unmodified `claude` binary, inheriting your keys, agents, ProxyJump, certificates and ControlMaster.
4. **Small Claude-specific layer, tolerant of format changes.** The session format is reverse-engineered and changes between versions. It is isolated in `internal/core/sessions` and `internal/core/rewrite`, and every rewritten transcript is re-read with the same reader before it is installed.
5. **Transcripts are secrets.** Encrypted transport only (SSH), a secret scan before moving, optional redaction, an append-only audit log.
6. **Cross-platform from day one.** Windows remotes run PowerShell through `-EncodedCommand` (their OpenSSH default shell is `cmd.exe`), paths use backslashes, and line endings are never converted.

## 4. Architecture

```
   ┌──────────────────────────── front-ends ─────────────────────────────┐
   │ hopsesh (CLI: cobra, TUI: Bubble Tea v2)   hopsesh-app (Wails v3)    │
   │ one-liners · --json · interactive TUI      macOS (Windows planned)   │
   └──────────────────────────────────┬───────────────────────────────────┘
                                      │ inventory (scan) → engine (plan, apply, undo)
┌─────────────────────────────────────┴────────────────────────────────────────┐
│ core (Go)                                                                     │
│  hosts      Tailscale + ~/.ssh/config discovery (connects to nothing)         │
│  transport  system ssh + SFTP · ControlMaster · host trust · Tailscale route  │
│  lnp        macOS local network privacy: gated targets, in-process probe      │
│  sessions   Claude locator: slug, config dir, head/tail reader, live registry │
│  repos      remote identity · batched git probe · clone · worktrees           │
│  rewrite    single-pass prefix map · separators · thinking · relocated record │
│  scan       secret rules · redaction of the copy                              │
│  link       resume command · start prompt · auth / Remote Control check       │
│  audit      append-only JSONL log                                             │
└──────────────────────────────────────┬───────────────────────────────────────┘
               update (verified self-update) · optional, consented remote helper:
               hopsesh agent --json (same binary, hash-pinned on the other machine)
```

- **Why Go:**
  - The core is mostly SSH and process orchestration, which Go does well.
  - Builds are fast (about 7 s for an empty app vs about 234 s for Tauri) and outside contributors find it easy to pick up.
  - Cross-compilation is trivial.
  - Bubble Tea is the strongest TUI ecosystem.
- **GUI choice: Wails v3.** Thin native shells (WKWebView / WebView2) over the same core. Wails v3 is still beta; the GUI is a thin layer over the core, so it could move to Tauri v2 without touching the core.
- **Two binaries, not one dual-mode binary.** Windows forces a binary to be either a GUI or a console program, so a dual-mode binary flashes a console or mangles output. The app bundle embeds the CLI.

## 5. Discovery: how sessions are found (no model calls)

**Machines.** Merge two sources, then ask consent per host:
- `tailscale status --json`: use DNSName, OS and online state; drop phones and expired nodes; mark shared nodes off by default.
- `~/.ssh/config` aliases (following `Include`); connections go through your `ssh`, so `Match`, ProxyJump and ProxyCommand apply exactly as usual.
- When an alias's host name does not resolve (a `.local` name on another network), the machine's Tailscale name is tried and remembered.

**Probe (one round trip per host):**
- Run with `BatchMode=yes`, `ConnectTimeout` and strict host-key checking.
- A new host key goes to a trust screen. It shows the fingerprint and marks it verified when it matches the `sshHostKeys` Tailscale reports for that host, or a key you already trust for it in `~/.ssh/known_hosts`.
- A changed key is a hard stop.
- The probe returns OS, architecture, home, Claude Code's config dir (`CLAUDE_CONFIG_DIR` or `~/.claude`), the `claude` path and version, and whether git is installed.

**Sessions, per consented host, cheapest first:**
1. Live state from the `<configDir>/sessions/<pid>.json` registry, with each pid checked on the machine.
2. An SFTP walk of `<configDir>/projects/*/*.jsonl`. A `stat` plus head/tail range reads extract the title, last prompt, cwd, branch, last activity, size and the Claude version that wrote it. This uses the same title and last-prompt rules the official picker uses.
3. One batched git probe per machine covering every distinct cwd: toplevel, remote URL, branch, upstream, unpushed commits, uncommitted files and worktrees.

**Optional remote helper:** with `hopsesh hosts helper install <machine>`, steps 1–3 run on the remote in one call (`hopsesh agent --json`), after its SHA-256 is checked against the pinned value. It is faster on hosts with long histories.

**Output format.** `hopsesh ls --json` prints `{"machines": [...], "groups": [...]}`. Abridged:

```json
{
  "machines": [{"name": "studio", "status": "ok",
                "facts": {"os": "darwin", "home": "/Users/alice", "claudeVersion": "2.1.284"}}],
  "groups": [{
    "identity": "github.com/alice/app", "name": "app",
    "remote": "git@github.com:alice/app.git", "localCheckout": "/Users/alice/git/app",
    "entries": [{"machine": "studio", "session": {
      "id": "<session-id>", "title": "refactor auth", "cwd": "/Users/alice/git/app",
      "lastActive": "2026-10-02T10:44:00Z", "lastPrompt": "run the migration tests again",
      "claudeVersion": "2.1.284", "sizeBytes": 12688143, "subagents": 7,
      "live": {"status": "idle"},
      "git": {"branch": "auth", "mainBranch": "main", "unpushed": 2, "dirty": 7}
    }}]
  }]
}
```

`hopsesh agent --json` (the helper) prints `{"schema": "hopsesh.agent/v1", "machine": {...}}` with the same session fields.

**Grouping.** Sessions group by repository identity, showing each machine's absolute path. Sessions outside git go in "No repository".

## 6. Transport: the pipeline behind "Hop here"

1. **Preflight:**
   - **Live source:** if the session is live, choose *handoff* (default: copy, then notify the old session to stop) or *fork* (`--fork-session`, both continue). A session running on this machine, or one already in the target folder, is refused.
   - **Versions:** a warning when the `claude` here is older than the version that wrote the session.
   - **Account:** read from `claude auth status` on both machines. Across accounts, thinking blocks are dropped whole (removing only the signature makes the API reject it).
   - **Size and retention:** a warning for large sessions; the copy gets a fresh mtime so Claude Code's 30-day cleanup does not remove it.
2. **Repo:**
   - Match by normalized remote (ssh or https form, with or without `.git`) among local checkouts in the repos folder and other common folders.
   - If missing: offer to clone into `<reposDir>/<name>` (layout configurable: flat or `host/owner/repo` ghq style), using your git credential helpers non-interactively.
   - On failure: show git's reason and ask for a path, or rescan `reposDir`.
   - If the remote has unpushed commits or dirty files, warn before the move.
   - **Worktrees:** the branch of each session and whether it ran in a worktree are shown. A session from a worktree can get a matching worktree here (`--worktree auto|create|main`), so the local checkout stays on its own branch.
3. **Copy:** over SFTP into a staging dir:
   - the JSONL, its `<id>/` sidecar (subagents, tool results, title) and `file-history/<id>/`;
   - optionally the project's memory folder, merged without overwriting newer files.
   - Live files keep growing, so a file is copied again until its size stops changing.
4. **Rewrite:**
   - **Prefix map, one pass per line:** source repo root (or worktree), home and config dir map to the target's real paths; JSON keys are rewritten too.
   - **JSON escaping:** handles escaped Windows backslashes, paths after JSON escapes, and converts separators below mapped folders between Windows and macOS/Linux.
   - **Leave alone:** `uuid`, `parentUuid` and `sessionId` are never touched; thinking blocks are left alone in same-account mode.
   - **Strip:** the `bridge-session` record, so the copy doesn't reattach to the old Remote Control link.
   - **Append:** a `relocated` record, the same mechanism Claude's own `/cd` uses.
   - **Unmapped paths** (e.g. `/Users/alice/data`) are left as is and listed.
   - **Then:** the secret scan runs, with optional redaction on the copy.
5. **Commit:**
   - **Location:** move into `<configDir>/projects/<slug(realpath(target cwd))>/`, using the exact slug algorithm (200-char cap plus hash, UTF-16 counting).
   - **Freshness:** set a fresh mtime.
   - **Duplicates:** set aside other copies of the same ID on the target (restorable with `undo`), since two copies make resume fail with not-found.
   - **Undo:** write the undo journal.
   - **Validate:** re-read the installed transcript with the session reader before committing.
6. **Launch:**
   - Print `cd <path> && claude --resume <id> <start prompt>`, adding `--remote-control "<name>@<host>"` when requested and eligible. "Open in Terminal" runs it.
   - The start prompt tells the resumed session it was moved (from where, to where, what was rewritten, what was left behind) and asks it to check the repository, files, tools and environment before continuing.
   - "Open in Claude desktop" uses `claude --desktop --resume <id>` where the installed `claude` lists that flag.

## 7. Linking old and new sessions (only through official features)

- **Remote Control:**
  - Launch with `--remote-control "<title>@<host>"` after an eligibility check. It needs a claude.ai subscription login; an API key, Bedrock or Vertex rule it out.
  - If not eligible, hopsesh says why and continues without it.
- **Telling the old session:** only possible when both are on Remote Control under the same account. hopsesh starts the new session with a short, visible first prompt asking Claude to `SendMessage` the old session: "This task continued on <host> as <name>; please stop editing." Otherwise hopsesh shows that text for you to paste.
- **Two-way messaging:** ordinary `ListAgents`/`SendMessage` between the distinctively named sessions (`<title>@<host>`). Each side's own settings decide whether a message is delivered or held for approval.
- **Planned: optional hook index.** A user-level `SessionStart`/`SessionEnd` hook could record session and Remote Control IDs to make live status exact (opt-in, removable).

## 8. Interfaces

**CLI:**

```
hopsesh                                 # interactive TUI: machines → repos → sessions
hopsesh hosts [allow|deny|add|helper] / hopsesh trust <host> / hopsesh doctor [host]
hopsesh ls [--host H] [--repo R] [--live] [--no-local] [--no-git] [--limit N] [--json]
hopsesh show <host>:<id|title>          # details, repository, branch and worktree state
hopsesh pull <host>:<id|title> [--clone] [--repos ~/git] [--worktree auto|create|main] [--fork]
          [--rc] [--notify] [--redact] [--desktop] [--run] [--yes] [--dry-run]
hopsesh import <file.jsonl>             # install a transcript copied by hand
hopsesh undo [<id>]
hopsesh update [--check]
hopsesh version
```

Exit codes and `--json` on the listing and moving commands make it scriptable. One-liner install:
- macOS/Linux: `curl -fsSL …/install.sh | sh`
- Windows: `irm …/install.ps1 | iex`

Both verify the checksum and the release-key signature of `checksums.txt` (docs/RELEASING.md).

**GUI:**
- 0 · machines and consent;
- 1 · sessions by repo (machine sidebar, search, live badges);
- 2 · preflight (repository found, clone or choose a folder; work left behind; worktree mode; path-rewrite preview; warnings; after-move options);
- 3 · ready (resume command, open in Terminal or the desktop app, text for the old session, undo).

On first use the app explains the macOS Local Network prompt, and shows a banner with a link to System Settings if access was denied. It asks once whether it may check GitHub daily for new versions.

**Config:** `~/.config/hopsesh/config.toml` (`%APPDATA%\hopsesh` on Windows) holds `repos_dir`, `layout`, `live_policy`, `remote_control`, `update_check` and the machines you added or allowed (with any helper hash). State lives in `~/.local/state/hopsesh` (`%LOCALAPPDATA%\hopsesh` on Windows): the audit log, undo journal, staging area, hopsesh's own `known_hosts`, remembered routes and start prompts. `HOPSESH_CONFIG_DIR` and `HOPSESH_STATE_DIR` override both.

## 9. Security and privacy

- **Threat model:**
  - The threats are a malicious remote (hostile filenames, path traversal, oversized files) and a compromised update channel.
  - hopsesh itself must never become a credential-movement tool.
- **Controls:**
  - Copies go to a staging folder first; only the session's own files are read.
  - Agent forwarding is off. hopsesh stores only its config, host-key trust, remembered routes and its own logs and undo data.
  - The helper deploys only with consent, is hash-pinned and checked before every run, listens on no port, and is removable.
  - Updates are signed (ECDSA over `checksums.txt`, verified by the binary; on macOS the new binary's signing team must match).
  - macOS local network privacy (TN3179): the app connects in-process first so macOS asks for hopsesh, waits for the answer, and explains a denial; Tailscale routes are not gated.
  - `.credentials.json`, `*.key`, `session-env/`, `shell-snapshots/`, sockets and the account fields in `~/.claude.json` are never copied.
- **Terms:** only the unmodified `claude` binary ever calls the model, under each machine's own login. hopsesh never calls the API.

## 10. Testing strategy

- **Unit tests:**
  - slug algorithm (macOS/Linux/Windows/Unicode/long paths);
  - rewriter: escaping, Windows paths, partial-prefix traps, thinking-block preservation, `relocated` placement;
  - title and last-prompt extraction.
- **End to end:** `internal/engine` tests run a full plan and apply (clone through an `insteadOf` remote, worktree recreation, undo) against temporary repositories.
- **Transport integration:** a Linux CI job (`scripts/integration-test.sh`) creates a second user with a session, runs `hosts add`, `trust`, `ls`, `pull` and `undo` against the runner's own OpenSSH server, and checks the installed, rewritten transcript.
- **Fuzzing:** the rewriter (output must stay valid JSON, including cross-OS separator conversion).
- **Planned:** a compatibility matrix of pinned Claude Code versions, and a Windows OpenSSH job.

## 11. Distribution, signing, updates

- **Build:** GoReleaser for the CLI. Distribution:
  - GitHub Releases;
  - a Homebrew cask, Scoop and winget manifests;
  - `.deb`/`.rpm`/`.apk` packages;
  - SBOMs (syft), a release-key signature and a Sigstore keyless signature over `checksums.txt`, and GitHub build provenance.
- **macOS:** darwin CLI binaries are signed and notarized by GoReleaser when the signing secrets are set; a separate macOS job builds, signs (hardened runtime), notarizes and staples the app (`scripts/build-macos-app.sh`). macOS 13 or later.
- **Windows GUI (v0.2):** SignPath Foundation (free for open source). SmartScreen warns until reputation builds.
- **Updates:** `hopsesh update` (see §9). The app asks once whether it may check GitHub daily, then only shows a link.

## 12. Status

**Built:** discovery and consent, host-key trust, `doctor`; listing with git, branch and worktree state; `pull` with clone, worktree recreation, path rewriting (including Windows), secret scan and redaction, undo, start prompt, Remote Control eligibility and old-session notice, automatic handling of cross-account moves, desktop-app open; TUI; macOS app with local network privacy handling; optional helper; self-update; release pipeline. No release has been published yet.

**Planned:**
- bringing unpushed commits and uncommitted changes along (`git bundle` plus patch into a new worktree);
- Windows GUI, and a Windows OpenSSH CI job;
- the optional hook index (§7);
- a compatibility matrix of pinned Claude Code versions.

## 13. Risks and mitigations

- **Format drift in Claude Code:** isolated locator and rewriter, every rewrite re-read before install, `relocated` written the way Claude itself writes it, a warning when the target version is older than the source; a pinned-version compatibility matrix is planned.
- **Anthropic ships an official local → local move:** hopsesh still adds discovery across machines, git-aware repo and worktree reconstruction, no same-account requirement and no cloud relay. When an official path appears, hopsesh can call it as a backend.
- **Wails v3 beta:** keep the GUI thin, with Tauri v2 as the fallback.
- **Windows signing reputation:** SignPath or Azure, and document the SmartScreen step.
- **Secrets in transcripts:** scan while moving, report counts, redaction option, never sent anywhere but your own machines.

## 14. Decisions (2026-10-02)

1. Name: **hopsesh** (availability research in §2).
2. License: **Apache-2.0**.
3. Stack: **Go + Bubble Tea v2 + Wails v3**, with Tauri v2 as the GUI fallback.
4. v0.1 scope: **CLI + TUI on macOS/Windows/Linux, GUI on macOS**; Windows GUI in v0.2.
5. Signing: **Apple Developer ID**; Windows via **SignPath Foundation** (free for OSS).
6. Defaults: live source → **handoff**; repos folder **~/git**, flat layout.
