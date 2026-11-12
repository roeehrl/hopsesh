# Multiple accounts and account-to-account session continuation

Status: approved by the user and implemented on `codex/account-profiles`, 2026-10-06.
This document preserves the researched design. [Accounts](accounts.md) is the operational
reference for the implemented interfaces and current limitations; mockups are illustrative.

Scope: Claude Code and Codex, local and SSH-connected machines, their CLI-backed integrated and
external terminals, repeated returns, cross-agent continuation and forks. Existing cloud-session
ownership and native desktop account switching are separate capabilities, not implied support.

Related work: [lineage plan](lineage-plan.md), [integration contract](account-lineage-contract.md),
[optional presence/daemon proposal](daemon-presence-proposal.md). The account implementation extends the existing lineage engine rather than introducing a second graph.

## Recommendation

Add **Accounts** as explicit, isolated agent runtime profiles. Each profile has a name, machine,
agent, state directory and independently inspected login status. Users can keep any number of accounts
available simultaneously, including multiple personal accounts or several work accounts, without
signing the others out. Personal and Work are examples, not built-in categories. Hopsesh launches the vendor's own
login flow and agent process in the selected profile; it never becomes an OAuth token broker.

Use **Continue with another account…** to transport the portable conversation into that account's
profile. Preserve the source and its original protected state. Offer **Return to Personal…** when
the lineage identifies an eligible original, showing exactly which new work will be added and any
losses. If native append is unsafe, create a portable continuation or separate branch with an
explicit explanation. Never promise byte-for-byte or hidden-state fidelity across accounts.

The account implementation builds on the lineage foundation. Release publication remains a
separate step. The optional daemon is strictly **after 0.4.0**, as requested.
Account transfers must work without installing or enabling a daemon.

## What the vendors support, and what remains unverified

Research used official documentation and local public CLI help/schema. Installed versions observed:
Claude Code 2.1.289 and Codex CLI 0.160.1. These observations are not a minimum-version support
promise or evidence of a successful live two-account transfer.

| Topic | Claude Code | Codex |
| --- | --- | --- |
| Separate runtimes | Explicitly documented `CLAUDE_CONFIG_DIR` multi-account setup [C1] | `CODEX_HOME` relocates runtime state; separate profiles must also isolate SQLite/config overrides [O2] |
| Important exception | Multiple Console logins without API keys are not isolated by config directories [C1] | `--profile` selects configuration; it is not an account-switching feature [O3] |
| Authentication | Run the vendor's interactive login for the selected root; detect effective provider overrides [C1, C2] | Run vendor login; inspect account/read or supported public status in the same runtime [O1, O4] |
| Secure storage | macOS Keychain entries are directory-keyed; file fallback and other OS storage are documented [C1] | File/keyring/auto modes documented; independent keyring logout/refresh behavior across roots still needs a live test [O1] |
| Identity strength | Organization alone is insufficient to identify a user; adapter needs typed user/tenant evidence | Installed account/read gives ChatGPT email and plan, but no stable user/workspace ID; API-key mode lacks account identity |
| Native desktop | Profile-specific launching is unverified; normal “Open in Claude” must not silently use its current account [C5] | Do not infer desktop profile-routing support from CLI roots; gate independently |
| Cross-account hidden state | Model-dependent restrictions; do not blanket-classify every historical thinking block [C4] | Compaction may contain opaque encrypted state; cross-account replay compatibility is not established [O5] |

Claude specifically documents the Console-without-API-key limitation; do not advertise all account
types as isolated. API keys are a separate billing choice, never an automatic workaround. Sign-in
options must also respect organization policies and configured providers. [C1]

Codex's generated local `v2/GetAccountResponse.json` was inspected using
`codex app-server generate-json-schema`. It reports ChatGPT `email` (nullable) and `planType`,
API-key type without a subject, and a Bedrock alternative. The schema was generated offline into
a temporary directory; no credentials were read. A UI label can display an email, but **email plus
plan cannot prove workspace identity**. The feature needs capability-based identity confidence,
not another hash presented as certainty. Public docs and installed schema can differ; test both
capability and supported version instead of assuming one permanent payload. [O4]

