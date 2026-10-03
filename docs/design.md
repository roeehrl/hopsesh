# hopsesh design

*hopsesh is unofficial and not affiliated with Anthropic or OpenAI. §19 lists what is built and what is planned.*

## 1. What it does, and what it deliberately doesn't

**Does.** hopsesh moves coding-agent sessions between your machines and between agents. Today the agents are Claude Code and Codex; each is a module (§5), and more can be added without touching the core.

With your permission, hopsesh:
- finds your machines (Tailscale and `~/.ssh/config`) and checks SSH access to each;
- lists every session of every installed agent on them, grouped by repository, with the agent, title, last prompt, last activity, branch, worktree, live state, and where else the session has been.

You pick a session, and hopsesh brings it to this machine:
- **in the same agent** (a move): the session's own files are copied, their paths rewritten for this machine, and installed byte for byte otherwise;
- **or in another agent** (a continuation, `--in codex`): the conversation is converted into the other agent's own session format, with a briefing that tells the agent where it came from and what did not carry over. This works on one machine too.

Either way it finds the repository here (or clones it), recreates the worktree, brings the code to the session's commit, marks the copy left behind, and prints the command to continue. A round trip (A → B → A) adds only the new work to the original. When the other machine also runs hopsesh, a session can be sent there too (`push`, §11).

**Doesn't:**
- No cloud relay: files move machine to machine over your own SSH.
- No copying of logins or credentials; each machine and agent stays logged in on its own.
- No model calls. Only the unmodified agents talk to their models, under each machine's own login.
- No telemetry.

**Why it's worth building.** Vendors now ship importers between agents on one machine (Codex imports Claude Code sessions), and community tools cover slices: one-way pushes (parcels), export bundles (cct, drev), bulk syncs (claude-sync, CtxHop), single-machine converters (sessport). What remains unclaimed is the combination: any supported agent to any other, across your own machines over plain SSH, with the repository, an honest account of what was lost, and a way home that keeps the original session intact.

**Later: cloud sessions.** The core speaks of *locations*, not only machines, so a module can later expose sessions that live in an agent's cloud through that agent's own CLI.

## 2. Name, repo and license

The trademark constraint first: Anthropic forbids "Claude Code" or "Anthropic" in a product name, and hopsesh applies the same caution to OpenAI's marks. The tool says "works with Claude Code and Codex" in plain text, with an "unofficial, not affiliated" notice naming both vendors.

**Name.** `hopsesh` ("hop a session") was free on GitHub, Homebrew, Scoop, winget, Debian, Arch, npm, PyPI and crates.io when checked (2026-10-02), and isn't tied to one vendor.

**License: Apache-2.0**, for its explicit patent grant.

## 3. Principles

1. **Consent first, read-only by default.** Discovery connects to nothing. Each machine needs explicit opt-in. Nothing is written until you confirm a plan.
2. **Plan → confirm → apply → undo.** Every write is a rendered plan first and is journaled; `undo` reverses it, on every machine it touched.
3. **The core knows no agent.** Everything agent-specific lives in a module behind a small SDK (§5). The core is tested with fake modules as well as real ones.
4. **Never regenerate a native transcript.** A same-agent move keeps the session's bytes (only paths change, under the module's rules). A continuation writes new sessions or appends to existing ones; it never re-renders turns an agent already wrote, so signed reasoning stays valid.
5. **Lineage travels with the session.** What hopsesh knows about a session's copies is stored beside the session itself (§10), not only on the machine that moved it.
6. **Use the user's own tools.** System `ssh` (with SFTP), `git`, `tailscale` and the unmodified agents, so your keys, ProxyJump, certificates and ControlMaster apply.
7. **Transcripts are secrets.** SSH only, a secret scan before moving, optional redaction, an append-only audit log, and each module declares files hopsesh must never open.
8. **Cross-platform.** Windows remotes run PowerShell through `-EncodedCommand`, paths keep their form, line endings are never converted.

## 4. Architecture

