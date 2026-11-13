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
| Live / busy detection | Full | Full | **Partial** | Signals: busy columns, `time_suspended` (a claimed turn that resumes on service restart), `GET /api/session/active` (meaning UNVERIFIED), TUI processes with `-s <id>`. An idle TUI opened from the picker or with `-c`, or a desktop window showing the session, cannot be seen. The move preview warns about this. |
| Stop | Full | Partial (never kills app) | **Partial** | `POST /api/session/{id}/interrupt`. Never SIGTERM the service. A TUI attached to the service is only a client, so it is not signalled (except a `--standalone` TUI hopsesh launched itself). `ErrSharedServer` semantics as for Codex. |
| Move to another machine | Full | Full | **Full** (with stated losses) | Export (parent + children) → JSONL bundle → `session import --directory <cwd>` on the target. Lost: `/undo` snapshots, fork fields, instruction state, slug, job rows, session-level times, archived state. Shown in the move preview. |
| Path rewrite | Full | Full | **Full** | The existing JSONL rewriter runs over the bundle. `project_id`, directory and slug are recomputed by import. |
| Repo / worktree setup | Full | Full | **Full** | Generic core. OpenCode-managed worktrees under `{data}/worktree/<pid6>/…` (or `worktree.directory`) declared in `Spec.Worktrees`. |
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
| F22 | Session turns in flight when the service dies stay claimed (`time_suspended`) and resume automatically when the service restarts (`resume_attempts`). | `src:core/src/session/store.ts:240-277` | source-verified |
| F23 | Usage: `session_v2.cost`, `tokens_*`; per-step tokens on assistant messages; `opencode stats --json`. Session info `model` is an optional `Model.Ref` (`{}` fails decoding); `projectID` is required but recomputed. No title-source flag. | `src:packages/schema/src/session.ts:32-63`; `session/title.ts:38-42`; `runner/llm.ts:202` | verified |
| F24 | Storage history: JSON files before 1.2.0; SQLite with 1.x tables from 1.2.0; 2.x adds `session_v2` and copies once (`kv` `migration.v1-v2`). `native` replaced the replay field in 2.1.0. | `src:…/v1-migration.bun.ts:541-765`; `CHANGELOG.md` v2.1.0 | verified |
| F25 | Migration cadence: 59 migrations; by month Jun 17, Aug 8, Sep 2, Oct 11; none since 2026-10-30 (`20261030054400_job`). | `$S/db2` copy; tag and dev | verified |
| F26 | Install methods: npm `@opencode/cli`; `curl -fsSL https://opencode.ai/v2/install \| bash`; `brew install anomalyco/tap/opencode-v2` (homebrew-core `opencode` is also 2.3.0). Docs: Windows package managers are not supported. mise older than 2026.10.9 installs 1.x. `OPENCODE_DISABLE_AUTOUPDATE=1` turns auto-update off. | docs `index.mdx:14-30`; `src:cli/src/services/updater.ts:608` | verified |
| F27 | Sharing discontinued (1.19.1). GitHub Action removed (2.2.0). No hosted agent. | changelog | verified |

**Version constraints**
- **Minimum: OpenCode 2.1.0** for both the binary and the **running service** (the plugin, import and
  API calls run in the service). Read side decodes both pre- and post-2.1 replay fields anyway.
