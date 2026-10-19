# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## Unreleased

### Added

- Sizes: `hopsesh ls` shows each session's size and each group's total, and ends with what
  each agent's sessions take on each machine (for example `my-mac (here)  1.3 GB: Claude Code 1.2 GB,
  Codex 172.7 MB`). In the app, a session's size is beside when it was last active, a
  group's total is in its heading, and the Machines screen says what each machine's
  sessions take. `hopsesh show` gives the size in KB, MB or GB.

### Fixed

- Undo removes the folders an operation's writes made, once they are empty: undoing a push
  no longer leaves an empty project folder (such as `~/.claude/projects/<folder>`) on the
  target. A folder that holds something else since, or was there before, stays.
- Scanning a machine over SSH no longer waits out the scan's 3-minute limit when it holds
  large Codex forks or moved sessions (a Mac with 10 GB of them: 184 s → 14 s). A listing
  no longer reads those transcripts over the network. A command about a session still checks
  its family there: a Codex fork's ancestry, and whether its moved copies are ahead or
  behind. Until then a listing shows another machine's Codex fork as a session of its own,
  and asks to verify a return to a copy there, as it does for a copy it cannot read.

## [0.5.0] - 2026-10-15

Every machine a full place: hopsesh can now run on your other machines, in your login session
there, set up from here. See [Machines](docs/machines.md).

### Added

- `hopsesh hosts setup <machine>` (Machines › Set up hopsesh) installs this hopsesh in
  `~/.hopsesh/bin` on a machine reached over SSH (the same verified release for another kind of
  machine), turns receiving on, and starts `hopsesh serve` in the login session there at each
  login (LaunchAgent, systemd user service, sign-in task; no administrator rights). Only on
  request: scans never install anything.
- `hopsesh serve` answers other machines from the login session (a Mac's Keychain and desktop
  apps included) through a private socket behind `hopsesh peer`, and runs sessions for them in
  terminals it owns. `hopsesh terminals run <machine>` and `terminals attach` show them here;
  in the app, **Continue on ‹machine›** does, and a new session or an account's sign-in can run
  there too.
- Machines says how hopsesh works with each machine (running there, there but not running,
  older, SSH only), what that lets it do, and sets it up or updates it step by step.
- Accounts is new: your accounts by email, who each tool (Claude Code, Claude desktop, Codex,
  the Codex app) is signed in as on every machine and in which folder, accounts only seen in
  records, when two accounts write to one folder, and each folder's sessions by account.
  Claude desktop's own sign-in is read from its account id and session index (never its
  tokens).
- Account folders live in `~/.hopsesh/accounts/<agent>/<name>` and can be added on any machine
  with hopsesh, signed in from this machine's browser, with an optional `<agent>-<name>` shell
  command. Folders made by earlier versions move there after you agree. A Codex folder keeps
  its sign-in the way your default folder does.
- Move › **Choose where it continues** lists every machine, folder and tool with who it runs
  as there, what carries over, and whether the copy left behind is protected. Bringing a
  session that is working on another machine offers to wait until it is idle, or to join it
  there. After a send, **Open it there** runs it in a tab here.
- Settings › **Hooks & skills** shows the command, protection hooks and skill on every machine
  and sets them up from here; `hopsesh doctor --all-machines` checks the same. The skill goes
  into every account folder. Sessions points out machines that need hopsesh, an update or
  their protection set up.
- A Mac with only the Codex app (ChatGPT.app) uses the app's built-in Codex for sign-in
  checks, resuming and protection hooks; a Codex CLI is still preferred when installed. Local
  and remote Macs alike.
- Codex voice conversations and Work threads from the Codex app are hidden by default;
  Settings › Agents (or `voice_threads` / `work_threads` under `[agents.codex]`) shows them.

### Changed

- Two machines agree on the newest peer protocol both speak (6, down to 0.4's 5) instead of
  requiring the same one.
- hopsesh never writes its files on a machine without hopsesh: the identity of one reached
  over SSH only is kept here until hopsesh is set up there.
- A Mac's sign-ins hidden from an SSH login are unknown, never a changed login; an original
  left on a machine without hopsesh is said to be unprotected (session list, move review,
  CLI); an unreadable account registry no longer hides a machine's sessions.
- Hopsesh's record of who ran a session is kept per folder, moves with the session and is
  shared between machines; inside Claude desktop it records desktop's own account.
- Agents' versions are probed in parallel.
- Version checks use semantic version precedence: a pre-release past a minimum counts (the
  Codex app's built-in 0.162.0-alpha), one before it does not. The Codex protection hook
  needs Codex 0.129.0 or newer (hooks and their review), down from 0.160.0.
- The protection card lists every detected agent, including one that cannot take the hook
  and why, says what each hook covers (Terminal, the desktop app), and keeps each step's
  result on screen until Done. "Not now" is remembered until the problems change.
- A skill copy installed by a newer hopsesh is never replaced by an older one.

- Sessions are attributed from every record of the account they ran under: Claude Code's
  per-run organisation stamp (not only Remote Control), Codex's session creator, and a new
  Hopsesh record written by its prompt hook. Runs that name the user match only that user,
  a session that changed accounts says so, and shared Team/Enterprise organisations name no
  one. To match them, Hopsesh reads the public identity next to each login on this machine
  (Claude Code's `oauthAccount`, Codex's account and user IDs); never tokens.
- Account filters, account groups, family names and move-back labels no longer change when a
  profile signs in to another account.

- CI: the weekly drift check compares from the code's version floors and watches the identity,
  record and hook formats hopsesh reads, and the Claude and Codex desktop apps; the skill is
  checked against the built program (and the release's) and a command-tree golden; remote
  scenarios run hopsesh over SSH with no hopsesh, nobody logged in, serve, 0.4.5 and offline.

### Fixed

- `hopsesh hosts setup` copies this hopsesh to a Linux machine of the same kind again
  instead of looking for a release: `uname -m` (x86_64, aarch64) is read as Go's arch name.
- Continuing a session from a machine without hopsesh in another agent says the original
  there is not protected, as a move does.
- hopsesh never stops the Codex app's process: its one app-server holds every app thread.
  Such a thread shows as open in ChatGPT desktop, with Open instead of a stop.
- The skill's reference named a `--fork` flag that does not exist.
- Claude Code sessions continue in place under the account signed in now (**Continue
  here**); Codex sessions that ran, or may have run, under another account are not resumed
  as they are (Codex would fail) but continue as a cleaned copy.
- The quick panel showed a profile's current email as each session's account.
- Sessions on other machines were shown as run by "another account" even when their root was
  signed in to it, because peers dropped the account fingerprint.
- The move review names the account that ran the session as its source account. It
  names profiles by folder and says who is signed in to each now, so a profile's label
  no longer changes with a new sign-in.

## [0.4.5] - 2026-10-14

### Fixed

- Sessions show the account that ran them, as Claude Code recorded it, instead of
  whichever account their profile is signed in to now. A profile that was signed in to
  another account before no longer labels that account's sessions with the new email;
  unrecorded sessions claim no account. Account filters and grouping follow suit.
- Status chips in the session list no longer get cut off mid-word: the account email
  shortens with an ellipsis and the Remote Control link folds to its icon when space is
  short, with the full text in tooltips and Details.
- Accounts offer **Sign in** only when a profile is signed out or its check failed; the
  sign-in check is a refresh button beside the status.

### Added

- The session list's status column is resizable from its header: drag, use the arrow
  keys (Shift for larger steps, Home/End, Enter to reset), or double-click to fit what the
  rows show. The width is remembered.

## [0.4.4] - 2026-10-13

### Fixed

- Closing the desktop app during a refresh now exits normally instead of hanging.
- The built-in terminal preserves complete input in order and reports write failures
  instead of silently losing bytes or leaving Codex waiting for an incomplete reply.
- Repository details say they are still being checked while discovery is pending,
  instead of incorrectly reporting that the checkout is missing.
- Movement indicators use the session list's right-hand status column, leaving room
  for session titles; session actions remain in the inspector.

- Local movement updates appear while other machines are still loading, preserving
  pending remote rows and selection. Return plans recheck the exact destination's
  current activity instead of requiring an already closed client to be ended again.
- Claude's verified default profile uses native configuration lookup for account
  probes and sign-in as well as resuming; explicit and custom roots stay isolated.
- Raising a History resource limit permits an immediate retry without a manual
  refresh. Settings invalidate obsolete reviews while preserving active transfers
  and their transports. Delayed process evidence can confirm terminal association
  without another scan, and concurrent tab exit does not deadlock that refresh.
- Background refreshes update list cells in place and keep compact rows at a stable
  height. Completed session lists stay visible throughout slower background reads.
- Session actions stay in the inspector instead of appearing over metadata on hover.
  Ending a reviewed original refreshes both sides before offering the move back.
- Unknown agent windows no longer display a made-up terminal badge or offer a Show action.
  Resuming the default Claude profile keeps its existing configuration and onboarding state.
- Native-fork cache results follow session identity rather than listing position.
  Reordering or adding sessions no longer attaches another conversation’s originals.
- Inspecting an original no longer replaces its conversation's parent row or adds
  duplicate rows after a refresh. Child rows name the actual session; blocked originals
  show “Blocked” and lead to the destination rather than resuming the protected source.
- The movement ledger no longer has an invisible 16 MiB cap. Settings → History exposes
  its size limit (64 MiB by default, up to 512 MiB), and limit failures link to that setting.
  Complete movement history is preserved, including ledgers larger than the former cap.

- Session list entries show where a conversation moved from or to, with the original
  visible as a selectable line underneath. The same movement labels appear in details,
  the terminal UI and CLI.
- Repository groups render their session rows without deferred WebKit visibility, which
  could leave an expanded group blank until another redraw.
- A return whose original is missing offers a reviewed new session on the same branch.
  A changed account observation no longer hides a missing original after a complete scan.

- A Claude Code session forked after it was compacted can be moved and continued. Its
  history starts at the compaction boundary, whose link to the earlier conversation
  points at a message copied after it; hopsesh refused it as "both copies changed:
  Claude branch has a forward parent reference".
- A move is always a move. The review no longer offers "Keep the old session running
  too", the source notice no longer offers "Fork here instead…", and `hopsesh pull` and
  `push` no longer take `--fork`: that option made a separate copy that was never blocked,
  silently overriding "Block the original". Separate copies hopsesh still makes on its own
  (both copies changed, bounded continuations) are called separate conversations.
- Moving back into a protected open original closes only its exact agent client before
  appending, then lets you reopen the same conversation without archiving it. Blocked clients
  still write bookkeeping with an old parent UUID; writing while they stayed open could
  leave returned messages outside the resumed history.
- Returning clears notices about forks from an earlier residency, so an unblocked
  original does not incorrectly say its old separate copy is still the destination.
- Large compact summaries and diagnostic attachments no longer make a real Claude
  session disappear when the lightweight head/tail windows contain only metadata.
- Return capacity accounts for instructions and tools already included in the original's
  measured usage. Near-limit originals carry a warning that the agent may need its own
  compaction before answering. When a new session is needed, the review explains why
  and does not ask you to end an original that will not be written.
- Blocking or warning in a moved session's original works on a default install. The
  protection hooks always pass the settings and state folders; naming the default
  folders explicitly made hopsesh look for this machine's identity in the wrong place,
  so the hook let every prompt through. A flag naming the default folder now changes
  nothing.
- An original that once had a separate copy is blocked again when it is moved. The move
  and the earlier copy were judged ambiguous, so no notice reached the agent's hook and
  the original was not blocked, while hopsesh showed it blocked.
- Opening a moved session in the Codex app shows its command without an empty
  `cd ''` in front.
- `hopsesh notices status` (and install and remove) run through the `hopsesh` command link
  recognise the hooks the app installed, instead of reporting them absent and installing
  a second set.

## [0.4.3] - 2026-10-11

### Changed

- The Sessions protection warning is one card: a short checklist (the hopsesh command,
  then each agent), one "Set up protection" button that installs the command and every
  hook, "Approve in Codex…" only when Codex's approval is the last step, and "Not now".
- Agents are named without account labels ("Codex", not "Codex · Codex default ·
  you@example.com"); the account appears only when an agent has several. A moved original's
  row says where it went in a few words, and its details say it once.

### Fixed

- The desktop app no longer burns CPU and rewrites its session catalog while idle. The
  catalog stored each session's full lineage revision graph (tens of megabytes on a long
  session) and rewrote it several times per refresh, about 20 MB/s of disk writes. It now
  keeps only what browsing shows (78 MB → 0.4 MB on a busy machine), writes nothing when a
  refresh finds no change, gives back the space once, and keeps its journal small.
- Refreshes no longer re-read and re-hash every moved session's whole transcript (hundreds
  of megabytes for a long Codex session) when nothing in that session changed, or rewrite
  each session's movement record. In the background the menu bar's list refreshes every
  five minutes instead of every minute.
- Refreshing unchanged sessions is nearly free: lineage files, native fork checks, each
  conversation family's comparison and each folder's git state are reused until a file
  they come from changes (by size and modification time), so a refresh opens nothing that
  did not change. When one copy of a moved session grows, only that copy is read again.
- An idle app writes nothing: the discovery lease is an OS file lock instead of a
  database row, and the catalog is not rewritten for check times or row order alone.
- The app no longer rescans for every write another agent makes to its session: changes
  refresh this machine at most every 5 seconds while you look at hopsesh (its window
  focused or the menu bar list open) and every 5 minutes otherwise, and a change no
  longer deletes cached summaries from the catalog.
- While nobody looks at hopsesh it stops watching session files altogether (each file
  event cost the app tens of wakeups, and agents write their sessions many times a
  second): an idle app now wakes about twice a second instead of over a hundred times.
  Looking at it again re-arms the watch and refreshes.
- A large native Codex fork no longer costs minutes of CPU and gigabytes of memory at
  every start: fork verification streams both rollouts instead of reading them whole,
  a lineage too large to record is refused before it is built, and finding the newest
  revisions of a long history is linear instead of quadratic.
- The desktop app keeps its memory near what it uses (a soft limit of 384 MiB, which
  `GOMEMLIMIT` overrides) instead of letting one large session read leave hundreds of
  megabytes resident.
- CI holds the app's idle resource use to budgets on Linux and macOS (`idle resources`):
  nobody looking means no rescans, no session reads, no writes, under 1% CPU and few
  wakeups, while another agent keeps writing its session and a large native fork is
  verified at the start.
- Two front ends opening the catalog at the same moment no longer leave one of them
  without it.

- A moved original's status no longer runs under the row's action button, and counts read
  "1 transfer", not "1 transfers".

## [0.4.2] - 2026-10-10

### Added

- The original of a moved session is blocked until you move it back (default), or only
  advised, or left alone (Settings › General, `original` in `config.toml`). Block mode
  refuses new prompts through the agent's hook and explains why; moving back clears it.
  "Remove block…" in the app, `U` in the TUI or `hopsesh unblock <session>` releases one
  original, with a warning that continuing there makes the copies diverge.
- Clear moved / original indicators: `◆ Moved out`, `● Moved copy`, `↩ Returned`,
  `! Diverged` with where the session went or came from, in the list, inspector, TUI and
  `hopsesh ls`/`show`. The old `movement_notices` setting is replaced by `original`.
- Codex hook trust: hopsesh asks Codex whether it trusts the hopsesh hooks and says when
  they are waiting for your approval in Codex (`/hooks`), instead of assuming installed
  hooks run. It never approves hooks for you.
- Installing the skill or the hooks from the app requires the hopsesh command, which the
  app offers to install first; a broken command link is reported.
- Settings › History (and `hopsesh history`): a lower context budget for transfers,
  "recent turns only" for older history, and memory/record/archive/file limits. Reaching a
  limit stops before anything is written and names the setting; history is never
  silently truncated. `pull --context-budget/--older` and the plan override them once.

### Changed

- Session titles are no longer changed to show movement; hopsesh shows it in its own
  views. A move, continuation, push or hand-off leaves the source's title as it was, and
  the destination gets the session's own title (no "(from …)" suffix). The list, TUI and
  app derive "moved to", "prepared in" and "continued in" from lineage. The `mark_moved`
  setting, `--no-mark`, the "Label the source session title" toggles and the deferred
  labels for open sessions are gone; an old `mark_moved` line in `config.toml` is ignored.
  Labels written by earlier versions are still read so they don't show as part of a
  title, and a return still clears them. For module authors: the `Marker` capability,
  `agent.Mark`, `MarkTitle` and `ParseMarkTitle` are removed; `Summary.LegacyLabel` and
  `StripLegacyLabel` read old labels.

### Fixed

- GUI “Check now” fetches the latest release immediately, even when daily automatic
  checks are off. Manual checks show progress and failures instead of reporting a
  cached or unavailable result as the newest version.
- A new Claude Code session's context uses the account's current model (from its most
  recent sessions) instead of a 64K fallback; Codex uses the window it reports itself.
- Transfers use the whole context budget: space the older-history extract leaves goes to
  more recent turns verbatim. The plan no longer repeats the carry-over summary as a
  warning.

- Open originals have a clear return status card and guided exit instructions,
  with an explicit check-and-review action instead of silently switching apps.
  Move menus omit empty sections and explain the blocker without internal IDs.
- Claude/Codex round trips reuse the exact original session across runtime profiles
  and first login observations, appending only missing portable work while preserving
  original records, IDs and historical bindings. Independent work and live writers
  still block; adapters without portable append retain a same-branch copy fallback.
- Claude return capacity uses native request usage and documented model windows
  instead of forcing long sessions into a new copy through a 64K byte fallback.
  Context overrides are rechecked, and genuinely full destinations still roll over.

## [0.4.1] - 2026-10-10

### Added

- Shared background runtime for desktop, TUI and headless CLI hosts, with private
  same-user IPC, one observer/scheduler, coalesced filesystem notifications and
  cached source freshness. Runtime health, settings and per-user login startup
  can be administered from the CLI without opening a graphical app.
- Detect, review and install a missing desktop app from the CLI using verified
  release manifests. Existing apps retain their updater path, with interrupted
  publication/rollback and concurrent-install protection.
- Experimental recipient-encrypted internet delivery for approved machines,
  including push/pull, repository-scoped receiving/sharing, offline queues,
  durable receipts, recovery and undo. Repeated returns, multi-hop journeys and
  independent forks retain the same causal lineage across SSH and relay routes.
- Separate experimental relay and signed-download services with scoped device
  authorization, admission/storage quotas, logical expiry, asynchronous ciphertext
  cleanup, bounded diagnostics and operator pause controls.

- Link a logical cloud task to a verified saved handoff in Settings or the CLI.
  Original ancestry and briefing context survive later checkpoints and agent
  changes; explicit cloud forks keep independent branches and trip counts.
  Identity associations appear in journey history without counting a transfer.

- Paired native machines publish approved inventory changes through the shared
  encrypted relay connection. Idle renewals replace repeated remote scans;
  source timestamps, expiry, pause state and current approvals remain explicit.

- Review and install cloud startup files from Settings or the CLI. Claude's
  cloud-only hook prepares fresh keys quietly; Codex setup includes reviewed
  repository guidance and optional environment Start skill instructions with an
  explicit unqualified-startup status. Missing task identity remains unsupported.
  Existing settings and unrelated repository instructions are preserved, modified files are
  refused, and stale previews cannot overwrite concurrent edits.

- Cloud session invitations in CLI and Settings: admit one fresh provider/session
  incarnation, check provisional claim status and revoke its delivery lease.
  Independent fingerprint approval still controls sharing; rebuilds and forks
  use fresh invitations. Retry preserves the original scoped credential.

- Optional relay browser approval: desktop uses an external browser with S256 PKCE
  and a temporary loopback callback; headless machines use `hopsesh relay login`
  with a short-lived code. Approval grants delivery only, separate from local
  conversation sharing and receiving. Private credentials stay in local state.
- Passive `hopsesh runtime observe` snapshots and `--watch` subscriptions do not
  initialize accounts, probe login, adopt imports or apply movement marks.
- Explicit logical cloud task continuity across approved resumes and rebuilds.
  Fresh keys and owner-signed generations supersede old connector access while
  verified native prefixes preserve checkpoint lineage. Checkpoint forks remain
  separate, and private change notifications shut down superseded connectors.

- Saved session metadata appears immediately in GUI, Quick access, TUI and
  `hopsesh ls --cached`, with progressive discovery, independent source retries,
  private summary caching and reconciled local file notifications.
- Session account labels and grouping across GUI, TUI and CLI. Account groups
  include retained source copies after a move and keep same-email profiles separate.

### Changed

- Configuration uses schema 5. Schema 4 files from 0.4 are refused rather than
  migrated. The app can preserve the incompatible file as a backup and start
  fresh; accounts, machines and approvals must be configured again. Resetting
  configuration does not remove native conversation files.

### Fixed

- Progressive default-account registration replaces the original local session
  row instead of temporarily showing it twice. TUI selection follows that same
  native file through partial scans, completed scans and runtime updates;
  explicit accounts and ambiguous bindings remain separate.

- Keep an explicitly inspected family copy visible through account enrichment
  and refresh, without duplicating it in account groups. A completed transfer
  releases source inspection so the destination family is visible on return.

- Relay recovery no longer inherits the slow healthy-idle polling interval after
  a transient failure. Consecutive failures retain bounded jitter and server
  retry delays; HTTP status errors identify the failed operation safely.

- Cloud connectors stop when their task association changes or their state
  directories move, preserving supersession checks without polling. Watchers
  subscribe before reading task metadata so startup cannot miss a scope change.

- Cloud task lookup rejects a copied or corrupted current-session pointer that
  refers to another provider, session or workspace, instead of returning that
  task's identity. Lookup remains read-only and preserves independent forks.

- Quick access keeps a local session selectable and previewable when first-time
  account discovery registers its default account. Explicit account selections
  remain exact; uncertain or ambiguous matches are refused, and hidden previews
  stay hidden. Browsing saved conversations does not require an available agent
  sign-in check.
- Queued Quick routes and existing selections survive default-account registration
  in the main window, while explicit account keys and different native files stay
  separate. Turning off Quick previews also suppresses reads already in progress.

- Accounts refreshes from shared runtime notifications instead of polling every
  two seconds. Sign-in checks sleep until a freshness deadline and stop off
  screen; background changes preserve active filters and account dialogs.

- Relay status preserves initialization failures and identifies a pending startup
  step instead of replacing every failure with a generic stopped-listener message.

- Repository probes no longer wait for the full inactivity timeout when a fast
  folder finishes before its watchdog installs its signal handler. Canceling a
  local probe also terminates its child processes, avoiding stray background work.
- Push refreshes cached or incomplete source selections before opening a peer
  connection, preventing stale-source failures and crashes after cached discovery.
- Relay HTTP recovery retries eligible exact submissions and acknowledgments on
  failed reused connections, without replaying policy refusals or fresh failures.

- Failed transfers preserve the source machine's scan error and recovery hint
  instead of reporting only that a session was not found. SFTP startup obeys
  cancellation and deadlines; closing a session stops and reaps its SSH process
  even when the server ignores end-of-input.

- Trusting an SSH machine with an explicit host-key alias and a nonstandard
  port records the name OpenSSH actually checks. Host-key collection failures
  also retain the fallback client's error instead of showing only a server banner.
  A stalled keyscan leaves time for the fallback instead of consuming the whole
  trust request deadline.

- Internet delivery status updates when notifications connect or disconnect,
  including while incoming messages are continuously arriving.

- Active internet requests wake the shared HTTP fallback receiver to collect
  replies promptly. Completed, canceled and expired requests return to idle
  reconciliation; healthy WebSocket delivery keeps using notifications.

- Concurrent transfers from separate machines preserve both source lineage
  receipts, even when their local journal IDs match. Recovery of the same journal
  cannot take over a receipt lock held by another active recovery process.

- Moving an original after creating a fork preserves the fork's ancestry and
  return destinations. Retrying the first transfer keeps its original receipt
  instead of reporting a conflicting causal history.

- Background GUI and TUI scans keep independent settings snapshots. Cloud consent
  and environment edits cannot alter an in-flight scan or crash catalog hashing;
  copied settings retain their revision for conflict-safe saves.

- Cloud admission preserves the signed absolute lease across relay objects;
  network latency cannot extend a connector's authority.
- Progressive startup preserves pressed controls. Shared observation watches
  Claude's process registry, retries concurrent account registration, and avoids
  creating an unused browsing database merely to invalidate it.

- Background inventory notifications no longer replay an already displayed scan
  or interrupt splitter dragging, keyboard resizing and form editing. Public
  pairing fingerprints wrap within compact enrollment dialogs.
- Cloud observations accept canonical Windows workspace paths when inspected
  from another operating system; remote metadata never grants local file access.

- Relay receiving starts independently of the first local inventory scan, so a
  slow scan no longer delays connections after a runtime restart.
- Reading large private-state registries on Windows avoids repeatedly propagating
  unchanged directory permissions. Every read still verifies ownership and the
  exact protected access list before accepting the file.

- Installing a missing macOS app cannot replace another app or filesystem entry
  that appears while the signed download is being verified. Publication refuses
  replacement atomically; existing installations keep their updater workflow.

- After explicitly forking rewritten cloud history, later checkpoints continue
  the accepted branch. Source acknowledgment recovers after interruption; undo
  restores the previous task ledger without changing cloud vendor files.

- Claude/Codex sign-in detection, Codex initialization ordering, and account
  refresh when the vendor sign-in terminal exits. Remote discovery retains public
  owner-machine identity metadata and explains SSH/Keychain check limitations.
- Background refreshes preserve selected conversations, focus, menus and scroll;
  early summaries retain provisional repository metadata during enrichment.
  Large session lists render bounded rows while preserving keyboard navigation.
- Scanning one machine does not rescan every peer. Remote reconciliation runs
  independently of local updates; Tailscale CLI failures are visible and peers
  without MagicDNS remain discoverable.

### Security

- Build with Go 1.27.2, which fixes the standard-library vulnerabilities reported
  against the previous pinned toolchain.

## [0.4.0] - 2026-10-08

- Imported conversations now end with a clearly attributed Hopsesh import notice, rather
  than a fabricated agent promise to start working. GUI, CLI and TUI explain when the
  destination still needs your first message; opening the desktop app only shows the chat.

hopsesh now works with the coding agents' clouds: it hands a session to Claude Code on the
web, Codex cloud, the GitHub Copilot cloud agent, Jules, Devin or Amp, brings their sessions
home, and hands one cloud's session on to another, always through the vendors' own
command-line tools, signed in as you. The desktop app gets a terminal of its own, the
hopsesh Terminal window, and sessions you open elsewhere go to a tab of iTerm2 or your other
terminal app. Codex cloud, Copilot, Jules, Devin and Amp support is experimental: Copilot,
Jules, Devin and Amp have been tested against stand-ins of their tools only, and Codex cloud
works only with older Codex cloud environments, because the `codex` command can't see
environments made in today's Codex cloud. The configuration format changed: after upgrading,
start fresh and add your machines again.

### Added

- **Terminal panels in the main window.** Choose Separate window, Bottom panel or
  Right panel in Settings → Terminal. Resize, maximize, hide, dock and detach the
  terminal while preserving its programs, screen, scrollback and input state.
  A failed move keeps the original terminal view usable.
- **Conversation families.** Group sessions and terminal tabs by verified forks and
  transfers in the GUI, TUI and CLI. Name and collapse families, distinguish branches
  from copies, and associate existing shells without changing conversation lineage.
  Native Codex forks require inherited-history evidence; unverified sessions stay separate.

- Choose System, Light or Dark in Settings → General → Appearance. The saved choice
  updates Hopsesh, Quick access and open terminal tabs immediately; System follows
  live OS changes. macOS native window chrome follows the same choice.

- Durable movement notices and branch-aware return destinations. Transfers report
  preparation until new agent work is observed; forks keep the original available.
  Notices can be disabled without losing lineage or returns. Receipts now require
  `lineage/5` and peers protocol 5; older versions are refused without migration.
  See [movement and return](docs/movement-return.md).


- **Menu bar and system tray Quick access.** Choose the supported Dock/taskbar/tray
  placement in Settings → Desktop, independently of login startup and window-close
  behavior. Search sessions, preview conversations, open exact session details and focus
  existing terminal tabs from the compact window. Linux detects tray support and keeps
  normal app access when the host is unavailable. See [Desktop presence](docs/desktop-presence.md).
- **Cloud sessions.** `hopsesh clouds` lists the clouds hopsesh can reach: Claude Code cloud
  (`claude-cloud`), Codex cloud (`codex-cloud`), the GitHub Copilot cloud agent
  (`copilot-cloud`), Jules (`jules`), Devin (`devin`) and Amp (`amp`). Each is off until you
  allow it (`hopsesh clouds allow <cloud>`, `clouds deny` to turn it off again), and
  `hopsesh clouds test` checks its login and the commands hopsesh uses, read-only. hopsesh
  reaches a cloud only through the vendor's own tool (`claude`, `codex`, `gh`, `jules`,
  `devin`, `amp`), run on this machine and signed in as you: it never reads a login or calls
  a vendor's servers, and the cloud sessions it starts run on your plan. `hopsesh ls --cloud`
  (or `hopsesh ls <cloud>:`) lists cloud sessions, with the local sessions Claude Code's
  Remote Control mirrors.
- **Hand a session off to a cloud:** `hopsesh handoff <session> --to <cloud>` (`hopsesh plan
  <session> --to <cloud>` shows the plan first) starts a cloud session that continues one on
  this machine or on another of yours. No cloud takes a conversation, so the cloud agent
  gets a briefing of about 2,000 tokens as its first prompt (likely secrets masked; you can
  read and edit it) and the code on a branch. A clean branch already on GitHub goes as it
  is; otherwise hopsesh pushes a `hopsesh/handoff/<date>-<id>` branch with a snapshot of the
  unpushed commits and changed files, leaving your checkout, index and branch as they were.
  Untracked files go only when you name them (`--untracked`); credential-like files (`.env*`,
  SSH keys, `*.pem`, `*.key`, …), Git LFS files and files over 50 MB never go.
  `--history-file` also commits the conversation as `.hopsesh/handoff.md`, and `--bundle`
  lets Claude Code upload a repository that isn't on GitHub. The session here is marked, and
  `hopsesh undo` deletes the branch (while the cloud hasn't pushed to it) and the mark; the
  cloud session stays in the vendor's list for you to archive there.
- **Claude Code cloud, both ways.** Claude Code starts a cloud session, and copies one home,
  only in a terminal you can answer, so hopsesh runs both steps where you see them: in your
  terminal from the command line, in the terminal UI (which hands over its terminal and
  comes back), or in the app's hopsesh Terminal window (or your own terminal app). A
  hand-off runs `claude --cloud "<briefing>"` in a hand-off folder hopsesh keeps for each
  repository and reads the session's link Claude Code prints, or asks you to paste it. The
  first time, Claude Code asks whether you trust that folder: you answer it, once per
  repository, and hopsesh never answers it or changes Claude Code's settings to skip it.
  `hopsesh pull claude-cloud:<id>` (or the session's link) makes a new worktree of its
  repository and runs `claude --teleport` there (`--run`, or the command to paste). Claude
  Code saves its copy only after you send a message in it: send one (even "ok") and
  hopsesh picks the copy up, checks it against the briefing when hopsesh
  started that cloud session (otherwise it says how many messages came), keeps the cloud's
  `claude/…` branch as `hopsesh/from/claude-cloud/…` and records it all for undo. `--in
  codex` continues it in Codex, and `--code-only` brings the branch alone. An agent running
  hopsesh has no terminal to answer in, so the plan says you have to run these steps
  yourself.
- **Codex cloud, both ways (experimental).** It works with older Codex cloud environments
  only: the `codex` command can't see environments made in today's Codex cloud, and hopsesh
  says so. `hopsesh handoff <session> --to codex-cloud --env
  <environment>` starts a task with the briefing and the code on a branch, or with a few
  changes as a starting diff on a branch already pushed (`--starting-diff`); `--attempts`
  asks for several attempts. The plan suggests the environments your recent tasks used, and
  hopsesh remembers your pick for the repository (`hopsesh clouds env codex-cloud`).
  `hopsesh pull codex-cloud:<id>` brings a task back: its diff committed on
  `hopsesh/from/codex-cloud/<id>` in a new worktree, and its title and outcome written as a
  new Codex session there (`--in claude` writes a Claude Code session). Only the first
  attempt comes back. hopsesh needs your `codex` signed in with ChatGPT and never reads its
  login.
- **Copilot, Jules, Devin and Amp (experimental).** Hand a session off with `--to
  copilot-cloud`, `jules`, `devin` or `amp`, list their sessions, and bring their code into a
  new worktree: Copilot's and Devin's pull request branch, or Jules's patch committed on
  `hopsesh/from/jules/<id>`. Copilot's session log and Amp's thread come home as text,
  written as a new session of the agent the session was handed off from (else Claude Code;
  `--in` picks another). Jules's and Devin's messages and an Amp orb's code stay in their
  clouds. Jules, Devin and Amp can't be told which branch to start from, so the briefing
  asks the cloud agent to check the handoff branch out first, and the plan says so. Most of
  these tools' output is undocumented: hopsesh reads it defensively and has been tested
  against stand-ins of them only, not against the real services.
- **Cloud to cloud:** `hopsesh handoff <cloud>:<id> --to <other cloud>` brings a cloud
  session here first, as `hopsesh pull` does, keeps that copy, and hands it off from here. A
  Claude Code cloud session goes on to Codex cloud from its own `claude/…` branch as it is;
  a Codex cloud task's diff is committed here and pushed on a handoff branch. The plan shows
  both legs and what the trip loses, and one `hopsesh undo` takes both back. `hopsesh clouds
  continue <hop>` goes on with a hop whose first leg ran in another terminal.
- **Add a cloud's work to the original session:** `hopsesh pull <cloud>:<id> --append` adds
  what came back to the session it was handed off from, when that session is as you left
  it. Its own turns stay byte for byte, and undo cuts the addition off again.
- **Branch clean-up:** `hopsesh clouds cleanup` lists the handoff branches hopsesh pushed and
  the clouds' own branches it brought home (`claude/…`, `copilot/…`), asks each remote,
  read-only, whether their work is in its default branch (by history, or a merged pull
  request where `gh` is installed), and deletes the merged ones only when you confirm, each
  only while it is where hopsesh saw it. Undo pushes them back. `delete_branch = "on-undo"`
  or `"never"` under `[clouds.<name>]` keeps them.
- **Clouds in the app:** a **Clouds** group in the sidebar; **Move → Hand off to ‹cloud›…**
  on every session, local or in a cloud (a cloud hopsesh can't use is listed with the reason);
  the hand-off sheet with the editable briefing, the branch, what stays on this machine and
  the options; the steps as they run, and a done screen with the link and Undo; **Bring to
  this Mac…** and **Get the code…** on cloud sessions; **Paste a cloud link** and **Find in Claude
  Code**; a card per cloud under **Machines** with Turn on, Test, the Codex cloud environment
  per repository and **Sign in**, which runs `claude auth login`, `codex login
  --device-auth` or `gh auth login --web` in a tab that records nothing; and **Activity →
  Look for merged branches**. In the terminal UI: clouds in the header and as rows, `enter`
  brings one here, `c` hands a session off, `e` picks the Codex cloud environment and `S`
  the starting diff.
- The skill handles "hand this off to Claude Code cloud" (or to any of the other clouds): the
  agent shows the plan and asks first. For Claude Code cloud it gives you the command to run
  in your own terminal. With `--add-rules`, `clouds` and `clouds test` run without asking,
  while `handoff`, `clouds cleanup` and `clouds continue` always ask.
- **The hopsesh Terminal window:** the app has a terminal of its own, a window with tabs.
  **Resume in hopsesh Terminal** runs a session in a tab, and so do Claude Code's hand-off
  and bring-back steps, cloud sign-ins and **Open a shell in its folder**. hopsesh starts the command and you do all
  the typing: it never writes into a program or answers a question for you. Each tab shows
  whether its program runs, waits for you or has exited (with its code), and has **Open in
  my terminal**, which ends the tab and runs the same command in your terminal app after
  asking. Banners say when Claude Code asks whether it trusts the hand-off folder (you
  answer it in the tab) and, when bringing a session back, to send one message, then that
  the copy is saved; the done screen says when the tab ended before Claude Code saved a copy.
  Copy, find and clear, links that open only after you confirm the whole address,
  Shift+Return for a new line in Claude Code, a screen reader mode, and colours that follow
  the app's light or dark look. Ctrl+` moves between the terminal and your sessions. It
  uses xterm.js 6.0.0 (MIT), shipped inside the app.
