// Package iterm2api is a small, opt-in client for iTerm2's scripting API (the protocol
// iTerm2 calls its "Python API": protobuf messages over a WebSocket on a Unix socket).
//
// hopsesh uses it only when the user has turned the API on in iTerm2 themselves, and only
// for three things the AppleScript path cannot do well: learning that a tab hopsesh opened
// has closed (an event, not polling), opening a split beside the session the user is in,
// and finding and focusing a session precisely by its TTY. Every caller treats an error
// from Connect as "use the AppleScript path instead"; nothing here is required.
//
// # What the client can say
//
// The client is hand-written and speaks a deliberately tiny part of the protocol. The only
// requests it can encode are:
//
//   - list sessions (windows, tabs and session ids; no titles, no contents)
//   - focus state (which window is key)
//   - create a tab, and split a pane, running a command in a directory
//   - activate (select and bring forward) a session
//   - read a session's "tty" variable, and set "user.hopsesh_*" variables
//   - subscribe to new-session, session-terminated and focus-changed notifications
//
// It has no way to send text, inject bytes, read a screen, buffer, selection, prompt or
// screenshot, monitor keystrokes, register functions, change profiles or preferences, or
// invoke menu items: those requests are not in this package at all, and allowlist_test.go
// fails the build's tests if one is added. Notifications other than the three above are
// skipped undecoded.
//
// # Credentials
//
// iTerm2 authenticates a connection with a single-use cookie and key. The client asks for
// one through AppleScript (`request cookie and key for app named "hopsesh"`, which macOS
// gates behind one Automation consent prompt), and only after a credential-free handshake
// has shown that the API server is actually listening, so a user who has not enabled the
// API is never prompted. When iTerm2 itself launched hopsesh with ITERM2_COOKIE and
// ITERM2_KEY set, those are used once and removed from the process environment. The cookie
// and key live in memory for the handshake only: they are never logged, written to disk,
// put in an error, or passed to a child process (ScrubEnv removes them from a child's
// environment). hopsesh never enables the API, never changes an iTerm2 or Claude setting,
// and never uses iTerm2's admin bypass file.
//
// # Exit status
//
// iTerm2's protocol reports that a session terminated, not how its program exited: there is
// no exit status in the notification and no exit-status variable. The exit code of a
// hopsesh step or session comes from hopsesh's own launch record (the outcome file that
// `hopsesh terminal-step` writes), and this package supplies the "it has closed" event.
//
// # Provenance
//
// iTerm2 and its protocol definition (api.proto) are GPLv2, which hopsesh's Apache-2.0
// licence cannot absorb, so no iTerm2 file is vendored, translated or generated from here.
// The messages are written from the protocol's public facts (message and field numbers,
// header names, the socket path), the minimum needed to interoperate, and only for the
// requests listed above. See NOTICE.md in this directory.
package iterm2api