```
  front ends    internal/ui/cli (cobra)   internal/ui/tui (Bubble Tea)   internal/ui/gui (Wails v3)
                internal/ui/skill (the skill every agent reads)
       │
  use cases     internal/app: scan · find · plan · apply · undo · push/serve · skill · key login
       │
  core          internal/core: host (machines, probe, confinement) · move (moves, continuations)
                convert (IR → text history, briefing) · lineage · journal · peer · transport
                hosts · repos · rewrite · scan · launch · integrate · registry · audit · lnp
       │
  modules       agents/claude · agents/codex          (registered in internal/agents/all)
       │
  contract      sdk/agent (Module, Host, capabilities) · sdk/ir (conversation IR, node ids)
```

- **Layering is enforced** by `internal/archtest`: the SDK imports nothing of hopsesh; modules import only the SDK; the core imports no module, use case or front end; the app gets modules from the registry, never by name; front ends get the registry from the composition root (`cmd/*`, `internal/agents/all`).
- **Why Go:** SSH and process orchestration, fast builds, trivial cross-compilation, Bubble Tea.
- **GUI: Wails v3**, a thin shell (WKWebView / WebView2) over the same app layer; Tauri v2 is the fallback.
- **Two binaries**, because Windows forces a binary to be a GUI or a console program. The app bundle embeds the CLI.

## 5. The module SDK

A module is compiled in (no plugins, no WASM: revisit if third parties ask, a module needs another language, binary size hurts, or modules must update apart from hopsesh). It implements `agent.Module`:

| Method | Does |
|---|---|
| `Spec()` | id, name, vendor, stability, tested versions, binaries, data roots (with env overrides), login env, secrets, worktree folders, instruction files (project and global), tool names, features |
| `Detect(h)` | the install on a machine, from the probe's facts |
| `List(h, in)` | the machine's sessions as `Summary` (title, cwd, last prompt, activity, branch, size, mark) |
| `Bundle(h, in, s)` | every file of one session |
| `PlanMove(src, dst, s, b, p)` | where each file goes, how it is rewritten (a `RewritePolicy`: protected keys, dropped records, renames), records to append |
| `Verify(h, mp, staged, p)` | checks the staged, rewritten files before they are installed |
| `Resume(in, key, p, o)` | the command that continues a session (fork, remote control, desktop app, a first prompt) |

Optional interfaces add capabilities, computed from what a module implements (`agent.Capabilities`) plus `Spec.Features`:

| Interface | Capability | Claude Code | Codex |
|---|---|---|---|
| `LiveDetector`, `Stopper` | `live`, `stop` | pid registry, SIGTERM | writer-lock probe, working or idle from the last turn; quits only an idle thread's `codex` process (the lock's holder) |
| `Marker` | `mark` | `custom-title` record | thread name in `session_index.jsonl` |
| `AccountProber`, `Sanitizer` | `account`, `sanitize` | `claude auth status`; drop signed reasoning across accounts | Codex's own `account/read`; drop encrypted reasoning and compaction across accounts |
| `Reader`, `Writer` | `read`, `write` | both; native replay of exact shell calls | both; history as text |
| `Importer` | `import` | | Codex's own importer of Claude Code sessions |
| `PostInstaller` | `post-install` | | makes Codex list the session (`thread/read`, `thread/name/set`) |
| `Integrator` | `integrate` | skill folder, permission rules | `~/.agents/skills`, a `.rules` file |
| `Notifier` | `notify` | the resumed session messages the old one | |

**The Host** is a module's only way to reach a machine (`agent.Host`: facts, FS, Exec, Path, Locks, Procs). Programs run with input on any machine, kept open until they answer (`codex app-server` stops at the end of its input); locks name their holders (`/proc/locks` on Linux, `lsof` elsewhere). The core confines it to the module's Spec: writes only under its roots and only through the undo journal, its secrets never opened (globs allowed), only its own binaries run. The machine probe is generated from all Specs and runs in one round trip (POSIX shell or PowerShell).

**Conformance.** `sdk/agent/agenttest` runs every module against a fake host: Spec sanity, listing (lineage files ignored), bundles, identity move plans, confinement, and Reader/Writer round trips. A new module adds fixtures from a real install of a tested version and passes the suite.

