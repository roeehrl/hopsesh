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
  `internal/core/peer` and `internal/app`. Current contracts use lineage/5 and peer v5.
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
The first bounded message is a labelled historical extract from original records:
source compaction, actual agent reply, chronological substantive user requests, and
separately framed tool activity. Never substitute a coalesced tool block for an agent
reply or present extracted text as fresh task authorization. Preserve multiline source
summaries, literal tool fences and the latest actual user request as a separate message,
including when attachments or oversized agent blocks follow it. Consecutive user turns
must not merge; generated boundaries carry no authored coverage. An empty extract must
not invent a revision. Too little capacity blocks writing instead of dropping the request.
Full portable text stays in the archive; no model-generated semantic summary is involved.
Regression evidence: `internal/e2e/context_capacity_test.go`, mandatory
`TestContextPressureRoutes` (ABABA/ABCA/ABCBCAB), SDK/module capacity tests and browser
continuation/recovery tests, plus `internal/core/convert/context_test.go` for source
record selection and request/quote boundaries. These static checks do not establish a
paid model turn.

Movement notices and returns: read `docs/movement-return.md`,
`internal/e2e/movement_test.go`, `internal/app/movement.go`, module `hooks.go`,
`sdk/agent/hooks*.go`, `sdk/agent/mark.go` and `internal/devtools/hsmatrix`.
The contract is lineage/5 and peer protocol 5; strict decoding refuses old versions.
Watch upstream SessionStart/UserPromptSubmit hook payloads, session/transcript identity,
profile-root precedence, output visibility, synchronous command behavior and trust gates.
Existing user hooks must survive installation/removal; unsupported vendor versions or
platforms must report pending/unsupported delivery, not success. Hook delivery must never
append native conversation nodes or establish authored work. Check prepared versus
continued against actual new agent work, forked versus departed parents, divergence,
last-checked observations, repeated returns, retry/undo and notices disabled while
lineage and return discovery remain available. Return candidates use the actual branch
and profile/binding, not labels; available/verify/same/behind/diverged/missing/live must
remain honest as upstream readers, live detection and fork metadata change. No paid calls
or writes to live vendor configuration are needed to validate these fixture scenarios.

Conversation conflict review: inspect `docs/return-conflict-review.md`,
`internal/core/move/comparison.go`, `agents/claude/branch_test.go` and
`internal/e2e/movement_comparison_test.go`. Claude's last-prompt is a checkpoint,
not necessarily the final saved response: follow only unambiguous descendants written
after it, preserve explicit later rewinds, and reject ambiguous native branches inside
the Claude module. Compaction replay ancestry uses preceding physical UUID occurrences;
identical authored messages are deduplicated, while changed replay content and forward
references remain rejected. Shutdown/checkpoint metadata alone must not invent new authored work.
Never grant legacy incomplete receipts coverage of omitted messages. Comparison evidence
uses fresh causal coverage and bounded ordinary-message/tool-name excerpts; private
reasoning and raw tool input/output stay hidden. Unavailable evidence is not divergence.
An old receipt that rejects newly exposed historical records can show bounded saved
messages explicitly labeled relationship-unverified, without exclusive counts or coverage.
GUI/TUI/CLI must identify both histories, explain that a difference does not establish
when work was written, and require explicit review then confirmation for a separate
conversation. Cancellation preserves both originals; notices are not ownership locks.

Portable return comparisons after a first account observation must verify the original
native anchors across the binding rotation (`internal/core/move/portable_return.go`,
`internal/e2e/movement_binding_test.go`). A changed binding alone is not independent
conversation work, but it must never grant native append permission. Added or rewritten
original work still blocks the return; successful portable copies retain the original.

Missing-original returns carry explicit new-replica intent through GUI/TUI/CLI and the
peer receiver. They must never select a surviving replica by accident. Hook commands
pin the installation's config/state scope; default-root registration must not orphan
older hooks. Failed or timed-out stdout writes remain retryable and cannot be reported
as supplied. Repeated identical notices use validated operation/status identity even
without a scan cache; lookup must not overwrite newer scan evidence.

Claude desktop opening: review `agents/claude/desktop.go`, `internal/app/resume.go`,
`internal/ui/gui/{termapps,sessions}.go`, `internal/ui/cli/tui.go` and SDK Command.TTY.
The documented `claude --desktop --resume <uuid>` (2.1.285+, macOS / Windows x64)
requires terminal stdin/stdout, a subscription login, and no conflicting options.
It rejects even an already-running Desktop session. The narrow focus exception uses
Desktop's own `claude://resume?session=<current-cli-id>` route only for a live session
whose entrypoint identifies Desktop and a verified public macOS bundle (2.19675.1+).
That route is an observed implementation contract, not a documented public focus API.
Watch issue anthropics/claude-code#80773: older native/previous-/clear IDs can duplicate
chats. Recheck live ownership on click. Never use a chat URL, guessed native ID, old
lineage ID, hidden prompt or retry that imports after a focus failure. Public bundle
metadata is the only Desktop file read. Unsupported live focus remains explicit;
no fallback merely activates the app. Check TTY launch timeout/exit status/cleanup,
no visible tab, session UUID propagation, and default-profile/root gates with module,
GUI service and browser tests.

