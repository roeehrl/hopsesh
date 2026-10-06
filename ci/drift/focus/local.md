Review the current Claude and Codex adapters AND their shared consumers. The generated
manifest's modules include accounts (rootEnv, unset, login, initialFiles), desktopScheme,
sourceFiles and integrationFiles. Read those declarations and the current implementation;
names/tags are labels, never account identity. The feature contracts are in
`docs/accounts.md` and `docs/account-lineage-contract.md`.

Check these surfaces against upstream changes:

- Account discovery and isolation: Claude's CLAUDE_CONFIG_DIR, `auth login`, public
  `auth status --json` fields, and the documented Console-login isolation limitation;
  Codex's CODEX_HOME AND CODEX_SQLITE_HOME, `login`, keyring storage configured by
  cli_auth_credentials_store, and app-server `account/read` with refreshToken:false.
  Watch changes to login modes, keychain/keyring scoping, public identity fields,
  provider overrides and environment precedence. Email/plan alone do not establish a
  stable Codex user/workspace identity. Never inspect or copy credential files.
  Trace these through `sdk/agent/runtime_profile.go`, `internal/core/profiles`,
  `internal/app/accounts.go` and the module account readers.
- Local and remote profiles: default/configured/registered roots, missing roots, public
  discovery over SSH/peer, stable owning-endpoint/profile IDs and locally retained tags.
  Login changes rotate binding epochs; edits/removal/default changes invalidate plans.
  An upstream identity or storage change must not silently select another profile,
  adopt a session into the wrong account or infer identity from duplicate labels.
- Transfers, forks and round trips: inspect `internal/core/move`, `internal/core/lineage`,
  `internal/core/peer` and `internal/app`. Current contracts use lineage/4 and peer v4.
  Cross-account continuation uses a portable transcript with a fresh native ID, preserves
  the original, and excludes signed reasoning, opaque compaction and private agent state.
  Watch changed record shapes, native fork metadata, tool/message IDs, resume semantics
  and account restrictions that could corrupt projections or causal anchors. Check
  repeated A→B→A→B→A, multi-party A→B→C→B→C→A, Claude↔Codex, observed login changes,
  divergent originals/forks and interrupted/retried transfers. Account/agent/binding
  trips must remain distinct from machine travel. Old formats are refused, not migrated.
- Desktop launch: `agents/codex/desktop.go` uses codex://threads/<uuid> for exact local
  chat navigation, not `codex app PATH`. It supports only the default ~/.codex profile:
  links cannot select another CODEX_HOME/login or express a fork/first prompt. Watch
  documented URL grammar, UUID constraints and host support. macOS detection verifies
  com.openai.codex (including the supported bundle names); ordinary ChatGPT is not
  Codex. Windows/Linux require a registered URL protocol. Check `internal/core/host`
  and launch-error propagation: no successful-open claim or terminal fallback on failure.
  Review Claude desktop launch requirements against its module too.
- GUI/TUI/CLI: follow capability and profile changes into `internal/ui/gui`,
  `internal/ui/tui`, `internal/ui/cli`, resume plans and account/destination selectors.
  Scoped agent@profile/id references and --target-profile must use the same binding
  checks. Availability, missing identity and unsupported desktop profiles must remain
  explicit. Upstream state/record changes must not make remote/cloud filters, scan status,
  active-terminal indicators or rendered conversation previews misleading. Cosmetic UI
  design is outside upstream drift; changed vendor data that breaks these consumers is not.
- Quick access and desktop presence (0.4.0): inspect `internal/ui/desktop`,
  `internal/ui/gui/desktop.go`, `assets/quick.js` and `docs/desktop-presence.md`.
  Wails v3 tray attachment, activation/taskbar visibility, autostart and focus-loss
  behavior must preserve a reachable main window and terminal-aware quitting.
  Watch reliable agent attention signals, profile-aware live detection and desktop
  launch gating: stale remote status must not become live attention. The popup and
  main app share one inventory; background refresh must not start authentication.
  Include the lifecycle capability matrix, Quick access browser scenarios and native
  selfcheck. A headless pass does not establish tray-host/compositor behavior.
- Storage and live sessions: inspect current readers/writers for paginated history,
  compressed .jsonl.zst rollouts, multiple rollouts per thread, SQLite/index precedence,
  instruction/skill folders, permissions and settings. Paginated history now has a writer;
  do not repeat the old claim that all paginated extension is refused. Assess remaining
  limits from code and upstream evidence, not the presence of a canary alone.

Relevant regression evidence: module account/desktop tests, SDK runtime-profile tests,
core profiles/lineage/move tests, `internal/e2e/lineage_routes_test.go`,
`internal/e2e/push_test.go` (TestAccountProfilePeerDestinations),
`internal/e2e/scenario/testdata/accounts.txtar`, and
`internal/devtools/webtest/tests/accounts.spec.ts`. These scenarios run in the existing
CI matrices; a weekly real-agent probe is narrower and makes no paid model calls. Do not
claim it tested authenticated cross-account continuation or actual desktop presentation.
The optional presence daemon is a proposal for a later release, not shipped functionality.

Context capacity and bounded recovery (0.4.0): inspect `sdk/ir/capacity.go`,
`agents/{claude,codex}/capacity.go`, `internal/core/convert`, and every writer/importer.
Watch effective model/profile context limits, Codex config profiles and compaction
replacement_history, paginated ordinals/history_base, Claude active-branch compaction,
and imported response items versus duplicate UI events. Unknown active state must
not permit an append. Vendor import must preflight its complete snapshot and validate
its output before opening; checking only the Hopsesh briefing repeats a known overflow.
Final payload budgets include notes, plans, tools, redaction and framing. Archives are
portable data with bounded retrieval, never native signed/private state or instructions.
Large first records must remain discoverable. Check cloud edited briefs and return
paths, capacity rollovers versus forks, retained originals with new independent work,
archive transport/undo and truthful prepared-versus-running reports in GUI/TUI/CLI.
Regression evidence: `internal/e2e/context_capacity_test.go`, mandatory
`TestContextPressureRoutes` (ABABA/ABCA/ABCBCAB), SDK/module capacity tests and browser
continuation/recovery tests. These static checks do not establish a paid model turn.
