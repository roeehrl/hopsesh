# Plan: first-class OpenCode support in hopsesh (for approval)

Baseline: hopsesh `main` @ 011555f. OpenCode CLI 2.3.0 (Homebrew) and OpenCode Desktop 2.2.7
(Electron, `ai.opencode.desktop`) on this Mac. Nothing below is implemented yet.

This revision applies two reviews of the first draft: a parity and completeness review against the
hopsesh source, and an accuracy review against the OpenCode 2.3.0 source and a copy of the local
database. Where a reviewer was not followed, the reason is given in §9.

---

## 0. Decisions needed from you

Each has a recommendation. Implementation starts once these are answered.

| # | Decision | Recommendation |
|---|---|---|
| D1 | Support OpenCode 1.x (1.19.x, still "Latest" on GitHub)? | Not in this round. 1.x shows as "unsupported version". Optional read-only PR 12 later. |
| D2 | Resuming with `opencode -s <id>` from a binary whose version differs from the running background service **restarts that service** (it is shared with the desktop app and interrupts any running turn). When no binary of the service's version is available, what should hopsesh do? | Warn and ask before resuming ("this restarts OpenCode's background service"). Use the desktop's staged CLI of the matching version when it exists, so the question rarely comes up. |
| D3 | Terminal tabs that hopsesh opens itself: attach to the shared service (sessions show live in the desktop app), or run `--standalone` (a process hopsesh owns and can stop, like Codex `--no-daemon`, but a second writer on the same DB)? | Attach to the shared service. |
| D4 | "Return into an existing OpenCode session" (PortableAppend). The only way is delete + re-import, which also loses the slug, instruction state and inbox, and may close an open desktop tab. Ship now, or start with "return creates a new linked session"? | Start with a new linked session. Delete + re-import as optional PR 11. |
| D5 | Fork on resume: pre-fork through the API and resume the child in the full TUI, or use `opencode mini -s <id> --fork` (minimal UI)? | Pre-fork through the API. |
| D6 | Context capacity: OpenCode's config file holds plaintext API keys here. Use only the session's model plus the models.dev catalog cached in the DB (custom/local models such as your `omlx` provider then get the fallback window), or add a reader that extracts only `model` and `limit.*` fields from the config (local and remote)? | Catalog only for the first release. Add the field-picking reader later if the fallback window is a problem. |
| D7 | Account probe: run `opencode auth list --format json --standalone` (source-verified to print no secrets; connection labels are user-written text) to show which providers are signed in? Account identity stays empty, so moves are never treated as "another account". | Yes. |
| D8 | OpenCode profiles (separate data, state and config dirs): the background service always binds port 49374, so a second profile's service fails while the default one runs. Give each profile its own port (`opencode service set port <n>` in the profile), or disable the service for profiles (each client then runs a private server)? | Own port per profile. |
| D9 | Test fixtures: may I (a) export and scrub your two existing OpenCode sessions, and/or (b) run sandbox sessions against your local `omlx` model (model calls on your LAN server)? | (b), with (a) only if (b) misses shapes. |
| D10 | Stability: hopsesh has only `Experimental` and `Stable` (no Beta). Ship as `Experimental`, flip to `Stable` after the nightly real-agent job is green for two weeks and the manual checklist (§7) passes? | Yes. |
| D11 | "Open in desktop app" for a session: OpenCode Desktop forwards `opencode://` links but nothing consumes a session link. Accept that hopsesh omits it (it still detects the app and shows imported sessions there)? | Accept. |

---

## 1. Summary and parity table

**Summary.** OpenCode 2.x keeps every session in one SQLite file, `~/.local/share/opencode/opencode.db`
(WAL mode, event-sourced, 59 migrations), and runs one shared background service (`opencode serve
--service`) used by the CLI, the TUI and the desktop app. hopsesh's SDK and core assume one file per
session, so the plan has three parts:

1. **An SDK and core contract for database-backed sessions** (PR 0, 1a, 1b):
   - a read-only, table-confined SQL capability on `Host`;
   - "virtual" bundle files that the module materialises;
   - a vendor-session journal op (delete, restore, stamp) so undo works for sessions that live in a DB;
   - a module stamp in place of file size and checksum for "used since" guards and caches;
   - a session-id minter, so the core uses `ses_…` ids instead of UUIDs;
   - an install path where the vendor import runs before lineage is recorded and its failure is fatal;
   - "env + subpath" roots and a main-root accessor in place of the hard-coded `Root("home")`.
2. **A new module `agents/opencode`**:
   - **Reads** locally with SQL from an allowlist of tables. Remotely it reads with `sqlite3`, else
     `python3 -I`, else `opencode api` / `session export`. It never copies the DB file.
   - **Never writes the DB.** Every write goes through OpenCode: `session import|delete` and
     `opencode api …` (rename, move, fork, interrupt).
   - **Never starts or restarts the shared service.** It probes with `opencode service status` and
     passes `--standalone` when the service is stopped. Resume commands use a binary of the same
     version as the running service.
   - **Protects sessions with a hopsesh-owned OpenCode plugin**, which plays the role of the Claude
     and Codex hook files. It blocks tool calls in a stale copy, not the prompt.
3. **The N-agent refactor** of app, CLI, TUI, GUI, test matrices and CI, which today assume two agents.

**Parity table.** Full = same as the best of Claude and Codex; Partial = works with a stated loss;
N/P = not possible; ✗ = intentionally not done.