- Sessions running in a tab show **In a tab** and **Waiting for you** and count in **Needs
  you**; the sidebar and the title bar show the terminal and how many tabs wait. A session
  runs in one tab at most: Resume shows its tab instead. On macOS a tab you can't see that
  waits for you raises a notification (at most one every 10 seconds) and a Dock badge; on
  Windows the terminal's taskbar button flashes. Quitting hopsesh while programs run in its
  tabs asks first and lists them, and closing the window hides it while they run (**Keep
  tabs when the window closes**).
- **Settings → Terminal:** where sessions, hand-offs and bring-backs open (**In this
  window**, the default, or **In my terminal**; a session's Resume menu picks another place,
  remembered for that agent), your terminal app, the
  font, its size, scrollback (5,000 lines by default), keeping tabs when the window closes,
  notifications, the screen reader mode and, on Windows, the bundled console host. The
  choices are saved under `[terminal]` in the configuration. What a tab shows is kept in
  memory only, never written to disk or a log.
- On Windows the app and its installer carry Microsoft's ConPTY (`conpty.dll` and
  `OpenConsole.exe`, MIT) in a `conpty` folder, which the tabs use instead of the older one
  built into Windows. Tabs never run batch files, so for an npm command shim (such as
  `codex.cmd`) a tab runs the program behind it directly, and so do the command line and
  the terminal UI.
