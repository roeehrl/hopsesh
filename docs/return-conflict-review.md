# Reviewing a return when conversation histories differ

The return review must show what makes an append unsafe before asking the user to choose an outcome. A causal conversation conflict is not a Git file conflict. Hopsesh does not merge two independently continued conversations into a single native history.

## Research and design decisions

- [NN/g: Error-message guidelines](https://www.nngroup.com/articles/error-message-guidelines/) recommends identifying the problem, explaining its cause in the user's language, and providing actionable recovery in the current workflow.
- [NN/g: Recognition and recall](https://www.nngroup.com/articles/recognition-and-recall/) supports displaying the evidence rather than making users remember what happened in each copy.
- [NN/g: Explicit differences](https://www.nngroup.com/articles/explicit-differences/) supports comparisons organized around differences and outcomes.
- [VS Code: Resolving merge conflicts](https://code.visualstudio.com/docs/sourcecontrol/merge-conflicts) demonstrates naming the compared sides and inspecting changes before choosing. Its warning that accepting both does not guarantee a valid merge applies especially strongly to agent conversations: separate histories must remain separate.
- [GOV.UK: Buttons](https://design-system.service.gov.uk/components/button/) recommends labels describing the performed action and one main action. “Review separate Claude Code session” only requests a fresh plan. “Create separate Claude Code session” is the later confirmation that writes it. “Cancel return” does neither; it never means deleting a copy.
- [WAI: Modal dialogs](https://www.w3.org/WAI/ARIA/apg/patterns/dialog-modal/) supports a single dialog with a clear heading and keyboard-operable controls. Comparisons stay in the existing review; no preliminary explanation dialog.
- [WCAG: Reflow](https://www.w3.org/WAI/WCAG21/Understanding/reflow) supports stacking comparison columns in narrow windows and wrapping long content.

## Evidence and privacy

The shared core compares causal coverage from fresh module Reader segments and the lineage graph. It identifies the source and destination by agent, runtime profile, machine, title and exact session key. The review shows shared logical revision counts, exclusive message/tool counts and bounded chronological excerpts on each side. Dates provide context, not a basis for attributing ownership or detecting divergence.

Reasoning, raw native records, tool inputs/results and attachment bytes are excluded from comparison excerpts. Comparison does not read credential files. User-visible message text is rendered through the app's existing safe Markdown renderer. Preview bounds are stated. Generated Hopsesh records are not counted as independent work. Failure to read or verify a destination is explicitly shown as unavailable evidence, never as proof that both conversations gained work.

An exclusive record is not necessarily new work written after the handoff. The Claude reader previously treated a stale `last-prompt` checkpoint as the final conversation leaf and could omit an already saved assistant response. Later checkpoint metadata could reveal those records and make causal coverage differ. The module now extends a checkpoint through an unambiguous continuation written after it, retaining explicit later rewind selections and rejecting ambiguous branches. Earlier incomplete transfer receipts are not retroactively granted coverage. The comparison shows records absent from the other conversation with their actual timestamps; it never asserts that the user resumed the original merely because these records were discovered later.

Movement hooks are notices, not exclusive ownership locks. The existing Claude integration subscribes to SessionStart and UserPromptSubmit and emits additional context. Installed/enabled status confirms configuration, not actual delivery or model obedience. Neither hooks nor later metadata repair content omitted by a reader. Native append still requires fresh verified causal evidence.

Claude compaction can replay the same UUIDs with reparented preserved messages; upstream reports describe [compaction replay](https://github.com/anthropics/claude-code/issues/92089) and [cross-boundary parent behavior](https://github.com/anthropics/claude-code/issues/48937). A global last-UUID lookup can create a loop into a retained tail. The module resolves each ancestor to its preceding physical occurrence and deduplicates identical authored records, while rejecting changed replay content, forward references and sidechains. This can expose historical messages omitted by an earlier reader. An old receipt may consequently reject insertion into its prior projection: that is unavailable causal evidence, not proof of post-move work. The review then displays bounded recent saved messages with an explicit unverified-relationship label and no exclusive counts. It does not repair or grant coverage to the old receipt.

## Actions and effects

The original destination cannot be overwritten to hide independent work. Review a separate destination session creates a fresh plan with `keep-both`, retaining validation of the pinned destination. No files, processes or lineage receipts change during review. The subsequent creation starts a separate logical conversation branch from the source; it preserves both existing native conversations, and it does not combine the destination-only work into the new copy. Repository checkout and code behavior remain those displayed in the repository section; a conversation branch does not itself create a Git branch.

Cancel return leaves both conversations unchanged. Failed planning offers retry with explicit unavailable evidence. The footer remains visible while scrolling. When a refreshed plan discovers divergence after ending an original, it returns the body to the comparison at the top.

## Verification

Core tests cover exclusive causal coverage, coalesced fragments, generated records, unavailable evidence, preview limits and privacy. Mandatory `TestMovement*` scenarios exercise both Claude → Codex → Claude and Codex → Claude → Codex with independently changed histories, confirming no writes during review and preservation of both originals during separate-session apply. `TestMovementStaleClaudeCheckpointReturn` verifies that an already saved final response/tool call is included before a handoff, metadata-only shutdown preserves causal coverage, a stale physical cursor is rejected, and a fresh return appends Codex work to the original Claude conversation. These scenarios are selected by the existing Linux/macOS/Windows CI matrix. Browser tests cover direct one-dialog review, explicit replan without apply/launch/stop, cancel, bounded safe Markdown, and narrow-window footer visibility. CLI and TUI also receive the comparison evidence from the shared plan.

## Large Codex rollouts and archive consultation

### Global history and resource settings

Large histories are not a Codex-only concern. The current sweep found the shared
`sdk/ir.BoundedReader` 256 MiB cap still used by Claude's reader and capacity
analysis, the same cap in portable archive creation/merging, and independent
1 GiB native-file guards in fork/recovery/write paths. Preview windows and cloud
log limits serve different purposes and should not be exposed as a single
"maximum session size" slider.

Research supports compact working context plus retrievable history. Anthropic
describes [compaction, persistent notes and on-demand context retrieval](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents),
and its [context editing](https://platform.claude.com/docs/en/build-with-claude/context-editing)
removes old tool outputs with explicit placeholders and configurable thresholds.
OpenAI offers [context compaction](https://developers.openai.com/api/docs/guides/compaction).
These provider facilities do not make vendor-private summaries portable between
agents. Configurable resource limits with hard implementation ceilings are also
an established pattern; see [SQLite runtime limits](https://www.sqlite.org/limits.html).

Recommended settings and behavior:

- **Handoff context:** Automatic by default; optionally a smaller user budget,
  always bounded by the receiving module's model/context allowance. Report
  conservative estimates honestly; a user setting cannot enlarge the model.
- **Older context:** retain native readable summaries when valid, explicit
  condensed extracts, the latest actual request, and recent complete turns.
  Offer "recent turns only" as an alternative. Label omitted content and retain
  it in the portable archive. Do not claim a deterministic extract is an AI
  summary. Do not add paid summarization calls implicitly.
- **Analysis resources:** an advanced validated memory budget; reaching it should
  use disk-backed processing where supported, rather than truncate canonical
  source history. A configurable disk allowance remains a hard stop if there is
  insufficient space; source files and existing destinations stay unchanged.
- **Transfer review:** separate included-context counts from archive preservation
  counts; inspect the oldest included boundary and excerpts. Permit a per-transfer
  override and return a structured limit error with the affected stage and
  a link to the appropriate setting.

Implementation belongs in shared configuration, SDK policy and the conversion /
archive pipeline; native record interpretation, branch selection, compaction
semantics and model capacity remain inside each agent module. Keep policy scoped
to each operation, including remote operations, rather than mutable global
variables. Persist archive references and coverage independently of shortened
receiving context. Never treat omitted or unread history as verified coverage.

Stages: (1) separate read-memory, record, archive-disk and model-context budgets;
(2) add streaming/disk-backed archive and lineage processing plus paged module
readers; (3) expose validated global settings in GUI/CLI/TUI and per-transfer
review; (4) run both-agent, both-direction scenarios in Linux/macOS/Windows
matrices. Include repeated multi-party returns/forks, huge tool records, malformed
records, tool-pair boundaries, Unicode, cancellation, low disk space, remote
policy propagation and original-file preservation after failure. Changing only
the cap or keeping a tail is insufficient for a safe native append/return.

Implemented (stages 1 and 3, part of 2):

- `ir.Limits` carries separate read-memory, record, archive and native-file budgets
  plus the context budget and older-context mode, per operation: in `move.Options`
  and the operation's context (`ir.WithLimits`), never in globals. Defaults equal the
  previous constants; `Normalize` enforces hard ceilings.
- Every reached limit is an `ir.LimitError` naming its stage and the `[history]`
  setting. The Claude and Codex readers, Codex record analysis, the portable archive
  and whole-file fork/recovery/verification reads (`agent.ReadNative`) use them.
- The portable archive is built by `convert.ArchiveWriter`: the prior archive is
  re-read record by record and merged without holding several copies.
- `convert.Render` applies the user budget (lower only) and `recent` mode, which
  replaces the extract with a labeled omission notice that carries no coverage. The
  report separates included context from `archiveRecords` and shows `oldestIncluded`.
- Settings → History (GUI), `hopsesh history` (CLI), and per-transfer overrides in the
  GUI plan, `pull --context-budget/--older` and the TUI plan (`b`, `o`). Peers apply the
  sender's context policy and their own resource budgets.

Remaining: paged module readers and fully disk-backed lineage processing (readers still
return whole segments), a free-disk preflight, and the cross-OS scenario matrix.

### Implemented fix

Codex persists repeated compaction replacement histories and runtime metadata; see its [rollout reconstruction](https://github.com/openai/codex/blob/main/codex-rs/core/src/session/rollout_reconstruction.rs) and [model context storage](https://github.com/openai/codex/blob/main/codex-rs/rollout/src/model_context.rs). Raw file size is not working-context size. The Codex module scans complete records incrementally, retaining conversation-bearing payloads and compaction message text while calculating replacement-context size independently. It retains every valid native record position/ordinal, validates paginated history, checks cancellation between bounded read fragments, and rejects oversized records or retained conversation explicitly. It never silently truncates conversation text. Other modules' decoding limits are unchanged.

The shared portable handoff explicitly requires bounded archive consultation before continuation: opening context, the last ten preserved records, and targeted search/pagination/chunks for relevant decisions and unfinished work. Offsets come from the actual merged archive, including earlier transfers. The complete handoff remains subject to the receiving model's payload budget. An agent must state what it consulted and report unavailable access; archived text never grants new authorization. This is an instruction, not proof that a model obeyed it.

A reset branch's origin is initially absent. Catalog observation can establish the first replica before planning rereads the persisted empty manifest. Merge accepts that additive initialization only when the originless side contains no replica on that branch and the parent/fork boundaries match. Different established origins still conflict atomically; initialization adds no historical coverage.

Regression coverage: `TestAnalysisLargeRepeatedCompaction` scans more than 256 MiB of repeated native state without retaining its repeated payload. Module tests retain positions, complete-line offsets, pagination validation, cancellation and memory limits. `TestMovementArchiveConsultation` runs both Claude-to-Codex and Codex-to-Claude in the mandatory Linux/macOS/Windows movement matrix, including a persisted reset source ahead of catalog origin initialization and actual committed archive offsets.
