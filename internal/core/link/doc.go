// Package link connects the old and new sessions using only official features:
// launching the new session with --remote-control, preparing the user-visible first
// prompt that asks Claude to notify the old session via SendMessage, and naming both
// sessions distinctly. It never writes to session sockets.
package link