## 6. Discovery

**Machines.** Tailscale (`tailscale status --json`: DNS name, OS, online; phones and expired nodes dropped; shared nodes off by default) and `~/.ssh/config` aliases (following `Include`). An alias whose host name does not resolve is retried through the machine's Tailscale name, and the route is remembered.

**Probe.** `BatchMode=yes`, `ConnectTimeout`, strict host-key checking. A new host key goes to a trust screen, marked verified when it matches the key Tailscale reports or one you already trust; a changed key is a hard stop. One round trip returns OS, architecture, home, every module's root variables and binaries (path and version), and git.

**Sessions.** For each enabled module with a data folder there: `List`, then liveness, then the lineage manifests beside the sessions, then one batched git probe for every distinct folder (toplevel, remote, branch, upstream, unpushed, dirty, worktrees). Owed marks are applied here (§10).

**Grouping.** By repository identity (normalized remote; a checkout without a remote is grouped by path), then copies of one session (by lineage, across agents and machines, or by key) merge into one item shown by its newest copy. `hopsesh ls --json` prints `{"machines": [...], "groups": [{"identity", "name", "remote", "localCheckout", "items": [{"entry": {...}, "copies": [...]}]}]}`; an entry has `machine`, `agent`, `agentName`, `session` (with `key: {agent, session}`), `live`, `git` and `lineage`.

## 7. Moving a session in the same agent

1. **Plan:** the repository (match by remote among local checkouts, clone into the repos folder, or a folder you choose; worktree `auto|create|main`), the target agent installed here, the live state, the account (another account sanitizes, per the module), copies already here (§10), the code (§12), and the mark.
2. **Copy:** every bundle file over SFTP into a staging folder; a file still growing is copied until its size settles.
3. **Rewrite:** the module's policy over JSONL files: a one-pass prefix map (repository, worktree, home, data roots; separators converted across Windows and POSIX), protected keys untouched (signatures, encrypted content), records dropped or appended as the module says. The secret scan runs here, with optional redaction.
4. **Verify:** the module checks the staged result.
5. **Install:** copies here that make way are set aside (restorable by `undo`), files placed with a fresh modification time (agents clean up old sessions by date), lineage written beside both copies, the module's post-install step, the copy left behind marked.
6. **Launch:** the module's resume command, with a first prompt telling the agent it was moved and asking it to check the repository, files, tools and environment.

## 8. Continuing in another agent

**The IR** (`sdk/ir`). A session is a chain of nodes (user and agent messages, reasoning, tool calls with structured intents such as shell or patch, tool results, plans, attachments). Each node's id is a content hash: RFC 8785 canonical JSON of what the model saw, chained to its parent, hashed with SHA-256 (tool inputs as their exact bytes). Readers lift a session from any cursor, so a later read gets only what is new.

**Conversion** (`internal/core/convert`). The target module's `Writer` declares a profile (context window, native replay). The default fidelity is **history**: the conversation as text, tool activity tagged as the prior agent's, long outputs shortened, the oldest steps summarised when the history exceeds 30% of the target's window. **note** sends only the briefing. `--native` replays exact shell calls as the target's own where its writer can. Reasoning from the other vendor is never carried. Roles alternate as both formats require.

**The loss report** says what was kept and what was not (messages, tool calls as text, reasoning dropped, outputs shortened, steps summarised, paths mapped), and the plan shows it before anything is written.

**The briefing** is a user note at the end of the history, followed by a short acknowledgement: where it came from, that the tool names in the history are not the target's, what to verify first (git status, the commit at transfer time), the open plan, instruction files only the other agent read, an optional handoff note the source agent wrote (`--note-file`), and with `--carry-rules` the user's instructions for every project of the source agent (otherwise they are reported). No prompt is sent unless `--go`.

**Another route:** `--via import` lets the target agent's own importer convert the session (Codex imports Claude Code sessions); hopsesh adds its briefing to the result and journals the new thread for undo.