Transfer review instructions: inspect `internal/core/move/instructions.go` and the
source-file packaging in `internal/app/peer.go`. The GUI previews declared global and
current-project instruction files and carries only the selected text snapshot in the
briefing, never overwriting destination rule files. Check path allowlists, missing and
oversized files, imported/parent rules (currently not followed), scoped account roots,
peer transport, and return-trip preservation. Agent account email/slug is display metadata,
not proof of native replay permission; cross-agent moves must not claim a different login.
Desktop availability and remembered launch choices must be checked against the actual
profile and installed application. Drift findings should distinguish a newer upstream
release from fixture coverage: help/schema probes do not certify model continuation or
justify expanding `Spec.Tested` or removing experimental status without versioned evidence.


Embedded terminal and conversation families (0.4.0): inspect
`internal/ui/gui/terminal_host.go`, `internal/core/pty/{session,stream}.go`,
`internal/ui/gui/assets/terminal`, `internal/app/families.go` and
`internal/ui/gui/terminal_binding.go`. All placements use one PTY and the same
restricted renderer; moving the view must never resume or fork an agent. Check
xterm serialized alternate/normal buffers, cursor and modes, replay/resize ordering,
backpressure, IME and stale-view input ownership when updating renderer dependencies.
Native checks run separate/bottom/right on macOS/Linux and bundled Windows ConPTY.

For agent releases, verify native parent metadata and exclusive ordinal boundaries,
complete inherited prefixes, registry timestamps and PID/wrapper evidence after
in-process fork/resume/clear. Unknown or conflicting associations stay explicit.
Do not infer Claude native fork ancestry from titles or prompts, or count replicas
as branches. Endpoint/profile/installation bindings scope identity. Native-only UI
identities are presentation metadata and never portable-history authority. Include
family grouping/rename/collapse and shell-association browser tests, TUI family
tests and the round-trip scenario's one-family/one-branch assertion. A real-agent
storage probe does not prove native window rehosting or account continuity.

Session discovery shares a disposable SQLite summary catalog across GUI, Quick,
TUI and CLI (`internal/core/catalog`, `internal/app/{catalog,watch,scan}.go`,
`sdk/agent/listing.go`). Check storage layouts, title/rename/mark sidecars, Claude
subagent directories and Codex session_index.jsonl dependencies: changes must
invalidate summaries. Parser changes require a new summary salt. Watch events are
hints with reconciliation and overflow handling. SessionWatchProvider paths must
exclude vendor diagnostics to avoid self-triggering listing loops; failed/partial scans never prove
deletion. Cached metadata is not live presence or transfer/account authority. Check
progressive loading, account/root namespace changes, cancelled/late publications,
selected-file previews, shared collector leases and large-list keyboard navigation.
Run discovery browser tests, TestSessionDiscovery and session-discovery.txtar on
macOS/Linux/Windows. Never store credentials or transcript bodies in the catalog.

Account refresh and discovery now rely on vendor public identity responses: watch
Claude auth status configDirectory/authMethod/exit code and Codex app-server initialize
handshake plus account/read nullable identity fields. Exercise JSON field reordering,
login-completion refresh, SSH keychain limitations, owner registry metadata provenance,
and independent same-email roots. Tailscale scripts must force TAILSCALE_BE_CLI=1;
cover DNS-less peers, BackendState and visible partial discovery errors. Refreshing
metadata must preserve a selected conversation DOM, focus and source-only scan scope.

Portable original-session returns are a separate contract from native replay:
Claude/Codex `Profile.PortableAppend` permits ordinary text deltas in a pinned
receiving root, with verified native anchors, historical binding segments,
current-login rechecks and no active writer. Check adapter changes against this
contract, binding-scoped selection, GUI return discovery and retry deduplication.
Claude context sizing also relies on assistant usage input/cache/output totals,
active-branch/compaction semantics, exact model IDs with documented default windows,
and local/project context overrides. A changed model window or usage schema affects
these capacity checks even when CLI flags remain unchanged; report such changes.

Large Codex rollout analysis must stay in the Codex module. Review incremental native record scanning, bounded record/retained-payload memory, cancellation, preserved record indexes/ordinals and cursor offsets. Repeated compaction replacement history is runtime context, not another copy of conversation work; capacity must still count the latest replacement correctly. Validate archive consultation instructions for both receiving agents, exact merged-archive starting offsets and bounded search/pagination, with no new authorization inferred from archived text. A reset branch may acquire its first origin only when the originless manifest has no replica on that branch and fork boundaries match; competing established origins must still fail atomically. Cover `TestAnalysisLargeRepeatedCompaction`, `TestMergeUnobservedBranchOrigin`, and mandatory `TestMovementArchiveConsultation`.
