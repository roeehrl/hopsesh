// Package remote holds hopsesh's remote-behaviour scenarios: hopsesh here working with
// another machine over real SSH as it is, with no hopsesh, with hopsesh set up but nobody
// logged in, with hopsesh in the login session, with an older hopsesh, and unreachable.
// The other machine is a throwaway user of the same machine (scripts/remote-scenarios.sh
// sets it up); the scenarios run only when HOPSESH_REMOTE_SCENARIOS=1, so `go test ./...`
// stays hermetic. This file has the helpers, which are tested without SSH.
package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Machine is a machine of `hopsesh ls --json` (the fields the scenarios check).
type Machine struct {
	Name        string `json:"name"`
	Local       bool   `json:"local"`
	Status      string `json:"status"`
	Error       string `json:"error"`
	Hint        string `json:"hint"`
	Hopsesh     string `json:"hopsesh"`
	Presence    string `json:"presence"`
	PresenceWhy string `json:"presenceWhy"`
}

// FindMachine returns the machine named name in `hopsesh ls --json` output.
func FindMachine(ls []byte, name string) (Machine, error) {
	var doc struct {
		Machines []Machine `json:"machines"`
	}
	if err := json.Unmarshal(ls, &doc); err != nil {
		return Machine{}, fmt.Errorf("ls --json: %w", err)
	}
	for _, m := range doc.Machines {
		if m.Name == name {
			return m, nil
		}
	}
	return Machine{}, fmt.Errorf("ls --json lists no machine %s", name)
}

// Check is one line of `hopsesh doctor --json`.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// FindCheck returns the doctor check named name.
func FindCheck(doctor []byte, name string) (Check, error) {
	var cs []Check
	if err := json.Unmarshal(doctor, &cs); err != nil {
		return Check{}, fmt.Errorf("doctor --json: %w", err)
	}
	for _, c := range cs {
		if c.Name == name {
			return c, nil
		}
	}
	return Check{}, fmt.Errorf("doctor --json has no check %q", name)
}

// Lookup follows a dotted path ("plan.unprotected") through a JSON document; ok is false
// when a step is missing.
func Lookup(doc []byte, path string) (any, bool) {
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return nil, false
	}
	for _, k := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[k]; !ok {
			return nil, false
		}
	}
	return v, true
}

// KeptEndpointPath is where hopsesh here keeps the identity of a machine without hopsesh
// (internal/app/endpoint_keeper.go): one file per configured machine name.
func KeptEndpointPath(stateDir, machine string) string {
	sum := sha256.Sum256([]byte(machine))
	return filepath.Join(stateDir, "machines", hex.EncodeToString(sum[:8]), "endpoint-id")
}

var endpointID = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidEndpoint reports whether s is a persistent endpoint identity (32 bytes in hex).
func ValidEndpoint(s string) bool { return endpointID.MatchString(s) }

// ClaudeSlug is the folder Claude Code keeps a working folder's transcripts in.
func ClaudeSlug(dir string) string {
	return regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(dir, "-")
}

// ClaudeSession is a small Claude Code transcript (the fixtures' version) for id in cwd.
func ClaudeSession(id, cwd, title string) []byte {
	lines := []string{
		fmt.Sprintf(`{"type":"user","uuid":"u1","parentUuid":null,"sessionId":%q,"cwd":%q,"version":"2.1.284","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"fix the build in %s/main.go"}}`, id, cwd, cwd),
		fmt.Sprintf(`{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":%q,"cwd":%q,"timestamp":"2026-10-01T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"Edited %s/main.go"}]}}`, id, cwd, cwd),
		fmt.Sprintf(`{"type":"custom-title","customTitle":%q,"sessionId":%q}`, title, id),
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// ShQuote quotes s for a POSIX shell.
func ShQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Crashed reports whether a command's output shows a Go panic or a fatal runtime error.
func Crashed(out string) bool {
	return strings.Contains(out, "panic:") || strings.Contains(out, "fatal error:") || strings.Contains(out, "goroutine 1 [")
}
