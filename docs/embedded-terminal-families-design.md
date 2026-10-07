# Embedded terminals and conversation families

Status: approved and implemented on the 0.4.0 development branch, 7 October 2026. Merge, cross-platform CI and release publication are separate gates. The sections below retain the approved design rationale; the implementation notes describe the delivered contract.

## Implementation and validation

Settings → Terminal offers Separate window (unchanged default), Bottom panel and Right panel. Right adapts to bottom below 1400 px. Drag or keyboard-resize the divider; maximize, hide or detach without launching another program. The right workspace exposes Session details without creating four narrow columns. System/Light/Dark follows the existing appearance setting.

A capability-authenticated loopback server serves only terminal assets and two typed WebSocket streams. Embedded content uses `sandbox="allow-scripts"` with an opaque origin; app bindings reject its origin even if it claims the main window ID. The old view stays available until its replacement loads. Each PTY checkpoints the xterm screen, buffers and modes in memory behind an output barrier; the new viewer restores at the old size before fitting. A transfer never writes this checkpoint to disk. Ownership checks reject stale input/resize/close/ack frames. IME composition must finish before moving. Loading/checkpoint failure releases the original view. Quit still ends owned programs after the existing confirmation; embedding does not add a daemon.

Display → Group by → Conversation family uses the same relationship index as TUI (`g`, arrows/Enter to expand/collapse) and `hopsesh ls --group-by family`. Group names and collapse choices persist. Copies of a branch count once; title/native-ID similarity across machines or profiles does not prove ancestry. Missing parents and conflicting/cyclic ancestry remain explicit. Native-only verified forks have stable presentation IDs even before a transfer persists an endpoint. These IDs never authorize append/merge.

Terminal tabs group by Family, Session or None. Unassociated shells and sign-ins stay in Other terminals. A local session’s More actions → Organize a shell with this conversation explicitly associates an existing shell for presentation only; it can be moved back to Other terminals. Native process evidence can rebind agent tabs after an in-process fork/resume. Missing, stale or ambiguous evidence is shown as “Session association not confirmed”; an old launch command is not offered as a rerun of a different session.

Claude retrospective forks without supported native parent evidence remain separate; Hopsesh-recorded Claude forks and verified Codex native forks are supported. No similarity matching or delegated-subagent inference was added. Custom collections, arbitrary split panes, following selection automatically and survival after Quit remain outside 0.4.0.

Regression coverage is in `internal/app/families_test.go`, `nativefork_test.go`, `internal/core/pty/handover_test.go`, GUI host/binding tests, TUI family tests and browser terminal-workspace/list suites. The round-trip undo scenario asserts that Claude → Codex → Claude remains one family and one branch. The existing cross-OS scenario matrices run it. Native macOS/Linux CI now tests separate/bottom/right renderers; Windows uses the same placements on bundled ConPTY plus a real WebView2 same-process dock test. Browser tests cover repeated moves, alternate screen/cursor, PID preservation, high output, rejected old capabilities, IME, failed loading, responsive layout, grouping persistence and shell association. Native tray/compositor and screen-reader behavior still requires platform verification; browser success alone is not a native release sign-off.

## Recommendation

Offer **In main window** and **Separate window** for Hopsesh Terminal. Keep the current placement for existing users; recommend the main-window bottom panel when they opt in. Support a right-side placement on wide screens and a temporary maximize action. Preserve external terminal choices. A dock/undock operation moves the view of the same running process; it must never resume the conversation again or interrupt a turn.

Add **Conversation family** to session grouping, and **Family / Session / None** to terminal grouping. A family contains the original conversation and confirmed forks. A branch can have several copies across agents, accounts and machines. A terminal is a running process attached to a specific copy, not a conversation branch itself. Use the same grouping vocabulary in GUI, terminal window and TUI.

## What the research supports

These are product recommendations inferred from established designs, not evidence that one layout wins for every user.