**The native copy.** Continuing on another machine also keeps the source agent's own copy of the session there, byte for byte and marked, when that agent is installed there. A later return to it on that machine then adds only the new work.

## 9. Interfaces

**CLI:**

```
hopsesh                                        # interactive TUI
hopsesh ls [--host H] [--agent A] [--repo R] [--live] [--no-git] [--limit N] [--json]
hopsesh show [<machine>:][<agent>/]<id|title>
hopsesh agents                                 # modules, capabilities, what is installed here
hopsesh plan [<machine>:][<agent>/]<id|title> [flags]     # never writes
hopsesh pull [<machine>:][<agent>/]<id|title> [--in A] [--fidelity history|note] [--native]
          [--via import] [--note-file F] [--carry-rules] [--go] [--to DIR] [--clone]
          [--worktree auto|create|main] [--fork] [--rc] [--notify] [--redact] [--app]
          [--stop-local] [--keep-both|--replace] [--no-sync] [--no-mark] [--push] [--run]
hopsesh push [<agent>/]<id|title> <machine> [same choices]
hopsesh receive [on|off]
hopsesh hosts [allow|deny|add [--password]|auth|setup-key] · hopsesh trust <machine> · hopsesh doctor
hopsesh skill [install|remove] [--add-rules] [--force]
hopsesh undo [<id>] [--list] [--force]
hopsesh update [--check] · hopsesh version
```

Listing and moving commands take `--json`; `--password-stdin` supplies a password machine's password (§16). Install with `curl -fsSL …/install.sh | sh` or `irm …/install.ps1 | iex`; both verify checksums and the release signature.