- **Your terminal app:** sessions, teleports and hand-off steps that hopsesh opens outside
  its own window go to a new tab in iTerm2's front window when iTerm2 is installed (else a
  Terminal window; Windows Terminal on Windows). The tab runs hopsesh's own launcher with an
  id, never a title or a prompt. In iTerm2 the tab gets a badge with the session's title,
  agent and machine, and `user.hopsesh_title`, `user.hopsesh_agent` and
  `user.hopsesh_machine` variables for your own title or status bar; it keeps the tab open
  with the exit code shown when the agent ends.
- `hopsesh open <session>` resumes a session on this machine in your terminal app
  (`--terminal <id>` picks one, `--here` runs it in this terminal). `hopsesh terminals` lists
  the terminal apps, chooses one (`--use`) and sets where the app resumes sessions
  (`--resume`). `hopsesh pull --run --terminal <id>` starts the moved session in a new tab.
- **A running session is shown, not started twice:** **Show in ‹terminal app›** in the app
  and `hopsesh open` bring its iTerm2 or Terminal tab forward, found from the agent's own
  process and the terminal it runs on, never from what the tab shows. Resuming a session
  that already runs is refused.
- When macOS denies hopsesh control of iTerm2, the launch opens in Terminal and the app says
  so; when Terminal is denied too, you get the command to copy. hopsesh only opens tabs and
  brings them forward, from a fixed list of AppleScript lines: it never types into or reads
  a tab, and never changes iTerm2's settings or installs its Claude Code integration.