| Primary source | Finding | Hopsesh decision |
| --- | --- | --- |
| [VS Code terminal basics](https://code.visualstudio.com/docs/terminal/basics) | Terminals can occupy a panel, editor area or new window, with named instances and split groups. | One terminal implementation with multiple hosts, explicit placement actions. |
| [VS Code terminal appearance](https://code.visualstudio.com/docs/terminal/appearance) | Terminal lists expose identity and status and adapt when there is one terminal. | Named tabs with agent, machine, account and state; avoid a second tree when only one tab is open. |
| [VS Code custom layout](https://code.visualstudio.com/docs/configure/custom-layout) | Panel position, alignment, visibility and floating windows are user choices. | Bottom first, right when space permits, maximize and detach; remember sizes. |
| [JetBrains tool-window modes](https://www.jetbrains.com/help/idea/viewing-modes.html) | Docked, floating and separate-window modes address different workspace needs. | Retain a separate window for multi-monitor use. |
| [Windows Terminal panes](https://learn.microsoft.com/en-us/windows/terminal/panes) | Pane resizing, zoom and keyboard navigation are established terminal controls. | Give focused terminals more space without changing process state. |
| [Windows Terminal overview](https://learn.microsoft.com/en-us/windows/terminal/) | Tabs can move between windows. | Make the same-process guarantee explicit in “Move to separate window.” |
| [Apple split views](https://developer.apple.com/design/human-interface-guidelines/split-views) | Adjacent resizable panes organize related content. | Use an obvious divider, preserve native titlebar clearance. |
| [GNOME adaptive layout](https://developer.gnome.org/hig/guidelines/adaptive.html) | Many narrow panes are difficult to adapt; progressive disclosure and breakpoints help. | Never squeeze sidebar + list + inspector + terminal into four narrow columns. |
| [WAI tree view](https://www.w3.org/WAI/ARIA/apg/patterns/treeview/) | Expansion, keyboard focus and selection have separate semantics. | Clicking a group expands it; selecting a session inspects it; neither launches a program. |
| [WAI tabs](https://www.w3.org/WAI/ARIA/apg/patterns/tabs/) | Tabs need labeled panels and predictable keyboard navigation. | Arrow keys within tab navigation; terminal input retains shell keys. |
| [WAI window splitter](https://www.w3.org/WAI/ARIA/apg/patterns/windowsplitter/) | Named, keyboard-operable separators expose their value. The pattern notes its example is still under review. | Label the resize handle and support arrows, Home/End and restore. Validate with native screen readers. |
| [xterm.js security](https://xtermjs.org/docs/guides/security/) | A terminal shares the risks of its scripting context; terminal output is untrusted. | Preserve the restricted renderer boundary before embedding. |
| [xterm.js flow control](https://xtermjs.org/docs/guides/flowcontrol/) | Producers can overwhelm renderers without acknowledged backpressure. | Keep bounded buffers and byte acknowledgements during hiding and rehosting. |
| [VS Code terminal persistence](https://code.visualstudio.com/docs/terminal/advanced) | Reconnecting to a process differs from relaunching one. | Dock/undock reconnects; app restart must not pretend a dead process survived. |

## Proposed GUI

### Main-window terminal

- A persistent **Terminal · 3 running · 1 needs you** control shows/hides the workspace. Hiding never stops a program. A tab waiting for input adds a badge without stealing focus.
- Bottom panel spans the content area beneath the session list and inspector, keeping the Places sidebar available. Start around 40% height; drag to resize. The whole panel can temporarily maximize, with **Restore layout** in the same position.
- The toolbar provides a family/session scope picker, **New shell**, **Layout**, **Maximize**, and **Hide terminal**. “Layout” offers Bottom, Right, Separate window. These are view operations, distinct from **End program**.
- Right placement uses the inspector's area rather than adding a fourth column. A compact selected-branch summary sits beneath the session list; full **Session details** opens as a drawer/tab in that workspace. At insufficient width the rendered layout switches to bottom; restore the saved right preference when space permits. Do not resize the PTY to zero when hidden.
- New users can choose the recommended embedded mode; existing users keep their separate window until opting in. Changing placement affects subsequent opens and can move existing Hopsesh tabs through an explicit action.
- Selecting another session does not silently replace a terminal accepting input. The workspace stays pinned to its current family; **Show selected session's terminals** changes scope explicitly. Optional “Follow session selection” can be offered, off initially.
- A terminal tab names its purpose (Claude Code, Codex, Shell, Tests) and shows its conversation branch. Its context header always names machine and account, especially when different from the selected inspector session.
- Hopsesh-controlled tabs can dock and detach. External iTerm2/Terminal/Windows Terminal instances remain **Open externally** entries with a **Show** action when supported; they cannot be reparented into Hopsesh. Existing “End here and open in…” retains its restart warning.
- **Hide terminal** and native window-close obey keep-running preferences. **End program** asks when an active agent/step would be stopped. Successful exited tabs follow existing auto-cleanup; errors remain readable. Collapsing a family never closes its tabs.

### Conversation families

In Display → Group by, add **Conversation family**. A family header shows a user-editable display name, distinct branch count, open Hopsesh terminal count and attention count. Suggested names derive from existing session titles, never an inferred parent based on title text.

Inside a family, use an original branch and indented fork rows. A row gives agent, machine, account where relevant, latest activity and terminal counts. Branch details show copies and the fork point; copies do not inflate the fork count. Flatten deep trees after a readable indentation limit, with a parent breadcrumb.

Example: Oarbank → Original (Claude on this Mac; a Codex copy on the MacBook), SEO experiment (fork), Packaging fix (fork). Moving Original Claude → Codex → Claude remains Original. Forking SEO from Original creates a second branch. Forking Packaging from SEO creates a child of SEO, with the family unchanged.

Filters apply to rows. Matching children retain ancestor labels with **1 of 3 branches shown**. Group collapse choices persist by stable family ID. An offline machine retains last-seen membership, marked with time; it never becomes “idle” just because scanning failed. If the source parent is missing, show **Parent unavailable** rather than inventing an Original.

Terminal grouping uses the same identity index: family → branch → running tabs. For small sets, use a family selector and a compact tab strip; for overflow, an expandable vertical list. Family count counts branches, terminal count counts processes. No automatic broadcast input. Unassociated shells and sign-in/setup tabs stay in **Other terminals**; explicitly associate a shell with a branch without asserting historical ancestry.

### Relationship evidence

Auto-group when an existing Hopsesh lineage manifest establishes a shared family/branch, or a module supplies a native parent reference verified in its correct machine/profile namespace. Native-only verified links may organize the read-only UI before portable-history adoption is possible; they do not authorize append/merge.

Do not auto-group by matching title, repository, Git branch, timestamps, or similar text. A subagent parent is not automatically a conversation fork. Keep separate relationship types: forked-from, delegated-from, continuation/copy. Conflicting parent references, cycles or incomplete evidence produce **Relationship needs review**, not a silent merge.

Optional manual **Organize together** creates a reversible collection, labeled **Custom group**, not a verified family and never used by round-trip planning. A future suggested relationship must show its evidence and require an explicit choice. Default grouping relies on confirmed relationships only.

## Current implementation constraints

- `internal/ui/gui/terminal.go`: `Terminals` owns one detached window; `TabMeta` associates processes with machine/session keys. `Gate` grants the main window broad bindings and restricts terminal windows. Terminal CSP currently refuses framing. Stream entry points check the terminal window identity.
- `internal/ui/gui/assets/terminal/terminal.js`: one emulator per process, input from user events, bounded stream/acknowledgement path, terminal output treated as bytes. Keep this renderer, not a second terminal implementation.
- `internal/ui/gui/assets/term.js`: main window already receives tab metadata and renders presence/attention. Extend this store with presentation state rather than another polling loop.
- `internal/core/lineage`: causal manifests already distinguish family identity, conversation branch, replicas and fork heads; `AdoptNativeFork` requires verified inherited projections. Reuse these identities. Do not create a second history database.
- Current session UI supplies a fork badge and movement journey, but lacks a complete family hierarchy and a family grouping choice. Term tabs need resolved family/branch association without letting the renderer assert lineage.
- Current remote machine scans can read sessions even without a remote Hopsesh install. Relationship availability must be capability-driven; global authoritative remote process presence is part of the separate 0.5 runtime work, not implied by this layout.

### Specific discovery gaps to resolve

The repo audit found Codex native-parent discovery and verification in `agents/codex/fork.go` / `internal/app/nativefork.go`, but no equivalent Claude native-parent summary/verifier. Claude's supported fork-launch command does not establish retrospective discovery of arbitrary forks. The [Claude session documentation](https://code.claude.com/docs/en/sessions) documents both separate-process forks and `/branch` switching the current process to the new session; it does not establish a stable persisted parent field to assume in our scanner. Start with Hopsesh-recorded Claude forks, add versioned native evidence only after fixtures prove it, and show unknown ancestry honestly.

The [Codex App Server documentation](https://learn.chatgpt.com/docs/app-server) exposes `thread/fork`, `forkedFromId` and completed-turn fork boundaries. Use this as evidence for the public relationship contract; do not equate its turn IDs with Hopsesh's on-disk ordinal handling. Pin real agent fixtures. The existing native-prefix verifier can return partial or empty proof without explicitly requiring that it reached a declared exclusive boundary. Add failing boundary tests and strengthen this verifier before relying on it for full-history ancestry.

Terminal metadata currently stays bound to the session launched into that PTY, except special cloud-adoption paths. Add evidence-backed rebinding for native `/branch`, `/fork`, `/resume` and `/clear` transitions. A vendor process/session registry or a controlled fork result can provide evidence; text printed by a terminal cannot. Account for child wrappers, PID reuse and delayed observations. While unresolved, show **Session association not confirmed**, retain the launched-session reference, and avoid claiming the original is still being worked on.

The PTY stream currently permits one viewer and replaces its predecessor, but individual streams do not automatically reconnect. The new view registry must enforce ownership on every input/resize/close action, not just attach. The family index must also avoid elevating current display grouping's bare-session-key fallback into causal proof: include endpoint, profile and installation identity even when a representative copy lacks a manifest.

## Architecture and rollout

1. **Isolation prototype first.** Prove a restricted embedded renderer on macOS WebKit, Windows WebView2 and Linux WebKitGTK. Prefer a separate scripting context with a minimal, typed broker. A same-origin iframe carrying the main-window Wails identity is insufficient. Verify subframe native-bridge access, origin/source validation and stream authorization experimentally. Retain the detached-only behavior wherever the boundary cannot be enforced.
2. **View ownership.** A backend view registry maps opaque view IDs to permitted PTY IDs and one input owner. Docking pauses user input briefly, transfers ownership atomically, restores the output cursor/emulator state, resizes only from the new owner, then restores focus. Failed transfer leaves the previous view usable. Stale views cannot type or resize. Switching layout never calls ResumeSession or spawns a process.
3. **Shared renderer.** Factor placement-specific chrome from the restricted terminal renderer. Keep validated typed actions, private sign-in behavior, link handling, byte limits, capture policies and existing ConPTY path. Preserve full-screen alternate-buffer state; replaying only a truncated byte tail may not reconstruct an interactive TUI correctly. Test a serialized emulator state or complete checkpoint/replay protocol before claiming seamless detach.
4. **Relationship index.** Expose read-only family/branch/replica DTOs with evidence and resolution state from the shared core. Join native parent references within endpoint/profile identity. Incrementally invalidate on changed transcripts/receipts. No repeatedly parsing every large transcript on a UI redraw; no automatic native-file rewrites during scans.
5. **Grouping.** Add family to saved list configuration and TUI grouping, then resolve terminal memberships from the same index. Maintain branch selection and collapsed state across scans/renames/hops. Add explicit metadata-only collection support separately if approved.
6. **Placement UI.** Implement saved embedded/separate preference, bottom/right placement, keyboard resizing, maximize, grouping and overflow. Preserve current external-terminal routing and keep-running semantics.
7. **Release gates.** Browser suites plus real native PTY tests on all three OS families must pass before enabling embedded mode. Coordinate reusable process/view ownership with 0.5 runtime work; embedding itself does not introduce a daemon or promise survival after Quit.

## Acceptance scenarios / CI matrix

| Area | Required checks |
| --- | --- |
| Placement | Detach/dock repeatedly while agent is busy, awaiting input, alternate-screen active, IME composing, printing quickly; same PID, command and PTY; no duplicate input; scrollback/cursor preserved. |
| Ownership | Old window sends late input/resize/ack after transfer; reject stale ownership. Renderer fails or transfer times out; retain/recover usable view. |
| Isolation | Malicious title/link/OSC/HTML output, forged tab/view IDs, cross-frame binding calls, unsupported message kinds, wrong source/origin; no access to app bindings/other PTYs. |
| Lifecycle | Hide, close-main with Keep, close-detached with Keep, Quit confirmation, successful cleanup, failed process retention, retry; no silent restart after app relaunch. |
| Layout | 1024×600, standard laptop, ultrawide, DPI/zoom, monitor unplug, long titles, 1/10/100 tabs, light/dark/system, keyboard-only and screen-reader navigation. |
| Fork evidence | Hopsesh fork; native Claude fork; native Codex fork; nested forks; missing parent; cycle; same title but unrelated; shared prompt only; delegated subagent; conflicting parent metadata. |
| Runtime rebinding | Native fork/resume within one process, delayed registry update, wrapper child, PID reuse, unrecognized session switch; tab association updates only from validated evidence. |
| Round trips | A→B→A; A→B→C→A; Claude→Codex→Claude; multi-account; original and fork move independently; fork point retained, family stable, round-trip counts remain branch-specific. |
| Remote | Parent first/child first scan, offline cached parent, SSH failure, moved endpoint, different profiles with reused native IDs, agent data accessible without remote Hopsesh. |
| Group UX | Search reveals matching descendant and ancestors, collapse persistence, attention visible in collapsed group, row selection never launches, grouped counts exclude duplicated replicas. |
| Energy | Idle embedded/hidden windows don't add scan timers; bounded output buffers; high-output input stays responsive; existing metadata events drive badges. |

Extend `internal/devtools/webtest/tests/terminal.spec.ts` and session grouping tests; add core lineage fixtures and `internal/e2e/scenario` cases; extend native `scripts/app-terminal-check.sh` and `scripts/windows-terminal-check.ps1`. Use the existing OS/agent/account scenario dimensions instead of a separate duplicate matrix.

## Approval choices

Recommended package: optional embedded terminal, bottom by default on opting in; right and detached alternatives; confirmed conversation-family grouping; no automatic text-similarity grouping; unchanged current placement for existing users. Prototype isolation first, then ship placement and grouping when native gates pass. Custom collections and arbitrary multi-pane split arrangements can follow without blocking the core feature.

Interactive mockups: `docs/mockups/terminal-families.html`. Bottom panel, Side workspace and Separate window are alternative placements of the same workspace, not three distinct implementations. The settings button opens proposed placement/grouping preferences; Family details shows relationship evidence. Branch selection, explicit Show terminals, tabs, grouping, hide/restore, maximize and detach controls are interactive. The Oarbank family and emails are illustrative, not a claim about the user's actual fork history. The mockup does not start programs, connect to machines or transfer sessions.
