# Hopsesh desktop presence and Quick access

Approved design · 6 October 2026 · Implementation targets 0.4.0.

See [desktop presence](desktop-presence.md) for implemented behavior and validation limits.

## Recommended direction

Add a native menu-bar/system-tray icon opening a compact **Quick access** window. Keep the full Hopsesh window for browsing, transfers, account setup, lineage review and other multi-step tasks. Use the same session records, identities, capability checks and terminal ownership rules in both views.

Offer three placement choices where supported: **Dock/taskbar only**, **menu bar/system tray only**, and **both**. Recommend Both for new installations with a working tray; preserve the current behavior for existing installations until the user changes it. Login startup remains off by default and independent of placement.

This is desktop-app background operation, not the proposed cross-machine daemon. It provides no new guarantee of remote live presence. A future daemon can improve the same presence model without changing the interface's meaning.

The accompanying interactive proposal includes macOS, Windows, KDE Wayland, and GNOME without a tray host. Its sessions and identities are fictional. Buttons simulate routing and never launch agents or stop processes.

## What the platform research establishes

| Platform | Verified behavior | Proposed product response |
| --- | --- | --- |
| macOS | Menu-bar extras support a window presentation for richer controls. Apple recommends focused popover tasks, explicit anchoring and simple dismissal. | A compact searchable session view, then one detail view. Complex work opens the main window. [Apple MenuBarExtra](https://developer.apple.com/documentation/swiftui/menubarextra), [Popover guidance](https://developer.apple.com/design/human-interface-guidelines/popovers). |
| macOS | An accessory application has no Dock icon or application menu bar; regular activation supplies normal app presence. | Regular policy for Dock/Both; accessory for menu-bar-only. Explain app-switcher effects. Never use prohibited activation for an interactive app. [Activation policy](https://developer.apple.com/documentation/appkit/nsapplication/activationpolicy-swift.enum). |
| Windows | Notification-area icons support status and access to related controls. The user controls whether an icon appears outside overflow. | A single-click status window; right-click native menu. Explain overflow instead of claiming the icon can be made permanently visible. [Notification area](https://learn.microsoft.com/en-us/windows/win32/shell/notification-area). |
| Windows | Tool windows are excluded from both the taskbar and Alt+Tab. | The popup is a tool window. Strict tray-only behavior for the main window must disclose the navigation tradeoff and be verified in a native spike. [Extended window styles](https://learn.microsoft.com/en-us/windows/win32/winmsg/extended-window-styles). |
| Linux | StatusNotifierItem defines status, activation, context menus and coordinate hints. Desktop hosts decide presentation. | Use the standard protocol; support a native menu fallback containing Open Quick access. Treat activation coordinates as hints. [StatusNotifierItem](https://specifications.freedesktop.org/status-notifier-item/latest/status-notifier-item.html). |
| Linux | GNOME provides AppIndicator/KStatusNotifierItem support through an extension; a usable tray cannot be assumed everywhere. | Detect a host. Disable unavailable modes with an explanation, keep the main window accessible, and offer Check again. Never install an extension automatically. [GNOME extension registry](https://extensions.gnome.org/extension/615/appindicator-support/). |
| Linux | X11 defines a skip-taskbar hint. That does not establish equivalent support for every Wayland compositor or toolkit. | Offer strict tray-only only after platform capability and actual shell behavior have been validated. The KDE Wayland mock depicts an unsupported configuration, not a claim that all KDE systems lack the capability. [EWMH](https://specifications.freedesktop.org/wm-spec/latest/ar01s05.html). |
| Linux sandboxing | The background portal manages background/autostart requests and background status; it is not a rich tray UI. | Use the portal when packaging requires it; keep tray support detection separate. [Background portal](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.Background.html). |

Microsoft's older notification-area UX guide supports restraint and meaningful status, but explicitly reflects Windows 7-era guidance. It is a behavioral reference, not a Windows 11 visual template. Hopsesh earns an optional tray presence through active session status and background work; it should not force background operation on existing users. [Legacy guidance](https://learn.microsoft.com/en-us/windows/win32/uxguide/winenv-notification).

## Placement and lifecycle contract

| Setting | macOS | Windows | Linux |
| --- | --- | --- | --- |
| App only | Dock only; no status icon | Normal taskbar window; no tray icon | Normal app window/launcher; no tray icon |
| Tray only | Menu bar; no Dock entry | Tray; main window's taskbar entry hidden, subject to verified native implementation | Available only with working tray and supported window-hiding behavior |
| Both | Dock and menu bar | Normal taskbar and tray | Normal app/launcher and tray when supported |

“Taskbar only” means the app uses normal taskbar behavior while its windows exist; it does not create or remove user-pinned shortcuts. The same principle applies to Dock favorites and Linux launcher favorites. Hopsesh never edits the user's pinned applications.

- **Close Quick access:** dismiss only. Never quit, close terminals or cancel transfers.
- **Close the main window:** configurable independently. With tray presence, default to keeping Hopsesh in the tray. macOS Dock-only preserves normal Mac close/reopen behavior. App-only Windows/Linux follows the existing application lifecycle, including terminal protection.
- **Quit Hopsesh:** explicit action, available from the native context menu and full app. Reuse the existing terminal-aware quit guard; show the number of owned live terminals and what will stop. Never imply that external terminals or remote sessions will stop.
- **Disable tray presence:** reveal/focus a recoverable full window before removing the last status icon. If a native transition fails, roll back to a visible state and explain the failure.
- **Tray host disappears:** restore accessible app presence; show the main window when necessary to avoid leaving a hidden process with no reachable interface. Watch for host restoration without repeatedly stealing focus.
- **Normal launch/relaunch:** focus the existing main window; never start a second background controller. First-run setup always shows a full window.
- **Login launch:** open quietly only if the user enabled login startup, configured tray operation, completed setup, and a tray host is usable. Otherwise show an accessible window.
- **OS shutdown/logout:** honor platform shutdown semantics, persist recoverable UI state, and avoid reopening windows or hanging on the ordinary interactive quit dialog.

Startup implementation should use supported platform mechanisms and reflect OS-denied/disabled state. Apple's ServiceManagement exposes login-item management; Linux package format may require the portal rather than direct autostart files. [SMAppService](https://developer.apple.com/documentation/servicemanagement/smappservice), [Background portal](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.Background.html).

## Quick access interaction design

**Geometry:** target 430 logical pixels wide, with height capped by the available monitor work area (approximately 560 pixels for the normal list). The native panel anchors near the clicked icon where supported, stays on the correct display, and avoids screen edges/notches. Large text can expand it within the work area. Long lists scroll internally. Settings can grow within that cap; advanced settings open the full app. The mockup expands to make all proposed settings reviewable.

**Header:** Hopsesh, Desktop presence settings, More. Underneath: “2 need you · 3 open terminal tabs.” Counts represent explicitly named things; a permission-waiting process is not reported as productive work.

**Search:** session title, project, agent, account label/identity and machine. Searching is read-only and does not open a terminal. Search results span the cached index rather than just the selected tab. Show remote freshness and scan errors independently.

**Focus:** Needs you first, then sessions running on this device. Permission-required states must come from a supported reliable signal; an agent merely printing a question must not be treated as authoritative permission state. If unavailable, show the known terminal/process state instead.

**Recent:** recent local and remote work. Remote status reads “Last seen running · 3m ago,” not “Running,” unless a trusted live provider supports that assertion. No remote control is implied by a scan result.

**Row selection:** click or Enter selects and shows details. It never launches or resumes an agent. Details show agent, machine, precise account identity plus arbitrary tags, two recent role-labeled messages, status and freshness. Use shared Markdown rendering with sanitization and safe external links; user/agent backgrounds differ while textual role labels remain visible.

**Explicit actions:**

- “Show existing terminal tab” focuses the matching tab. Multiple matching tabs open a picker scoped to that session; More → Open terminals lists all tabs.
- “Open in Codex app” appears only for an exact supported local route, installed app and compatible profile. Apply the same capability gating to Claude desktop, external terminal and shell actions.
- “Review transfer to this Mac…” opens the full transfer review, with exact source and destination selected. A remote that lacks receiving support never gets an enabled receive action.
- “Review return in Hopsesh…” opens lineage/conflict review. Do not implement fork/merge decisions in a transient popup.
- “Open session details” opens the main window, reveals and highlights the selected session. “Open Hopsesh” without a selection opens the normal session view.

Use a consistent back control at the upper left for popup detail/settings screens. Keep a single surface, without cascading popovers. Standard Escape/outside-click dismissal; toggle the icon to close. Click-through dismissal must not activate the background application accidentally. Save presentation state locally so reopening is predictable.

**Native context menu:** Open Quick access, Open Hopsesh, Open terminals, Desktop presence…, Quit Hopsesh. Do not duplicate the entire session browser in this menu. On tray hosts that primarily offer menus, Open Quick access is the reliable route to the rich panel.

**Keyboard and accessibility:** native focus order, clear focus ring, accessible names, logical list navigation and Enter-to-select; Escape-to-dismiss. Full action access without hover. Test screen readers, large text, high contrast, reduced motion and RTL. No global shortcut by default; any optional shortcut must be user-configurable and conflict-checked.

**Status icon:** macOS template image; appropriate theme/DPI assets on Windows/Linux. A restrained attention indicator appears only for actionable states. No perpetual spinning or blinking. Tooltip contains aggregate state, not emails or conversation text. Windows overflow and Linux host settings remain under user control.

**Privacy:** message previews are a separate preference. Hide them in the popup when disabled; no automatic hover previews. System notifications are independently opt-in and respect OS focus settings. Background refresh must never display an authentication prompt or raise a terminal. No credential collection, account switching, or automatic remote acceptance is introduced.

## Shared state, refresh and performance

Use a single Go-owned session/presence store for the full window and popup. Push local terminal lifecycle events immediately. Render the current cache when the icon is clicked, then coalesce a refresh; keep Refresh disabled while a refresh is running. Use the existing machine scheduler and per-machine backoff for remote scans, with their last-success/error data visible. Do not introduce a second independent remote polling loop.

Background local discovery should be event-driven where reliable; use a conservative fallback scan (proposed 60 seconds), suspend unnecessary work when the app is inactive, and pause/reconcile around system sleep. These are proposed desktop scanning settings, not claims that remote activity can be observed in real time. Measure idle CPU, memory and scan costs before settling the interval. No terminal launches or hidden SSH password prompts from refresh.

## Implementation findings and plan

Inspected checkout: `/Users/tnt/git/hopsesh-accounts`, Wails `v3.0.0-beta.27`.

The current desktop entrypoint terminates after the last window closes on macOS and has existing terminal-aware close/quit hooks. Its frontend refresh logic depends partly on focus/visibility. Adding a tray icon alone would not make its status trustworthy while the main window is hidden. These are explicit integration changes, not a new background executable.

The pinned Wails source already includes system tray attachment, click handlers, window hiding and a Windows HiddenOnTaskbar option. Its Linux positioning and focus handling vary by desktop. Current Wails documentation confirms focus-loss hiding is disabled for some focus-follows-mouse window managers. A universal “click outside always dismisses” promise is therefore inappropriate: retain Escape, icon toggle and a visible Close action in those configurations. [Wails tray documentation](https://v3.wails.io/features/menus/systray/).

Implementation sequence after approval:

1. **Native capability spike.** Verify runtime Dock-policy changes, exact Windows taskbar/Alt+Tab behavior, tray host detection/loss, popup anchoring and input focus on Linux X11/Wayland. Do not assume APIs in newer Wails documentation exist in the pinned version. Record supported capabilities and any restart requirement.
2. **Desktop presence controller.** Add typed placement, startup and close-behavior preferences; desired versus effective capability state; lifecycle transition tests. Preserve existing installations' behavior. Invalid combinations fail visibly instead of leaving inaccessible background work.
3. **Shared read model.** Move session snapshots, attention signals, terminal counts and scan freshness behind one backend controller. Keep account IDs distinct from flexible labels; reuse lineage and launch capability checks.
4. **Platform adapters.** Native icon/menu and popup window, runtime policy changes where supported, shell restart recovery, startup integration and single-instance reopen routing. Avoid a Wails upgrade unless the spike demonstrates it is needed.
5. **Frontend.** Implement the approved Quick access view using shared sanitized conversation rendering and session action components. Register the popup with the existing window security/consent gates; never grant an unrestricted alternate route to privileged actions.
6. **Settings and handoff.** Wire atomic settings application, unsupported-mode explanations and per-action routing into the main window. Preserve selection and highlight it on arrival. Remove duplicated refresh logic and obsolete close handlers rather than maintaining parallel implementations.
7. **Validation and drift checks.** Add scenarios to existing CI matrices, plus native desktop acceptance runs. Extend upstream drift detection for the Wails tray/activation APIs and any relied-on desktop deep links. Report packaged-app verification independently from headless tests.

No daemon service, cross-machine control protocol, automatic extension installation or release version change is included in this proposal.

## Acceptance matrix

| Area | Required scenarios and evidence |
| --- | --- |
| Lifecycle unit tests | All placement/close/startup combinations; unsupported mode; failed icon creation; host loss; switching tray off while hidden; repeated toggles; shutdown/relaunch; no second controller. |
| Existing transfer/account matrices | Popup routes use exact source session/account IDs; multiple same-tag accounts; returned/forked sessions; unsupported desktop profile; cloud not configured; remote readable but not receivable; stale/offline remote status. |
| Terminal integration | Zero/one/multiple tabs; session-scoped picker versus all-tabs picker; integrated versus external terminals; ended tabs; no new terminal on selection/search; quit warning reflects owned live processes. |
| Frontend/browser CI | Focus/Recent/search/details; sanitized Markdown; role distinction; keyboard selection; filtering; disabled refresh; settings; long names; RTL; 320px and normal-width layouts; light/dark/high-contrast adaptations. |
| macOS packaged app | Supported macOS versions; regular/accessory transitions; Dock reopen; multiple displays/Spaces; notch; menu-bar hiding; VoiceOver; login startup denied/disabled; active PTY and transfer close behavior. |
| Windows packaged app | Supported Windows versions; tray overflow; Explorer restart; taskbar on different screens; fractional DPI; keyboard focus/Alt+Tab; Narrator; startup; user-pinned shortcut unaffected. |
| Linux desktop sessions | KDE X11 and Wayland; GNOME with/without AppIndicator extension; supported Xfce/Cinnamon environments; no DBus watcher; watcher restart; a focus-follows-mouse compositor; constrained packaging when distributed. Record actual capability results per configuration. |
| Efficiency and privacy | Main window hidden; idle/load/sleep/wake; bounded refresh; no background auth prompt; previews disabled; no sensitive tooltip/notification text; no network behavior from prototype. |

Headless Linux containers can validate DBus protocol handling and state transitions but cannot establish real panel placement, compositor focus, OS accessibility or packaged-app lifecycle correctness. Those need native evidence.

## Approval decisions

1. Approve **Quick access**, centered on attention, active terminals and recent work, with explicit actions after selection.
2. Approve **Both recommended for new installs**, while existing installs keep their current behavior until changed.
3. Approve **capability-dependent Linux modes**, with a visible fallback instead of pretending tray-only always works.
4. Approve **separate login-startup and window-close settings**, plus reuse of terminal-aware quit protection.

These decisions authorize the feature design. Platform spike findings that materially change these behaviors should be brought back before implementing a different experience.

## Proposal validation performed

The standalone interactive preview was browser-tested across all four variants: row selection, explicit terminal action, search, preference saving, and disabled unsupported modes. No JavaScript runtime errors were reported. The 320-pixel viewport had no horizontal document overflow; light/dark screenshots were inspected and the narrow settings layout adjusted. These checks validate the prototype only, not native tray integration or the conversation's inline renderer.