- **iTerm2's Python API, when you have turned it on yourself:** hand-off steps open in a
  split beside the session you are in, the app learns at once when a step's tab is closed
  and shows how a session you opened there ended ("exited N", or "closed" when the tab was
  closed first), and finding and focusing a session's tab go through the API. hopsesh's
  client can open, split, find and focus tabs and set its own labels; it has no request
  that types into a session or reads its screen. With the API off, or on any error, hopsesh
  uses AppleScript, and it never asks again after you said no.
- `hopsesh agents --json` includes each module's spec: its programs, data folders, the
  secrets it never opens, instruction files, features, desktop apps, and its clouds with
  their fidelity, needs and the upstream changes the drift check watches.
- Each release has a test bundle, `hopsesh-testbundle-<version>.tar.gz` (with a `.sha256`,
  in `checksums.txt` and with build provenance): the stand-in agents for six platforms, the
  agents' fixtures, the modules' specs and scrubbed payloads other projects contribute, so a
  project that ships hopsesh can test against exactly that version. See
  `testbundle/README.md`.
- For module authors (the SDK is v0 and changes with the app): cloud capabilities
  (`CloudLister`, `CloudSender`, `CloudStepReader`, `CloudFetcher`, `CloudAdopter`,
  `CloudLinker`, `CloudTester`, `CloudFollower`, `CloudArchiver`), clouds declared in
  `Spec.Clouds` (with the cloud's sign-in command and `BriefBranch` for a cloud that can't
  name its starting branch), cloud-only modules (`agent.NoLocal`), `Spec.TerminalEnv` for
  variables a module's programs get in the app's tabs, `agent.LinksIn` and `agent.LinkOn`,
  which read links on an exact host only, and the cloud conformance kit
  (`agenttest.RunCloud`, `agenttest.RunCloudWith`).

