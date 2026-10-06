# Desktop presence (0.4.0)

Settings → Desktop presence chooses where Hopsesh lives: Dock/taskbar/app launcher,
menu bar/system tray, or both. New installations prefer both when a tray is available;
existing settings without a desktop section retain their previous behavior.

Click the status icon for Quick access. Focus shows local sessions needing attention
and running locally. Recent and search include cached remote sessions, with the time
of their last scan. Selecting a row shows details and a sanitized conversation preview;
it does not launch a terminal. Explicit buttons focus an existing tab or open a supported
agent desktop app. Full session details, including transfer review, open the main window with
the exact session selected, its group expanded and its row highlighted.

Login startup, what happens when the main window closes, the attention indicator and
Quick access message previews are separate preferences. Login startup is off unless
the user enables it. The setting reads actual OS registration state and reports errors.
Quit always uses the terminal-aware confirmation. Closing Quick access just dismisses
it. User-pinned Dock/taskbar shortcuts are never changed.

macOS menu-bar-only removes the app from the Dock and app switcher. Windows tray-only
hides the main window's taskbar and Alt+Tab entry; its terminal window remains reachable.
Windows controls whether the tray icon is in overflow. Linux requires an active
StatusNotifier host (GNOME may need its AppIndicator extension). This GTK4 integration
cannot promise hiding the taskbar entry, so strict tray-only is unavailable on Linux;
Both is offered when a tray is available. Host loss restores ordinary app access.

The Go backend owns a shared session snapshot. Local discovery runs at most about once
a minute while tray operation or a visible window needs it; presence refreshes every
5 seconds with a window visible and every 30 seconds in the background. Open terminal
state changes update immediately. Sleep and screen-lock events suspend background
refresh, and waking reconciles the cache on the next tick. Quick access's Refresh is local-only, even before
the first full scan. Other machines and clouds retain the existing explicit/foreground
scan behavior, consent, freshness and errors. Remote previews open in the full app so
a small background window never starts a password conversation. This is not a network
daemon and cannot claim that a remote session is working now based on an old scan.

Implementation: `internal/ui/desktop` owns native shell capabilities and lifecycle;
`internal/ui/gui/desktop.go` owns settings, cache and exact-session navigation. The main
and small windows use `assets/desktop-settings.js`; conversation rendering reuses the
same sanitized Markdown renderer. Terminal webviews remain isolated by their asset gate.

Validation runs in the existing Linux/macOS/Windows Go test matrix and Chromium/WebKit
browser matrix. `TestDesktopLifecycleMatrix` covers mode × close behavior × capability;
`TestQuick*` covers first local refresh, selection and privacy. The native app selfcheck
exercises supported placement transitions and navigation through the actual service.
OS shell restarts, login launch, screen readers, multi-monitor placement and real Linux
compositor behavior additionally require native acceptance testing; browser tests alone
do not prove them. See [the approved design](desktop-presence-design.md).
