// Package link connects the old and new sessions using only official Claude Code
// features: it builds the resume command (--resume, --fork-session, --remote-control,
// --desktop), the start prompt that tells the resumed session it was moved and asks it
// to check its environment (and, when asked, to notify the old session with
// SendMessage), and the text to paste into the old session otherwise. It reads
// `claude auth status` to decide whether Remote Control is available and whether both
// machines use the same account. It never writes to session sockets.
package link