## Defects and integration gaps in the existing architecture

These were findings at design time, addressed by the implementation; they are not live
account-transfer reproductions.

| Area | Finding | Required change |
| --- | --- | --- |
| SDK/runtime | One `Install` per agent, roots from machine environment; no account profile identity | Explicit runtime context through discovery, scan, read, write, auth and launch |
| Claude account probe | Calls literal `claude` without the selected install/root context; organization hash stands in for account | Use selected binary and scoped environment; distinguish subject, tenant and provider |
| Codex account probe | App-server root is scoped, but email hash is used as account key | Typed evidence and confidence; never compare it with native creator account IDs |
| Transfer sanitization | Different-account detection depends on both keys being known and unequal | Unknown means unverified, not same account; explicit portable policy |
| References/inventory | Machine + agent + native ID are insufficient for two profiles | Include profile and binding provenance in references and replicas |
| GUI terminals | Existing-tab lookup uses machine/session key | Include profile so “Open terminal” cannot attach to another account's process |
| Host confinement | Root facts derive from machine environment, not a selected runtime | Resolve and confine each profile independently before filesystem access |
| Tests | Existing account sanitization fixtures do not prove simultaneous native account isolation | Add stateful profile, credential-store and live vendor gates |

Relevant modules: `sdk/agent/{agent,session,capability,host}.go`, `agents/claude/account.go`,
`agents/codex/{index,codex}.go`, `internal/core/{host,move}`, `internal/app/pull.go`,
`internal/ui/gui/terminal.go`, `internal/e2e/codexmove_test.go`.

## Identity and profile model

Keep four concepts separate: machine installation, runtime profile, observed login binding,
and conversation replica. The detailed [lineage contract](account-lineage-contract.md) defines
their relationship to family, line, revision, receipts, forks and trip counts.

Account names are arbitrary optional labels, never an enum or identity key. There is no two-account
limit and no restriction to one account per category, email, agent or machine. Show the observed
email/account identity beside the name, plus agent and machine, and workspace/provider where
reliably available. Do not infer Personal/Work from the email domain. Renaming preserves profile ID,
lineage, receipts and return destinations. Duplicate names remain distinguishable through this
context; use a short profile-ID suffix when all displayed details collide. Email remains a display
hint, not an equality key. New-account setup may use the reported identity as its default name.

Selectors, filters, groups, CLI ambiguity handling and TUI rows all use the same profile collection
and immutable IDs. Account-to-account plans choose an exact source session and any compatible
registered destination profile; there is no special Personal-to-Work route. Test three-plus personal
profiles, multiple work profiles, mixed agents, duplicate names, renames during an active journey,
and identical email labels with distinct workspace evidence.

### Account tags

Add arbitrary, user-managed tags independent of names and identity. An account can have zero,
one or several tags: for example `a@b.com` and `a@c.com` tagged Personal, `a@d.com` tagged Work,
and one profile tagged both Work and Client. There are no reserved Personal/Work account types.
Normalize whitespace and case for tag matching while preserving a readable display spelling.
Tags never select credentials, imply provider tenancy, authorize transfers, or establish account
identity. Do not infer them from an email domain, subscription plan or organization.

Store tags with the Hopsesh profile registration, separate from vendor files and lineage identity.
Rename/tag edits preserve RuntimeProfileID, AuthBindingID and all replica references. Initially,
tags are local organization metadata, including for registrations of remote profiles; passive
peer discovery does not overwrite them or export private labels. Users can apply a tag to selected
local/remote registrations explicitly. Future settings synchronization requires its own conflict
and sharing policy; it is not implied by presence sharing.

GUI and TUI Accounts support editing tags, filtering by tag, an Untagged filter, and collapsible
Tag/Agent/Machine groups. A profile with multiple tags can appear in multiple groups; totals count
unique profile IDs. Session filters can use their profile's tags without collapsing independent
branches. CLI JSON exposes tags as an array; proposed account tag commands operate on explicit
profile IDs, with bulk selection allowed and ambiguous names rejected.