- **The sessions screen, redesigned.** The sidebar lists places: Needs you, All sessions,
  This Mac and your other machines, and the clouds that are on. The list groups by
  repository, location, agent, status or last active (or not at all), sorts within the
  groups, has comfortable and compact rows (compact on its own for 150 sessions or more, or
  when the list is narrower than 600px), and its groups collapse (⌥-click for all). The
  Filter menu (⇧⌘F: status, location, agent, repository, last active, has; is / is not)
  shows as chips, beside a text filter (⌘F) and the Display popover (⌘J, also in the View
  menu); all of it but the text is kept in `[list]` and `[list.filter]`. The sidebar and
  the details panel can be resized and hidden (dividers, the title bar's buttons, the View
  menu: ⌃⌘S and ⌥⌘I, Ctrl+B and Ctrl+I on Windows), kept in `[window]`; on macOS the title
  bar is 52px and the sidebar button sits where AppKit puts its own.
- **One set of actions:** Resume in ‹place›, Show in ‹place›, Bring to this Mac…, Send to
  ‹machine›…, Continue with ‹agent›…, Hand off to ‹cloud›…. The details panel puts them in
  one row: the main action as a split button whose menu lists the other places (the one
  you pick becomes that agent's default, `[agents.<id>] place`), Move ▾ (an action that
  can't run now stays, with its reason) and ⋯ (Rename…, which writes the title into the
  agent's own data and can be undone, reveal the file, copy the resume command or id).
- **What a session is and where it is open:** the details panel shows the end of the
  conversation (the last two exchanges, a line for each turn's tool calls, Load earlier,
  Open transcript), read on its machine and never during a scan; markdown is rendered
  locally, raw HTML stays inert and images never load (Settings → General → Show
  conversation previews turns it off). Rows and the panel show where a session is open:
  hopsesh tabs, iTerm2, Terminal, Windows Terminal, an editor, tmux, ssh or the Claude app,
  from the agents' own registries and the process table every few seconds while the
  window is in front (Apple Events only when you click Show); two processes on one session
  say "Open twice". Titles never need a model: the title given, the name the Claude app
  gives, the agent's own, the first prompt or reply, else "Untitled · folder (branch)".
- **More in the app:** setup is one line on All sessions; the list refreshes this machine
  every minute while the window is in front; in the palette, Enter shows a session in the
  list and ⌘Enter runs its action; cloud sessions carry a cloud badge; "In the cloud" leaves
  out Remote Control mirrors, which run on their machine; added machines are scanned
  straight away, with Retry when a scan fails; Move offers only the clouds that are on.
- **Account profiles:** name and tag independent Claude Code and Codex accounts (Settings →
  Accounts, `a` in the terminal UI, `hopsesh accounts`, one store), find their folders here
  and on allowed machines, and sign in with the vendor's own command. A transfer plan names
  its destination profile, and plans refuse edited profiles or a login that changed. A
  return across profiles becomes a portable copy and leaves the original as it was; if both
  copies changed, hopsesh offers a separate branch and never merges. See
  [Accounts](docs/accounts.md).
- **Codex sessions open in the Codex app** with the exact thread selected (the Resume menu,
  `hopsesh open <session> --app`); an account in a custom folder opens in a terminal,
  because the app's links can't select it.
- Module SDK: `Previewer` (`preview`) and `Renamer` (`rename`), `PreviewText` and
  `BuildPreview`; `LiveInfo.Procs` lists every process that has a session open, and
  `LiveInfo.Name` the name the running agent gives it.
- A cloud hand-off's tab runs in the background and comes forward when Claude Code asks
  something or the step fails. Tabs whose program ended well close on their own (Settings →
  Terminal → Close a tab when its program ends).

### Changed

- **Upgrading from 0.3: add your machines again.** The configuration format is now schema 4,
  with `[clouds.<name>]` and `[terminal]`. A configuration file from 0.3 is refused, never
  converted: the app offers to set it aside and start fresh (the old file stays next to the
  new one as `config.toml.old-<date>`), and the command line names the file to move aside.
  Then add and allow your machines again. Your sessions are not affected. A configuration
  file written by a newer hopsesh is reported as newer: update hopsesh.
- Lineage manifests (the `.hopsesh.json` file beside each session hopsesh moved) have a new
  format (`lineage/4`) that records cloud copies, account profiles and forks. Manifests written by 0.3 are not read: a session
  moved with 0.3 is treated as if hopsesh had not moved it before. Update hopsesh on every
  machine where you run it, so they read each other's manifests.
- The peer protocol is now 4: a push between hopsesh 0.3 and 0.4 stops at hello and names
  the machine to update to 0.4.0 or later.
- The app now resumes sessions in its hopsesh Terminal window by default. To open them in
  your terminal app as before, choose **In my terminal** in Settings → Terminal, or pick
  the place from a session's Resume menu. The command line still runs sessions in your terminal.
- The macOS app's Automation permission text names your terminal app (iTerm2 or Terminal)
  and says hopsesh only opens tabs and brings a session's tab forward.
- The macOS app's self-update opens the new disk image with `diskutil image attach` on
  macOS 27, which deprecates `hdiutil attach`, and with `hdiutil` on earlier systems.

### Fixed

- Keep bounded transfers readable: quote original conversation records and compaction
  summaries, preserve message formatting, fence tool output and retain the current
  user request separately. Very small context allowances block conversion instead of
  silently dropping the request; full portable archives remain available.
- Use the same detected-agent names under local and remote machine sidebar rows,
  including installed agents with no sessions; keep connection failures explicit.
- Wait for native window readiness before changing desktop placement or focusing
  early reopen and Quick access requests, preventing a Windows WebView2 startup crash.

- Preserve transfer-review scroll position when options change, explain unavailable
  account emails without repeating profile labels, match the close-behavior explanation
  to the selected setting, and label missing journey origins instead of showing `/`.

- Make window-close behavior default to keeping Hopsesh running in the background,
  independently of menu-bar or tray placement on macOS, Windows and Linux. Preserve
  explicit Quit preferences and reopen the hidden window when launching Hopsesh again.
- Show selectable instruction files and text previews in transfer review, distinguish
  account profiles from verified identity, explain live-source snapshots and deferred
  moved labels, and place a remembered launch choice beside Continue. Single-account
  destinations display their identity without a redundant Default selector.

- Use the main window’s agent icons in Quick access. Put this machine first in
  Accounts and give remote setup notices the same collapsible machine grouping.

- Explain remote account setup prerequisites on the Accounts screen instead of
  silently omitting connected machines without a Hopsesh identity. Replace the
  sun-shaped Settings icon with a recognizable gear.

- Show pending actions, screen loading, retryable read failures, and terminal
  connection status throughout the GUI and Quick access. Keep previous sessions
  visible during refresh, lock pending saves, prevent repeated session launches,
  ignore obsolete navigation/plan responses, and stop reporting failed copies as
  successful. Add delayed/failing-response browser scenarios to the CI GUI matrix.

- Show a startup screen immediately while the bridge and first scan load. Report
  module/initialization failures with a reload action; keep shortcuts from operating
  on an uninitialized inventory and offer recovery when startup is unusually slow.

- Open the selected Claude Code session in Desktop instead of only activating the
  app. Use the supported CLI for saved sessions, with a temporary PTY and visible
  errors; focus a currently owned Desktop session only on a verified app build.
  Gate unsupported versions/platforms/profiles. Replace the tray arrows with the
  official Hopsesh arch and dots, rendered as a macOS template image.

- Avoid false divergence on portable returns when the first account observation rotates
  a profile binding. Verify the original's native history while keeping native append
  restrictions and independent-work conflict checks intact.

- Prevent oversized working contexts in Claude/Codex conversions, vendor imports, cloud
  returns and repeated round trips. Count final briefings and existing active context;
  preserve portable text in journaled archives with bounded retrieval. Full destinations
  can roll over to a new native session on the same logical branch. Recovery is available
  in the GUI, TUI and CLI; originals remain available. See [context safety](docs/context-safety.md).
- Keep Codex sessions with large first messages visible. Report imported history and
  preparation status accurately; enforce edited cloud briefing limits and record cloud
  return conversion losses.


- Windows UI validation waits for the actual main window and uses the remote-session
  continuation label. Screenshot failures now fail CI and retain their own reports.
- The install script (`curl … | sh`) failed on macOS in a terminal with a UTF-8 locale
  ("arch…: unbound variable"): macOS's /bin/sh read the "…" after `$arch` as part of the
  name.
- With hopsesh's settings or state in other folders (`HOPSESH_CONFIG_DIR`,
  `HOPSESH_STATE_DIR`), a session or step opened in iTerm2 or Terminal couldn't find its
  launch: the terminal app doesn't pass on hopsesh's environment. hopsesh now passes the
  folders to the tab itself.
