# Movement notices and return destinations

A transfer prepares another copy of a conversation. It does not establish that an agent
has continued working. Hopsesh records movement beside the native transcript and derives
notices and return destinations when it scans sessions. Hopsesh never appends native
user, assistant or tool messages to deliver these notices. The vendor controls how
hook context is displayed and retained during a normal session turn.

`movement_notices` defaults to true in `config.toml`. Set it to false to hide movement
notices; `--notify=false` disables the notice for a particular transfer. This leaves
lineage, return discovery, comparison, conflict detection and undo intact. Native title
marks are controlled separately by `mark_moved`/`--mark`. A cross-agent title mark says
“prepared in”; it cannot prove subsequent work. Callers using `move.Options` directly
must set `Notify: true` when they want a notice; config defaults apply in the app layer.

When a return cannot safely append, the [conversation conflict review](return-conflict-review.md)
shows verified differences and the effects of creating a separate conversation. A
difference does not prove that work happened after a move; old incomplete transfers
may have omitted already saved messages. Notice hooks supply context, not an exclusive
ownership lock that prevents the original from being continued.

## Native agent setup

In **Hopsesh → Settings → General**, **Record movement notices** controls the default-on
recording preference. Under **Movement notice delivery**, **Set up local notice hooks**
enables delivery inside supported native agents; recording alone does not install hooks.
That section also shows each hook's installation status. Alternatively, use
`hopsesh notices install`. Installation covers
supported local Claude Code and Codex roots, including registered account profiles. To
select one profile, use `hopsesh notices install --agent claude --profile <profile-id>`.
`hopsesh notices status` reports installation and disabled/unsupported reasons;
`hopsesh notices remove` removes only Hopsesh's hooks, preserving unrelated hooks.
Run setup on each machine where native sessions should receive notices. Remote machine
registration alone does not install hooks there.

Hooks deliver a short notice when a session starts/resumes or the user submits a prompt.
They never start a model turn, stop a session, or edit its transcript.
Delivery is deduplicated per movement and status; another round trip can produce a new
notice even when its wording is identical. Vendor trust, project policy and disabled-hook
settings still apply. “Supplied to hook” records Hopsesh's output, not proof that the
model read it. Already-running sessions receive a notice at the next supported event.

The current adapters accept stable Claude Code 2.x from 2.1.284 and stable Codex 0.x from
0.160.0; unknown or prerelease versions are refused pending validation. Windows commands
use explicit PowerShell handling; macOS and Linux use the configured command shell.
Disabling movement notices stops delivery without deleting lineage or the installed hook
configuration. Uninstall the hooks separately if desired.

## Persisted contract

Receipts use `lineage/5`; peers require protocol 5. The hop's `notify` field records the
choice durably so restart and receipt recovery preserve it. Older receipt formats and
peer protocols are refused, with no automatic migration or compatibility fallback.

`ActiveHops()` excludes compensated operations and implementation-only native backups.
`ReturnReplicas(current)` walks the current arrival's causal ancestry and returns unique
previously visited replicas on the same branch, most recent first. It excludes the
current replica, sibling branches and a fork's parent. A native replica is identified
by endpoint, agent, profile/binding, session and branch, never by display name alone.
`Departure(current)` reports an unambiguous active departure with notices enabled.
Returning clears that departure; an undone transfer produces no active notice. Fork
notices describe the separate branch and leave the parent available.

Claude Code and Codex explicitly support appending ordinary portable text to an
original session in the selected agent/profile root. A return verifies that exact
session's native anchors and lineage, computes missing authored revisions, and
appends only the new work. Its native ID, earlier records and branch remain intact.
Cross-agent conversion does not require matching Claude/Codex profile IDs or emails.
Source-private reasoning, signatures and tool state are never replayed across profiles.

A profile's first public account observation can rotate Hopsesh's opaque binding.
That observation is not proof the user changed accounts. Verified native anchors
carry earlier authorship into a new binding segment; historical provenance is not
rewritten. Portable text append uses the receiving profile's current binding and
rechecks its public login, pinned root, native head and activity before writing.
Independent work, rewritten history and an open destination still block the return.
Modules without an explicit portable-append contract keep the fresh-session fallback.

An open original appears as an actionable return card. Choosing **Move back** opens
the return review directly, with no separate explanation dialog. Closing a desktop
conversation tab can leave
its agent process running; Cmd+W is not evidence that Claude Code released its session.
The review plans against the exact original. When the destination module
implements `agent.Stopper` and `agent.LiveDetector` and its host supports graceful
termination, the local review offers **End original session and check again** in
its fixed bottom action bar. The blocker appears above the conversation details.
The move layer validates the pinned destination, account, root and native cursor,
then delegates all process handling to that module. Claude's module reads its own
registry and asks every process for that session to exit normally; no other
conversation is stopped, and no process is forcibly killed.