### Automatic account discovery: local and remote

Discover **supported logged-in runtime profiles**, not every account ever used on the device.
Start with each agent's documented default root, Hopsesh-managed roots, registered custom roots,
and an explicitly configured root override. Deduplicate canonical roots without changing their
vendor path identity. Do not recursively search the filesystem, enumerate unrelated Keychain
items, scrape browser sessions, or copy authentication files. A custom root unknown to Hopsesh is
not discoverable by magic; offer Register existing profile. Native desktop account lists remain
unsupported until an agent exposes a verified interface.

On app/TUI entry and configured-machine connection, perform a bounded discovery check. Reuse
normal inventory refresh while a client is open; provide per-machine Scan with checking, cancelled,
failed and last-success states. Disable only that machine's Scan while its check is in flight and
coalesce duplicate requests. A daemon is not required. Observing after every Hopsesh client exits
is the separate optional post-0.4.0 background feature. Existing refresh cadence is a configurable
inventory policy, not a new promise that all accounts are continuously observed.

Use public noninteractive, profile-scoped status probes without initiating login or selecting an
account. Codex account/read uses refreshToken=false; validate that each supported adapter does not
cause unexpected authentication or configuration writes. Unsupported evidence produces Unknown.
Known identity may be shown with a checked timestamp; do not overstate token validity or workspace
verification. API-key/helper modes without a reported subject show Provider/identity unavailable,
not a guessed email. Historical sessions may have a different account from today's login.

For SSH machines already configured by the user, inspect the same supported remote roots under
that remote OS user, using bounded prompt-free checks. Credentials stay on the remote machine.
No new daemon, account login, service installation, automatic host trust or inbound receive setting
is enabled. Show results per machine with provenance (default, registered, managed) and confidence.
Unreachable/auth-required/unsupported machines keep their prior results marked Last checked;
missing or failed results never delete a known account or claim it signed out.

Present newly discovered profiles for review/adoption without changing their root or signing them
out. Match an existing registration by scoped profile/root identity, never email or display name;
retain its custom name and tags. Re-check effective identity before the first transfer/launch.
Remote setup is available when nothing is signed in, but discovery never performs it automatically.

Add tests for repeated discovery without duplicates, same email across machines/workspaces,
multiple custom roots, one alias for the same root, rename/tag preservation, unknown identity,
remote disconnection and recovery, scan cancellation, per-machine coalescing, and a credential-store
canary proving discovery leaves secrets/configuration untouched. Preview discovery is simulated;
no real local or remote account scan has been performed for this design.

Proposed profile metadata: opaque ID; agent; immutable registered root; user label; managed or
adopted ownership; machine/OS-user endpoint; selected binary; credential backend capability;
configuration generation; login observations and timestamps. Store paths and human identity
labels locally. Portable manifests contain opaque IDs and provenance, never tokens or passwords.

A binding contains provider/auth mode, typed subject and tenant/workspace evidence when available,
confidence (`verified`, `limited`, `unknown`), observation source and timestamp. Reauthentication
to a verified unchanged association refreshes the observation. A detected change creates a new
binding and invalidates plans. When the vendor exposes insufficient evidence, say so: Hopsesh
cannot detect every external account switch and must avoid operations requiring that certainty.

Do not infer a transcript's historical owner from today's login. Imported or previously switched
directories can contain mixed histories. Record verified native provenance when available; mark
older unknown history unverified and use portable projection. Native work across a changed binding
creates a new provenance segment, without rewriting the prior one.

### Setup and authentication

1. Accounts shows profiles grouped by machine, then agent. The existing default can be registered
   in place; adoption does not move credentials or change shell configuration.
2. Add account chooses agent, name and machine. Create a private empty directory under Hopsesh's
   application state `profiles/<uuid>/<agent>`, or register an existing directory after checking
   ownership and duplicate roots. Resolve symlinks for collision detection, but preserve the
   vendor's original path spelling where credential-store keys depend on it.