- A Claude Code session resumed in a terminal that a Claude Code session had started
  (directly or through the terminal app) inherited its CLAUDE_CODE_CHILD_SESSION marker,
  and Claude Code then saved nothing of it. hopsesh now removes Claude Code's session
  markers before it resumes a session.
- Reading a Windows machine with many sessions could stall for 30 seconds and then fail:
  the long script hopsesh sent there over standard input sometimes never arrived. Such a
  script is now uploaded over SFTP, run from the file and removed.
- One session folder that git could not read quickly, such as a folder in iCloud Drive or
  OneDrive whose files are not downloaded, made every scan wait for it, and with it
  `hopsesh ls`, `plan` and `pull`, sometimes for minutes. Each git call hopsesh makes to read
  a folder now gets 20 seconds to answer (30 seconds on another machine over SSH), on macOS,
  Linux and Windows; a folder that is slow but answers is still read in full. A folder where
  git does not answer in time is reported as such, not as one without a repository: moving
  that session stops and says why, and the other sessions are not affected. `pull`, `plan`,
  `push` and `show` now ask git only about the folders of the session they name.
- Two operations started in the same millisecond could share an undo journal, so one's undo
  record overwrote the other's.

## [0.3.1] - 2026-10-04

Sessions from Windows machines now come over with their repository, the installers are
friendlier, and a move stops instead of guessing when the other machine's checkout cannot
be read.