**GUI:** three panes. On the left, scopes: **Needs you** (sessions waiting for an answer), all sessions, this Mac, each machine (with its state) and each agent, then Activity and whether this Mac receives sessions. In the middle, sessions by repository, each with its state (Needs you, Working, Idle, Ended, Moved), its other copies and its main action. On the right, the selected session: **Resume** (in a terminal or the agent's app), **Hop here** / **Hop back**, **Continue in…** another agent, **Send to…** a machine with hopsesh, then its repository, last prompt, copies and where it has been. An action opens the plan as a sheet: a summary of what is added, changed and removed; the conversation (whole or a briefing, carried / changed / left out, a note, the briefing itself, your instructions, the importer, native replay); the repository and code; what blocks it, with the buttons that fix it (quit the copy here, keep both, replace, clone, choose a folder); and the options the target agent supports. Progress shows in the sheet; the result page has the start command, Terminal or the agent's app, the loss report and Undo. **Activity** lists every operation with Undo (asking before it throws away work done since) and the marks still owed. **Machines** adds, checks, logs in to and removes machines, lists what discovery found, and switches receiving. **Settings** has tabs: General (repos folder, layout, defaults, folders), Agents (on or off, capabilities, remote control, the importer), Skill, Command line and Updates. ⌘K finds any session or command; ↑↓, ↩ and ⌘↩ work in the list; the Session menu has ⌘1–3, ⌘R and ⌥⌘Z (undo the last hop). Agents are pictured by their installed desktop app's icon, read on this machine and never shipped (`app_icons`, on by default), else the module's own mark (`Spec.Icon.SVG`, drawn for hopsesh, never a vendor's logo), else their initials. An older configuration is set aside on request (§9 config).

**Config:** `config.toml` in `~/.config/hopsesh` (`%APPDATA%\hopsesh`), schema 3: `repos_dir`, `layout`, `mark_moved`, `sync_code`, `push_source`, `update_check`, `app_icons`, `[agents.<id>]` (`disabled`, `remote_control`, `import`), `[peer] receive`, and the machines. A file in an older format is refused, never migrated; the app offers to set it aside and start fresh. State lives in `~/.local/state/hopsesh` (`%LOCALAPPDATA%\hopsesh`): the audit log, undo journals, staging, owed marks, hopsesh's own `known_hosts`, routes and start prompts. `HOPSESH_CONFIG_DIR`, `HOPSESH_STATE_DIR` and `HOPSESH_MACHINE` (this machine's name in marks and lineage) override.

## 10. Round trips and lineage

**Lineage manifests.** Beside each session hopsesh touched (`<session>.hopsesh.json`, ignored by the agents) is a manifest: a logical session id, every replica (agent, key, location, head node, byte offset, version), the hops between them (move or continue, fork, fidelity, the byte range hopsesh wrote), and loss reports. Every move updates it on both ends, so the next hop works from any machine, even one that never ran hopsesh before. Because node ids are content hashes, a lost manifest can be recomputed from the transcripts.

**Marks.** The copy left behind is retitled in its agent's own list ("↪ moved to studio · title", "↪ continued in Codex on studio · title"), with its modification time kept. A copy still open is marked once it ends: the owed mark is recorded and applied by a later scan, unless the copy grew in the meantime.

**Same agent, coming back.** The copy here is compared with the incoming one, by lineage when there is one:

| Copy here | What happens |
|---|---|
| unchanged since it left | replaced; the old one stays available to `undo` |
| changed since it left, or newer | **conflict**, refused by default: `--replace` sets it aside, `--keep-both` brings the incoming one in under a new id; never merged |
| open here | refused, or `--stop-local` quits it first |

**Another agent, coming back.** The relation between the source and the copy in the target agent here decides: **new** (no copy: a new session), **append** (the copy is as it was left: only the new work is rendered and appended, its own turns stay byte for byte, its title replaces the mark), **same** (nothing new), **behind** (only the copy here changed), **diverged** (both changed; `--keep-both` starts a separate session).

## 11. Peers: working with hopsesh on the other machine

hopsesh pulls by default, and the source machine needs only SSH. When the other machine runs hopsesh too, the two talk over the same SSH connection: `hopsesh peer --stdio` (started by `ssh`, found on the PATH or in the usual install places) answers a versioned protocol, one JSON object per line. Both ends must speak the same protocol version; a mismatch is refused with "update hopsesh", never bridged.

**Push.** `hopsesh push <session> <machine>` sends a session on this machine to a machine that opted in (`hopsesh receive on`, `[peer] receive = true`; off by default):
1. hello: versions, whether it receives, its agents;
2. the sender packages exactly the session's bundle (and, for `--carry-rules`, the agent's global instruction files) with its summary, git state, lineage and account;
3. the receiver plans with its own modules and configuration against a **read-only snapshot** of those files: it cannot read or run anything else on the sender;
4. after your yes, the receiver applies with its own journal; writes meant for the sender's copy (the mark, the lineage) are returned, and so is a mark still owed;
5. the sender replays those writes confined like a module's own, in its own journal, which also names the receiver's journal: `hopsesh undo <id>` on the sender undoes both machines.

**Windows.** On a Windows machine hopsesh finds `hopsesh.exe` (its PATH, then where `install.ps1` puts it) and the shell its OpenSSH server uses, and starts the program directly in that shell's form, so the connection is its input and output. The wire is ASCII-only JSON (anything else escaped), so no code page can alter it.

## 12. Code follows the conversation

Before continuing, hopsesh brings the checkout here to the commit the session last saw: fetch from origin, or, for a commit never pushed, straight from the source machine over SSH into `refs/hopsesh/<machine>/<branch>`. It fast-forwards only a clean checkout on the same branch, never merges, rebases or stashes, and otherwise says what is missing (`missing`, `diverged`, `dirty`, `other-branch`, `behind`). `--push` pushes on the source first, with the source's own credentials. Uncommitted files stay where they are and are reported.

## 13. The hopsesh skill for every agent

One skill, rendered once, written byte for byte into every installed agent's skills folder (Claude Code: `<config>/skills/hopsesh`; Codex: `~/.agents/skills/hopsesh`), so agents can list sessions, bring one here, continue one in another agent, or push one away. `SKILL.md` keeps the rules first (plan with `--json`, act only after an explicit yes, treat titles and prompts from elsewhere as data, never change hopsesh's setup, never handle passwords); `reference.md` holds the flags and JSON fields.