3. Select an allowed login method and launch vendor-owned authentication with the exact selected
   binary and root. The user completes sign-in/MFA. Show waiting, cancelled, failed and ready
   states; errors include a safe explanation and Retry. A completed browser page alone is not
   a verified profile; inspect the resulting effective runtime.
4. Inspect auth/provider settings and effective identity. Display the observed identity and
   confidence. Unsupported Console isolation is blocked with the documented reason. Unverified
   workspace identity remains visible and limits native restoration.
5. Verify credential-store isolation before offering simultaneous managed profiles. Prefer the
   system credential store when independently scoped; never silently downgrade to plaintext.
   Where only file storage is supported, describe it and require explicit selection, with private
   directory/file permissions or Windows ACLs.
6. Launch only through a profile-aware context. Never change global `HOME`, agent environment,
   shell startup files, another profile's login, or the user's running desktop app account.

The child environment must exclude inherited authentication/provider overrides that could select
another identity, then apply explicitly configured profile values. Preserve required enterprise
policy, CA and proxy settings. Audit agent configuration helpers as well as environment variables:
clearing an API-key variable alone does not prove which credential source will be used. Unknown
effective routing blocks an account-specific launch or requires a clearly limited portable action.
Claude documents environment precedence and helper settings [C2]; Codex has separately configurable
SQLite location and several auth-related environment variables [O2].

Bind Codex app-server processes, sockets and shared-daemon connections to the profile. Never attach
to an arbitrary already-running default server. Test server reuse and shutdown without disturbing
another profile. Hopsesh uses vendor-managed OAuth; the experimental externally supplied-token
app-server flow is outside this proposal. [O4]

On remote machines, credentials remain there. Use vendor-supported interactive/device flows and,
where supported, a temporary SSH callback tunnel. Do not copy local auth files to make remote
setup seem seamless. An offline machine can show saved profiles with “Last checked,” not “Ready.”
File-only access cannot establish the target runtime's current identity.

“Remove from Hopsesh,” “Sign out,” and “Delete managed profile data” are separate actions. Default
removal unregisters only. Destructive deletion names the directory and affected histories; adopted
directories are never implicitly deleted. Sign-out/retarget is blocked while managed processes use
the profile, and reports externally detected activity rather than killing it.

Profiles isolate state and routing, **not security between tools running as the same OS user**.
For mutually untrusted accounts/projects, separate OS users or VMs are required. Do not describe
private directory permissions as a sandbox against another process owned by that same user.

### Configuration, repository and tool access

Do not copy the entire source profile into the destination. Transfer neither authentication nor
permission grants, trusted-project settings, MCP credentials, hooks or arbitrary executable helpers.
Offer a separately reviewed configuration preset later if needed. The target uses its own policies,
model access and tool configuration. Plans show unavailable tools/attachments and referenced local
files that will not travel. Existing code/worktree transport remains explicit; account identity is
not a substitute for Git credentials or permission to access the destination repository.

## Continuation, return and fork behavior

| Scenario | Proposed behavior |
| --- | --- |
| Claude Personal → Claude Work on one Mac | New native ID in Work's root; same logical line for Move; keep Personal's original intact |
| Claude A on Studio → Claude B on laptop → A | Same profile-aware transfer protocol over the existing peer connection; return targets Studio/A's exact replica |
| Claude A → Codex B → Claude A | Visible-history projection with losses; provenance identifies only B's newly authored work on return |
| A → B → A → B → A | Four committed transfers, two round trips to that line's origin; retries and scans add none |
| A → B → C → A | Each receiving replica gets its missing revisions; one return to origin, with B and C contributions preserved |
| A → B → C → B → C → A → B | Six transfers, one origin round trip; returns to other stops are shown separately |
| Original and fork both move | Separate Line IDs, selection, receipts, statuses and counters; family grouping does not collapse them |
| Source also gains work while away | Divergence: Keep both defaults to a new line; no automatic conversation merge |
| Login changes during a plan | Reject on observed mismatch; re-plan under the new binding; never silently choose another profile |
| Historical account unknown | Fresh portable projection with an explicit limitation; no assertion of native protected-state compatibility |

