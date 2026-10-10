# Protecting the original, and return destinations

A transfer prepares another copy of a conversation. It does not establish that an agent
has continued working. Hopsesh records movement beside the native transcript and derives
the moved-out state, the moved copy's origin and return destinations when it scans
sessions. Hopsesh never changes a session's title to show movement and never appends
native user, assistant or tool messages to the original.

## Block, advise or leave alone

`original` in `config.toml` (Settings › General › "When a session moves, the original")
decides what the copy left behind does until the session moves back:

- `block` (the default): the agent refuses new prompts in the original and shows why, so
  the moved copy stays the one source of truth and moving back is a clean return.
- `advise`: the agent is told (and shows) that the session moved; work can continue.
- `off`: the original is left alone.

`--notify=false` (or unchecking "Block the original until you move back" in the plan)
leaves one transfer's original alone. Moving back clears the block or advice by itself.
To keep working in one original anyway, remove its block: the app's "Remove block…"
(with a warning), the TUI's `U` (press twice), or `hopsesh unblock <session>`; "Block
again" / `hopsesh unblock --restore` puts it back. A removed block applies to that
movement only; a later move blocks again. Continuing in an unblocked original makes the
copies diverge, so moving back then needs the
[conversation conflict review](return-conflict-review.md) or a separate fork.

A difference does not prove that work happened after a move; old incomplete transfers
may have omitted already saved messages. A separate fork never blocks its parent.

Earlier versions could label the source title (“↪ prepared in Codex · …”). Hopsesh still
reads such a label so it does not show as part of the title, and a session without
lineage that carries one is shown with the movement it describes; a return clears it.

## Protection hooks

Blocking and advice work through a hook the agent runs on `SessionStart` and
`UserPromptSubmit`. Block mode answers `UserPromptSubmit` with
`{"decision":"block","reason":…}` on every prompt (Claude Code and Codex both refuse the
prompt and show the reason); `SessionStart` cannot block, so it explains the block. Advise
mode adds context once per movement and status. Hooks never start a model turn, stop a
session, or edit its transcript; they fail open on errors and time out after 2 seconds.

Install them in **Settings › General › Protection hooks**, or with
`hopsesh notices install` (`--agent claude --profile <profile-id>` for one profile);
`hopsesh notices status` and `remove` report and remove only hopsesh's hooks. The hooks
and the skill run the hopsesh command, so installing either from the app requires it
(the app offers to install it first). Run setup on each machine whose originals should be
protected; registering a remote machine does not install hooks there.

**Codex reviews every new or changed hook** and skips it silently until the user trusts
it (`/hooks` in the CLI, or the app's Review hooks prompt). Hopsesh asks Codex itself
(`codex app-server`, `hooks/list`) whether it trusts the hopsesh hooks, shows "waiting for
your approval" in Settings, `hopsesh notices status` and a Sessions warning, and never
approves hooks on the user's behalf. The hook command stays the same across hopsesh
updates (the app's bundled CLI path), so an update does not ask for review again. The app
shows an original as blocked only when its agent's hook is installed and trusted.

The current adapters accept stable Claude Code 2.x from 2.1.284 and stable Codex 0.x from
0.160.0; unknown or prerelease versions are refused pending validation. Windows commands
use explicit PowerShell handling; macOS and Linux use the configured command shell.

## Indicators

Every view uses the same words, with a glyph that also works without color:
`◆ Moved out` (blocked), `◇ Moved out` (advised or unprotected), `! Diverged` /
`Unblocked`, `● Moved copy` (the active copy, with where it came from), `↩ Returned`,
`⑂ Fork`. The app shows the chip in the row and a line under the title in the inspector;
`hopsesh ls`/`show` and the TUI print the same words.

## Persisted contract

Receipts use `lineage/5`; peers require protocol 5. The hop's `notify` field records the
choice durably so restart and receipt recovery preserve it. Older receipt formats and
peer protocols are refused, with no automatic migration or compatibility fallback.

`ActiveHops()` excludes compensated operations and implementation-only native backups.
`ReturnReplicas(current)` walks the current arrival's causal ancestry and returns unique
previously visited replicas on the same branch, most recent first. It excludes the
current replica, sibling branches and a fork's parent. A native replica is identified
by endpoint, agent, profile/binding, session and branch, never by display name alone.
`Departure(current)` reports an unambiguous active departure with protection recorded (`notify`); `Departed(current)` ignores `notify` and drives the moved-out state.
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
| `diverged` | Each history contains work absent from the other; compare the messages before reviewing a separate branch. |
| `missing` | A successful scan of the expected binding did not find the original. |
| `live` | The destination needs incoming work but is open; review before returning. |

`Entry.Movement` is optional. `prepared` means delivery succeeded but subsequent authored
work has not been observed; `continued` requires newly observed agent work. Imported
history, title changes and hook delivery do not establish continuation. `diverged` means
each history contains work absent from the other, without establishing when that work was written. `forked` describes a separate fork. A last-checked
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
