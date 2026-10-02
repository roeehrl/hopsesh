// Package hops keeps the local log of completed moves (state/hops.jsonl): which session
// went from which machine to this one, and whether the copy left behind has been marked
// as moved. A mark that could not be written yet (the old session was still running) is
// "pending" and is written by a later scan once that session has stopped.
package hops

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Mark states for the copy left behind.
const (
	MarkDone    = "done"    // the old copy carries "↪ moved to …"
	MarkPending = "pending" // write it once the old session has stopped
	MarkOff     = "off"     // not wanted (fork, or the user turned it off)
	MarkFailed  = "failed"  // gave up (see MarkError)
)

// Hop is one completed move to this machine.
type Hop struct {
	Time       time.Time `json:"time"`
	SessionID  string    `json:"sessionId"`
	Title      string    `json:"title"`
	From       string    `json:"from"` // machine name the copy came from
	To         string    `json:"to"`   // this machine's name
	Fork       bool      `json:"fork,omitempty"`
	SourceFile string    `json:"sourceFile"`
	Mark       string    `json:"mark"`
	MarkError  string    `json:"markError,omitempty"`
	Tries      int       `json:"tries,omitempty"`
}

var mu sync.Mutex

func file(stateDir string) string { return filepath.Join(stateDir, "hops.jsonl") }

// Append records a hop.
func Append(stateDir string, h Hop) error {
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(file(stateDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(h)
	_, err = f.Write(append(b, '\n'))
	return err
}

// Load returns every recorded hop, oldest first.
func Load(stateDir string) []Hop {
	mu.Lock()
	defer mu.Unlock()
	return load(stateDir)
}

func load(stateDir string) []Hop {
	b, err := os.ReadFile(file(stateDir))
	if err != nil {
		return nil
	}
	var out []Hop
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var h Hop
		if json.Unmarshal(sc.Bytes(), &h) == nil && h.SessionID != "" {
			out = append(out, h)
		}
	}
	return out
}

// Pending returns hops from a machine whose old copy still needs its mark, newest
// first per session (an older pending hop of the same session is superseded).
func Pending(stateDir, from string) []Hop {
	all := Load(stateDir)
	seen := map[string]bool{}
	var out []Hop
	for i := len(all) - 1; i >= 0; i-- {
		h := all[i]
		if seen[h.SessionID] {
			continue
		}
		seen[h.SessionID] = true
		if h.From == from && h.Mark == MarkPending {
			out = append(out, h)
		}
	}
	return out
}

// Update changes the newest hop of a session from a machine and rewrites the log
// atomically.
func Update(stateDir, sessionID, from string, change func(*Hop)) error {
	mu.Lock()
	defer mu.Unlock()
	all := load(stateDir)
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].SessionID == sessionID && all[i].From == from {
			change(&all[i])
			break
		}
	}
	var buf bytes.Buffer
	for _, h := range all {
		b, _ := json.Marshal(h)
		buf.Write(append(b, '\n'))
	}
	tmp := file(stateDir) + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file(stateDir))
}

// Last returns the newest hop of a session, if any.
func Last(stateDir, sessionID string) (Hop, bool) {
	all := Load(stateDir)
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].SessionID == sessionID {
			return all[i], true
		}
	}
	return Hop{}, false
}