### Protected state and fidelity

Cross-account projection preserves available visible user/assistant content and representable
tool results, with a per-transfer loss manifest. It omits opaque protected state unless the module
has an independently verified compatible path. Files, images and tool context must be classified
as included, referenced-only, unavailable or transformed; a count of messages alone is insufficient.

Claude's current documentation makes thinking restrictions model-dependent. Sonnet 5.5 has an
account/linked-account check; newer models also bind thinking to its original message prefix.
Removing or restoring intermediate thinking can invalidate subsequent blocks. Therefore preserve
the original native transcript and never splice another account's thinking into it. [C4]

Codex compaction can replace previous material with an opaque encrypted item. Do not assume the
full visible history is still recoverable or that the item is portable to a different account.
If only a summary remains, show “Earlier detail unavailable” and require choosing a summary-based
continuation or cancel. A lossless-history option must be unavailable in that state. [O5]

### Returning safely

Use the shared lineage projection map to exclude already delivered revisions and generated
briefings. Return only genuinely new authored work. A receipt records coverage plus representation
and losses; sanitizing a copy does not erase its logical ancestry.

When the original is unchanged, its anchors are valid, its account binding is sufficiently verified,
and the module supports a safe native continuation, preserve its existing bytes/hidden state and
append the compatible new visible work. For Claude, validate the resulting native request sequence
with the supported model/harness; preserving bytes alone does not prove request-prefix validity.

If protected-state safety or identity is uncertain, use a new portable destination replica and
explain that the original remains available. If the original has independently changed, create a
separate line by default. Do not silently replace it, reinsert deleted thinking, or call a new
portable copy “restored original.” Codex's present workspace-identity gap makes this gate especially
important; seamless original restoration is conditional, not promised for every account type.

An active native writer blocks append/replacement. Hopsesh-controlled locks and optional presence
are helpful but cannot exclude an independent native process; check native heads immediately before
write and keep the existing recovery journal. The daemon must not weaken these checks.

### Cloud and native desktop limits

Changing local account profiles does not transfer an existing cloud conversation's ownership.
Fetch under the source account, then project into the chosen local profile. A later cloud handoff
creates work under an explicitly configured destination account; it does not mutate the old cloud
session's owner. Keep unconfigured cloud destinations out of the action menu.

Integrated terminal, Terminal and iTerm launches can carry a scoped environment. Native desktop
apps require separately verified profile-specific routing. Until supported, explain why “Open in
Claude/Codex app” is unavailable for that profile and offer the scoped terminal. Never launch the
default app and suggest that Hopsesh selected its account.

## GUI, TUI and CLI

**Sessions:** account label beside agent and machine; filters for machine, agent, account, activity
and location; grouping by account is collapsible. Independent branches stay distinct. The inspector
shows the exact replica, last identity check, recent rendered conversation, and profile-aware launch
actions. A terminal badge counts only tabs associated with that replica/profile and labels evidence
for external terminals separately.

**Actions:** “Continue in Work · Claude Code · This Mac,” “Return to Personal · Studio,” and “Open
shell in repository.” Avoid “Resume here.” The transfer review shows From, To, incoming work,
source preservation, missing capabilities and losses before its single primary action. Changing
destination invalidates the old preview. Setup is available beside an unavailable account, not
hidden behind a failed transfer.

**Accounts:** shared upper-left Back to sessions in normal, loading, error and completion states.
Show per-profile auth state and timestamps; distinguish signed out, checking, identity limited,
policy blocked and unreachable. The setup flow reports vendor sign-in progress and actual result.
Profiles are selected per operation; no global selector that implies switching running terminals.

**Journey:** machine + agent + account at every stop, line/fork identity, transfer receipt state,
losses and derived counts. Detail distinguishes returns to the account/agent origin from machine
travel. Show “Last observed working on MacBook · Work · 12 seconds ago” only with evidence, and
“Status unavailable” after the observation expires. Presence is a future optional enhancement.

