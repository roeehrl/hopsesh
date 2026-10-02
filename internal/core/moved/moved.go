// Package moved defines how hopsesh marks the copy of a session left behind after a
// handoff. The mark is an ordinary `custom-title` record (the one /rename writes), so
// Claude Code's own resume picker shows the old copy as "↪ moved to <host> · <title>",
// and hopsesh can read where the session went.
package moved

import (
	"encoding/json"
	"strings"
)

const (
	prefix = "↪ moved to "
	sep    = " · "
)

// Title is the title given to the copy left on the old machine.
func Title(host, title string) string {
	host = strings.TrimSpace(host)
	if title == "" {
		return prefix + host
	}
	return prefix + host + sep + title
}

// Parse reports whether t is a moved title, the host the session moved to, and the
// original title.
func Parse(t string) (host, title string, ok bool) {
	rest, found := strings.CutPrefix(t, prefix)
	if !found || rest == "" {
		return "", t, false
	}
	host, title, _ = strings.Cut(rest, sep)
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, " \t") {
		return "", t, false
	}
	return host, title, true
}

// IsTitle reports whether a custom title is a moved mark.
func IsTitle(t string) bool {
	_, _, ok := Parse(t)
	return ok
}

// Record is the JSONL line appended to the old copy (with its trailing newline).
func Record(sessionID, host, title string) []byte {
	b, _ := json.Marshal(struct {
		Type        string `json:"type"`
		CustomTitle string `json:"customTitle"`
		SessionID   string `json:"sessionId"`
	}{"custom-title", Title(host, title), sessionID})
	return append(b, '\n')
}