The action consumes the review's one-use token and invalidates its plan even if
stopping fails. A fresh review includes any records saved during shutdown. It never
applies the transfer automatically. Saved history remains intact. Codex does not
implement `Stopper`, and the Windows host cannot gracefully signal sessions, so those
destinations show manual instructions and **Check again**. In a terminal, use the
agent's exit command, such as `/exit`. If the desktop app has no session exit control,
quitting that app ends its other conversations too; the UI explains that consequence.

Claude capacity uses the resumed branch's last complete request usage (including
cached input), plus conservative byte estimates for later records. Documented models
with a default 1M context window use that window; unknown models retain the small
fallback. User/project settings, configured compaction limits and the receiving
machine's context overrides are fingerprinted and rechecked. A genuinely full or
unmeasurable destination still receives a separately reviewed bounded continuation.
This never silently discards or overwrites the original history.

When an explicit return cannot update the original, the GUI offers **Review new
session on the same branch**; the TUI offers **N** in the blocked plan. Both review
`NewReplica` with the chosen destination profile retained, the exact destination
session cleared and fork/conflict overrides disabled. The CLI explains removing
`--target-session` and adding `--new-session`. This replans a fresh portable replica;
it does not apply the blocked plan or force a fork. Independent or rewritten
destination work still blocks until the user explicitly chooses separate branches.

## Scan results

`Entry.Returns` supplies destination identities and an explanatory reason with each status:

| Status | Meaning |
|---|---|
| `available` | The destination lacks conversation work present in this copy; review a return plan. |
| `verify` | Current destination state or identity has not been verified. |
| `same` | Both copies already cover the same conversation. |
| `behind` | The destination contains newer work than this copy. |
| `diverged` | Both copies have independent work; preserve branches and review the conflict. |
| `missing` | A successful scan of the expected binding did not find the original. |
| `live` | The destination needs incoming work but is open; review before returning. |

`Entry.Movement` is optional. `prepared` means delivery succeeded but subsequent authored
work has not been observed; `continued` requires newly observed agent work. Imported
history, title changes and hook delivery do not establish continuation. `diverged` means
both copies contain independent work. `forked` describes a separate fork. A last-checked
time describes the observation; cached or unreachable destinations are not new live
observations. Return actions still build and review the ordinary move plan.

When an earlier destination is missing, “Review new session there” carries explicit
new-replica intent (`--new-session` in the CLI). It cannot silently append to another
surviving copy in that profile. The new native session stays on the logical branch;
independent destination work still requires an explicit separate fork.

## Verification

`internal/e2e/movement_test.go` uses real Claude and Codex readers/writers in temporary
homes for all four agent pairings, transfer/return with notices on/off, fork, persisted
retry and undo. It checks native identity, receipt queries, no-work returns and unchanged
conversation nodes. `movement_binding_test.go` reproduces first-observation binding rotation across all four
agent pairings, with both unchanged observed bindings and first-observation rotation,
including unchanged, independently extended and rewritten originals, and
checks original-ID portable delta appends, unchanged byte prefixes and idempotent retries. `lineage_recovery_test.go` checks notice and return metadata after a
simulated interruption. `lineage_routes_test.go` checks the queries at every stop in
ABABA, ABCA and ABCBCAB routes, including profile and account routes.

The scenario matrix adds `quiet-roundtrip` and `fork` to ordinary round trips. Its helper
reads receipts on each side of SSH; the runner checks active hops, notify preferences,
return candidates and departures. The real-module tests prove original-ID returns across runtime profiles and binding
observations, with explicit fresh-session and capacity-rollover scenarios tested separately. PR CI runs these over Linux,
macOS and Windows loopback SSH and supported OS pairs. Nightly runs cover triples and
longer stateful histories. The dedicated e2e steps select `TestMovement` alongside
lineage, account and context scenarios.

`TestMovementHookCommandInvocation` executes the modules' generated lifecycle commands
through their host shell and checks the exact argument vector and unchanged stdin. It
covers both events, executable paths and profile values with spaces/quotes/shell syntax,
and rejects control characters. Windows CI invokes Claude's explicit PowerShell command and
Codex's Windows command override through both PowerShell and the cmd.exe fallback; unsupported-Windows assertions are not a substitute for that
execution. `scenario/testdata/notice-hook.txtar` drives the real CLI parser on every OS,
including malformed JSON/session/path values and untrusted quoted paths.

These tests use fixture homes and stand-in vendor executables. They do not establish a
paid model turn, successful delivery through a live vendor hook, or real desktop display.
The hook adapters' version/platform gates and preservation of unrelated user hooks are
covered by their own module/SDK tests. The upstream drift review watches those contracts
alongside the scan and return consumers.
