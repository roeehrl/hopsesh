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