- `Spec.Tested = ["2.2", "2.3"]`; the 2.1.0 floor is declared as a drift floor with its source file.
- **Schema gate (read side):** at open, check `PRAGMA table_info` for every table and column the module
  reads (`schema.go` manifest). Missing column → list nothing, show "OpenCode schema not recognised
  (latest migration X)". Extra migrations are allowed, logged, and flagged by the drift watcher.
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
| `spec.go` | • ID `opencode`, Name "OpenCode", Vendor "Anomaly" (verify the legal name), Stability `Experimental`, Tested.<br>• Binaries: `opencode` (VersionArgs `--version`).<br>• Roots: `data`, `config`, `state`, and on macOS `desktop` (`~/Library/Application Support/ai.opencode.desktop`) (§3.2). Main root: `data`.<br>• Secrets: `{state}/service*.json`, `{config}/service.json`, `{data}/auth.json`, `{config}/opencode.json`, `{config}/opencode.jsonc`, `{data}/opencode*.db*` (so no file path, bundle copy or peer package can copy the DB; SQL access is separate, §3.3).<br>• `LoginEnv`: `XDG_DATA_HOME`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, `OPENCODE_CONFIG_DIR`, `OPENCODE_DB` (GUI started from Finder).<br>• `Tools` (feeds the briefing's tool names).<br>• Instructions `["AGENTS.md"]`; GlobalInstructions `{config}/AGENTS.md`.<br>• Worktrees: the OpenCode-managed worktree dir.<br>• Kinds `["archived"]`.<br>• TerminalEnv `OPENCODE_SESSION_ID`.<br>• `Icon.Apps`: `/Applications/OpenCode.app`. `DesktopScheme: "opencode"` (detection only, D11).<br>• `Accounts`: `RootEnv` (multi-var, §3.7), `Unset` (provider key variables, `OPENCODE_DB`, `OPENCODE_PASSWORD`, `OPENCODE_SERVER_PASSWORD`, `OPENCODE_SERVER*`), `Login` `opencode auth login`, `LoginElsewhere` text. Exact `auth login` syntax taken from the `--help` snapshot.<br>• `SQLTables` allowlist (§3.3).<br>• Features: CapFork only after verification; no CapApp. |
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
| `live.go` | `Live` (busy columns, `time_suspended`, `/api/session/active` when the service is up, TUI argv via `Procs.Commands`) and `Stop` (interrupt). |
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
| `integrate.go` | `Integration`: SkillDir `~/.agents/skills`; RuleFile = the plugin's `config.json`. |
| `hooks.go` / `hooktrust.go` / `plugin/` | Embedded plugin (`plugin/index.ts`, `plugin/package.json`) and a `HookDir` implementation (§5). |
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
- DSN `file:<db>?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)`. Never `immutable=1`.
- One `BEGIN` read transaction per batch; close after each operation so no reader blocks checkpoints.
- `SQLITE_BUSY` → three retries with backoff. Never checkpoint, vacuum or write.
- **Main guard: modernc's authorizer** (`RegisterAuthorizer` via `conn.Raw`): `SQLITE_READ` on a table
  outside `Spec.SQLTables`, or on a denied table, is refused.

**Remote tiers** (`internal/core/host/sql_remote.go`), chosen per machine and cached:
1. `sqlite3 -readonly -json 'file:…?mode=ro' "<query>"` over SSH Exec (always on macOS, common on Linux).
2. `python3 -I` with a fixed embedded script using the `sqlite3` module (read-only URI, JSON out).
3. **No SQL:** the module falls back to `opencode service status`, then `opencode api GET /api/session`,
   `GET /api/session/{id}/message` and `session export`, with `--standalone` when the service is stopped.
   This returns projected JSON (parts merged, legacy shapes decoded) and no credential bytes. Slower;
   used on Windows and minimal Linux.

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
- **Exec uses the PATH `opencode`.** Subcommands reuse a service of another version (`mismatch:"ignore"`),
  so no version matching is needed for Exec. API features used are gated on the service version from
  `GET /api/info`.
- **No `--server`.** It needs the service password, which hopsesh does not read.
- **Resume and fork-resume commands** use a binary whose version equals the running service's:
  the PATH `opencode` if it matches; else `/Applications/OpenCode.app/Contents/Resources/opencode-cli`
  or a staged desktop CLI (`{desktop}/cli/<ver>-<arch>/opencode-cli`, version read from the directory
  name, no exec) of that version. If none matches, apply D2. If the service is stopped, any binary
  works. Check during PR 2 that the core accepts an absolute binary path in a resume argv.
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

**Watch paths:** `{data}/opencode.db-wal`, `{data}/opencode.db`, `{data}/hopsesh/opencode/`.

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
- **`Write(New)`:**
  1. Session id from the core's `SessionIDMinter`; `msg_` ids ascending; times start at "now − n ms".
  2. Info: `{id, title, agent:"build", projectID:"global" (recomputed), location:{directory:cwd}, time…}`;
     `model` omitted unless the target config model is known (never `{}`).
  3. Messages: user items → `user {text, files:[], agents:[], time}`; agent text → `assistant {agent,
     content:[{type:"text",text}], finish:"stop", cost:0, tokens:0…, time}`; NativeReplay shell items →
     assistant `{type:"tool", id:call_…, name:"shell", executed:true, state:{status:"completed",
     input:{command}, content:[{type:"text",text:output}], metadata:{exit}}}` (content non-empty);
     an `idle {outcome:"succeeded"}` after each assistant turn (necessity UNVERIFIED); the briefing and
     the `[hopsesh] Import ready` item as the last assistant text.
  4. Write JSONL to the anchor, convert, import (success = `Imported session: <id>` on stdout).
- **Result:** `WriteResult{SessionID, Path: anchor, From:0, To: settled count, Cursor}`. The projection
  maps each `ir.Item` to its `msg_` id via `ir.ProjectionFor`.
- **Lost into OpenCode:** non-shell tool calls (flattened, as for Claude and Codex), reasoning, Claude
  and Codex signatures and encrypted content, images unless textual, cost and token history,
  per-message model identity.
- **Lost out of OpenCode:** `native` replay, snapshots and revert, instruction state, child detail
  (summarised), media blobs (names only).
- **Fetch and adopt append** are gated on `Profile.PortableAppend` (today `fetch.go:495` sets
  `CanAppend` for any Reader+Writer; `write.go:213` and `adopt.go:439` then append). Fixed in PR 5 with
  a test.

---

## 5. Protection, rules and skills (plugin)

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
- `internal/core/presence/presence.go:40-41,154-199`: generic `KindApp{Agent}`; OpenCode rules
  (`OpenCode`, `OpenCode Helper*`; `opencode-cli serve --service` and `opencode serve --service` are the
  service, not sessions).
- `internal/core/profiles/store.go:331`; `internal/core/move/fetch.go:286`; `internal/config/config.go:60-80`
  (generic `Kinds` map).
- `internal/ui/cli/{movement_hooks.go,accounts.go:14,71,serve.go:284,root.go,pull.go:26,cd.go:27-44,skill.go}`.
- `internal/ui/tui/{accounts.go:238-342,tui.go:722}`.
- `internal/ui/gui/{accounts_view.go:62-389,622,inspect.go:363,move_targets.go:115-122,protection.go:89,terminal_binding.go:18-60}`.
- GUI JS: replace per-agent maps with `agentInfo(id)` and capability flags in `accounts.js`,
  `actions.js`, `guard.js`, `inspector.js`, `listview.js`, `quick.js`, `sessions.js`, `settings.js`,
  `machines.js`, `term.js`; `style.css` `--opencode` token, chips, `.tgrid` column count from a variable.
- `internal/core/scan/scan.go`: optional `sk-or-`, `gsk_`, `xai-` secret rules.

**B. SDK contracts (PR 1a)**
- `sdk/agent/agent.go`: `Root.EnvSub`, `Spec.SQLTables`, `MainRoot()`.
- `sdk/agent/capability.go`: EnvSub in `DefaultInstall`; Rules unchanged (Allow/Ask).
- `sdk/agent/session.go`: `BundleFile.Virtual`, `Materializer`, `VirtualSessions`, `SessionStore`,
  `SessionStamp`, `SessionIDMinter`, `InstallsViaVendor`.
- `sdk/agent/hooks.go`: `HookDir`.
- `sdk/agent/sql.go`, `sdk/agent/confine.go` (`confinedSQL`; the DB stays a Secret for FS access),
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
| 0 | N-agent refactor: registry and capability lookups in app, CLI, TUI, GUI Go/JS/CSS; generic app kind; config Kinds; **main-root accessor replacing `Root("home")`** | ~1.5k + 0.6k |
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

**Total:** about 13–14k lines production and 9.5k test across PRs 0–10. Flip to `Stable` is a separate
one-line PR after D10's condition is met.

**Risks**
1. **Schema changes.** Migrations come in bursts (F25). Mitigations: column gate, writes only through
   OpenCode, drift watcher on migration ids and OpenAPI hash.
2. **Service restart by a resume.** The TUI replaces a service of another version (F13). Mitigation:
   version-matched resume binary, D2 prompt, checklist steps 2–3.
3. **Auto-start of the service** by subcommands. Mitigation: `service status` probe and `--standalone`;
   the probe/start race is harmless (§3.4).
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
- `Plugin.define` and hooks on a 2.1/2.2 service: sandbox with a 2.2.7 standalone binary.
- Hot reload of plugin directories: sandbox, watch `GET /api/plugin`.
- `permission.hook` deny/allow/ask in practice: sandbox shell call.
- Meaning of `GET /api/session/active` (attached vs busy): sandbox with an idle TUI.
- Fork message-id behaviour: sandbox `api POST …/fork`.
- Whether child exports carry `parentID` (checked exports show none): export a session with a subagent.
- Skill dedup across two roots: sandbox with the same skill in both.
- MCP tool naming inside `execute`: source and a sandbox MCP server.
- Idle-message necessity on import: sandbox import without idles.
- Standalone server of an older binary on a newer DB: sandbox.
- Exact `auth login` syntax: `--help` snapshot.
- Absolute binary path accepted in a resume argv by the core: read during PR 2.
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