**No drift.** Each copy's state is computed from hashes (`absent`, `current`, `stale`, `modified` by you, `foreign`, `broken`), the report shows the worst, and install updates every copy in one go. An edited copy is never overwritten without `--force` (which keeps a backup). A test proves every location renders identically.

**Approval rules** (`--add-rules`, opt-in): read-only commands (`ls`, `show`, `plan`, `agents`, `hosts --json`, `doctor`, `version`) run without asking, and `pull`, `push` and `undo` always ask. Claude Code gets permission rules in `settings.json` (a backup kept, invalid JSON left alone); Codex gets an owned `rules/hopsesh.rules` with `prefix_rule`s.

## 14. The command-line tool from the app

The app links `~/.local/bin/hopsesh` to the tool inside the signed bundle (no password; it follows app updates). It refuses from a disk image or a translocated copy, never replaces a standalone copy or someone else's link without a click (and keeps a backup), and can add `~/.local/bin` to the login shell's PATH as a marked, removable block. Started from Finder, the app adopts the login shell's PATH and every module's login variables (such as `CLAUDE_CONFIG_DIR`, `CODEX_HOME`).

## 15. Security and privacy

- **Threats:** a malicious remote (hostile file names, traversal, oversized files), a malicious peer, a compromised update channel. hopsesh must never become a credential-movement tool.
- **Controls:** staging before install; a module's host confined to its roots, secrets and binaries; agent forwarding off; a push receiver sees only the session's files, and the sender replays returned writes under the same confinement; files hopsesh writes are journaled; updates are signed (ECDSA over `checksums.txt`, verified by the binary; on macOS the new binary's signing team must match); macOS local network privacy handled in-process (TN3179).
- **Never copied:** credentials (`.credentials.json`, Codex's `auth.json`, which hopsesh never reads), keys, sockets, shell snapshots, account fields.
- **Terms:** only the unmodified agents call their models, under each machine's own login.

## 16. Password login

Keys are the default, but some machines only take a password. Such a machine is marked (`hosts add … --password`, `hosts auth <m> password`, or the app's Login button); only `auth = "password"` reaches the config. ssh asks hopsesh through `SSH_ASKPASS` (its own binary, over a per-connection socket with a random token), and hopsesh answers only account-password prompts: never key passphrases, host-key questions or git's HTTPS prompts. The password comes from the macOS Keychain (per machine, on by default), a hidden terminal prompt, the app's dialog, or `--password-stdin`, and lives in memory for the run. A refused password is forgotten and asked again, twice at most. `hosts setup-key <m>` logs in once, adds this machine's public key there, proves key login works, and only then switches the machine to keys.

## 17. Testing

- **Unit:** canonical hashing and node ids, slug and path rules, the rewriter (escaping, Windows paths, partial prefixes, protected keys), the converter (budget, roles, briefing), journals (including undo of an append after the agent kept writing), snapshots, the peer protocol (hello first, versions, refusals).
- **Conformance:** `agenttest` on both modules, with fixtures from Claude Code 2.1.284 and Codex 0.153.2.
- **End to end** (`internal/e2e`): moves, conflicts and keep-both; Claude ↔ Codex continuations with the round trip and the native copy; `--carry-rules`; the skill in every agent; a push to a real `hopsesh peer --stdio` process, with undo on both sides and refusal.
- **Desktop backend:** the window's calls end to end (scan, continue, round trip, Activity and undo with and without force, sending to another machine, machines and receiving, starting fresh from an old configuration).
- **Layering:** `internal/archtest`.
- **Over real SSH** (Linux CI, a second local user behind the runner's sshd): `integration-test.sh` (pull, continue in Codex, push and undo both sides, refusal), `roundtrip-test.sh` (code follows, marks, hop back, conflict, keep-both), `password-test.sh` (a separate password-only sshd).
- **Against the installed agents**, opt-in: `HOPSESH_REAL_AGENTS=1 go test ./internal/e2e` (Codex lists what hopsesh writes; Codex's importer route; Codex's login; quitting a real Codex TUI found by its lock), and `HOPSESH_PAID_SMOKE=1 scripts/paid-smoke.sh`, a real Claude Code → Codex → Claude Code round trip with three short model calls.

## 18. Distribution, signing, updates

- **CI** (GoReleaser on a `v*` tag): Linux and Windows archives, packages, SBOMs, build provenance, a draft release.
- **The maintainer's Mac** (`scripts/release-sign.sh`): verifies the provenance, builds the macOS CLI and app from the tag, signs (Developer ID, hardened runtime), notarizes and staples, writes and signs `checksums.txt`. No signing secret is in GitHub (`docs/RELEASING.md`).
- **Updates:** `hopsesh update`; the app asks once whether it may check GitHub daily.
- **Planned:** a Homebrew tap; Windows GUI signing through SignPath Foundation.

## 19. Status

**Built:** the module SDK with Claude Code and Codex modules; moves, continuations both ways (history, note, native replay, Codex's importer), the briefing and loss report, `--carry-rules`, the native copy; lineage manifests, marks and owed marks, round trips with append, conflicts and keep-both; code sync; peers and push; the skill in every agent with approval rules; CLI, TUI and the macOS app; password login; self-update; the release pipeline. Released: 0.1.0 and 0.2.0 (Claude Code only); this design is 0.3.0.

**Planned:**
- more modules, Reader first: OpenCode, Hermes, Gemini CLI, Goose, Amp, Crush, Cursor CLI;
- sessions that live in an agent's cloud, as a module capability through the agent's own CLI;
- a Windows GUI; a Windows OpenSSH CI job;
- quitting sessions and probing locks on Windows (no graceful signal there yet);
- a compatibility matrix of pinned agent versions.

## 20. Risks and mitigations

- **Format drift in the agents:** each format is isolated in its module, every move is verified before install, fixtures come from pinned versions, and writers of a format hopsesh has not tested are marked experimental.
- **Vendors ship their own transfer:** hopsesh can call it (`--via import` already does) and still adds discovery across machines, the repository, an honest loss report and the way home.
- **Agents tolerate anything:** both accept a converted session without checking where it came from, so hopsesh supplies the discipline: declared fidelity, a loss report, never carrying another vendor's opaque reasoning, never regenerating native turns.
- **Wails v3 beta:** the GUI stays thin, with Tauri v2 as the fallback.
- **Secrets in transcripts:** scanned while moving, counted, optionally redacted, sent only to your own machines.

## 21. Decisions

From 2026-10-02 (0.1 and 0.2): name **hopsesh**; **Apache-2.0**; **Go + Bubble Tea v2 + Wails v3**; GUI on macOS first; Apple Developer ID, SignPath for Windows; repos folder `~/git`, flat layout.

The redesign (approved 2026-10-02):
1. 0.2.0 tagged before the redesign.
2. Older configuration is refused and set aside, never migrated.
3. The SDK is public (`sdk/`), in the same module, v0.
4. Modules are compiled in.
5. Pull by default; peers over SSH when the other machine runs hopsesh, push only to a machine that opted in.
6. Default fidelity: history within 30% of the target's window, plus the briefing; `--fidelity note` one flag away.
7. hopsesh's writer is the default Claude → Codex route; Codex's importer is `--via import`.
8. Native replay is a module capability; the app offers it for every target that declares it.
9. The source agent's own copy is kept next to a continuation (the native copy): a round trip resumes the latest state, and the earlier turns come back byte for byte.
10. Lineage lives in a manifest beside each session and travels with it; content-hash ids let it be recomputed.
11. Canonical hashing: RFC 8785 + SHA-256.
12. The local journal and audit log are a cache and a trail, not the source of truth.
13. The briefing is an in-transcript note plus an acknowledgement; no automatic prompt (`--go`).
14. Codex lists installed sessions at once (on for this machine).
15. One skill, identical bytes in every agent's folder, with per-copy status and no drift.
16. Global instructions are reported; `--carry-rules` carries them.
17. Next modules Reader-first (OpenCode, Hermes, Gemini, Goose, Amp, Crush, Cursor); "coding-agent sessions" wording with a notice naming both vendors.
