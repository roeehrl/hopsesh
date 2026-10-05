package termapp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Tickets and launch records, in hopsesh's private state folder (folders 0700, files
// 0600). A ticket is what a terminal's `hopsesh terminal-open <id>` runs: written by the
// app or the command line, read once by the verb, and removed. A launch record is what
// the verb leaves while the agent runs: the session, the tab's terminal device and the
// agent's process id, so "Show" can find the tab of a session hopsesh started (Codex's
// own files have no process id). The verb removes it when the agent ends; a record whose
// process is gone is ignored and removed by Running.

// Ticket is one launch's command, for hopsesh's own verb to run.
type Ticket struct {
	Kind  Kind     `json:"kind"`
	Argv  []string `json:"argv"`
	Dir   string   `json:"dir"`
	Env   []string `json:"env,omitempty"`
	Unset []string `json:"unset,omitempty"`
	// LoginEnv are the agent's folder variables as the app saw them (CLAUDE_CONFIG_DIR, …),
	// set for the agent only where the terminal's environment lacks them.
	LoginEnv map[string]string `json:"loginEnv,omitempty"`
	// Key is the session (agent/session) a KindSession launch resumes.
	Key    string    `json:"key,omitempty"`
	Labels Labels    `json:"labels"`
	Made   time.Time `json:"made"`
}

// Record is a running launch of a session: which tab (by terminal device) and which
// process.
type Record struct {
	Ticket  string    `json:"ticket"`
	Key     string    `json:"key"`
	TTY     string    `json:"tty,omitempty"`
	PID     int       `json:"pid"`
	Program string    `json:"program,omitempty"` // TERM_PROGRAM, for people
	Started time.Time `json:"started"`
}

// Store keeps tickets and records under a folder (<state>/terminal).
type Store struct{ Dir string }

var ticketID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// IsTicket reports whether id looks like a ticket's id.
func IsTicket(id string) bool { return ticketID.MatchString(id) }

func (s Store) path(sub, id string) string { return filepath.Join(s.Dir, sub, id+".json") }

func (s Store) write(sub, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, sub), 0o700); err != nil {
		return err
	}
	tmp := s.path(sub, id) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(sub, id))
}

// Save writes a ticket and returns its id.
func (s Store) Save(t Ticket) (string, error) {
	if !t.Kind.Valid() || len(t.Argv) == 0 || !filepath.IsAbs(t.Dir) {
		return "", errors.New("a ticket needs a kind, a command and an absolute folder")
	}
	if t.Kind != KindSession {
		t.Key = "" // only sessions are found again
	}
	if t.Made.IsZero() {
		t.Made = time.Now().UTC()
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	return id, s.write("tickets", id, t)
}

// Take reads a ticket and removes it (a ticket runs once), refusing one older than
// maxAge.
func (s Store) Take(id string, maxAge time.Duration) (Ticket, error) {
	if !IsTicket(id) {
		return Ticket{}, errors.New("not a ticket")
	}
	p := s.path("tickets", id)
	b, err := os.ReadFile(p)
	if err != nil {
		return Ticket{}, fmt.Errorf("the launch is gone (it ran already, or it was cancelled): %w", err)
	}
	_ = os.Remove(p)
	var t Ticket
	if err := json.Unmarshal(b, &t); err != nil {
		return Ticket{}, err
	}
	if time.Since(t.Made) > maxAge {
		return Ticket{}, fmt.Errorf("the launch is more than %s old; start it again", maxAge)
	}
	if !t.Kind.Valid() || len(t.Argv) == 0 || !filepath.IsAbs(t.Dir) {
		return Ticket{}, errors.New("the launch is not one hopsesh wrote")
	}
	return t, nil
}

// Drop removes a ticket that never ran (its terminal did not open).
func (s Store) Drop(id string) {
	if IsTicket(id) {
		_ = os.Remove(s.path("tickets", id))
	}
}

// Record writes a running launch's record.
func (s Store) Record(r Record) error {
	if !IsTicket(r.Ticket) || r.Key == "" || r.PID <= 0 {
		return errors.New("a record needs its ticket, its session and its process")
	}
	if r.Started.IsZero() {
		r.Started = time.Now().UTC()
	}
	return s.write("running", r.Ticket, r)
}

// Forget removes a launch's record (its agent ended).
func (s Store) Forget(ticket string) {
	if IsTicket(ticket) {
		_ = os.Remove(s.path("running", ticket))
	}
}

// Running lists the records of a session (key "": all) whose process alive says runs,
// removing the others.
func (s Store) Running(key string, alive func(pid int) bool) []Record {
	entries, err := os.ReadDir(filepath.Join(s.Dir, "running"))
	if err != nil {
		return nil
	}
	var out []Record
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !IsTicket(id) {
			continue
		}
		b, err := os.ReadFile(s.path("running", id))
		if err != nil {
			continue
		}
		var r Record
		if json.Unmarshal(b, &r) != nil || r.Ticket != id || r.PID <= 0 {
			s.Forget(id)
			continue
		}
		if !alive(r.PID) {
			s.Forget(id)
			continue
		}
		if key == "" || r.Key == key {
			out = append(out, r)
		}
	}
	return out
}
