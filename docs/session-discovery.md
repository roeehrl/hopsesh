# Session discovery

The main window opens with saved session metadata and an enabled search field. An
initial installation starts with a usable empty list; sessions appear in batches
while sources are read. The count and each machine's phase distinguish saved,
reading, complete and failed results. A failed source retains its saved rows. A
successful complete enumeration removes deleted rows. Stop checking cancels the
current full scan; the rows already found remain usable.

Saved metadata never means an agent is currently running. Cached rows have unknown
presence until live evidence arrives. Session actions resolve current source and
account bindings before proceeding. Conversation previews read just the selected
file through their own connection; previews and transfer validation do not reuse
saved transcript bodies. Discovery does not replace an open transfer review.

GUI, Quick access, TUI and CLI share the private disposable SQLite catalog under
`<state directory>/catalog/sessions-v1.sqlite`. Overlapping browsing scans with the
same source selection follow one collector through a renewable 15-second lease;
crashes expire the lease. Source generations and transactional merges prevent late
scans or independent machine scans from losing newer results. Explicit action
validation bypasses summary reuse and collector leases. `hopsesh ls --cached`
returns saved metadata immediately, without SSH, git or vendor commands; normal
`ls` retains its complete-result behavior. `--host local` also filters cached rows.

Claude and Codex summaries are keyed by machine, agent, profile, root, parser
version, file size/modification time, and relevant title/subagent inputs. A growing
file is not cached while it changes. Parser changes must increment the summary
salt. Watch notifications invalidate changed paths and coalesce updates for 300 ms.
Claude/Codex watchers cover session directories and title/lineage metadata; vendor
diagnostics are excluded so probes cannot trigger their own refresh loops.
GUI and TUI reconcile every minute to cover missed notifications, watcher limits,
network filesystems and account-root changes. Remote/cloud listing remains explicit
or scheduled by the existing frontend refresh policy; local file notifications do
not authenticate to another machine.

The catalog stores summaries, public account labels and repository/lineage metadata,
including bounded prompt excerpts, never credential files or complete transcripts.
It is isolated by enabled sources, root overrides and account registration bindings.
Unreadable cache storage falls back to discovery; confirmed database corruption
rebuilds the disposable catalog. Summary reuse expires after 24 hours. Snapshot
writes prune expired summaries, cap summaries at 100,000 rows and snapshots at 100
scopes/30 days. Resetting the state directory removes the catalog along with other
state; deleting only `catalog/` while Hopsesh is stopped rebuilds just browsing data.

Large expanded groups render bounded chunks near the viewport. Keyboard navigation,
search, selected rows and group expansion continue to address the complete list.
Loading announcements use a polite status region rather than interrupting every row.

Validation includes the macOS/Linux/Windows `TestSessionDiscovery` route and
`session-discovery.txtar`, shared-writer/lease/corruption tests, parser invalidation
and cancellation tests, and Chromium/WebKit tests for progressive discovery,
selection/focus preservation, late publications and a 10,000-session list.
