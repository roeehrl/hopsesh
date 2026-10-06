# Session lineage and round trip implementation plan

Status: implemented on `sessions-redesign` using `lineage/3` and peer protocol 3.
The four defects below now have regression tests. Release still requires the hosted
scenario and OS-pair gates; local verification is recorded at the end of this document.

## Confirmed behavior and defects

The ordinary same-machine Claude Code → Codex → Claude Code path and existing two-machine
move/return tests pass. That does not establish arbitrary multi-party synchronization.
The previous implementation had four reproduced failures, preserved as regression cases:

| Reproduction | Current failure | Required invariant |
| --- | --- | --- |
| A Claude → B Codex; add work in Codex; update B Claude; return to A Claude | Reports already synchronized although A lacks the new work | Delivery to B must never acknowledge delivery to A |
| Diverge two copies; Keep both on return | Two native session IDs collapse into one GUI/TUI row | Every independent branch stays selectable |
| Move the original again after Keep both | Replace can set aside the independent fork too | Only the explicitly selected branch and destination may be changed |
| Reorder an equivalent manifest's replicas | Return target and conflict classification change | Serialization order cannot select a destination |

The replacement chooses destinations by branch and persistent endpoint, requires explicit
selection when several replicas match, and compares revision coverage. Inventory groups
copies within one branch. Causal references determine history; timestamps only label it.

## Identity and history model

Replace the current manifest with a single new format. Use these distinct identities:

| Record | Meaning |
| --- | --- |
| Family ID | Shared ancestry, used to group related branches |
| Branch ID | Independently continuing conversation; never conflated with its family |
| Endpoint ID | Persistent machine installation or cloud account identity; host aliases and display names are labels |
| Replica ID | Native session ID plus agent, endpoint and branch |
| Revision ID | Immutable authored change with explicit parent revision IDs |
| Operation ID | One requested transfer, stable through retries and recovery |
| Projection receipt | Which revisions a specific native replica received, with representation and fidelity |