Repeated navigation keeps the same position and order; waiting/error results are announced without stealing focus, following [W3C consistent navigation](https://www.w3.org/WAI/WCAG22/Understanding/consistent-navigation.html) and [status message guidance](https://www.w3.org/WAI/WCAG22/Understanding/status-messages.html).

**TUI:** the same plan DTO, identity confidence, loss flags and explicit actions. Accounts opens a
keyboard-navigable list; Enter opens details rather than immediately launching an agent. Review
shows target profile and route; interactive confirmation defaults to cancel on errors. Noninteractive
mode requires exact IDs when names are ambiguous and returns structured actionable errors.

Implemented CLI grammar:

```sh
hopsesh accounts list
hopsesh accounts add claude "Research" --tag Personal
hopsesh accounts login <profile-id> --run
hopsesh pull claude@<source-profile-id>/<session-id> --target-profile <target-profile-id> --in claude
hopsesh push claude@<source-profile-id>/<session-id> laptop --target-profile <remote-profile-id> --in codex
```

A remote source uses its configured machine alias before the scoped key, followed by a colon.
Keep the existing pull/push grammar and preview/apply behavior. Proposed flags select Hopsesh runtime
profiles, not Codex's configuration `--profile`. Machine + agent + profile scope must resolve labels
uniquely, or require opaque IDs. Extend JSON output with profiles, binding confidence and losses;
do not put credentials in command arguments or JSON.

## Implementation sequence and module ownership

| Phase | Deliverable | Completion gate |
| --- | --- | --- |
| 0. Contract | Agree profile/binding/replica semantics with lineage owner; precise support matrix | No competing lineage model; explicit format/version decision |
| 1. Capability probes | Per-agent public auth adapters, isolation experiments, compatibility fixtures | Two live accounts cannot bleed login, logout, refresh, SQLite or server state |
| 2. SDK/runtime | Profile registry, scoped host/install context, environment policy, secret exclusions | All agent operations select the same confined runtime; root collision tests |
| 3. Core and peers | Profile-aware references, inventory, journals, locks, receipts and launch tickets | Same native IDs across profiles remain independent; stale plans fail |
| 4. Transfer/return | Portable projection and losses; safe original append where verified; forks elsewhere | Stateful route and crash/retry matrix passes with exact authored coverage |
| 5. Interfaces | Accounts setup, session facets, explicit actions, Journey, CLI/TUI parity | GUI/TUI flows cover success, waiting, failure, unknown and conflict states |
| 6. Release | Remove old assumptions, docs and unsupported menu paths; real-agent release smoke | Supported-build matrix and interactive end-to-end evidence, not just passing fixtures |

SDK: add runtime-profile/auth-context types and capability methods, retaining the existing IR writer
profile terminology for conversion only. Agent adapters own vendor-specific probing, auth launch,
sanitization and native compatibility. Core owns profile lookup and transfer policy. App services
expose one plan/state DTO to all interfaces. Peer requests name registered profiles and capability
versions; they cannot inject arbitrary remote root paths or environment variables.

Delete the single-install-per-agent assumption where it addresses sessions, email/org equality
shortcuts, “unknown equals same” logic, default-root auth probes, profile-blind terminal lookup,
and misleading native-app actions. Remove superseded fixtures/docs rather than adding permanent
fallback paths. No backward-compatibility layer is required now; incompatible peers/sidecars fail
before mutation with a clear message. Do not delete native user data to achieve schema cleanliness.

## Required validation

Extend the existing `internal/devtools/hsmatrix` scenario infrastructure, OS pairs and stateful
lineage routes. Pairwise combinations do not replace ordered journey tests. Use two profiles as a
separate dimension from two machines, and distinguish auth mode from agent type.

- **Isolation:** default/adopted/new profiles; same and different OS users; macOS Keychain,
  Linux file/keyring and Windows ACL/backend support; root alias/symlink collisions; inherited
  API keys/providers; helpers; SQLite overrides; app-server/socket reuse; sign-out/refresh of one
  profile while the other runs; managed policy refusal; cancelled and expired auth.
- **Identity:** two users in one organization; same email across workspaces; missing email;
  unknown historical ownership; mixed-account transcript; change before apply and before launch;
  external changes the protocol cannot distinguish must retain limited confidence.
- **Journeys:** both directions for Claude/Claude, Codex/Codex and Claude/Codex; same-machine and
  OS-pair routes; A/B/A/B/A, A/B/C/A and A/B/C/B/C/A/B; exact-once authored sentinels and independent
  destination receipts; no-op transfers, pending acknowledgments, retries and every crash boundary.
- **Fidelity:** pre/post compaction, model-dependent thinking, missing attachments, tool results,
  truncation, native rewind/edit, invalid projection anchors, prefix preservation and explicitly
  acknowledged summary-only continuation. Never mark dropped protected state as exact delivery.
- **Branches/activity:** forks before and after moves, concurrent originals, sibling-safe Replace,
  correct native-fork adoption, external active writers, stale presence and native-head races.
- **Interfaces:** consistent Back location, resizable inspector, rendered recent conversation,
  keyboard/focus behavior, disabled actions with reasons, correct terminal counts and identity;
  TUI width handling; deterministic CLI JSON/errors; no unconfigured cloud destinations.

CI uses synthetic profiles and fixtures without real credentials. Live vendor gates require two
user-authorized test accounts per supported auth type and a small paid continuation when required;
run on designated hosts, keep secrets out of logs, and record versions, capability and fidelity.
No paid tests or real account login were performed for this proposal. Native desktop support gets
its own gate and remains unavailable until demonstrated.

## Historical proposal validation

During the design phase, only design and preview files were authored. The other session's runtime
changes were left intact. The interactive mockup was checked in Chromium at three widths, with
light/dark visual inspection. Setup, unsupported Console sign-in, grouping/filtering, transfer,
changed-login blocking, incomplete-history consent, return/fork and TUI navigation checks passed
without script errors. Markdown links, fences and illustrative JSON were checked. These checks
validate the proposal's presentation, not real authentication isolation or native session replay.

## Approved decisions

| Decision | Recommendation |
| --- | --- |
| Account model | Multiple simultaneous isolated profiles, per-operation selection |
| Transfer fidelity | Visible portable history by default across accounts; explicit loss review |
| Return behavior | Preserve original; append only when verified safe; portable copy/fork otherwise |
| Divergence | Keep both as distinct lines by default |
| Identity limitations | Show them and limit unsafe operations; never infer workspace from email |
| Native desktop | Gate account-specific opening until independently verified |
| Release scope | Finish current lineage/UI release first; accounts in a subsequent release |
| Daemon | Optional, after 0.4.0; observation first, no dependency for transfers |

## Sources

Sources accessed 2026-10-06. Assertions above distinguish vendor documentation, local code findings,
unverified compatibility and proposed Hopsesh behavior.

- [C1 — Claude authentication and multiple accounts](https://code.claude.com/docs/en/authentication)
- [C2 — Claude environment variables](https://code.claude.com/docs/en/env-vars) and [settings](https://code.claude.com/docs/en/settings)
- [C3 — Claude CLI reference](https://code.claude.com/docs/en/cli-reference)
- [C4 — Claude preserved thinking restrictions](https://platform.claude.com/docs/en/build-with-claude/preserved-thinking)
- [C5 — Claude desktop](https://code.claude.com/docs/en/desktop)
- [O1 — Codex authentication](https://learn.chatgpt.com/docs/auth)
- [O2 — Codex environment variables](https://learn.chatgpt.com/docs/config-file/environment-variables)
- [O3 — Codex configuration profiles](https://learn.chatgpt.com/docs/config-file/config-advanced)
- [O4 — Codex app-server protocol](https://learn.chatgpt.com/docs/app-server)
- [O5 — OpenAI compaction](https://developers.openai.com/api/docs/guides/compaction)