| hopsesh capability | Claude Code | Codex | OpenCode (planned) | Why / how |
|---|---|---|---|---|
| List sessions (local + remote) | Full | Full | **Full** | `session_v2` ⋈ `project`. Child sessions (`parent_id`) are counted as Subagents and not listed, as Codex skips subagent threads. Archived sessions are a Kind. Remote: §3.3. |
| Preview / last prompt / title | Full | Full | **Full** | Last *n* settled messages with `session_part` merged (F5). TitleSource: OpenCode has no custom-vs-generated flag; report Generated, Untitled when the title matches OpenCode's untitled fallback, and Custom when hopsesh set it (recorded in the sidecar). |
| Rename | Full | Full | **Full** | `opencode api PATCH /api/session/{id}` `{title}`. OpenCode generates titles only while a session is untitled, so a rename sticks. |
| Live / busy detection | Full | Full | **Partial** | Signals: the execution claim `time_suspended` (set when any turn starts, kept when the service shuts down mid-turn, F22), `time_busy` after `time_idle` (2.2.2+), and `GET /api/session/active` (the service's running turns, source-verified), asked only when a row has a turn in flight. With the service stopped a claimed turn is either running in a `--standalone` window or suspended; nothing tells which. An idle TUI or a desktop window showing the session cannot be seen; TUI processes are not listed (the Host has no process listing). The move preview warns about this. |
| Stop | Full | Partial (never kills app) | **Partial** | `POST /api/session/{id}/interrupt`. Never SIGTERM the service. A TUI attached to the service is only a client, so it is not signalled (except a `--standalone` TUI hopsesh launched itself). `ErrSharedServer` semantics as for Codex. |
| Move to another machine | Full | Full | **Full** (with stated losses) | Export (parent + children) → JSONL bundle → `session import --directory <cwd>` on the target. Lost: `/undo` snapshots, fork fields, instruction state, slug, job rows, session-level times, archived state. Shown in the move preview. |
| Path rewrite | Full | Full | **Full** | The existing JSONL rewriter runs over the bundle. `project_id`, directory and slug are recomputed by import. |
| Repo / worktree setup | Full | Full | **Full** | Generic core. OpenCode creates its worktrees outside the repository, under `{data}/worktree/<project id, 6 chars>/…` (or `worktree.directory`), so `Spec.Worktrees` (folders inside a repository that git status ignores) does not apply. |
| Change dir in place (`cd`) | Full | Full | **Full** | `api POST /api/session/{id}/move {directory}`. The move is queued and applied at a step boundary; hopsesh confirms by waiting for a `location-switched` message. In-session hint: `/cd <dir>`. Note the agent itself can move its session with the `session_move` tool. |
| Resume / continue command | Full | Full | **Full** | `<bin> [<dir>] -s <id> [--prompt …]`, where `<bin>` matches the running service's version (§3.4, D2). Printed only after hopsesh confirms the session exists, because `-s` creates an empty session for an unknown id. |
| Fork on resume | Full | Full | **Partial** | No `--fork` for the full TUI. Pre-fork with `api POST …/fork`, then resume the child (D5). CapFork only after the sandbox check of fork message ids. |
| Protection (stale-copy guard) | Full (hooks) | Full (hooks + trust) | **Partial** | Plugin under `{config}/plugins/hopsesh/` blocks tool calls (`execute.before` throws) and adds a notice instruction. OpenCode has no typed prompt rejection, and a rejected tool call is an error the model may work around. |
| Skill install | Full | Full | **Full (no extra install)** | OpenCode reads `~/.agents/skills` and `~/.claude/skills`. Duplicate-skill behaviour UNVERIFIED (§3.6). |
| Allow-rules for hopsesh commands | Full | Full | **Full** | Plugin `permission.hook("evaluate")`: Allow entries → `allow`, Ask entries → `ask`. The user's `opencode.json` is never edited. |
| Accounts / multiple profiles | Full | Full | **Partial** | Profile = own `XDG_DATA_HOME`, `XDG_STATE_HOME`, `OPENCODE_CONFIG_DIR`, plus its own service port (D8). Sign-ins are per provider, not per person. |
| Account probe | Full | Full | **Partial** | Provider list from `auth list --format json --standalone` (D7). `Account.Key` stays empty, Confidence limited. Never the `credential`, `account`, `control_account`, `account_state` tables, never `auth export`. |
| Capacity / usage | Full | Full | **Full (context) / Partial (plan)** | Model from `session_v2.model`; window from the `kv` models.dev catalog, else fallback (D6). Usage from `session_v2.tokens_*`. Plan meters only for OpenCode Go. |
| Desktop app awareness | Full | Full | **Partial** | Detect `/Applications/OpenCode.app` and its processes. Imported sessions appear in the desktop because it shares the DB. No session deep link (D11). |
| Lineage / forks / undo | Full | Full | **Partial** | Lineage sidecar beside a hopsesh anchor file (§3.5). Native forks (`fork_session_id`, `fork_boundary`) are read locally, but import does not carry them, so hopsesh records the fork in its sidecar before a move. Undo deletes the imported session, guarded by a stamp so work done since is never deleted. |
| Cloud hand-off from OpenCode | n/a | n/a | **Full (to Claude / Codex clouds)** | Existing pipeline: Reader → IR → cloud driver. |
| OpenCode's own cloud / share | — | — | **N/P** | Sharing discontinued in 1.19.1, GitHub Action removed in 2.2.0, no hosted agent. |
| Continue Claude/Codex → OpenCode | Full | Full | **Full (new session)** | Writer builds a transfer and imports it. NativeReplay for shell only, as the converter defines. |
| Continue OpenCode → Claude/Codex | — | — | **Full** | Reader lifts messages and parts to IR. |
| Return into an existing OpenCode session | Full | Full | **Partial, phase 2** | No append API. New linked session until PR 11 (D4). |
| Peer push (hopsesh ↔ hopsesh) | Full | Full | **Full** | Virtual files are materialised into the package; the receiver reads the JSONL. |
| GUI / TUI / CLI | Full | Full | **Full** | After the N-agent refactor (PR 0). |
| Client API (`docs/client-api.md`) | Full | Full | **Full** | Agent enum gains `opencode`. |
| OpenCode 1.x installs | — | — | **✗ in this round** | Different tables, CLI and plugin API (D1). |

---

## 2. OpenCode facts the design relies on, and version constraints

Abbreviations:
- `src:` = github.com/anomalyco/opencode at tag v2.3.0 (a6b15d33), checked out under `$S/src/oc`.
- `local:` = observed on this Mac.
- `$S` = `/private/tmp/claude-501/-Users-tnt-git-hopsesh/6149fe06-a1ca-4a87-9fa0-af58e0246275/scratchpad/opencode`

| # | Fact | Source | Status |
|---|---|---|---|
| F1 | Repo moved from `sst/opencode` to `anomalyco/opencode`. Two release lines: 1.x (v1.19.5, "Latest" on GitHub) and 2.x (2.3.0, 2026-11-13). | `gh repo view`; `gh api releases/latest` | verified |
| F2 | Paths: data `$XDG_DATA_HOME/opencode` (default `~/.local/share/opencode`), config `~/.config/opencode`, state `~/.local/state/opencode`, cache `~/.cache/opencode`, on **every OS** (no platform branch; `os.homedir()` based). `OPENCODE_CONFIG_DIR` replaces the config dir. `opencode debug paths` prints them. | `src:packages/util/src/global-roots.ts`; `local:` | verified (macOS), source-verified (Windows) |
| F3 | DB file: `$OPENCODE_DB`, else `opencode.db` for latest/dev/beta/next/prod channels, else `opencode-<channel>.db`. WAL mode. A read-only open works while the service runs. | `src:packages/cli/src/database-path.ts`; `local:` | verified |
| F4 | A session is a `session_v2` row plus `session_message` rows (unique `(session_id,seq)`), `session_part` rows (`(message_id,ordinal)`), optional `blob_ref`/`blob`, `instruction_state`/`instruction_blob`, `event_sequence`, and `job` rows. Children have `parent_id`; forks have `fork_session_id` and `fork_boundary`. | `src:packages/core/src/session/sql.ts`; `$S/db/schema.sql` | verified |
| F5 | Assistant content may be inline in `session_message.data.content`, in `session_part` rows, or both: part rows first by ordinal, then inline items. | `src:…/session/projector.ts:439-700` | source-verified; local data has only the inline form |
| F6 | Message types: `user, assistant, synthetic, system, skill, shell, compaction, idle, agent-switched, model-switched, location-switched`. The bus events are named `session.agent.selected` / `session.model.selected`, which explains an earlier conflicting report. | `src:packages/schema/src/session-message.ts:46-65`; OpenAPI | verified |
| F7 | Tools: `shell, read, edit, write, patch, glob, grep, subagent, skill, webfetch, websearch, question, todo, schedule, artifact`; Code Mode `execute` (JavaScript; MCP tools are reached through it by default, permission action `<server>_<tool>`); `session_rename, session_move, worktree_create/list/remove, models`; `list_mcp_resources, read_mcp_resource`; image generation. read/edit/write take `path`; shell takes `{command, workdir?, timeout?, background?}`. | `src:packages/core/src/tool/plugin/*.ts`, `opencode.ts:115-235`; docs `tools.mdx:229-310` | verified |
| F8 | IDs: `ses_` + 12 hex of `~(ms·0x1000+counter) mod 2^48` + 14 base62 (newest first); `msg_` same without the NOT (ascending). Import only checks the `ses`/`msg_` prefix; seq comes from array order. | `src:…/session-id.ts`; `session-message.ts:21`; `transfer.ts:83-93` | verified |
| F9 | Project id: git → `sha1("git-remote:"+lower(host)/path)` of origin without `.git`; else `<git-common-dir>/opencode`; else root commit; else `global`. No VCS → `sha1("directory:"+abs)`. SSH host aliases give different ids per machine; harmless, since import recomputes it. | `src:packages/core/src/project.ts:309-457` | verified |
| F10 | CLI 2.3.0: `opencode [dir] -s/--session <id>` (creates the session if no session has that id; id must start with `ses`), `-c`, `--prompt`, `--standalone`, `--server URL`. Most subcommands accept `--standalone`/`--server`; `service *`, `debug paths`, `upgrade` and `uninstall` take neither. | `local:` help; `src:packages/cli/src/session-target.ts`, `commands.ts:129-142,500-520` | verified |
| F11 | `session export [id]` prints `{info, messages}` with settled messages only and no `seq`. `session import <file> --directory D` keeps the session id, recomputes `project_id`, directory, slug and version, numbers seq `index+1`, and needs fresh `msg_` ids for a clone (message id is a global PK). It publishes `session.created` with only `parentID, title, agent, model, metadata, permissions`, then inserts messages. **On an existing id it prints "Session already exists" on stderr and exits 0.** The import runs in one DB transaction. `session delete <id>` also deletes children. | `src:…/session/transfer.ts:77-122`; `handlers/session/import.ts:49-52`; `core/src/bus.ts:316-404`; `local:` sandbox | verified |
| F12 | HTTP API through `opencode api <METHOD path> [-d json]` (auth handled by the CLI): `PATCH/DELETE /api/session/{id}`, `POST …/move {directory, delivery?}` (queued, applied at a step boundary; directory must exist on the service's machine), `POST …/fork {before?}`, `POST …/interrupt`, `GET /api/session/active`, `GET /api/session`, `GET /api/session/{id}/message` (projected, parts merged), `GET /api/info`, `GET /api/plugin`, experimental instructions `PUT /api/experimental/session/{id}/instructions/entries/{key}`. | `$S/openapi.json`; `src:core/src/session/move.ts` | verified (shapes); move/fork not run |
| F13 | **Service.** One shared `serve --service` on port 49374 (28894 on Windows). Registration `{state}/service.json` (`service-<channel>.json` off the main channels), mode 0600, holds a password. `opencode service status` prints the URL or `stopped` and starts nothing. **Every subcommand except `run` auto-starts the service when it is stopped** (`ServerConnection.resolve()` → `Service.ensure`). Subcommands use `mismatch:"ignore"` and reuse a service of another version. **The TUI and `mini` use `mismatch:"replace"`: they SIGTERM a service of another version and start their own.** The desktop app also replaces a service of another version, and reconnects to any version afterwards. | `src:packages/cli/src/services/server-connection.ts:35-99`; `client/src/effect/service.ts:49-185`; `handlers/default.ts:35-38`; `mini.ts:13-14`; `api.ts:21-22`; `handlers/service/status.ts`; `server-process.ts:73`; `server/src/process.ts:151-158`; `desktop/…/cli-version.ts:16`; `local:` logs | verified |
| F14 | `--standalone` runs a private `serve --stdio --port 0` child that exits with the command. `--server` takes its password only from `OPENCODE_PASSWORD`/`OPENCODE_SERVER_PASSWORD`. | `src:services/standalone.ts`; `env.ts:10-11` | verified |
| F15 | Shell-tool child env: `OPENCODE=1`, `AGENT=1`, `AI_AGENT=opencode`, `OPENCODE_SESSION_ID=<id>`. | `src:…/tool/plugin/shell.ts:399-402` | verified |
| F16 | **Plugins (v2).** `export default Plugin.define({id, setup(ctx)})` from host-injected `@opencode/plugin`. Directories in `{config}/plugin(s)/` are auto-discovered; local plugins are not npm-installed; the entry is `server.*` or `index.*` at the dir root, or package.json `name`+`exports`. No user approval step, but managed policy `plugin.use: deny` blocks plugins and config remove entries match ids (`-hopsesh`, `-*`, `-prefix.*`). The plugin runs **inside the service process**. Context has `app, location, options, agent, event, permission, plugin, session, shell, storage, tool, …` and **no `log`**. `tool.hook("execute.before")` throw → that one call fails with `Tool.Error`. `permission.hook("evaluate")` can set `allow`, `ask` or `deny` + message, only for tools that assert a permission. Session hooks run in `Effect.promise`, so a throw is a defect. 1.x plugins do not run. | `src:core/src/plugin/{source-directory.ts,module.ts:52-54,106-112,hooks.ts:23,supervisor.ts:25-41}`; `plugin/src/{host.ts:25-58,promise/plugin.ts:28-59,promise/adapter.ts:632,688-698,promise/storage.ts}`; `core/src/permission.ts:183-205` | source-verified; `Plugin.define` on 2.1/2.2 UNVERIFIED |
| F17 | Events `session.created`, `session.moved`, `session.forked` exist. Import emits `session.created`. | `src:schema/src/session-event.ts:40,83,193` | verified |
| F18 | Skills are read from `~/.config/opencode/skills`, `.opencode/skills`, `~/.claude/skills`, `~/.agents/skills` and project equivalents. `OPENCODE_DISABLE_CLAUDE_CODE*` gate only the `.claude` roots. | docs `skills`; `src:server-process.ts:127-131`, `config/discovery.ts:150-170` | verified |
| F19 | Instructions: `AGENTS.md` (global `{config}/AGENTS.md` plus walk-up). v2 does not read `CLAUDE.md`. | docs `instructions.mdx:42` | verified |
| F20 | Credentials live in the DB (`credential.value` is plain JSON text; also `account`, `control_account`, `account_state`). `{config}/opencode.json` may hold plaintext provider keys (it does here). `auth list --format json` prints `{id,name,connections:[{type:"credential",id,label,method,status}\|{type:"env",name}]}`, no secret values. | `src:core/src/credential/sql.ts:5-14`; `handlers/auth/list.ts`; `schema/src/connection.ts` | verified |
| F21 | Desktop: `/Applications/OpenCode.app`, bundle `ai.opencode.desktop`, scheme `opencode://`. Stages its CLI at `~/Library/Application Support/ai.opencode.desktop/cli/<ver>-<arch>/opencode-cli`. `opencode://` URLs are forwarded to the renderer but nothing consumes a session link. UI state in `drafts.sqlite`. | `local:`; `src:desktop/src/renderer/startup/deep-links.ts` | verified |
| F22 | `time_suspended` is the execution claim: set when a turn starts (in any process, in the transaction of `Execution.Started`, which also sets `time_busy` from 2.2.2), released when it ends (with `time_idle`). A service shutdown keeps the claim and records no idle, and the next service start resumes the turn (`resume_attempts`); a process that dies keeps it too. So a claimed row is a running turn or a suspended one. `time_compacting` is not written by 2.3.0. `GET /api/session/active` lists the "foreground Session drains currently owned by this OpenCode process" (running turns, not attached clients). | `src:core/src/session/{store.ts:240-277,execution.ts:75-81,140-158,projector.ts:847,1064}`; `protocol/src/groups/session.ts:225` | source-verified |
| F23 | Usage: `session_v2.cost`, `tokens_*`; per-step tokens on assistant messages; `opencode stats --json`. Session info `model` is an optional `Model.Ref` (`{}` fails decoding); `projectID` is required but recomputed. No title-source flag. | `src:packages/schema/src/session.ts:32-63`; `session/title.ts:38-42`; `runner/llm.ts:202` | verified |
| F24 | Storage history: JSON files before 1.2.0; SQLite with 1.x tables from 1.2.0; 2.x adds `session_v2` and copies once (`kv` `migration.v1-v2`). `native` replaced the replay field in 2.1.0. | `src:…/v1-migration.bun.ts:541-765`; `CHANGELOG.md` v2.1.0 | verified |
| F25 | Migration cadence: 59 migrations; by month Jun 17, Aug 8, Sep 2, Oct 11; none since 2026-10-30 (`20261030054400_job`). | `$S/db2` copy; tag and dev | verified |
| F26 | Install methods: npm `@opencode/cli`; `curl -fsSL https://opencode.ai/v2/install \| bash`; `brew install anomalyco/tap/opencode-v2` (homebrew-core `opencode` is also 2.3.0). Docs: Windows package managers are not supported. mise older than 2026.10.9 installs 1.x. `OPENCODE_DISABLE_AUTOUPDATE=1` turns auto-update off. | docs `index.mdx:14-30`; `src:cli/src/services/updater.ts:608` | verified |
| F27 | Sharing discontinued (1.19.1). GitHub Action removed (2.2.0). No hosted agent. | changelog | verified |

**Version constraints**
- **Minimum: OpenCode 2.1.0** for both the binary and the **running service** (the plugin, import and
  API calls run in the service). The read side upgrades rows written before 2.1 as OpenCode does
  (`SessionLegacy.message/part`: `state`/`providerState` → `native`, `providerResultState` merged into
  the tool's `native`, `recent` → `metadata.recent`, `providerContext` → `native.messages`).
- `session_part` (2.2.1) and `session_v2.time_busy` (2.2.2) are read when the database has them; a
  database last opened by 2.1.x or 2.2.0 has neither.
- `Spec.Tested = ["2.2", "2.3"]`; the 2.1.0 floor is declared as a drift floor with its source file.
- **Schema gate (read side):** at open, check `PRAGMA table_info` for every table and column the module
  reads (`schema.go` manifest). Missing column → list nothing, show "OpenCode schema not recognised
  (latest migration X)". Extra migrations are allowed, logged, and flagged by the drift watcher. Every
  read goes through the gate; its verdict is kept per database and latest migration id.
- **Cross-version moves:** a newer export imported into an older service fails on unknown message
  types. Gate a move on target service version (`GET /api/info`, or the standalone binary's version)
  ≥ the source session's `version`.
- **Standalone with an older binary:** opening a DB written by a newer OpenCode from an older
  standalone server is UNVERIFIED. Use `--standalone` only with a binary whose version is ≥ the highest
  `session_v2.version` in the DB; otherwise refuse the write with a clear message.
- **1.x-only DB** (no `session_v2`): "OpenCode 1.x, not supported yet" (D1). Legacy JSON storage is
  ignored (OpenCode migrated it).

---

## 3. Architecture

### 3.1 Package `agents/opencode` (mirrors `agents/codex` and `agents/claude`)

| File | Contents |
|---|---|
| `opencode.go` | `Module`; `Spec()`; `Detect`; `List`; `Bundle`; `PlanMove`; `Verify`; `Resume`; `SessionWatchPaths`. |
| `spec.go` | • ID `opencode`, Name "OpenCode", Vendor "Anomaly" (verify the legal name), Stability `Experimental`, Tested.<br>• Binaries: `opencode` (VersionArgs `--version`).<br>• Roots: `data`, `config`, `state`, and on macOS `desktop` (`~/Library/Application Support/ai.opencode.desktop`) (§3.2). Main root: `data`.<br>• Secrets: `{state}/service*.json`, `{config}/service.json`, `{data}/auth.json`, `{config}/opencode.json`, `{config}/opencode.jsonc`, `{config}/config.json` (the legacy global config), `{data}/opencode*.db*` (so no file path, bundle copy or peer package can copy the DB; SQL access is separate, §3.3).<br>• `LoginEnv`: `XDG_DATA_HOME`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, `OPENCODE_CONFIG_DIR`, `OPENCODE_DB` (GUI started from Finder).<br>• `Tools` (feeds the briefing's tool names).<br>• Instructions `["AGENTS.md"]`; GlobalInstructions `{config}/AGENTS.md`.<br>• Worktrees: none (OpenCode's are outside the repository).<br>• Kinds `["archived"]`.<br>• TerminalEnv `OPENCODE_SESSION_ID`.<br>• `Icon.Apps`: `/Applications/OpenCode.app`. `DesktopScheme: "opencode"` (detection only, D11).<br>• `Accounts`: `RootEnv` (multi-var, §3.7), `Unset` (provider key variables, `OPENCODE_DB`, `OPENCODE_PASSWORD`, `OPENCODE_SERVER_PASSWORD`, `OPENCODE_SERVER*`), `Login` `opencode auth login`, `LoginElsewhere` text. Exact `auth login` syntax taken from the `--help` snapshot.<br>• `SQLFiles` `{data}/opencode*.db` and `{db}`; `SQLTables` allowlist and `SQLDenied` (§3.3).<br>• Features: CapFork only after verification; no CapApp. |
| `paths.go` | Resolves roots and the DB file (F2, F3); picks the channel DB. |
| `service.go` | `serviceState(h)`: `opencode service status` → URL or stopped; `GET /api/info` → version. `execArgs(...)` adds `--standalone` when stopped (and the binary-version check from §2), and sets `OPENCODE_DISABLE_AUTOUPDATE=1`. `resumeBinary()` (§3.4). |
| `schema.go` | Required-column manifest; `checkSchema(db)`; latest migration id. |
| `sql.go` | Typed queries over the Host SQL capability: `listSessions`, `sessionRow`, `messages(afterCount)`, `parts(msgIDs)`, `children(id)`, `projects`, `stamp(id)`. |
| `remote.go` | Remote read tiers (§3.3): `sqlite3` CLI, `python3 -I`, `opencode api`/`session export`. |
| `model.go` | Go types for `session_v2`, message envelopes and content items (F4–F7). `mergeParts` implements F5. `settled(msg)` applies the same predicate as `session export`. Unknown types kept as raw JSON. Two loaders into the same types: SQL rows, and export/JSONL. |
| `summary.go` | Row → `agent.Summary`: CWD = `directory` (native separators on Windows); Title, TitleSource (§1); LastPrompt; LastActivity = `time_updated`; AgentVersion = `version`; NativeParent = `fork_session_id`; Subagents = child count; Kinds archived; `Size` = settled-message count; `Path` = anchor (§3.5); GitBranch empty (OpenCode does not store it). Children are not returned as separate summaries. |
| `ids.go` | Mint `ses_`/`msg_`/`call_` ids (F8); `remapIDs(transfer)` for keep-both copies. Implements the core's `SessionIDMinter`. |
| `export.go` | `Materialize`: export of the parent and each child turned into **JSONL** (line 1 = `{"kind":"info",…}`, then one line per message). Local: built from SQL (same projection as export, checked by a parity test). Remote: `session export`. Inverse `jsonlToTransfer`. |
| `install.go` | `AfterInstall` (runs before lineage, §3.5): JSONL → transfer JSON in `{data}/hopsesh/opencode/tmp/`; keep-both id remap; set children's `info.parentID` from the source `parent_id` (child exports may lack it, UNVERIFIED); `session import <file> --directory <cwd>` parent first, then children. Success only when stdout contains `Imported session: <id>` (exit code is 0 on conflict). On "already exists" with a re-sync intended: export a backup to `{data}/hopsesh/opencode/backup/<id>-<ts>.json`, stamp-check, `session delete`, import. Idempotent (re-run during recovery finds the session and returns). |
| `store.go` | `SessionStore` (§3.5): `Stamp`, `Delete` (stamp-guarded), `Restore(backup)`. |
| `live.go` | `Live` (the execution claim and busy columns; `/api/session/active` when the service is up and a row has a turn in flight) and `Stop` (interrupt). |
| `preview.go` | `Preview` via `agent.BuildPreview`; `Rename` via `api PATCH`. |
| `reader.go` | `Read` → `ir.Segment` (§4). Works from SQL rows or from the JSONL file at `Summary.Path`. |
| `writer.go` | `Profile`, `Write` (§4). |
| `recover.go` | `RecoverWrite`: anchor exists and session exists → WriteResult from the DB; anchor exists, session missing → re-import. |
| `projection.go` | `ProjectSanitized`. |
| `sanitize.go` | `Sanitize()` RewritePolicy (§3.8). |
| `capacity.go` | `ContextCapacity`: model from `session_v2.model`; window from the `kv` models.dev catalog (`kv` key `models-dev:%`), else `ir.FallbackWindow` (D6). Usage from `tokens_*`. |
| `account.go` | `Account`: provider ids and labels from `auth list --format json` (D7); `Key` empty; Confidence limited. `LocalIdentity` false. `ResumeAcrossAccounts` true. |
| `fork.go` / `orphan.go` | `VerifyNativeFork`: compares content hashes of the parent's messages up to `fork_boundary` with the child's prefix (fork message-id behaviour UNVERIFIED). `NativeParentGone` = parent row missing. Local only; across machines lineage comes from the sidecar. |
| `cd.go` | `DirChanger`: `PlanChangeDir` (creates the anchor if missing, §3.5), `ChangeDir` = `api POST …/move` then wait for `location-switched`, `InSession` = `/cd <dir>`. |
| `integrate.go` | `Integration`: SkillDir `~/.agents/skills`; RuleFile `{config}/.hopsesh/rules.json`, which the plugin reads (as built in PR 6; the plan said the plugin's `config.json`). |
| `hooks.go` / `hooktrust.go` / `plugin/` | Embedded plugin (`plugin/index.js`, `plugin/hopsesh.js`, `plugin/package.json`; plain JavaScript as built in PR 6), its `HookDir`, the notice hook's JSON (`NoticeHookProtocol`) and `HookTrust` (§5). |
| `desktop.go` | App detection, version, `CheckApp`; staged-CLI discovery from the `desktop` root. |
| `icon.svg` | Neutral icon; trademark wording checked. |
| `testdata/2.3.0/…` | §7. |

Registration: one line in `internal/agents/all/all.go`. The package imports only `sdk/agent`, `sdk/ir`
and stdlib; no SQLite driver (the Host provides SQL).

### 3.2 Roots and detection
New SDK root form: `agent.Root{Name:"data", EnvSub: []EnvSub{{Env:"XDG_DATA_HOME", Sub:"opencode"}}, Default:{"*":"~/.local/share/opencode"}}`.

| Root | Env (subpath) | Default |
|---|---|---|
| `data` (main) | `XDG_DATA_HOME` (+`opencode`) | `~/.local/share/opencode` |
| `config` | `OPENCODE_CONFIG_DIR` (as is), else `XDG_CONFIG_HOME` (+`opencode`) | `~/.config/opencode` |
| `state` | `XDG_STATE_HOME` (+`opencode`) | `~/.local/state/opencode` |
| `desktop` (macOS) | — | `~/Library/Application Support/ai.opencode.desktop` |

Changes:
- `sdk/agent/agent.go`: `Root.EnvSub`; `Spec.MainRoot()` (first root) and `Install.Main()`.
- `DefaultInstall` (`sdk/agent/capability.go:185-215`) and `internal/core/host/host.go` `SpecEnv` learn EnvSub.
- `OPENCODE_DB` is read in `Detect` and kept as `Install.Roots["db"]`; used only through SQL.
- Present = the data root exists (nightly CI must `mkdir -p ~/.local/share/opencode`, as it does for `~/.claude`).

**Replace the hard-coded `Root("home")` (PR 0).** It appears in production code at
`internal/app/preview.go:211-214` (preview refuses paths outside it, so PR 2 needs this),
`internal/app/movement_hooks.go:160,176,196,257,290,341`, `accounts.go:156,219`,
`account_folders.go:113`, `scan.go:518,547`, `owner_ledger.go:116`, `watch.go`,
`remote_default.go:88`, `internal/ui/gui/accounts_view.go:242`, and the notice-hook resolver
(`movement.go:606-609`); many tests use it too. Each becomes `Main()`, or the root a capability
declares (hooks name their own root). Preview allows a session path under any Spec root.

### 3.3 Reading the live database safely

**SDK.** `sdk/agent/sql.go`: `type SQL interface { Query(ctx, db, q string, args ...any) (Rows, error) }`,
discovered through an optional `SQLHost` interface so other hosts are untouched. Rows are materialised
as `[]map[string]any` with max-rows and max-bytes guards. A `Batch` form runs several queries in one
read transaction (consistent WAL snapshot across message and part queries).

**Local** (`internal/core/host/sql_local.go`, modernc.org/sqlite v1.61.0, already in `go.mod`):
- DSN `file:<db>?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)`. Never `immutable=1`: it
  takes no lock, so a read could overlap OpenCode (or a short `opencode … --standalone`) writing and
  checkpointing, and return torn pages as rows. A WAL database no program has open has no `-wal` or
  `-shm` file; the local reader (modernc) then makes them, as any reader does, and leaves them
  (owned by the user, empty, harmless to OpenCode). Where the folder cannot be written, the read
  fails and says so; it never falls back to an immutable read.
- A missing database file is `ErrNotInstalled`, never `ErrNotFound`: the core reads `ErrNotFound`
  from a stamp as "the session was deleted" (undo, receipts, Seal).
- One `BEGIN` read transaction per batch; close after each operation so no reader blocks checkpoints.
- `SQLITE_BUSY` → three retries with backoff. Never checkpoint, vacuum or write.
- **Main guard: modernc's authorizer** (`RegisterAuthorizer` via `conn.Raw`): `SQLITE_READ` on a table
  outside `Spec.SQLTables`, or on a denied table, is refused.

**Remote tiers** (`internal/core/host/sql_remote.go`), chosen per machine and cached:
1. `sqlite3 -readonly -json 'file:…?mode=ro' "<query>"` over SSH Exec (always on macOS, common on Linux),
   when the shell marks BLOBs in its JSON (`.mode json --blob-quote sql`, current shells). A batch
   whose SQL is over 96 KiB (sshd passes the command line as one argument, which Linux caps at
   128 KiB) is read with python3, or refused (`ErrTooLarge`) where there is none.
2. `python3 -I` with a fixed embedded script using the `sqlite3` module (read-only URI, JSON out, the
   same authorizer as locally), its request on stdin. An older sqlite3 shell, which prints a BLOB as
   text, is never used: without python3 the machine has tier 3.
3. **No SQL:** the module falls back to `opencode service status`, then `opencode api GET /api/session`,
   `GET /api/session/{id}/message` and `session export`, with `--standalone` when the service is stopped.
   This returns projected JSON (parts merged, legacy shapes decoded) and no credential bytes. Slower;
   used on Windows and minimal Linux, and for a WAL database no program has open: the C SQLite of
   tiers 1 and 2 cannot open it read only (no `-shm` to read through), so the read returns
   `ErrUnsupported` instead of reading it as immutable.

For tiers 1 and 2: arguments are bound by literal quoting in a vetted helper (`sqlquote.go`), only the
module's fixed queries are sent, and a token scan rejects any query naming a denied table.

**The DB file is never copied** (no snapshot tier). Denied tables: `credential`, `account`,
`control_account`, `account_state`, `permission`, `workspace`. Allowlist: `session_v2,
session_message, session_part, project, project_directory, worktree, job, event_sequence, kv`
(`models-dev:%` keys only), `migration`.

**Why SQL locally rather than the API everywhere.** The API is the more stable surface, but every API
or export call spawns a process and, with the service stopped, a standalone server. Listing and preview
run on every GUI refresh, so local reads stay on SQL behind the column gate; the API is the remote
fallback and the source of truth in a parity test (§7).

### 3.4 Service safety and binary choice

- **Never start, stop or restart the service.** Never run `service start|restart|stop`.
- **Before any Exec** that would auto-start the service, `opencode service status`. If it prints
  `stopped`, pass `--standalone`. There is a race if the service starts between the probe and the call;
  both then write through SQLite locking and import is one transaction, so the result is still correct.
- **Exec uses the probed `opencode`** (the confined Host runs the binary Detect found). Subcommands reuse a service of another version (`mismatch:"ignore"`),
  so no version matching is needed for Exec. API features used are gated on the service version from
  `GET /api/info`.
- **No `--server`.** It needs the service password, which hopsesh does not read.
- **Resume and fork-resume commands** use a binary whose version equals the running service's:
  the PATH `opencode` if it matches; else `/Applications/OpenCode.app/Contents/Resources/opencode-cli`
  or a staged desktop CLI (`{desktop}/cli/<ver>-<arch>/opencode-cli`, version read from the directory
  name, no exec) of that version. If none matches, apply D2. If the service is stopped, any binary
  works; if `service status` fails, the resume is refused (the service may be running). The core
  runs an absolute program path as it is (`app.resolveProgram`).
- **hopsesh-owned terminal tabs** follow D3 (`internal/ui/gui/terminal_binding.go` gets a module
  capability `OwnedTabArgs`: Codex `--no-daemon`, OpenCode none under the recommendation).

### 3.5 Writing, identity, lineage, undo

**No direct SQL writes.** The DB is event-sourced (`event_sequence` must stay ahead of message seqs); the
service publishes every change on an in-process bus and streams it to clients over SSE; turn execution
state lives in the service; and the schema changes in bursts (F25). Every mutation goes through OpenCode:

| Mutation | Command | Journal compensation |
|---|---|---|
| Install / copy | `session import` | stamp-guarded `session delete`, then re-import the backup if one was taken |
| Re-sync over an existing id | backup export + `session delete` + import | delete + re-import backup |
| Rename | `api PATCH` | `api PATCH` old title |
| cd | `api POST …/move` | move back (stamp-guarded) |
| Fork | `api POST …/fork` | `session delete` of the child |
| Stop | `api POST …/interrupt` | none |

**New core and SDK pieces** (PR 1a/1b):
1. **Vendor-session journal op.** `OpVendorSession{Agent, Key, Backup, Stamp}` in
   `internal/core/journal/journal.go`, and `Reach.Sessions` (Delete / Restore / Stamp) backed by a new
   optional module capability `SessionStore`. `Seal` records the module stamp (settled-message count +
   `time_updated`); undo refuses when the stamp changed ("you continued this session in OpenCode").
   Move "set aside" of a DB session = backup export + stamp-guarded delete, journaled.
   `RecordCreated` gains a session form. `OpChangeDir` gets the same stamp guard.
2. **Install order.** A capability flag `InstallsViaVendor`: the core runs `AfterInstall` **before**
   `recordLineage` (today `apply.go:222` vs `:225`), treats its failure as fatal and journaled (today a
   warning, `apply.go:226-228`), and re-runs it idempotently in interrupted-operation recovery
   (`operation.go`). `heads()` (`apply.go:360-380`) reads staged files through the Reader's JSONL mode.
3. **Session-id minter.** Optional `SessionIDMinter`, used by the core in `keepBoth()`
   (`plan.go:546-548`, `repo.go:320`), portable writes (`write.go:102`) and the capacity archive path
   (`capacity.go:45,56,94`). The core uses `WriteResult.SessionID` everywhere after a write.
4. **Virtual bundle files.** `BundleFile.Virtual`; optional `Materializer`. `internal/core/move/apply.go`
   (stage step) and `write.go` call it instead of reading `Root/Rel`. `internal/app/peer.go:505-521`
   (`packageOf`) materialises too; the receiver's snapshot host (`peer.go:266`, `host/snapshot.go:39`)
   reads the JSONL through the Reader's file mode.
5. **Session stamp for caches.** Optional `SessionStamp` (or `Summary.Size` + `LastActivity` for modules
   with the `VirtualSessions` marker) replaces `Stat(Session.Path)` in `internal/app/movement_read.go:33-60`,
   the fingerprint in `movement_memo.go:129-131` and the receipt guard in `journal/receipt.go:24-31`.
   A missing anchor is tolerated.
6. **Listing cache.** `ListingSummaryKey(ctx,h,key,signature,salt,parse)` in `sdk/agent/listing.go`;
   signature `id|time_updated|settled count|part count`.

**Anchor file and lineage.**
- `Summary.Path = {data}/hopsesh/opencode/sessions/<ses_id>.jsonl`.
- Sessions hopsesh installed have the anchor (the staged, rewritten export). Native sessions get it on
  first need: a lineage write, a `cd`, or a move (created eagerly then).
- Lineage sidecar `<anchor>.hopsesh.json` (missing = empty, which `lineage.Read` already tolerates).
- The notice-hook lookup computes the anchor from the data root and the session id; it does no Scan or
  List, keeping that path local-only (`movement.go:575-577`).
- Before a move, a native fork (`fork_session_id`) is written into the sidecar, because import drops it.

**Bundle layout.** Main `sessions/<id>.jsonl` (Virtual, Rewrite `jsonl`); sides
`sessions/<id>.children/<child>.jsonl` (Virtual, jsonl). The existing `RewriteJSONL`, `Protect`,
`DropRecords`, `Rename` and `DropElems` work unchanged. No new rewrite kind.

**Size, cursor, drift.** `Summary.Size` and `Cursor.Offset` = **number of settled messages**, the same in
SQL mode and JSONL mode (export has no `seq`, and import renumbers). `Cursor.Head` = last settled
message id. A revert that drops messages lowers the count or changes the head → `ErrDiverged`.
`sourceDrift` (`internal/app/movement.go:395-418`) already compares Offset with Size on one scale;
document "offset unit is module-defined".

**Children.** Moving or continuing a parent carries its children. A child alone cannot be moved or
continued (import fails without its parent); hopsesh blocks it with a message.

**Not moved.** `snapshot/<pid>/…`, `shell/<pid>/*.out`, `tool-output/` (shell output is inline in tool
content). Lost on the target: `/undo` for earlier turns, fork fields, instruction state, slug, job rows,
session-level times, archived state. Shown in the move preview.

**Moves are refused** while the session is Live, including `time_suspended` set (it will resume on the
next service start, F22).

**Watch paths:** the database Detect chose (`{data}/opencode.db`, `OPENCODE_DB`'s, or another
channel's `opencode-<channel>.db`) and its `-wal`, and `{data}/hopsesh/opencode/`.

### 3.6 Skills and instructions
- Nothing to install: OpenCode reads `~/.agents/skills` and `~/.claude/skills` (F18).
  `Integration.SkillDir = ~/.agents/skills`. hopsesh already installs to both for Codex and Claude, so
  OpenCode may see two `hopsesh` skills; whether it dedups by name is UNVERIFIED (check in PR 6; if it
  does not, the two copies are identical, so the effect is cosmetic).
- The skill template gains an OpenCode example and documents `OPENCODE_SESSION_ID` for "this session".
- `internal/e2e/scenario/testdata/skill.txtar:5` expects `(Codex)`; it will read `(Codex, OpenCode)`.
- The cross-agent briefing carries `CLAUDE.md` content to OpenCode, since v2 does not read it.

### 3.7 Accounts and profiles
- `ProfileSpec.RootEnv` gains a multi-variable form: `XDG_DATA_HOME=P/data`, `XDG_STATE_HOME=P/state`,
  `OPENCODE_CONFIG_DIR=P/config`. `OPENCODE_CONFIG_DIR` is used instead of `XDG_CONFIG_HOME` so child
  tools (git and others) keep their own config; `XDG_DATA_HOME`/`XDG_STATE_HOME` still reach child
  tools (minor).
- On profile creation hopsesh runs, under the profile env, `opencode service set port <n>` with a free
  port recorded in the profile (D8). Without it the profile's service fails with EADDRINUSE while the
  default one runs (port 49374).
- `Inherit` gains a JSON copier; copying `opencode.json` (holds keys) only with consent.
- The desktop app uses only the default profile.

### 3.8 Rewrite policy (`sanitize.go`)
- **Protect keys:** `native`, `id`, `callID`, `call_id`, `hash`, `uri` (when it starts with `media:`),
  `signature`, `providerID`, `model`.
- **Rename keys:** `["id","sessionID","parentID"]`. Message and call ids for keep-both copies are
  re-minted in `AfterInstall`, not by the rewriter.
- **Sanitizer (`OtherAccount`):** drop `reasoning` content items; strip `native` from every content
  item; drop `compaction.native`. Order is kept, so the projection is the identity minus dropped items.
  With `Account.Key` empty this runs only when the user picks "other account" explicitly.
- **Separators:** Windows targets use `Mapping.ToSep="/"` (OpenCode stores `/`; migration
  `20260601010001_normalize_storage_paths`).

---

## 4. Cross-agent conversion (OpenCode ↔ neutral IR)

### Reader (OpenCode → IR), `reader.go`
- `Native.Format = "opencode.v2"`. Anchor = `<msg_id>` or `<msg_id>/<ordinal>`.
- Cursor = `{Offset: settled count, Head: last settled msg id}`.
- Order: by `seq` (SQL) or array order (JSONL), then the merged parts order (F5). Unsettled messages
  are skipped. Reading from the end cursor returns nothing; `from.Head` not found → `ErrDiverged`.

| OpenCode | IR |
|---|---|
| `user.text` (prefer `metadata.displayText`) | message / user |
| `user.files[]` | attachment (name, mime; paths noted; data omitted) |
| `assistant.content[text]` | message / agent |
| `content[reasoning]` | reasoning (dropped by convert, counted) |
| tool `shell` | tool_call Shell{Command, Dir=workdir} + tool_result (exit from metadata) |
| `read` → Path; `edit`/`patch` → Edit; `write` → Write | |
| `glob`/`grep`/`websearch` → Search; `webfetch` → URL | |
| `subagent` | Subagent{agent, description, prompt}; child summarised as tool_result text |
| `execute` (Code Mode), MCP tools, `list_mcp_resources`/`read_mcp_resource` | generic tool_call (name and input as text); MCP name split UNVERIFIED |
| `session_rename`, `session_move`, `worktree_*`, `models`, `todo`, `question`, `skill`, `schedule`, `artifact`, image generation, unknown tools | generic tool_call (flattened to text by convert) |
| `compaction` (completed) | compaction node with `summary` |
| `shell` message (user `!cmd`) | tool_call Shell by actor user + result |
| `system`, `synthetic`, `idle`, `agent-switched`, `model-switched` | skipped |
| `location-switched` | note "moved to <location>" |
| `skill` message | user note "used skill <id>" |
| tool state `error` | tool_result with error flag |

### Writer (IR → OpenCode), `writer.go`
- **Profile:** `{Window: ContextCapacity, NativeReplay: true (shell only), PortableAppend: false}`;
  true only with PR 11 (D4).
- **`Write(New)`** (as built in PR 5; the PR 5 note below has the reasons):
  1. Session id: `WriteRequest.SessionID` (the core minted it with the `SessionIDMinter`); without
     one in OpenCode's form, one made from the operation id. `msg_` ids ascending (time part from the
     message's time, tail hashed from the session id, operation and position); message times are the
     items' own, kept increasing, items without one after them; the session's creation time is the
     write's.
  2. Info: `{id, title, projectID:"global" (recomputed), location:{directory:cwd}, cost, tokens, time}`;
     no `model` and no `agent` (OpenCode picks its default for the next turn; never `{}`).
  3. Messages: user items → `user {text, files:[], time}`; agent text → `assistant {agent:"build",
     model:{id:<source model or "unknown">, providerID:"hopsesh"}, content:[{type:"text",text}],
     finish:"stop", cost:0, tokens:0…, time:{created, completed}}`; NativeReplay shell items →
     assistant `{type:"tool", id:call_…, name:"shell", executed:false, state:{status:"completed",
     input:{command}, content:[{type:"text",text:output}], metadata:{status, truncated, exit}},
     time:{created, ran, completed}}` with `finish:"tool-calls"` (a failed call without an exit status
     is `state:{status:"error", error:{type:"tool.execution", message}}`); an `idle {outcome:"succeeded"}`
     after each turn of replies, as OpenCode's own turns end; the briefing (a user message) and the
     `[hopsesh] Import ready` item (the last reply) as the converter renders them.
  4. Write JSON lines to the anchor, then the transfer to `{data}/hopsesh/opencode/tmp/`, import
     (success = `Imported session: <id>` on stdout; a session already there with these messages is
     done, with others `ErrDiverged`). Verified with a real 2.3.0 `--standalone` import (PR 5).
- **Result:** `WriteResult{SessionID, Path: anchor, From:0, To: settled count, Cursor}`. The projection
  maps each `ir.Item` to its `msg_` id via `ir.ProjectionFor`.
- **Lost into OpenCode:** non-shell tool calls (flattened, as for Claude and Codex), reasoning, Claude
  and Codex signatures and encrypted content, images unless textual, cost and token history,
  per-message model identity.
- **Lost out of OpenCode:** `native` replay, snapshots and revert, instruction state, child detail
  (summarised), media blobs (names only).
- **Fetch and adopt append** are gated on `Profile.PortableAppend` (`relateFetch` offers the original
  only to such a writer; `appendOriginal` refuses otherwise). Done in PR 5, with tests.

---

## 5. Protection, rules and skills (plugin)

As built in PR 6, with the corrections the "Left by PR 6" note (§8) explains: the plugin is
plain JavaScript (`index.js`, `hopsesh.js`), the instruction entry's key is `hopsesh-notice`
(OpenCode refuses a slash), the rules live in `{config}/.hopsesh/rules.json` (the plugin's
`config.json` names that file), the plugin also checks again after a failure only once 10 s
have passed and keeps a verdict 30 s for tool calls, a subagent's session follows its root
session's verdict, and HookTrust asks only a running service (a private server answers
before it loads plugins). The text below is the plan.

**Files** in `{config}/plugins/hopsesh/`, owned by hopsesh:
- `index.ts`: `Plugin.define({id:"hopsesh", setup})`.
- `package.json`: name and version, no dependencies (`@opencode/plugin` is injected by the host).
- `config.json`: the **full notice-hook argv** (binary, `--profile`, pinned dirs, as `hooks.go:37-48`
  builds for Claude and Codex), `rulesVersion`, `allow` and `ask` patterns.

**What `setup(ctx)` does:**
1. Keeps an in-memory verdict per session id.
2. `ctx.event.subscribe({types:["session.created","session.moved","session.forked"]})` → runs the
   notice-hook (`event:"SessionStart"`), stores the verdict, and puts any notice with
   `ctx.session.instructions.entry.put("hopsesh/notice", text)` (experimental API, F12).
   This also fires for hopsesh's own imports; the notice-hook decides.
3. `ctx.session.hook("prompt")` → notice-hook with `event:"UserPromptSubmit"`, stores the verdict, puts
   a loud instruction entry when blocked. **Never throws** (a throw is a defect in the service).
4. `ctx.tool.hook("execute.before")` → if no verdict is cached for the session (for example after a
   service restart, when a suspended turn resumes without a prompt, F22), runs the notice-hook first.
   Throws `Error(reason)` while blocked. This is the protection: a stale copy cannot edit files or run
   commands. The model sees a tool error each time and the notice tells it to stop.
5. `ctx.permission.hook("evaluate")`: while blocked, `deny` with the reason (for tools that assert a
   permission); otherwise shell resources matching `allow` → `allow`, matching `ask` → `ask`. An
   explicitly configured user `deny` stays final.
6. Every spawn has a 5 s timeout and fails open, like the Claude and Codex hooks; failures are written
   to stderr (there is no `ctx.log`) and counted in `ctx.storage` so HookTrust can show them. The plugin
   reads no files outside its own dir.

**Install and remove.** A new SDK `HookDir` contract (owned directory, several files, Merge/Has/Remove,
atomic replace), with the root named by the declaration instead of `Root("home")`
(`movement_hooks.go:196`). `Has` = dir present + `config.json` argv names this hopsesh binary and
profile. Installed only when the **service** version is ≥ 2.1.0 (the plugin runs in the service);
`Plugin.define` on 2.1/2.2 is verified in PR 6 before the floor is final.

**Trust (`HookTrust`).** Trusted when `opencode api GET /api/plugin` (JSON) lists `hopsesh` as loaded,
no config remove entry matches it (`-hopsesh`, `-*`, `-<prefix>.*`), and managed policy does not deny
plugins. Hot reload of plugin directories is UNVERIFIED; if it does not happen, HookTrust reports
"loads after the next OpenCode service restart" (hopsesh never restarts it).

**Core change.** `internal/ui/cli/movement_hooks.go:76-139` drops the claude/codex gate and adds a
payload adapter keyed by agent: OpenCode sends `{session_id, directory, event}` with no transcript path;
the anchor is computed from the data root (§3.5). Output for OpenCode:
`{"block":bool,"reason":…,"context":…}`.

---

## 6. Cross-cutting changes

**A. N-agent refactor (PR 0, no OpenCode yet)**
- Main-root accessor replacing `Root("home")` (§3.2).
- `internal/app/machine_status.go:157,794,812-885`: Spec name; per-module hook `Has` instead of the regex.
- `internal/app/accounts.go:87`, `account_folders.go:84`, `fetch.go:259`.
- `internal/core/presence/presence.go:40-41,154-199`: generic `KindApp{Agent}`. The OpenCode rules
  (`OpenCode`, `OpenCode Helper*`; `opencode-cli serve --service` and `opencode serve --service` are the
  service, not sessions) move to PR 7, with the rest of desktop awareness.
- `internal/core/profiles/store.go:331`; `internal/core/move/fetch.go:286`; `internal/config/config.go:60-80`
  (generic `Kinds` map).
- `internal/ui/cli/{movement_hooks.go,accounts.go:14,71,serve.go:284,root.go,pull.go:26,cd.go:27-44,skill.go}`.
- `internal/ui/tui/{accounts.go:238-342,tui.go:722}`.
- `internal/ui/gui/{accounts_view.go:62-389,622,inspect.go:363,move_targets.go:115-122,protection.go:89,terminal_binding.go:18-60}`.
- GUI JS: replace per-agent maps with `agentInfo(id)` and capability flags in `accounts.js`,
  `actions.js`, `guard.js`, `inspector.js`, `listview.js`, `quick.js`, `sessions.js`, `settings.js`,
  `machines.js`, `term.js`; `.tgrid` column count from a variable. The `style.css` `--opencode` token
  and its chips move to PR 8, with the GUI copy; until then an unknown agent gets the default chip.
- Roots are resolved in one place: `Root.Vars` (the variables that can set a root) and `Root.Locate`
  (its folder on a machine), used by `DefaultInstall`, `host.SpecEnv`, the notice-hook resolver, the
  owner ledger and the catalog key. PR 1a adds EnvSub there only.
- Module facts the interfaces need are declared, not inferred: `ProfileSpec.InheritsSignIn` (not
  "Inherit is non-empty"), `Spec.Desktop`, `Spec.Plans`.
- A stand-in third agent (`internal/testkit/thirdagent`, several roots, accounts, a desktop app)
  is registered beside Claude Code and Codex in app, CLI, TUI and GUI tests; agenttest checks that
  Detect names the main root. The release plugin's skill is rendered from the registry.
- Moved out of PR 0: the optional `sk-or-`, `gsk_`, `xai-` secret rules in
  `internal/core/scan/scan.go` go to PR 2.

**B. SDK contracts (PR 1a)**
- `sdk/agent/agent.go`: `Root.EnvSub`, `Spec.SQLTables`, `MainRoot()`.
- `sdk/agent/capability.go`: EnvSub in `DefaultInstall`; Rules unchanged (Allow/Ask).
- `sdk/agent/session.go`: `BundleFile.Virtual`, `Materializer`, `VirtualSessions`, `SessionStore`,
  `SessionStamp`, `SessionIDMinter`, `InstallsViaVendor`.
- `sdk/agent/hooks.go`: `HookDir`. The checks that a hook file sits directly in the main root
  (`internal/app/movement_hooks.go`, `remote_default.go`) use it, so a plugin under
  `{config}/plugins/hopsesh` passes (PR 6 depends on this).
- `sdk/agent/sql.go`, `sdk/agent/confine.go` (`confinedSQL`; the DB, named by `Spec.SQLFiles`, stays a
  Secret for FS access),
  `sdk/agent/listing.go` (`ListingSummaryKey`).
- `sdk/agent/runtime_profile.go`: multi-var `RootEnv`, JSON `Inherit`.
- `sdk/agent/agenttest/{suite.go,fakehost.go}`: FakeHost SQL (modernc on a temp file built from fixture
  `.sql`); FakeHost `Programs` mutate that DB so List sees what Write imported; the append section of
  the writer test (`suite.go:206-227`) runs only when `Profile.PortableAppend`; `VirtualSessions`
  relaxes the byte-length checks (`suite.go:180-186`) to `Cursor.Offset == Summary.Size`; bundle check
  accepts "exists or Virtual with a Materializer".

**C. Core (PR 1b)**
- `internal/core/host/{host.go,sql_local.go,sql_remote.go,sqlquote.go}`.
- `internal/core/journal/{journal.go,receipt.go}`: `OpVendorSession`, `Reach.Sessions`, stamp in `Seal`,
  stamp guard for `OpChangeDir`.
- `internal/core/move/{apply.go,write.go,plan.go,repo.go,capacity.go,operation.go}`: Materialize,
  install order, fatal `AfterInstall`, recovery re-run, id minter.
- `internal/app/{movement.go,movement_read.go,movement_memo.go,peer.go,cd.go}` and
  `internal/core/host/snapshot.go`: anchor-by-id lookup, stamps instead of file stat, peer
  materialisation, cd journaling with eager anchor (`cd.go:212,279`).

**D. Tests and devtools** (§7). **E. CI and drift** (§7).

**F. Docs and packaging (PR 10)**
- `README.md` (`:7,44,60-66,148,270,529-534,786-797`; close #10), `README.ja.md`, `README.zh-CN.md`.
- `CONTRIBUTING.md:94-104`.
- `docs/design.md:87-93` (capability table to 3 columns), `:215-316`.
- `docs/accounts.md`, `docs/accounts-design.md`, `docs/accounts-discovery-refresh.md`,
  `docs/client-api.md:171`, `docs/drift.md`, `docs/session-discovery.md` (DB-backed sessions),
  `docs/movement-return.md`, `docs/context-safety.md`, `docs/daemon-presence-proposal.md`,
  `docs/lineage-plan.md`, `docs/embedded-terminal-families-design.md`, `docs/return-conflict-review.md`,
  `docs/machines.md`, `docs/desktop-presence.md`, `docs/desktop-presence-design.md`.
- `plugin/plugin.json`, `plugin/.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json:9`,
  `plugin/README.md`; regenerate `plugin/skills/hopsesh/*` with `scripts/update-plugin.sh`.
- `.goreleaser.yaml:56`, `scripts/package-manifests.sh:11`, `packaging/windows/hopsesh.nsi:56`,
  `packaging/macos/Info.plist.in:16`.
- `demo/Dockerfile` (`opencode-stub.sh`), `demo/laptop-setup.sh`, `demo/studio-entrypoint.sh`.
- `CHANGELOG.md` Unreleased › Added; trademark notice.
- Out of repo: the codonic.dev product page, after release.

---

## 7. Testing

**Unit (`agents/opencode`), about 70 tests**
- list (top-level, children counted not listed, archived, mixed inline/part content, forks);
- schema gate (missing column → refuse; extra migration → allowed);
- live/stop (busy, suspended, active, TUI argv; interrupt only);
- service probe and `--standalone` decision; resume binary choice (PATH match, desktop CLI match,
  none → D2 path); standalone refused when the binary is older than the DB's sessions;
- reader determinism, anchors, divergence; **SQL-mode vs JSONL-mode parity** on the same fixture;
- writer shape: valid transfer, ascending ids, `model` omitted when unknown;
- ids encode/decode; keep-both remap; JSONL↔transfer; children carry `parentID`;
- import success detection (exit 0 + "already exists" is a failure);
- stamp-guarded delete; backup and restore;
- rewrite policy (protected `native`); sanitizer;
- capacity (catalog / fallback);
- account: FakeHost SQL spy and authorizer test assert denied tables are never read;
- cd (waits for `location-switched`), rename, fork, orphan;
- plugin install / Has / Remove; hook trust (`-hopsesh`, `-*`, prefix, managed deny);
- plus `agenttest.Run`.

**Plugin tests** (`plugin/` with Bun, run in CI where Bun is available): verdict cache, cache-miss call
in `execute.before`, prompt hook never throws, allow/ask/deny mapping, timeout fails open.

**Fixtures** in `agents/opencode/testdata/2.3.0/`: `schema.sql` (from `$S/db/schema.sql`), `data.sql`
(scrubbed rows: user alice, `/home/u/git/demo`, recomputed project id, one fake `credential` row to
prove it is never read), `export-*.json`; a `2.2.7/` variant with inline-only content; a synthetic
`session_part` case. No binary `.db` in git; the DB is built at test time. Capture per D9 in an
isolated sandbox (`XDG_*` → scratch, `--standalone`); scrub script `internal/devtools/ocscrub/`.

**Testkit and fakes**
- `internal/testkit/fakeagent/fakeagent.go`: `OpenCode()`.
- `internal/devtools/fakeagent/main.go`: an `opencode` Go stand-in backed by modernc: `--version`,
  `debug paths`, `service status`, `session list|export|import|delete` (exit 0 + stderr on conflict,
  parent required for children), `auth list --format json`, and `api` for session PATCH/DELETE, move,
  fork, interrupt, active, info, plugin, session list and messages.
- `internal/testkit/fakecloud/programs.go`: `OpenCodeVersionLine`.
- `internal/testkit/testkit.go:21-90`: clear `XDG_*` and `OPENCODE_*`; `DemoHome` builds an OpenCode DB.
- `demoseed -opencode`; `skillcheck` env and dirs.

**Test bundle**
- `internal/devtools/testbundle/{build.go,check.go}`: opencode stand-in; validate `.sql` and export
  fixtures, including scrub checks.
- `testbundle/opencode/2.3.0/` (export JSON, plugin event payload).
- `testbundle/schemas/opencode-transfer.schema.json`, `opencode-plugin-notice.schema.json`. schemalite
  has no `oneOf`/`anyOf`, so the transfer schema checks only the common envelope (`id`, `type`, `time`)
  and per-type checks live in `check.go`.

**Scenario, e2e, matrices**
- `internal/e2e/scenario`: `opencode-list.txtar`, `continue-in-opencode.txtar`,
  `opencode-notice-hook.txtar`, `opencode-accounts.txtar`, `opencode-move.txtar`; update `skill.txtar`.
- `internal/e2e`: `continue_test.go` (Claude→OpenCode→Claude, Codex→OpenCode→Codex, undo of
  continue-into-OpenCode); `context_capacity_test.go`; `movement_test.go`; `cd_test.go` (including cd
  undo with the stamp guard); `skill_test.go`; `lineage_routes_test.go` (three-agent start set,
  all-pairs alternation); new `opencodemove_test.go` (undo, keep-both, undo refused after use,
  interrupted-operation recovery re-running the import); fetch-append gating; `peer_test.go` OpenCode leg.
- `hsmatrix/model.go`: 3×3 agent dimension (worktree stays Claude-only); `helper.go` seeder and reader;
  `runner.go` skill paths; `main.go` env.
- Mesh and OS-pair scripts (`remote-scenarios.sh`, `matrix-{loopback,lima,local}.sh`,
  `matrix-{loopback,wsl}.ps1`, `integration-test.sh`): install the stand-in; "OpenCode over SSH" leg
  covering remote tiers `sqlite3`, `python3`, and API-only (Windows).
- OpenCode legs for `scripts/roundtrip-test.sh`, `scripts/windows-peer-test.ps1`; review
  `scripts/password-test.sh`, `scripts/paid-smoke.sh`, `scripts/iterm-smoke.sh` for a leg.

**Playwright (`internal/devtools/webtest`)**: `main.go` worlds; `real/setup.ts` stand-in; update
`accounts*.spec.ts` (6 tool cells), discovery, move-targets, movement, protection; new
`continue-opencode.spec.ts`, `opencode-protection.spec.ts`, `opencode-accounts.spec.ts`.

**Real-agent tests** (`HOPSESH_REAL_AGENTS=1`, `internal/e2e/realagents_test.go`): `TestOpenCode*`, no
model calls, isolated XDG, `--standalone`. Import a fixture export, then hopsesh list / preview / move
(loopback) / continue-into-OpenCode / cd / rename / undo / plugin install, verified with
`session export` and `api GET /api/plugin`. Because the Go stand-in cannot check the real schema, any
change to the writer must pass a real `--standalone` sandbox import (run locally and nightly).

**CI**
- `ci.yml:155-157`: stand-in names and versions.
- `nightly.yml:61-79`: `mkdir -p ~/.local/share/opencode`; install real OpenCode (macOS
  `brew install anomalyco/tap/opencode-v2`; Linux `npm i -g @opencode/cli`; Windows via npm, UNVERIFIED
  since the docs say Windows package managers are unsupported); check the version read; run
  `-run 'OpenCode'`; `OPENCODE_DISABLE_AUTOUPDATE=1`.

**Drift watcher**
- `internal/devtools/driftmanifest/targets.go`: `agentWatch["opencode"]` with latest from npm
  `@opencode/cli` and GitHub releases of anomalyco/opencode (both lines; there is no Homebrew source
  type), `--help` snapshots of all subcommands including `auth login`, migration-id list from a
  sandbox DB, OpenAPI hash (sandbox, `--standalone`), `@opencode/plugin` `.d.ts` hook names, transfer
  schema; `Watch.Grep` covering every `Accounts.RootEnv` variable; `IntegrationFiles`. An
  `opencode-desktop` app entry.
- `coverage_test.go:17-60` obligations above. `floors_test.go:105-140`: declare the 2.1.0 floor with its
  source file; the loop at `:136` stops hard-coding `{"claude","codex"}`. `floors_test.go:148-185`:
  OpenCode format ids and Fields present literally in the reader files.
- `ci/drift/test-hopsesh.sh:11-25` app list includes `opencode-desktop`.
- `ci/drift/probe.sh`: an install method other than npm-only where needed, and `cli()` env isolation
  adding `XDG_*`, `OPENCODE_CONFIG_DIR` and auto-update off.
- `ci/drift/focus/local.md`, `groups.json`.

**Manual end-to-end checklist (this Mac, then a second machine over SSH)**
1. Sandbox (`XDG_*` → scratch, `--standalone`): list / preview / continue-into-OpenCode /
   move-loopback / undo; confirm with `opencode session list`.
2. Real install with the desktop running (2.2.7 service, 2.3.0 CLI): `hopsesh list` shows both
   sessions; the service PID (from `ps`, not the file) does not change across every hopsesh command.
3. Resume from hopsesh picks the desktop's 2.2.7 CLI and the service PID stays the same; with no
   matching binary, the D2 prompt appears.
4. Continue a Claude session into OpenCode; it appears in the TUI and in the desktop app. One prompt,
   if you agree to a model call.
5. Continue it back into Claude and Codex; lineage shows the chain.
6. Install protection; move the session to the second machine; in the left-behind copy a tool call is
   blocked with hopsesh's reason, also after `opencode service restart` done by you. Undo: the remote
   session is deleted and the local copy is unblocked. Undo after continuing remotely is refused.
7. `cd` to a worktree: `location-switched` appears and the resume opens there.
8. Accounts: create an OpenCode profile; it gets its own port and service and does not see default
   sessions.
9. Busy detection while the desktop runs a turn; Stop interrupts the turn and leaves the service.
10. Second machine: Linux with `sqlite3`, Linux with only `python3`, and Windows (API tier) if available.

---

## 8. Delivery, risks, open items, non-goals

**PR sequence.** Each PR merges on its own, behind `Stability: Experimental` until PR 9. Branches use
neutral names (`opencode/…`).

| PR | Content | Size (prod + tests) |
|---|---|---|
| 0 | N-agent refactor: registry and capability lookups in app, CLI, TUI, GUI Go/JS/CSS; generic app kind; config Kinds; **main-root accessor replacing `Root("home")`**; one root resolver; third-agent tests | ~1.5k + 0.6k |
| 1a | SDK contracts: EnvSub roots, SQL interface + confinement, Virtual/Materializer, SessionStore/Stamp/IDMinter/InstallsViaVendor, HookDir, ListingSummaryKey, multi-var profiles; agenttest + FakeHost SQL | ~1.4k + 0.9k |
| 1b | Core: Host SQL local (authorizer) + remote tiers; journal `OpVendorSession` and stamp guards; apply order, fatal AfterInstall, recovery; id minter in move/write/capacity; peer materialisation; stamps in caches and receipts; cd journaling | ~1.5k + 1.0k |
| 2 | `agents/opencode` read side: spec, paths, service probe, schema gate, list, summary, preview, rename, live/stop, capacity, account, reader (SQL + JSONL + remote API); fixtures; registration | ~2.0k + 1.3k |
| 3 | Stand-in `opencode`, testkit/demohome, testbundle, scenario txtars (list/preview) | ~0.9k + 0.5k |
| 4 | Same-agent move: materialise, plan/verify, AfterInstall import, children, keep-both, backup, undo, `cd`, fork/orphan; e2e move, undo, recovery, peer | ~1.4k + 1.0k |
| 5 | Writer, RecoverWrite, projection; fetch/adopt append gating; continue Claude/Codex ↔ OpenCode e2e; capacity | ~1.1k + 0.9k |
| 6 | Plugin protection + rules (allow/ask/deny) + notice-hook generalisation + HookTrust + machine_status; skill template; plugin tests | ~1.0k + 0.7k |
| 7 | Accounts/profiles (multi-env, port, JSON inherit), presence/desktop awareness, GUI accounts grid, TUI picker | ~1.0k + 0.6k |
| 8 | GUI copy and Playwright specs | ~0.4k + 0.8k |
| 9 | Matrices (hsmatrix 3×3, lineage routes, mesh/OS-pair scripts), nightly real-agent job, drift watcher and floors | ~0.7k + 1.0k |
| 10 | Docs, READMEs (3 languages), plugin/marketplace, packaging, CHANGELOG | ~0.9k copy |
| 11 *(optional, D4)* | PortableAppend into an existing session (idle- and stamp-gated delete + re-import with backup) | ~0.5k + 0.4k |
| 12 *(optional, D1)* | OpenCode 1.x read support (list / preview / continue-from) | ~0.7k + 0.5k |

**Left by PR 0 for later PRs** (from its review):
- PR 2: the `sk-or-`, `gsk_`, `xai-` secret rules.
- PR 6: the notice-hook output (`internal/ui/cli/movement_hooks.go`) is Claude/Codex JSON. And
  `HookTrustReporter` means two things today, "reports trust" and "runs a new hook only after the
  user approves it" (`HookReview`, the Settings and guard texts, `trustAtLogin`, the SSH reason in
  `machine_status.go`); OpenCode will report hook state with no approval step, so PR 6 splits them.
- PR 7: OpenCode process rules in `presence.go`; the desktop "installed" check accepts only `.app` or
  `.exe` (`accounts_view.go`, `movement_hooks.go`), so a Linux desktop app needs its own rule.
- PR 8: `style.css` agent tokens and chips (`--claude`, `--codex`, `.chip.agent-*`, `.msg-l.agent-*`),
  the Settings bell and trust texts, and the Playwright specs and webtest fixtures that name two agents.
- PR 3: `internal/devtools/testbundle` stand-in names and the testkit stand-ins (done in PR 3).
- PR 9: the two-agent loops in `internal/e2e`, hsmatrix, `driftmanifest` (`agentWatch`),
  `skillcheck`, `scripts/matrix-*`, `ci/drift` and the nightly and drift workflows.
- PR 10: the skill template's examples and `/cd` lines, the plugin files, READMEs, packaging, demo.

**Left by PR 1a for later PRs.** PR 1a adds the SDK contracts as optional interfaces; no core code
calls them yet, so Claude Code and Codex behave as before. Names differ slightly from §3.5: the
stamp is `agent.SessionStamp` (a string starting with its form, `v1:…`) from an optional
`SessionStamper`, `SessionStore` adds `Backup`, the install flag is
`VendorInstaller.InstallsViaVendor()`, `Spec.SQLDenied` lists the denied tables beside
`Spec.SQLTables`, and `Spec.SQLFiles` names the database files SQL may open (`{data}/opencode*.db`,
and `{db}` for `OPENCODE_DB`; each is a Secret for file access, with its `-wal`/`-shm` files, so
the spec need not list the DB under `Secrets`).
- PR 1b, local reader: `moduleHost` implements `agent.SQLHost`; its reader implements
  `agent.SQLTableLimiter` with a modernc authorizer as `agenttest/sqlhost.go` does: allow
  `agent.SQLTableFuncs()` and the allowed tables, never the denied ones, and a read with no
  column of a name that is no table or view of the database (a WITH name; read the schema's
  names before the authorizer is registered). Register it on a dedicated connection never
  reused for another module. Set `SQLITE_LIMIT_LENGTH` to the statements' `MaxBytes`, read rows
  with one `agent.NewSQLBudget()` per Batch, and map SQLite's "too big" to `ErrTooLarge`.
  `agent.Confine` already checks each query (`agent.CheckSQLQuery`), the database file
  (`SQLFiles`) and gives each call `DefaultSQLTimeout`. The snapshot host returns no reader.
- PR 1b, remote tiers: the remote reader implements `SQLTableLimiter` too. After `sqlquote` puts
  the arguments in, run `agent.CheckSQLQuery` again on the final text. sqlite3 tier:
  `-readonly -safe -bail -init /dev/null -json` (`-safe` needs 3.37). python3 tier:
  `conn.set_authorizer` with the same rules. Escape `?`, `#` and `%` in `file:` URIs. Cap the
  bytes read from stdout before decoding; fill `Rows.Columns` in the query's order, repeats
  included, and check with `agent.CheckRows` (or the Batch's `SQLBudget`); return whole numbers
  as `int64`.
- PR 1b, move: stage, write and `packageOf` call `agent.Materializer` for `BundleFile.Virtual`;
  `apply.go` checks `agent.InstallsViaVendor`; `keepBoth`, `repo.go`, `write.go` and `capacity.go`
  use `agent.NewSessionID`; the journal's `OpVendorSession` uses `SessionStore`; `Seal`, the
  receipts, `movement_read.go` and `movement_memo.go` use `agent.StampOf` (with a fresh Summary,
  never the listing cache's) and tolerate a missing anchor when `agent.IsVirtual`. When a
  journaled stamp's `Format()` differs from the module's current one, undo asks instead of
  refusing for good.
- PR 6: install `HookIntegration.Dir` with `agent.InstallHookDir` / `RemoveHookDir` over a local
  `HookDirFS` whose `ReadDir` uses Lstat, under the machine's hook lock, and check it with
  `HookDir.In`. `Staging` is required, inside the same root and outside `plugins/` (for
  OpenCode, `{config}/.hopsesh`).
- PR 7: `PinMain` places an added account's main root inside its folder (`Spec.ProfileLocate`) and
  a default account keeps the machine's `RootVars` variables. Inherit keys may name a root
  (`{config}/opencode.json`); only plain values are copied. Code that lists an account's
  variables reads `ProfileSpec.Vars()`, not `RootEnv`.
- PR 2: with `permission` denied, a query cannot name `session_v2.permission` (the checker refuses
  any mention of a denied name). Fine unless PR 2 needs that column.
- Not changed: `WindowsPath.Clean` turns a UNC root (`\\server\share`) into `\server\share`,
  which affects `EnvSub` on network home folders; it predates PR 1a.

**Left by PR 1b for later PRs.** The core now stages, installs, journals and undoes sessions kept
in a database; `internal/testkit/dbagent` is a stand-in module that keeps them in SQLite (its
program writes in the test's process) and the e2e and app tests run moves, continuations, cd, peer
packages and undo through it.
- PR 2/4/5 (the module): keep `Summary.Path` at `agent.AnchorRel` (the notice hook finds lineage
  from the session id alone). `Write` uses `WriteRequest.SessionID` (the core minted it with
  `agent.NewSessionID` and journaled the session before the write; a writer that picks another id
  is followed, and that id is journaled after the write) and imports the session itself: the core
  runs no `AfterInstall` after a write. `RecoverWrite` imports again when the interrupted write had
  not, and finds a session `Write` gave its own id (derive it from `OperationID`). `AfterInstall`
  (moves) imports idempotently (it runs again after an interrupted move), and a session already at
  the id with other content than the placed anchor is `ErrDiverged`, never success: it may be work
  done since a first, interrupted run, and the core then leaves it out of the undo unless it
  stamped it as the operation's. `Restore` refuses over newer work (the core also checks the
  stamp first and restores only a session that is gone).
- PR 2 (reads): a missing database is `ErrNotInstalled` from the Host's SQL, and `ErrNotFound`
  means only "no such session". On another machine a WAL database no program has open reads as
  `ErrUnsupported`: use tier 3 then.
- The core stamps a session the agent made right after the import or write, so a later Seal (a
  recovery's) keeps that stamp. Undo keeps a set-aside session's backup until it is imported
  again; a set-aside the agent refused (used between backup and delete) leaves the journal.
- Undo of a session kept in a database reaches other machines over SSH, as the move did (cd undo
  still runs on this machine only); the activity list checks this machine's sessions and leaves
  other machines' to the undo (`journal.ErrOutOfReach`). A receipt whose session is gone is
  dropped (stale), not retried; one whose database is missing stays pending.
- The remote SQL probe still runs on its own 20-second context (`SQLHost.SQL()` takes none); a
  failed probe is kept 30 seconds.
- PR 11 (append into an existing session): the continue path reads an expected destination by its
  key (`Copies`), but appending and its rollback are not exercised with a virtual target yet.

**Left by PR 2 for later PRs.** `agents/opencode` now lists, previews, reads (SQL, JSON lines
and the API tier), renames, detects live turns, stops them, probes accounts and capacity, and is
registered. Fixtures are the 2.3.0 sandbox capture plus `testdata/synthetic/` (part rows with
inline content, unsettled messages, an unknown type, an attachment, a skill, and fake credential
rows that tests prove are never read).
- Resume: a new optional `agent.HostResumer` (`ResumeOn`) checks the session exists and picks a
  program of the running service's version (PATH, the desktop's bundled `opencode-cli` with its
  `.version` file, or a staged `{desktop}/cli/<ver>-<arch>/opencode-cli`). `app.Resume` uses it
  for sessions on this machine. Commands made at plan time (moves, continues) still come from the
  pure `Resume` with the PATH `opencode`: PR 4/5 should resolve the printed command after the
  import. D2 is a refusal with an explanation (`restartError`); "ask, then resume anyway" needs a
  confirmation in the user interfaces and a `ResumeOptions` field (PR 7/8).
- Moves: `Verify` refuses every move (`ErrUnsupported`) until PR 4 adds the import
  (`VendorInstaller`), `SessionStore`, children import and keep-both id remapping. `PlanMove`
  already places the main file and `sessions/<id>.children/<child>.jsonl`, with the §3.8 policy.
  Not expressible as a `RewritePolicy`: stripping `native` from content items and dropping
  `compaction.native` for another account (only reasoning items are dropped); `uri` is not
  protected (a `media:` ref holds no path).
- Materialize leaves out the session's `permissions`: its column is named like the denied
  `permission` table, which `CheckSQLQuery` refuses to mention. PR 4 decides (export for moves,
  or a narrower rule).
- API tier: `List` leaves `Size` 0 (counts need an export per session), `SessionStamp` exports.
  `GET /api/session` is paged by `cursor.next` (tested with a fake over three pages).
- Reader: a subagent's answer arrives as a `synthetic` message and is skipped like all
  synthetic messages; the child's reply is not folded into the `subagent` tool result (PR 5).
  Tool inputs are re-encoded with sorted keys, because the export and the database order keys
  differently and a node id covers the input bytes.
- The reader's file mode is used only where nothing runs (Exec fails: a peer's package); a
  machine without SQL reads OpenCode's export first, since an anchor hopsesh wrote may be
  stale. PR 4's reads of staged files need a file-only entry (`decodeJSONL`).
- `Summary.Size` counts messages. `Entry.SizeUnit` says so, `ls` and `show` print "24 messages",
  and byte totals leave them out. The GUI shows no per-session size yet (PR 8, if wanted).
- Live: TUI processes (`-s <id>`) are not detected: the Host lists no processes, and asking for
  the holders of `opencode.db` would flock the database.
- TitleSource is never "custom": a hopsesh rename is not recorded yet (sidecar, PR 4).
- Account: providers configured only in `opencode.json` are not listed by `auth list`, so a
  working local-model setup reads "no provider signed in" (PR 7).
- No `desktop` root: roots are writable, and the staged CLIs are only read, by path.
  `TerminalEnv` cannot hold `OPENCODE_SESSION_ID` (it takes `NAME=value`); resume commands unset
  it instead, as Claude's unset its session markers.
- Tests that use `testkit.Env` find the real `opencode` through its absolute `Candidates` (as
  they find a real `codex`), so they may run `opencode --version`, `service status` or
  `auth list --standalone` under the test's home. `testkit.Env` now empties the `XDG_*` and
  `OPENCODE_*` variables; OpenCode reads an empty `XDG_STATE_HOME` (`??`) as a relative folder,
  so PR 3's stand-in should unset them instead.
- Drift: a minimal `agentWatch["opencode"]` (docs, releases, help, code paths, formats, the
  2.1.0 floor in `service.go`); PR 9 adds migration ids, the OpenAPI hash, the plugin API and the
  desktop app entry. The docs URLs assume the `dev` branch.
- Sandbox finding: OpenCode mints a session id up to a few hundred milliseconds before the row's
  `time_created`.
- After review: every read goes through the schema gate (cached per database and migration id);
  `session_part` and `time_busy` are optional, so 2.1.x and 2.2.0 databases read; rows written before
  2.1 are upgraded as `SessionLegacy` does; `Live` reads the execution claim and asks the service
  only for rows with a turn in flight; a failed `service status` refuses a resume; a missing program
  (exit 126/127) is "did not run", and only "Session not found" or an HTTP 404 on a session is
  `ErrNotFound`; `ContextCapacity` falls back where the database cannot be read and counts from a
  compaction newer than the last reply; the catalog is read from `models-dev:v2:catalog` or a custom
  source's `models-dev:v2:catalog:<sha1>`; channel databases are watched; the SQL allowlist is the
  five tables read.
- PR 4: `Materialize` keeps stored `media:` refs (user `FileRef`, tool `ToolFileRef`) where
  OpenCode's export hydrates them from `blob`, which is not allowlisted: it matches the export only
  for sessions without media. Decide with `permissions` (above) whether moves use the export.
- API tier: `List` also leaves the prompts and the OpenCode version empty (an untitled session stays
  untitled there); prompts would need an export per session.
- Reader: a user `!cmd` shell that settles after a later message shifts the settled indexes, so a
  cursor taken in between may read as `ErrDiverged` (not reproduced; a full re-read recovers).
- PR 7: `Live` sets no PID, `Procs` or `App` (Codex sets all three); the GUI's `terminalEnv` matches
  a program's base name, so a resume through the desktop's `opencode-cli` shows that name in its tab
  title (no other effect: OpenCode has no `TerminalEnv`).
- PR 9: the agenttest suite has no subtests yet for Previewer, LiveDetector, Stopper, AccountProber,
  ContextSizer, Renamer, HostResumer or SessionWatchProvider, and plan-move-identity checks only the
  first session; the drift entry has no real-agent `Tests` pattern until real-agent tests exist.

**Left by PR 3 for later PRs.** The stand-in `opencode` is `internal/testkit/fakeopencode`
(`fakeagent.OpenCode()`; the fakeagent program as `opencode`; in the test bundle). It keeps a
modernc database with the 2.3.0 schema where OpenCode keeps it and plays `--version`, `debug
paths`, `service status`, `session list|export|import|delete`, `auth list --format json`,
the TUI's service start and the API calls hopsesh makes. Its export of the sandbox's rows is
the 2.3.0 export byte for byte (all 14 fixtures), and an export imported and exported again
is unchanged but for the update time, folder, project and fork. Its import decodes the
transfer with 2.3.0's schemas (`decode.go`, ported from `packages/schema/src`): a wrong
shape is "Bad Request", and members the schemas do not declare are dropped, as OpenCode
drops them. Differential runs against the real 2.3.0 (`--standalone`, `XDG_*` and HOME in
a scratch copy of the sandbox) cover 140 cases: import (success, duplicate, children,
missing members, wrong shapes, unknown members, links, subfolders), export, list, the API
(pages, filters, get, rename, archive, permissions, messages and their cursors, active,
interrupt, plugin, fork, move) and delete. All match except the queued move (below) and
project ids that hash a scratch folder's own path. `DemoHome` has three OpenCode sessions
(and one child) in `git/demo`; `demoseed -opencode`, `skillcheck`, the test bundle
(stand-in, SQL and export fixtures with scrub checks, an `export/` slot whose exports must
decode without a dropped member, `opencode-transfer.schema.json`) and `opencode-list.txtar`
use it.
- Sandbox findings (F11 and others): an import whose message ids exist fails with "Error:
  Failed to import session: Internal Server Error" (keep-both must re-mint `msg_` ids, PR 4);
  a missing `--directory` is "LocationNotFoundError: Location not found: <dir>"; an unreadable
  file is "Error: Failed to read session data: ENOENT…", bad JSON "SchemaError: Expected a
  valid JSON string"; any shape the schemas refuse (an assistant without `agent`, a tool
  call without `state`, a user file given as a `media:` ref, an unknown `finish`) is "Error:
  Failed to import session: Bad Request"; times are kept in whole milliseconds. Errors end
  with Bun stack lines (the stand-in prints the first line only). `session delete` prints
  "Session <id> deleted". API errors print the body (`{"_tag":…,"message":…}`, with `"kind"`
  "Params", "Query" or "Payload" for a request that does not decode) on stdout and "HTTP
  <code> <text>" on stderr, exit 1; an unknown path prints no body. `session list` lists the
  working folder's project's top-level sessions, archived ones too.
- Session info: OpenCode prints `location` after `title`, `subpath`, `metadata` and
  `permissions`, against its schema's order (`revert` is assumed to come before it too; no
  capture has one), and a `location-switched` message's `projectID` and `subpath` before
  `location`. `subpath` is `path.relative(project folder, folder)`: the project folder has
  its links resolved and the session's folder does not, so a folder reached through a link
  (macOS's `/var` for `/private/var` included) gives "../…"; it is absent at the project
  root. PR 4 must carry it as OpenCode wrote it.
- Fork (PR 4's `VerifyNativeFork` and D5): the title is "<title> (fork #1)", a fork of
  "<t> (fork #n)" is "<t> (fork #n+1)", an untitled session's fork has no title. The
  boundary is `before`, else the last message of any kind ("through"); the fork holds the
  settled messages up to it, each with the parent's `seq` and the id `<event's msg id>_<seq>`
  (seq gaps stay: `_5, _6, _18, …`), their part rows, a compaction's `keepID` remapped, and
  the parent's folder, agent, model, metadata and permissions. A `before` that is not there
  is 404 `MessageNotFoundError` (hopsesh's `apiError` reads any 404 under `/api/session/` as
  `ErrNotFound`: PR 4 should tell "message not found" apart); a session without messages is
  400 with `"kind":"empty_session"`.
- PATCH `/api/session/{id}`: `archived` true/false sets or clears `time.archived` (and the
  update time); `permissions` replaces the rules; members it does not know are ignored; an
  empty `title` asks a model for a title, which the stand-in has not (it answers 500).
  `GET /api/session` filters by `project` and, with it, `subpath` (alone it is ignored);
  a cursor carries the filters. Messages: 50 a page by default, `?type`, a cursor cannot be
  combined with `order` (400 `InvalidCursorError`), and a cursor naming a message the session
  does not have gives an empty page. Empty pages print `"cursor":{"previous":null,"next":null}`.
- PR 4 (`cd`): `api POST …/move` with `--standalone` answers 204 but the move stays queued:
  the private server exits before the next step boundary, and the session keeps `time.busy`
  with no idle, so it then reads as busy. The stand-in applies a move at once. Decide how a
  `cd` with the service stopped confirms (or refuse it). A relative folder is taken from the
  session's folder, "~" from the home folder.
- OpenCode reads the `XDG_*` variables and `OPENCODE_CONFIG_DIR` with a truth test (empty is
  unset), `OPENCODE_DB` with `??`: an empty `OPENCODE_DB` opens the data folder as the
  database. (The PR 2 note said `XDG_STATE_HOME`; PR 3's first note said
  `OPENCODE_CONFIG_DIR` too. Both were wrong.) `testkit.Env` still marks them with "", and
  `testkit.Setenv`, `Apply` and `Environ` unset what is empty (every caller uses them; the
  Playwright real setup deletes empty values). Harnesses that build their environment from
  this process's call `testkit.UnsetAgentEnv` (their own home) or use `testkit.OpenCodeEnv`
  (the machine's home kept: the remote scenario test, hsmatrix), so no test reads the
  machine's real OpenCode data. Tests using `testkit.Env` can still find an installed real
  `opencode` through its absolute `Candidates`; with the variables unset it would run on the
  test's home.
- Turns: `$FAKE_OPENCODE_ACTIVE` lists the background service's turns; their rows claim the
  turn (`time_suspended`, a `time_busy` after the last idle), as the service's do, and
  `active`/`interrupt` on a private server (`--standalone`) see none. An interrupt adds an
  `idle` "interrupted" message. The TUI (`opencode [dir] -s <id>`) starts the service, or
  logs that it replaced one of another version ("opencode: replaced the background service
  <old> with <new>"), prints one line and exits 0.
- What the stand-in does not play: rows written before 2.1 (no `SessionLegacy` upgrade),
  media in `blob` (refs stay, and an export of a session with media is not OpenCode's),
  `--sanitize`, event rows, instruction state, jobs and title generation. The project id
  reads `<git common folder>/opencode` after the origin, as OpenCode does; git runs with the
  stand-in's environment.
- PR 6: `GET /api/plugin` lists no plugins (the sandbox listed none for a local plugin folder
  either); teach it the real shape once captured. `skill.txtar` is unchanged: OpenCode has no
  `Integration` yet, so `skill install` still prints "(Codex)". The plugin event payload slot
  and `opencode-plugin-notice.schema.json` come with the plugin.
- PR 4/5: the stand-in is not in `fakecloud.Programs` (it writes real files, and FakeHost
  tests keep the module's own fake); an in-process program over `agenttest`'s `SQLFile` can
  join it. The scenario's same-machine `pull --to` is refused by the generic "destination is
  synchronized" rule before `Verify` (the scenario now checks that message); `opencode-move.txtar`
  comes with moves.
- PR 8: the test world's `DemoHome` now lists OpenCode sessions; Playwright specs that count
  the demo's sessions or agents may need updating (not run in this PR). On Windows its
  sessions' folders use `\`, as `path.resolve` gives them (not run on Windows).
- PR 9: the differential runs (`run.py`, `run2.py`, `run3.py`) and the capture scrubber
  (`ocscrub.py`, §7's `internal/devtools/ocscrub/`) are scripts in the session's scratchpad;
  make them devtools for the nightly real-agent job.
- PR 8: the browser specs (`internal/devtools/webtest`) still run on `testkit.DemoHomeClaudeCodex`,
  the demo home without OpenCode's sessions; switch them to `DemoHome` and update the counts,
  agent subtitles and filters they assert when they gain OpenCode.

**Left by PR 4 for later PRs.** OpenCode sessions now move between machines, are kept beside
an original, set aside, backed up, restored and deleted, change folder (`cd`), and native forks
are verified. Every change goes through OpenCode's program (`session import|delete|export`,
`api POST …/move`), with `--standalone` when `service status` says the service is stopped; the
module never writes the database. Done here from the earlier notes: `VendorInstaller`
(`install.go`: AfterInstall imports the placed copy, the parent first, children after their
parents; a session already there with the placed messages is done, one with other messages is
`ErrDiverged`; success only on "Imported session: <id>", so exit 0 with "Session already exists"
is checked again, never taken as success), `SessionStore` (`store.go`), keep-both id remapping,
children, `cd`, fork/orphan, `Verify` (it checks the staged copy instead of refusing), the
resume command resolved after the import, a file-only read of staged copies (`Read` reads
`Summary.Path` as a file whenever it is not the session's anchor), and TitleSource "custom".
Decisions and what is left:
- **Materialize reads OpenCode's export** (`session export`), with the version that last wrote
  the session from the database, wherever OpenCode's program runs. It is what the import reads:
  the session's permission rules (the column the module cannot name) and its media inline (the
  database keeps `media:` refs, which an import refuses as "Bad Request"). Where nothing runs it
  is made from SQL as before, and a session with media is refused (`ErrUnsupported`). Backups
  are OpenCode's exports too (`{data}/hopsesh/opencode/backup/<id>-<ns>.json`, children in
  `<…>.children/<n>-<child>.json`); a restore imports those files as they are, ordered by their
  `parentID` (a cycle is refused). Moves cost an export (and a `service status`) per family
  member at the source. An export a move or backup carries runs a private server only behind
  the same version check as a change (`writeService`); when that check fails, or the service
  state is unknown, the session is read from the database instead (media refused), so risk 13's
  list of standalone calls now reads "the account probe, writes, exports behind the write
  check, and reads where SQL is unavailable".
- **Undo keeps a backup whole.** The journal keeps, besides the backup file a set-aside entry
  names, every file the operation created beside it named after it (`<name>.<anything>`, so
  OpenCode's `<…>.children/`) while the session is not put back, and removes them together once
  it is. An import gives a restored session a new `time_updated`, so a restore that stopped
  halfway could not be told from other work by its stamp: undo now marks a vendor-session entry
  `Undoing` when it deletes a created session or begins a restore, skips such a deleted session
  on the next undo, and finishes such a restore by calling `Restore` again (idempotent: the
  same message ids are done, others are `ErrDiverged`, the only "other work"). A set-aside
  session at another stamp is likewise restored when forced, kept when `Restore` refuses. A
  set-aside session is never put back while the session the operation made at its id is
  still there.
- **The program's folders are pinned.** Every OpenCode command gets `XDG_DATA_HOME` and
  `XDG_STATE_HOME` from the machine's facts (as Detect resolved the roots), so it writes the
  database hopsesh read and asks for the service registered there, also when hopsesh runs with
  other variables than the person's shell. An account profile's variables (PR 7) come after.
- **Keep-both ids.** The core mints the copy's id and its rewrite renames the session's id; the
  module gives the children and every message new ids (message ids are keys of the whole
  database), each the old id's time part plus a tail hashed from the copy's id and the old id
  (a fork's `_<seq>` kept), so a recovery makes the same ids. They are replaced only where
  OpenCode refers to them: session `id`/`parentID`, `revert.messageID` (2.3.0's import drops
  `revert`), message `id`, `metadata.keepID`, a subagent answer's `metadata.childID`, a tool
  state's `metadata.sessionID`. Text, tool input and output stay as staged, so the copy reads
  as the staged conversation (its IR node ids and lineage heads match); a subagent's answer, or
  a later `subagent` call's input, may still name the original's child. The core maps such a
  copy to its source node for node (`move.renamedProjection`, only when every node has the
  source's content in order), as it cannot pair them by anchor.
- **Stamps** are the parent's (settled count and update time). `session delete` deletes the
  children too; a child used on its own since is not guarded separately (left: fold the
  descendants' counts and updates into the stamp, which changes every stamp hopsesh recorded,
  so it waits for a stamp format version). `Delete` reads the stamp right before the delete;
  the service can still add a turn in between, since OpenCode's delete names no state.
- **Install failures.** Only the copy's own divergence is `ErrDiverged` (the core then drops
  the session from the journal); a child that OpenCode has with other messages fails the
  install with a plain error, so the root this run imported stays the operation's and undo
  deletes it. A placed family whose parents make a cycle is refused before any import.
- **Unknown service state.** When `service status` fails, a change (import, delete, restore,
  rename) is refused (`unknownService`), as `cd` and resume already were; only reads where SQL
  is unavailable still run a private server.
- **Cross-version moves** are refused when the copy was last written by a newer OpenCode than
  the running service (or, stopped, the program): first through the new optional
  `agent.InstallChecker`, which `move.Apply` asks with the session's `AgentVersion` before it
  sets aside the copy the move replaces (so nothing here changes), then again in AfterInstall
  for every session of the placed family. A session holding a message
  type the target OpenCode does not know fails at its import (2.3.0 answers "Bad Request"), as
  a restore of such a backup does.
- **`cd` needs the running service** (§2 F12 and the PR 3 sandbox finding): a private server
  takes the move, exits before the step boundary and leaves the session marked busy. With the
  service stopped (or unknown) the plan is blocked with "type /cd <dir> in the session, or start
  OpenCode", and nothing runs; undo of a `cd` needs it running too. With it running, hopsesh
  posts the move and waits (30 s) for a newer `location-switched` message naming the folder:
  every 100 ms from the database, or, where hopsesh cannot read it, every second through
  `api GET …/message`, asking `service status` before each call and stopping when the service
  no longer runs (a call without `--standalone` would start it again). `PlanChangeDir` does
  not create the anchor §3.1 and §3.5 planned: the plan's `Main` is the summary's path (the
  anchor), and nothing in a `cd` writes there.
  A same-machine `pull --to` is now this folder change. The stand-in now plays the private
  server's move as 2.3.0 does (queued, `time_busy` set, no message); the service applies it at
  once. Not run against the real service (the sandbox never starts one).
- **Resume after the import.** `move.Apply` asks `HostResumer.ResumeOn` on the target once the
  session is there; a refusal (D2) leaves the plan's command for the user to read, with the
  reason as a warning, but not runnable: `Result.Run` and the plan's `Resume` are empty, so
  `pull --run`, the TUI and the GUI do not launch a command that would restart a service of
  another version. A
  no-work sync (`sync.go`), recovery's early return (`operation.go`) and continuations
  (`continue.go`, PR 5) still print the pure `Resume` command.
- **Forks.** `VerifyNativeFork` reads both sessions with SQL (local only; `ErrUnsupported`
  elsewhere) and compares the fork's messages up to its boundary with the parent's by `seq`,
  then node by node. `NativeParentGone` is the parent's row missing. Fork on resume (D5:
  `api POST …/fork` then resume the child, CapFork) is not done: PR 5. `forkInputs`
  (`internal/app/statmemo.go`) keys its cache on the anchors' stats, which a virtual session may
  not have, so OpenCode forks are verified again on each scan (cheap with SQL; PR 9 could key
  it on stamps).
- **Titles.** hopsesh records the titles it gives (a rename, a keep-both copy) in
  `{data}/hopsesh/opencode/titles.jsonl`, one `{"id","title"}` line each (the last counts),
  written as standalone appends: an operation's undo takes out its own line, and a line added
  later never makes an earlier undo refuse (a whole-file rewrite through the journal did). A
  session whose title is still the recorded one lists as "custom". A write that fails is
  ignored (it is a listing note). Undoing a rename takes out its record but does not rename
  the session back in OpenCode (the PATCH is not journaled): left for a later PR.
- **Leftovers on disk.** The transfer files AfterInstall writes for `session import`
  (`{data}/hopsesh/opencode/tmp/<id>-<content hash>.json`, so another operation never rewrites
  one) and backups stay until an undo removes the journaled ones; the module host has no
  remove. A later PR could clean `tmp/` after a sealed operation.
- **Move preview warnings.** `agent.Bundle` now carries `Warnings`, which the core adds to the
  move plan's. OpenCode's say that a window showing the session while it is idle cannot be
  seen (risk 5), and that a session of the `claude-agent` provider moves as an OpenCode
  session (non-goals).
- **Stated losses** are unchanged (archived state, slug, fork fields, times, instruction
  state); the move preview does not list them yet (PR 8 copy).
- **Real-binary check** (opencode 2.3.0, a fresh copy of the sandbox, `XDG_*` and HOME in the
  scratch folder, a guard program that refused any command without `--standalone` and answered
  `service status` itself): all 12 captured root sessions moved to another data root through
  hopsesh's import exported back with messages equal to the source's export (folder
  rewritten), info differing only in time, project and (forks) fork; the subagent session came
  with its child; keep-both made a copy with new session, child and message ids beside the
  original; undo of the keep-both deleted only the copy; a delete at a stale stamp was refused,
  at the right one it deleted the session and its child; a backup restored twice gave one
  session with its child; the unforced undo of the move was refused after that restore and the
  forced one deleted it. `cd` was refused (service stopped). The harness was not committed
  (PR 9 makes such checks devtools). Re-run after the review fixes (a fresh copy, the same
  guard; 93 commands reached the real binary, every one `--standalone`): the same results,
  plus: the move preview carried the idle-window warning; in a keep-both copy the subagent
  answer's `metadata.childID` named the copy's child; `titles.jsonl` held the copy's line and
  its undo took it out; a set-aside made through the journal, undone while its folder was
  gone ("Location not found"), kept the backup and its child's file, and a second undo with
  the folder back restored the session with its child and removed both files; a restore after
  the root alone had been imported (its stamp now another: 2.3.0's import sets
  `time_updated`) imported only the child.

**Left by PR 5 for later PRs.** Sessions now continue between Claude Code or Codex and OpenCode.
`writer.go` writes OpenCode's own transfer (§4) at the session's anchor and has OpenCode's program
import it under `WriteRequest.SessionID` (the core runs no AfterInstall after a write);
`RecoverWrite` finds the session by the same id (or the one made from `OperationID` when the
request had none in OpenCode's form), checks the placed file is the one the request makes (the
same bytes: ids, times and the session's creation time, read back from the file, are made from
the request), writes it when the write stopped before it, and imports it when OpenCode does not
have it; a session there with other messages is `ErrDiverged`. Done here from the earlier notes:
the projection (each written node's item, a shell call's call and result), fetch/adopt append
gating (`PortableAppend`), the remaining resume call sites (a continuation, a no-work sync,
recovery and a resume where the session already is now go through `finishResume`, so
OpenCode's `ResumeOn` checks the session and picks the program; a recovery keeps the fresh
plan's resume options), and a subagent's answer folded into its call's result. The id minting
on the capacity-rollover and fetch paths was already in the core (PR 1b); PR 5 tests both with
OpenCode. Decisions and what is left:
- **Return into an OpenCode copy (D4).** A writer without `PortableAppend` gets no append from the
  core: where the copy here is behind the source (an append for Claude or Codex), the plan rolls
  over to a new session that continues it, as for a full destination (lineage `Rollover`; the
  copy stays as it is; the plan warns "OpenCode cannot add to an existing session…"). PR 11
  replaces this with delete + re-import.
- **Replies need a model.** OpenCode's schema requires `model` on every assistant message; hopsesh
  names `{id: <the source's model, or "unknown">, providerID: "hopsesh"}`. OpenCode replays a
  reply's provider state only to the model that made it, and no provider is called `hopsesh`, so
  nothing is replayed (hopsesh writes none). The session names no model or agent: OpenCode
  continues it with the model and agent it would pick for a new session. A bounded continuation
  of an OpenCode session could name the source's model (same machine and profile); it does not
  yet. How the TUI and desktop app show such a reply's model was not looked at.
- **Shape corrections to the plan.** Shell calls are `executed:false` (true means a tool the
  provider ran itself; OpenCode would then not send the result as a tool message), a tool item
  needs `time` (`created`, `ran`, `completed`), and the session has no `agent` (the captured
  sessions have none). The idle messages are kept (OpenCode's own turns end with one); whether
  an import without them works was not tried. A call that exited non-zero gets a second text
  item, `Exit code: N`, as OpenCode's own shell tool gives its model (the exit status in
  `metadata.exit` is not shown to it); an output read from OpenCode that ends with that line is
  not given it twice. The folder a call ran in (`ir.Shell.Dir`, which Codex records) is written
  as the shell input's `workdir`, its paths mapped by the converter like the command's. Every
  message time is before the write's own (room left for the messages after it), so a source
  clock ahead of this one never gives a written message an id after the ones OpenCode makes for
  the next turn.
- **Listing.** A session's first and last prompts leave out hopsesh's notes (`[hopsesh] …`, the
  briefing a continuation writes as a user message), so a written session lists the person's
  last prompt. Capacity leaves out hopsesh's replies (no tokens, no model): a written session's
  usage is its bytes, and a new session's model is never `hopsesh/…`.
- **Subagent answers** (PR 2's note): a synthetic message from a subagent's session (metadata
  `source:"subagent"`, `childID`) is read as a second result of the call that started that
  child (what the call returned, then the answer), where the answer came, so the nodes before it
  do not change when it arrives; the converter uses a call's last result. Node ids are chained,
  so every node after an answer has a new id once, at this upgrade, against what the PR 2–4
  reader gave: a cursor or lineage head taken past an answer before it reads as diverged (only
  development data; OpenCode is not released). A consumer that pairs a call with its first
  result only would miss the answer. An answer whose child no call names is still left out. An
  answer that arrives after its call was carried (a parent continued while a background
  subagent still ran, continued again once it answered) is lost on the incremental return: the
  call is covered, so only the answer node is rendered, and the converter renders results only
  through their call (`convert.history` has no case for a lone result). Not reproduced; a fix
  renders such a result as text (PR 9, with the two-agent loops).
- **Fork on resume (D5)** is not done: it needs a fork through `api POST …/fork` at the moment a
  resume is launched (journaled, from a UI action), not a plan-time command; CapFork stays off.
  PR 7/8, with the D2 confirmation, which also needs a `ResumeOptions` field.
- **Writer and versions.** The writer uses message types every 2.1+ OpenCode has, and checks no
  target version; only 2.3.0 was run. A move still checks versions (`CheckInstall`). A write
  through a running service of another version is not checked either: `session import` then
  runs in that service, whose import schema may differ from the program's (a D2-like risk for
  writes, not for resumes). PR 7/8 decides with the D2 confirmation whether a write asks too.
- **Hand-offs.** An OpenCode session handed off to a cloud and brought back into OpenCode comes as
  a new OpenCode session (tested with Codex cloud); asking to add the cloud's work to the
  original (`--append`) is refused at planning ("OpenCode cannot add to an existing session…").
  Cloud briefings now say "an OpenCode session".
- **`--target-session` naming an OpenCode copy** rolls over to a new session that continues the
  named copy, with the same warning, as a full destination does; it is not refused, because a
  plan with several OpenCode copies asks which one to continue, and that choice still matters
  for the lineage.
- **Recovery.** A recovered continuation journals the session it is to make before
  `RecoverWrite` (the interrupted run may have saved its request and stopped before journaling
  it), and a target it cannot reach for the resume check keeps the planned command, as a
  no-work sync does.
- **Stamp at the write, not fixed.** The stamp of a written session is read in a query after the
  import (`stampMade`; in a recovery after `RecoverWrite`'s message check). A turn the person
  added to the new session in between would be recorded as the operation's, so an unforced
  undo would delete it. The window is one query; closing it needs the writer to give the stamp
  it left (with its settled count checked against the messages it wrote) and Seal not to stamp
  a session the write found used (an SDK change: a stamp on `ir.WriteResult`). PR 9 or later.
- **A written session can start with a reply.** A history that begins with an agent item (a
  compaction summary, which the converter renders as agent text, or a fitted history cut
  before its first prompt) is written with an assistant message first. Whether OpenCode 2.3.0
  sends such a history to providers that refuse one (Gemini, older Anthropic models) as it is
  was not checked; this is the converter's for every writer, not OpenCode's alone. To check
  with PR 9's sandbox runs (a compacted Claude Code session continued into OpenCode); a fix
  writes an OpenCode `compaction` message or a prompt first.
- **Attribution and fidelity, minor.** Every written reply names `Header.Model`, the source's last
  model, also in a session that switched models. Attachment placeholders ("[an attachment …
  was here]") are user messages, so they can be a written session's listed first or last
  prompt (`ownPromptSQL` leaves out only `[hopsesh] `). A person's own shell command from an
  OpenCode `shell` message is replayed into another agent as the assistant's tool call (the
  converter's, from before). A failed call with no output and no exit status reads back as
  "The command failed.".
- **Tests carried to PR 9.** PR 3's note item "the stand-in is not in `fakecloud.Programs`" is
  still open (the cloud test links the test binary as `opencode` instead); PR 9 joins it there
  or drops the item. §7's OpenCode legs in `context_capacity_test.go`, `lineage_routes_test.go`,
  `peer_test.go` and a remote continue into OpenCode are not here (PR 5's are in
  `opencodecontinue_test.go`); PR 9, with the two-agent loops. A resume where the session
  already is (`ResumeHere`) goes through `finishResume` but no test reaches it with OpenCode
  (it needs an account boundary OpenCode does not have). The D2 tests use a service version
  no desktop app ships, since `desktopCLIs` also reads `/Applications`; the e2e world is not
  hermetic there (PR 9: let tests set the desktop app folders).
- **Leftovers on disk.** The written anchor is journaled; undo removes it, the transfer file in
  `tmp/`, the title line in `titles.jsonl` and the lineage beside the anchor. After a kept
  operation the transfer file in `tmp/` stays, as for moves (PR 4).
- **Stand-in.** `$FAKE_OPENCODE_FAIL` ("session import") makes a command fail as a program that
  died would; the recovery tests use it. The test bundle's `export/written-by-hopsesh.json` is
  real 2.3.0's export of a session hopsesh wrote (shell calls, a failed call without an exit
  status, notices), scrubbed.
- **Real-binary check** (opencode 2.3.0, a fresh copy of the sandbox, a guard program that refused
  any command without `--standalone` and answered `service status` itself; Claude Code and Codex
  data in the scratch folders): a written transfer with every item kind imported, and OpenCode's
  export of it equalled the written messages; a Claude Code session and a Codex thread continued
  into OpenCode through hopsesh (the Codex one interrupted after the write and recovered: one
  import), OpenCode's exports equalled the placed messages (no `model` on the session, the title
  kept), hopsesh listed them with the person's last prompt and read them back; undo of the Codex
  one deleted it through OpenCode. Then one `opencode run -s <id> --standalone` prompt against
  omlx (D9; data, state and cache in the copy, the config dir the user's, unchanged): OpenCode
  continued the written session and answered from its history ("PLUM-7"). Continued back, the
  reply was added to the original Claude Code session (its old lines byte for byte) and written
  as a new Codex thread, without the earlier legs' notices. Through the guard, 7 commands
  reached the real binary (imports, exports, a delete), every one `--standalone`; the prompt was
  the only other run. Run twice on fresh copies with the same results; in one run the local
  model's reasoning called a hopsesh notice in the history "a prompt injection attempt" (and
  still answered from the history), which is about the notices' wording, not OpenCode. The
  harness was not committed (PR 9). Re-run after the review fixes (a fresh copy, the same
  guard): the transfer `writeRequest` makes (`writer_test.go`, now with the `Exit code: 2` line
  and a `workdir`) imported, and real 2.3.0 exported exactly its messages (that export, the
  folder scrubbed, is the bundle's `written-by-hopsesh.json`, which `TestWrittenAsExported`
  compares with the writer); the Claude Code and Codex legs, the recovery (one import) and the
  undo as before; one `opencode run -s <id> --standalone` prompt answered "PLUM-7" from the
  history (config dir listing unchanged); back into Claude Code (an addition, its old lines byte
  for byte) and into Codex (a new thread). 8 commands reached the real binary through the guard
  (3 imports, 3 exports, a delete, `--version`), every change `--standalone`.
- **PR 6:** a session continued into OpenCode keeps its lineage at `agent.AnchorRel`, so the
  notice-hook lookup by session id finds it; until the plugin exists, the OpenCode copy a
  continuation leaves behind is unprotected and the result says so ("not protected").
  Continuations print OpenCode's resume command from `ResumeOn`; a D2 refusal leaves it shown,
  not runnable, as for moves.

**Left by PR 6 for later PRs.** OpenCode sessions are now protected: `hopsesh notices
install` puts hopsesh's plugin in `{config}/plugins/hopsesh` (a `HookDir` the app installs
whole with `agent.InstallHookDir`/`RemoveHookDir`, staged in `{config}/.hopsesh`, under a lock
file there), and the plugin refuses every tool call of a session whose copy hopsesh moved or
continued elsewhere, with hopsesh's reason. Done here from the earlier notes: the notice-hook
generalisation (`agent.NoticeHookProtocol`: the module decodes the plugin's call and encodes
its answer; Claude Code and Codex keep the command-hook JSON), the split of
`HookTrustReporter` ("reports trust") from the new `agent.HookApprover` ("runs a new hook
only after the user approves it": Codex), used for the GUI's `hookReview`, the SSH check's
"trust checked at login" and the needs list; hook folders in `machine_status.go` (read over
SSH by the program their notice hook runs); a hook the agent does not run (`HookMissing`) is
now a warning ("installed, not running in …") instead of "installed". Decisions and what is
left:
- **The plugin** (`agents/opencode/plugin/`): plain JavaScript, `index.js` with
  `Plugin.define` from the host's `@opencode/plugin` (nothing installed; `package.json` names
  the API range, `>=2.2.7 <3`, for people only), the logic in `hopsesh.js`, and a
  `config.json` per install: the notice hook's argv (spawned with no shell), the rules file,
  a 5 s timeout and a 30 s verdict cache. It asks the notice hook on every prompt (`session.hook
  ("prompt")`, never throwing) and on `session.created`/`moved`/`forked`, keeps a verdict per
  root session (a subagent's session follows its root, found with `session.get`), and asks again
  on a tool call with no fresh verdict (a restart, a resumed turn). While blocked,
  `tool.hook("execute.before")` throws the reason (every tool, read-only ones too: a prompt
  cannot be refused), `permission.hook("evaluate")` denies, and the instruction entry
  `hopsesh-notice` (keys are `[a-z0-9._-]`, no slash) tells the model why; an allowed verdict
  takes an earlier entry out. A hook that fails, prints nothing or takes over 5 s lets the call
  through, is logged to the server's stderr, and is counted in the plugin's storage (`kv`
  under `plugin:<hex id>:failures`, which `HookTrust` reads); the plugin then asks again only
  after 10 s, so a hanging hopsesh does not stall every call. A config without an argv
  registers nothing.
- **The notice hook's JSON for OpenCode:** `{session_id, directory, event}` in (no transcript:
  the core finds the anchor from the id; the folder is not used), and always a verdict out:
  `{"block":false}`, `{"block":false,"context":…}` or `{"block":true,"reason":…}` on either
  event. A failure prints nothing, which the plugin counts.
- **Trust.** `HookTrust` asks `opencode api GET /api/plugin` only while the service runs (a
  private server lists nothing: it answers before loading plugins, confirmed on 2.3.0), for the
  home folder, asking again for up to three seconds while the list lacks OpenCode's own
  plugins. hopsesh's plugin listed `active` in its folder is trusted; absent is `HookMissing`
  ("a config entry removes it, `-hopsesh` or `-*`, or a policy denies plugins": hopsesh never
  reads OpenCode's config, so it cannot say which); `failed` is `HookMissing` with OpenCode's
  error and log ref; another folder's `hopsesh` is `HookMissing`. With the service stopped
  trust is unknown ("loads when it starts"), and the machine counts the original as
  protected, as for an installed hook it cannot ask about. A removal in one project's
  `.opencode/opencode.json` is not seen (the list is the home folder's). The plugin itself is
  not hot-reload-tested (source says a change in a plugin folder reloads it).
- **Floor.** The plugin is offered for OpenCode 2.2.7 and newer (loading and hook
  registration seen on the desktop's 2.2.7 CLI in an empty sandbox, the hooks run on 2.3.0),
  judged by the program's version; a service of another version runs it (the plugin runs in
  the service). PR 7, with the D2 confirmation, could judge by the running service's version;
  a service too old only shows as "not loaded" now.
- **Rules** (`integrate.go`): `{config}/.hopsesh/rules.json` (`{format, bin, allow, ask}`, the
  core's `app.Rules`), owned by hopsesh and written by `skill install --add-rules`; the plugin
  reads it on each permission: every command of a shell call matching an allow prefix after
  the exact `bin` → allow, any matching an ask prefix → ask, else OpenCode's own rules; a deny
  the user configured never reaches the plugin. The rules do nothing without the plugin; the
  skill report says "present" either way (PR 8 could say so).
- **Skill.** OpenCode now has an `Integration`: it shares Codex's `~/.agents/skills` copy
  (`skill.txtar` reads "(Codex, OpenCode)"); a second copy in `~/.claude/skills` is one skill
  in OpenCode (source: skills are kept by id). The template says how a blocked OpenCode
  session behaves and that `$OPENCODE_SESSION_ID` names the session; its other examples
  stay for PR 10.
- **Accounts (PR 7).** Only the default config folder gets the plugin: OpenCode has no
  `Accounts` yet, so `movementHookInstalls` finds no profile. A profile's own
  `OPENCODE_CONFIG_DIR` needs its own plugin (the argv's `--profile` then names it).
- **GUI copy (PR 8).** Settings still says the hooks run "before a prompt", and "trusted by
  the agent" reads oddly for a loaded plugin; the guard shows "set up (not checked yet)" for an
  agent that asks no approval. The failure count is in `HookTrust.Failures` and the detail,
  shown by `notices status`, not yet by the GUI; it is never reset.
- **Leftovers.** An instruction entry stays in a session if the plugin is removed while the
  session is blocked (OpenCode keeps entries; the next allowed verdict would take it out).
  The staging folder keeps its lock file.
- **Stand-in.** It finds plugins in `{config}` and the working folder's `.opencode`, applies a
  config's removals and `plugin.use` policies, lists them from a running service (OpenCode's
  own plugins first; `-*` does not remove those), and with `$FAKE_OPENCODE_TOOL` plays a `run
  -s <id>` turn whose one shell call goes through hopsesh's plugin as Go code (no JavaScript),
  printed as 2.3.0's `run` prints a refused tool (`✗ <command> failed`, `Error: <reason>`);
  the turn's messages are not recorded and the command never runs.
- **Test bundle.** The PR 3 note's slot is here: `testbundle/opencode/<version>/notice/<event>/`
  holds what the plugin writes to the notice hook (`opencode-plugin-notice.schema.json`; the
  checker also has the module decode it), with a prompt's and an event's call as the plugin
  makes them (the folder scrubbed). The bundle's README describes the stand-in's plugins.
- **Plugin tests (PR 9).** `plugin/hopsesh.test.js` (Bun's test runner; 24 tests: verdict
  cache, cache-miss check, prompt hook never throws, allow/ask/deny, timeout and failures fail
  open and are counted, subagent root, setup) runs from `go test ./agents/opencode` where Bun is
  installed and skips otherwise. Bun is not installed on this Mac: they ran under Node with a
  `bun:test` stand-in (not committed), all passing. CI has no Bun yet: PR 9 adds it
  (`oven-sh/setup-bun`) so they run there.
- **Windows** (not run): the plugin spawns the argv as is (no shell, `windowsHide`), so the
  PowerShell wrapper of the command hooks is not needed.
- **Real-binary check** (opencode 2.3.0; a fresh copy of the sandbox, data, state, cache and
  home in the scratch folder, behind the PR 5 guard that refuses any command without
  `--standalone` and answers `service status` itself): `hopsesh notices install` wrote the
  plugin into a scratch config folder, which was then copied to the sandbox repository's
  project-local `.opencode/plugins/hopsesh` (the user's config folder, used for the omlx
  provider, was never written: its listing was the same before and after). Two `opencode run
  --standalone` prompts against omlx: a new session's `hopsesh version` shell call ran (the
  plugin asked the notice hook: its delivery receipt appeared), the session was continued into
  Claude Code with hopsesh, and then the original's `echo` call failed with `✗ echo … failed` /
  `Error: Blocked by hopsesh. Prepared in Claude Code on sandbox. …` and the model, which also
  saw the `api/hopsesh-notice` instruction entry, quoted the reason and told the user to unblock.
  The session's export shows the tool `state.status: "error"` with hopsesh's reason. No warning
  or error in OpenCode's log. A probe plugin also loaded and registered its hooks with the
  desktop's 2.2.7 CLI (an empty scratch sandbox). `api GET /api/plugin --standalone` and `GET
  /api/skill --standalone` answered empty lists (before loading), and `GET /api/model
  --standalone`, which waits for plugins, did load them. 11 commands reached the real binary
  through the guard (2 runs, a session list, an export, 2 api probes, 5 `--version`), every
  other one `--standalone`; the harness was not committed (PR 9).

**Total:** about 13–14k lines production and 9.5k test across PRs 0–10. Flip to `Stable` is a separate
one-line PR after D10's condition is met.

**Risks**
1. **Schema changes.** Migrations come in bursts (F25). Mitigations: column gate, writes only through
   OpenCode, drift watcher on migration ids and OpenAPI hash.
2. **Service restart by a resume.** The TUI replaces a service of another version (F13). Mitigation:
   version-matched resume binary, D2 prompt, checklist steps 2–3.
3. **Auto-start of the service** by subcommands. Mitigation: `service status` probe and `--standalone`;
   the probe/start race is harmless (§3.4). The opposite race is not: a call made without
   `--standalone` because the probe said "running" starts the service again if it stopped in between
   (`ServerConnection.resolve` → `Service.ensure`). Reads keep such calls rare: `Live` asks
   `/api/session/active` only when a row has a turn in flight. `service status` also prints "stopped"
   for a registered service that is starting, unhealthy or of an incompatible build; hopsesh cannot
   tell those apart without reading the registration, which it never does.
13. **`--standalone` is not free of side effects.** Every server process runs OpenCode's schedules
   (`Schedule.Service.run`; `server/src/process.ts`): a standalone server may take the schedule lease
   in `kv`, fire due schedules when `experimental.schedules` is on, wake sessions, and run a newer
   binary's migrations on the database. It never starts the service (restart continuity is
   service-only). Mitigation: as few standalone calls as possible (the account probe, writes,
   exports a move or backup carries, behind the same version check as writes, and reads where
   SQL is unavailable).
4. **Protection is weaker than Claude/Codex:** tools are blocked, prompts are not; a rejected tool is
   an error the model may route around; fail-open hides hook failures. Mitigations: notice entry,
   permission deny, cache-miss check after restarts, failure counter shown by HookTrust.
5. **Undetectable attached clients** (idle TUI, desktop window) let a move proceed while a client is
   attached. Mitigation: preview warning; protection blocks the left-behind copy.
6. **Undo deleting newer work.** Mitigation: stamp guard on every delete; backups before re-sync.
7. **`-s` creates empty sessions** for unknown ids. Mitigation: resume commands only after an existence
   check.
8. **Profiles leak `XDG_DATA_HOME`/`XDG_STATE_HOME` to child tools** (minor).
9. **1.x is still "Latest" on GitHub;** many users may see "unsupported" (D1).
10. **Fast release cadence;** Tested list goes stale. Drift watcher opens issues.
11. **Windows:** paths source-verified, install and runtime behaviour not run.
12. **The agent can move its own session** (`session_move` tool) without hopsesh; lineage records the
    directory hopsesh last saw, and the next List shows the new one.

**Still UNVERIFIED, each with how to verify**
- `Plugin.define` and hooks on a 2.1 service (PR 6: loading and hook registration seen on the
  desktop's 2.2.7 CLI and on 2.3.0, the hooks run on 2.3.0): a 2.1 binary in a sandbox.
- Hot reload of plugin directories (source: a change in a `{plugin,plugins}` folder reloads
  plugins; not run, since the sandbox never starts a service): with a running service.
- `permission.hook` allow/ask in practice (PR 6: a `hopsesh version` call ran in `opencode
  run` without `--auto`, which the rules allow, though OpenCode's default may allow it too;
  the deny was not seen, since `execute.before` refuses first): sandbox shell calls.
- Fork message-id behaviour: sandbox `api POST …/fork`.
- Whether child exports carry `parentID` (checked exports show none): export a session with a subagent.
- Skill dedup across two roots: source-verified in PR 6 (skills are kept in a map by id, a
  later source replacing an earlier one), not run (`GET /api/skill` on a private server
  answers before it loads them).
- MCP tool naming inside `execute`: source and a sandbox MCP server.
- Idle-message necessity on import: sandbox import without idles.
- Standalone server of an older binary on a newer DB: sandbox.
- Exact `auth login` syntax: `--help` snapshot.
- Windows install and runtime.

**Non-goals**
- Writing OpenCode's SQLite directly.
- Reading or copying credentials, `auth.json`, `service.json`, the DB file itself, or the desktop's
  `drafts.sqlite`.
- Using `--server` (needs the service password).
- Moving `/undo` snapshots or `shell/*.out` files.
- OpenCode sharing or cloud (discontinued or absent).
- OpenCode Console / Go billing beyond the read-only usage meters.
- 1.x plugin compatibility.
- Treating OpenCode's "Claude Agent" provider sessions as Claude sessions; they are OpenCode sessions,
  with a move-preview warning when `model.providerID == "claude-agent"`.
- Remote-control parity (Claude-only).
- Changes to the codonic.dev site within this work.

---

## 9. Review findings not followed, or followed differently

- **Read everything through the OpenCode API (accuracy review, "better alternatives").** Followed for
  remote machines and as the parity oracle in tests; not for local listing, preview and reader, which
  stay on SQL because the API spawns a process (and a standalone server when the service is stopped)
  on every refresh (§3.3).
- **Derive account owners from `model.providerID` (parity review, item 12).** Not followed. A provider
  is not a person or account, so `Account.Key` stays empty and moves are never treated as "another
  account" by default.
- **Version-matched binaries need SDK support for several probed candidates (parity review, item 5).**
  Not needed after the accuracy review: Exec uses the one probed `opencode`; resume binaries are found
  by reading the desktop's staged-CLI directory names, with no exec (§3.4).
- **Name OpenCode's data root `home` (parity review, item 3, option 1).** Not followed; the main-root
  accessor fixes the same sites without a misleading root name. There are more `Root("home")` sites
  than the review counted (58 occurrences including tests).
- **Extend schemalite with `oneOf` (parity review, item 23, option 1).** Not followed; the schema checks
  the envelope and Go checks the variants, which keeps schemalite small.
- **Persist the blocked flag with `ctx.storage` or a marker file (accuracy review, item 7).** Followed
  differently: a cache miss in `execute.before` asks the notice-hook, which reads hopsesh's own lineage.
  That survives restarts without a second copy of the verdict that could go stale.
- **Remote snapshot tier and the old Q2/Q3.** Dropped as the reviews recommended; no decision remains.