### Changed
- The macOS disk image opens on a window: drag hopsesh to Applications.
- The Windows installer has a welcome page that says what it will do, a progress page, and
  a finish page that opens hopsesh and links to the getting-started guide. The licence page
  is gone; the LICENSE file is still installed.

### Fixed
- A Windows machine with more than a few dozen sessions lost its git details: the
  script that reads them, passed on the command line, outgrew cmd.exe's 8191-character
  limit. Long PowerShell scripts now go through standard input, and sessions from that
  machine come over with their repository again.
- When hopsesh cannot read the session's git checkout on the other machine (a dropped
  connection, say), it now tries once more and then stops and says so. Before, it took
  the folder for one without a repository: where the same path existed here (the same
  home folder on both machines), the session arrived without its branch or unpushed work.
- A Codex session that went to another machine and came back no longer keeps its "moved"
  mark in Codex's own list: the mark is the thread's name in Codex's shared index, which
  outlived the copy it marked.
- Unpushed commits and never-pushed worktree branches now come along from Windows machines
  too, through a git bundle (git over ssh does not work against Windows' OpenSSH, which
  runs commands through cmd.exe); before, hopsesh asked you to push them first.
- Moving or continuing a session from a Windows machine now rewrites its paths when its
  folder is a git repository: git reports `C:/Users/…`, the agents record `C:\Users\…`,
  and the two never matched, so the copy kept the other machine's paths.