Store immutable records by ID and explicit references. Validate all references, bounds,
duplicate IDs, conflicting bodies and cycles before use. Compare ancestry, not wall clocks;
timestamps are for display. Union identical records idempotently; reject conflicting records
with the same ID. This adopts explicit ancestry from [Git's merge-base model](https://git-scm.com/docs/git-merge-base)
and causal concurrency tracking from [Dotted Version Vectors](https://arxiv.org/abs/1011.5808).
Start with explicit revision parents; add compressed causal summaries only if profiling
requires them, preserving the same semantics.

Do not automatically interleave divergent conversation text. Text CRDTs define merge
semantics for concurrent edits, but cannot establish that two agents' reasoning/tool
sequences remain valid together. [Automerge's document model](https://automerge.org/docs/reference/documents/)
is useful background, not a dependency required by this design.

## Preserve provenance through conversion

Per-destination cursors alone fix the first reproduction but not A → B → C → A. C can
contain a rendered copy of A's old conversation plus new B and C work. The return must
identify and omit A's already-present history without discarding either new contribution.

Extend the IR and writer contracts with source revision coverage for every output item.
Coalesced messages retain all source IDs. Generated briefings are marked generated and
never become newly authored work on the next scan. A summary carries the covered IDs plus
`summarized` fidelity; dropped reasoning and attachment placeholders must remain explicit
losses, never be advertised as exact copies.

Persist a projection map beside the native transcript. Each mapping ties logical revisions
to native record identities, byte spans and content hashes. Readers validate these anchors
before treating imported text as previously delivered. Path rewriting changes a native
representation, not its logical origin. Preserve original native bytes for same-agent moves.

If compaction, rewind or a native rewrite invalidates anchors, reconstruct only from
vendor ancestry that the module can verify. Otherwise show “History changed; cannot safely
append” and offer a separate branch. Never infer ancestry from matching titles, timestamps
or similar text. A receipt records both delivered coverage and fidelity; logical coverage
does not claim that the target agent possesses all original bytes or hidden state.

## Planning and applying a transfer

1. Read fresh source and destination metadata and native heads. Resolve the requested
   branch and explicit destination replica. If several candidates remain, require a
   target selection in the plan; array ordering must never decide.
2. Compute missing authored revisions from that replica's receipts and validated native
   projection. Show source-only work, destination-only work and conversion losses.
3. If destination is an ancestor, append missing work where supported. If source is an
   ancestor, report that no new work is needed. If histories diverged, preserve both by
   default; replacing one branch requires naming the affected replica. Never touch a sibling.
4. Freeze expected heads, write ranges and operation ID in the plan. Revalidate before
   writing; reject concurrent agent edits or changed metadata. Active native writers block
   destructive writes. There is no automatic conversation merge.
5. Journal intent locally and at the receiving peer; stage data; install/append under the
   existing destination lock; durably record the destination receipt and committed operation;
   then acknowledge it to the source and update its moved mark. Source acknowledgment failure
   becomes recoverable pending acknowledgment, not a second transfer.
6. Retry with the same operation ID and fingerprint. Return its committed result, or resume
   the prepared transaction. Reject the same ID with different input. Follow the explicit
   request-identity approach in [AWS's idempotent API guidance](https://aws.amazon.com/builders-library/making-retries-safe-with-idempotent-APIs/).
7. Undo records a compensating event and checks the native destination has not gained work.
   It does not erase history or silently rewind an unrelated branch.

An atomic rename of one sidecar cannot make a native transcript, sidecar and two machines
atomic together. Use the existing journal plus recoverable prepare/commit receipts and
verify every crash boundary. [SQLite's atomic-commit explanation](https://www.sqlite.org/atomiccommit.html)
motivates durable intent and recovery ordering; merely putting the index in SQLite would
not make external native files transactional. The portable per-session lineage remains
authoritative; a local index is rebuildable.

## Forks and moved marks

Keep both creates a new Branch ID with an explicit parent revision and parent branch. The
original remains unchanged. Both branches can move, return and fork independently. Native
forks must be discovered using a module capability that returns verified native ancestry;
unknown ancestry stays independent. Agent-created IDs after a fork launch must be adopted
only after confirming which native session was created.

A deliberate “copy and continue both” operation creates a branch before either side writes.
Unplanned concurrent work is displayed as divergence until the user chooses how to preserve
it. Copies of one branch may share a row; distinct branches never do. Family grouping is an
optional expandable group above those rows.

Moved marks reference a committed operation, source branch and destination replica. Returning
clears only the matching source mark after destination acknowledgment. A fork's mark and
counter cannot be cleared or advanced by moving its parent or sibling.

## Counts and visible history

Use precise labels: **Transfers**, **Round trips to origin**, and **Returns to this location**.
The origin is the branch's starting endpoint and agent. “Location” in these counts includes
the agent, so two different agents on one machine are distinct stops. Keep machine-only
travel available as a separate filter. Native backup copies are not travel stops.

| Route on one branch | Transfers | Round trips to origin A |
| --- | ---: | ---: |
| A → B → A → B → A | 4 | 2 |
| A → B → C → A | 3 | 1 |
| A → B → C → B → C → A → B | 6 | 1 |

The last route has four revisits to previously visited locations: B, C, A and B. That is
not four round trips to A. Derive counters from committed route events, never increment
mutable counters on retry. No-op scans/transfers and native backup creation do not count.
Forks start their own route and counters while retaining the parent's ancestry. Show undone
events in history; exclude them from active-route totals and label them undone.

GUI rows show branch identity and a round-trip badge. The inspector has an expandable
Journey with machine, agent, operation, branch point, receipt status and losses. Selecting
a copy shows its exact replica and receipt state. Plans say “Return to Claude Code on A —
add 4 new messages” or “Create fork on A”; never a vague resume or newest-copy inference.
TUI and CLI use the same derived DTO and counts, with a history/detail command and explicit
branch selectors. Both present offline knowledge as “Last seen”, not verified current state.

The shared Back control belongs in the upper-left shell toolbar across normal, loading,
error and result states, consistent with [W3C navigation guidance](https://www.w3.org/WAI/WCAG22/Understanding/consistent-navigation.html).
Scan progress uses an indeterminate status and accessible announcements, following
[W3C ARIA progress guidance](https://www.w3.org/WAI/WCAG22/Techniques/aria/ARIA25).

## Implementation sequence and deletion work

1. Preserve the four reproduction scenarios as regression tests when the replacement lands.
   Add graph/receipt property tests first: merge order independence, idempotence, clock skew,
   cycle rejection, malformed references and independent branch identity.
2. Implement the new lineage format, persistent endpoint IDs and strict parser. Delete the
   index-based hop representation, mutable-head synchronization inference and wall-clock
   merge winner. There is no legacy reader, dual write or migration shim. Old sidecars are
   reported unsupported and can be explicitly archived; never silently relabel them as
   verified new lineage. Native sessions remain untouched.
3. Add provenance to IR, converters, native readers/writers and per-replica receipts. Test
   coalescing, summaries, path rewrites, signed reasoning, compaction and rewind.
4. Replace pull/continue/push/handoff/fetch target selection and apply protocols together.
   Peers negotiate this format before any mutation; incompatible peers fail clearly.
5. Replace family-wide inventory collapsing and replace-all-related-copy behavior. Add fork
   adoption, marks, derived counters, GUI/TUI journey views and exact branch selectors.
6. Delete superseded grouping/selection helpers, old fixtures and obsolete documentation;
   remove unused IR fields or wire them completely. Run the full matrix and real-agent smoke
   gates before merging or releasing this replacement.

## Tests and release gates

`internal/devtools/hsmatrix/model.go` now includes cross-agent `roundtrip` rows.
A dedicated stateful route suite complements the transport matrix. Pairwise
coverage is complementary; it cannot substitute for temporal multi-party scenarios.
The existing nightly `-t3` is covering-array strength three, not a three-machine test.

Mandatory routes are ABABA, ABCA and ABCBCAB, with Claude/Claude, Codex/Codex and mixed
assignments; same-machine agent changes and cross-machine changes; pull and push; work and
no-work at each stop. Add fork-original/fork-sibling routing, multiple target replicas,
offline source acknowledgment, retry after every write boundary, and undo after new work.
Keep Linux/macOS/Windows scenario jobs and real OS-pair transports. Nightly suites add
long seeded random histories; PR CI runs every mandatory route with bounded fixtures.

Assert each authored sentinel appears exactly once where expected, missing work is never
reported synchronized, original native prefixes stay intact on append, branches retain
separate rows and IDs, only the selected destination is replaced, and counters/marks match
committed events regardless of metadata serialization order or clock skew. Assert loss
annotations survive every subsequent hop. Crash recovery must leave a complete receipt or
a recoverable pending operation, never an untracked successful native write.

Finally exercise actual installed agents: resume, native fork, compaction, append and paid
continuation on both agents, plus real machine transport. Fixture success proves planner
and format invariants; it does not prove a vendor accepts the resulting session interactively.
Record the tested versions and inspect the installed GUI/TUI before release.


## Implemented behavior and verification

- `internal/core/lineage` stores immutable authored revision ancestry, branch identities,
  endpoint-scoped replicas, projection states, causal transfer operations and undo events.
  Strict parsing rejects unknown formats/fields, conflicts, malformed references and cycles.
  Old sidecars require explicit, undoable archival; native conversation bytes are preserved.
- `sdk/ir`, both native agent modules and `internal/core/convert` carry revision coverage
  through coalescing, summaries, generated briefings and sanitized native copies. A native
  anchor changed by compaction or rewind blocks automatic append; an explicit snapshot fork
  records unverified history as a loss. Forks inherit prior fidelity losses.
- Native move/continue operations persist their stable request, planned heads, writer request,
  committed native cursor and receipt intent. Retrying recovers receipts without rewriting
  native files. Later native work blocks recovery. Source acknowledgments remain visible
  in Activity and recover on retry. Destination locks and frozen cursors protect selected
  replicas; independent forks are never replaced or stopped along with their parent.
- A visit with no new authored work synchronizes lineage receipts and may synchronize code;
  it does not write conversation bytes or count another transfer. Original native IDs are
  selected on return. Fork creation starts independent route counters at the fork endpoint.
- The GUI inspector displays branch/origin, transfer and origin-return counts, causal history,
  undone events and fidelity losses. CLI/TUI show the same branch-derived counts. Plans
  require a selected destination when multiple replicas qualify. Deferred native titles are
  tied to the committed operation, source replica and branch, and cannot mark a later return.
- Mandatory `TestLineage` E2E scenarios run in each Linux/macOS/Windows scenario job, alongside
  the existing SSH transport matrix and real OS pairs. The suite has 48 work-at-every-stop
  route cases plus 12 workless/alternating cases across ABABA, ABCA and ABCBCAB, both starting
  agents and all agent assignments. Separate scenarios route originals and sibling forks,
  test multiple destinations, reordered manifests, stale plans, interrupted writes in both
  conversion directions, failed destination receipts and uncertain cloud sends.
- Nightly adds 12 seeded routes with 24 hops each and alternating work. These use three
  isolated machine homes and actual module readers/writers. They are temporal correctness
  tests, not claims of three physical machines. Hosted SSH/OS-pair jobs prove transport.

Local verification on 2026-10-06: a paid round trip through installed Claude Code 2.1.289
and Codex 0.160.1 passed, including resuming the original Claude session after append.
The inspector/conversation/navigation/layout browser checks passed in Chromium and WebKit
(28 tests). Native receipts, route/counter, merge, recovery and fork regressions have also
passed locally; final hosted gate results belong in the pull request, not inferred here.

### Provider boundaries

Hopsesh-created forks are tracked for both agents. Discovery of an agent-created fork
requires a vendor-declared parent plus verified native inherited records. Codex's materialized
forks expose this information; paginated ancestry that is absent from the local file is
reported unverified. Claude's local fork files currently offer no verified parent capability
in this module. Those sessions remain independent rather than being linked by similar text.

Cloud drivers without idempotency keys cannot safely repeat an uncertain vendor send.
Hopsesh returns the durable pending operation and requires adoption or undo; it never sends
again automatically. A completed cloud operation returns its cached result and recovers
pending source receipts. Cloud briefings and code-only task results retain their reduced
fidelity; receiving every logical revision does not restore hidden reasoning or tool state.

Final provenance regressions also cover plan records, generated-context exclusion,
coalesced tool/result coverage, summarized-history coverage and fragment-level return
filtering. Explicit selection tests create two native replicas on the same destination
branch, select each in turn and prove the other native file is untouched. A scoped-stop
regression proves a sibling branch's running session is not stopped.

Acknowledgment writes lock and merge the current sidecar instead of overwriting a newer
acknowledgment from another destination. An owned lock survives a process exit and can be
recovered by the same journal; other operations remain pending. Activity's **Retry
acknowledgement** and `hopsesh lineage retry <journal-id>` repair metadata without native
writes. Receipt recovery updates only metadata undo guards, so later authored native work
still blocks destructive undo. Peer pushes retain explicit destination/operation IDs and
use the same transfer identity in both undo journals; portable undo identity does not
use either machine's clock. Alias-rename recovery is covered by the native crash test.

Hosted matrix regressions exposed and now cover valid Claude append parents and active
leaf checkpoints, the public push `--in` flag, and profile-scoped endpoint identity.
Independent explicit configuration folders under one OS account have distinct endpoints;
SSH aliases of one profile retain one endpoint. This prevents the Windows loopback transport
from treating two isolated native files as a single replica. Consecutive undo advances only
metadata guards that match the exact restored bytes before compensation; all native guards
remain unchanged. Both no-new-work consecutive undo and refusal after later authored work
have mandatory regressions. The cloud fetch/convert/two-undo matrix row passed locally.