- A session that ran in an agent worktree whose branch was never pushed now comes over
  with its branch, fetched straight from the other machine like unpushed commits on the
  checked-out branch (before, hopsesh stopped with "branch … exists neither locally nor on
  origin").

## [0.3.0] - 2026-10-03

hopsesh now works with more than one coding agent: Claude Code and Codex, each a module
behind a small SDK, and continues a session in the other agent. The desktop app comes to
Windows, and both apps update themselves. The configuration format changed; an older file
is refused and set aside (the app offers this), and machines are added again.

### Added
- The desktop app on Windows (amd64 and arm64): a per-user installer (no administrator
  rights) with a Start menu entry, the hopsesh command on your PATH and an uninstaller.
  Passwords can be remembered in Windows Credential Manager, sessions open in Windows
  Terminal (or PowerShell), and the window uses Ctrl shortcuts. Not code-signed yet, so
  Windows SmartScreen asks before the first run.
- Both apps update themselves: Settings → Updates → Install and restart, after the same
  checks as `hopsesh update` (release signature and checksum; on macOS also the same
  Apple developer and notarization). `hopsesh update` run from inside an app updates
  the whole app.
- Codex sessions: listing, live state, moving between machines (paths rewritten,
  encrypted content untouched), marks in Codex's own list, and Codex lists what hopsesh
  installs right away.
- Continuing a session in another agent, on another machine or the same one:
  `pull --in codex` / `--in claude`. The conversation arrives as text history (oldest steps
  summarised when long) or only a briefing (`--fidelity note`), with exact shell calls
  replayed natively where the target can (`--native`), or through Codex's own importer
  (`--via import`). Every plan shows a loss report and the briefing the other agent gets;
  `--note-file` adds a handoff note and `--carry-rules` your instructions for every project.
- Round trips across agents: going back adds only the new work to the original session,
  whose earlier turns (and their signed reasoning) stay byte for byte. Continuing on
  another machine also keeps the source agent's own copy there for that purpose.
- Lineage beside each session (`<session>.hopsesh.json`) that travels with it, so any
  machine knows a session's copies and hops.
- `hopsesh push <session> <machine>` sends a session to another machine's hopsesh, which
  receives it only after `hopsesh receive on` there; one `undo` reverses both sides.
- Codex: hopsesh tells whether Codex is working or idle, can quit an idle Codex session
  open here (`--stop-local`), and knows which account a machine's Codex is signed in to
  (from Codex itself; `auth.json` is never read). Moving a Codex session to another
  account drops the encrypted reasoning and compaction that only the old one can use.
- Sending to and from Windows machines with `push`.
- Windows: paths a session names by their 8.3 short form (`C:\Users\LIHUA~1\…`) move
  with their folder; hopsesh starts on a machine whose user name is in Japanese or Chinese
  through its short path; machines with Git for Windows on their PATH are recognised as
  Windows.
- The app can send a session on this machine to another one ("Send to…").
- `hopsesh agents` lists the supported agents and what each can do; `ls --agent` filters.
- The skill is written to every installed agent (Claude Code and Codex) as identical
  files, with per-copy drift detection and approval rules for each agent.
- A redesigned app: scopes (Needs you, each machine, each agent), sessions with their
  state and copies, a details pane with the session's history, the plan as a sheet with
  a summary and one-click fixes, a result page with the loss report, an Activity page
  with undo, a Machines page (receiving, login, host keys, discovery), tabbed settings,
  a ⌘K palette and keyboard control.
- Agents are pictured by their installed desktop app's icon (read on this machine; turn it
  off in Settings), else a mark the module draws, else their initials. Modules declare both
  in `Spec.Icon`.
- `[agents.<id>] import = true` sends continuations into that agent through its own
  importer by default.
- `HOPSESH_MACHINE` sets the name this machine has in marks and lineage.

### Changed
- `--desktop` is now `--app` (open in the agent's desktop app, where it has one).
- Undoing a mark or a line in an agent's shared index removes only hopsesh's own bytes,
  so lines written afterwards stay.
- `undo` refuses when a session it would change was used since (that work would be
  lost), including a session that came back from another agent and was continued; `undo
  --force`, or "Undo anyway" in the app, does it anyway.

### Removed
- The optional remote helper (`hosts helper`, `hopsesh agent`) and `hopsesh import`.
- The `live_policy` and top-level `remote_control` settings (remote control is now per
  agent).

### Fixed
- On Windows, `hopsesh trust` (and adding a machine in the app) works with servers whose
  host keys Windows' `ssh-keyscan` cannot read, such as Ubuntu 24.04's OpenSSH: the key is
  then read through one ssh connection and still shown for confirmation.
- Continuing a session in another agent on the same machine uses the session's own
  checkout wherever it is, instead of asking to clone it into the repos folder.
- A session is recognised as already being in a folder reached through a symlink (such as
  macOS's `/var`), instead of being moved onto itself.
- The terminal UI no longer carries out a plan you pressed `y` on while a changed one was
  still being worked out.

[0.5.0]: https://github.com/roeehrl/hopsesh/releases/tag/v0.5.0
[0.4.5]: https://github.com/roeehrl/hopsesh/releases/tag/v0.4.5
[0.4.4]: https://github.com/roeehrl/hopsesh/releases/tag/v0.4.4
[0.4.3]: https://github.com/roeehrl/hopsesh/releases/tag/v0.4.3
[0.4.2]: https://github.com/roeehrl/hopsesh/releases/tag/v0.4.2
[0.4.1]: https://github.com/roeehrl/hopsesh/releases/tag/v0.4.1
[0.4.0]: https://github.com/roeehrl/hopsesh/releases/tag/v0.4.0
[0.3.1]: https://github.com/roeehrl/hopsesh/releases/tag/v0.3.1
[0.3.0]: https://github.com/roeehrl/hopsesh/releases/tag/v0.3.0
