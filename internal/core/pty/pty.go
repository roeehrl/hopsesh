// Package pty runs the programs of the app's terminal tabs: each in a pseudo-terminal of
// its own (a Unix PTY, or a Windows pseudoconsole: Microsoft's newer conpty.dll and
// OpenConsole.exe when the app carries them, else the system's), with its output kept in
// memory for the tab's window and passed to it with flow control (see Serve).
//
// The rules (docs/design.md §15, "The app's terminal"):
//   - A tab's program is started from its argument list, never typed into a shell.
//   - Its input comes only from the user: the keys, pastes and terminal answers the tab's
//     window sends over its stream (Serve). Nothing else in hopsesh writes into it; this
//     package has no exported way to.
//   - What it prints lives in memory only: the tab's scrollback (for the window to draw it
//     again) and, for a step hopsesh started (Spec.Capture), a bounded capture that only
//     the module's reader looks at once the program ended (Session.StepOutput). Neither is
//     ever logged, journaled or written to disk; both are zeroed when the tab closes.
//   - A sign-in tab (Spec.Private) keeps no capture and reads nothing of the output, not
//     even the notifications that mark a tab as waiting.
package pty

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// State is where a tab's program is.
type State string

const (
	// Running: the program runs (or hopsesh cannot tell that it waits).
	Running State = "running"
	// Waiting: the program asked for the user (a bell or a notification), or printed
	// something and went quiet since the user last typed (Reason says which).
	Waiting State = "waiting"
	// Exited: the program ended (Info.Code has how).
	Exited State = "exited"
)

// Why a tab is Waiting.
const (
	ReasonBell         = "bell"
	ReasonNotification = "notification"
	ReasonIdle         = "idle"
)

// Capture is what hopsesh keeps of a tab's output besides its scrollback.
type Capture uint8

const (
	// CaptureNone keeps nothing for hopsesh: a resumed session, a shell, a sign-in.
	CaptureNone Capture = iota
	// CaptureStep keeps the last term.MaxCapture bytes for the module's step reader (the
	// session link a claude --cloud step prints), read once the program ended.
	CaptureStep
)

// Spec is a tab to open.
type Spec struct {
	// Argv is the program and its arguments, run as they are (never through a shell).
	Argv []string
	// Dir is the folder it runs in (absolute; "" is the user's home folder).
	Dir string
	// Title is what the tab says it runs ("claude --cloud in hopsesh's hand-off folder for
	// demo"); "" is the program's name.
	Title string
	Env   Env
	// Cols and Rows are the terminal's starting size (0: 100 by 30).
	Cols, Rows int
	Capture    Capture
	// Private marks a sign-in (claude auth login, codex login, gh auth login): nothing of
	// the output is read, not even for the waiting state. It cannot capture.
	Private bool
	// SystemConsole runs the tab on the system's pseudoconsole even when the app carries
	// Microsoft's newer one (Windows; the user's choice, for a security tool that blocks
	// the bundled OpenConsole.exe). Elsewhere it is unused.
	SystemConsole bool
}

// Info is a tab as the window shows it.
type Info struct {
	PID     int       `json:"pid,omitempty"`
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Program string    `json:"program"` // the program's file name
	Dir     string    `json:"dir"`
	State   State     `json:"state"`
	Reason  string    `json:"reason,omitempty"` // when Waiting
	Code    int       `json:"code"`             // when Exited: the exit code (-1 unknown; 128+n for signal n)
	Cols    int       `json:"cols"`
	Rows    int       `json:"rows"`
	Capture bool      `json:"capture"` // hopsesh reads this tab's output once it ends (a step)
	Private bool      `json:"private"` // a sign-in: nothing of it is read
	Backend string    `json:"backend"` // "pty", "conpty (bundled)" or "conpty (system)"
	Started time.Time `json:"started"`
}

// Options set up a Manager.
type Options struct {
	// Version is hopsesh's version, for TERM_PROGRAM_VERSION.
	Version string
	// Scrollback is how many bytes of output a tab keeps for its window (0: 1 MiB).
	Scrollback int
	// IdleAfter is how long a program that printed something since the user last typed
	// must stay quiet to count as waiting (0: 3 seconds; negative: never).
	IdleAfter time.Duration
	// MaxTabs is how many tabs may be open at once (0: 16).
	MaxTabs int
	// OnChange is told every change of a tab's state, and its removal (State ""). It is
	// called without locks held, from the goroutine that saw the change, and must not
	// block.
	OnChange func(Info)
	// ConptyDir is where Windows looks for a bundled conpty.dll (with OpenConsole.exe in
	// its x64 or arm64 folder): "" is the "conpty" folder beside the program. Elsewhere
	// it is unused.
	ConptyDir string
}

// ErrClosed is returned once the Manager was closed.
var ErrClosed = errors.New("the terminal tabs are closed")

// Manager holds the open tabs.
type Manager struct {
	opts Options

	mu     sync.Mutex
	tabs   map[string]*Session
	order  []string
	closed bool
}

// NewManager is a Manager with opts (defaults filled in).
func NewManager(opts Options) *Manager {
	if opts.Scrollback <= 0 {
		opts.Scrollback = 1 << 20
	}
	if opts.IdleAfter == 0 {
		opts.IdleAfter = 3 * time.Second
	}
	if opts.MaxTabs <= 0 {
		opts.MaxTabs = 16
	}
	if opts.ConptyDir == "" {
		if exe, err := os.Executable(); err == nil {
			opts.ConptyDir = filepath.Join(filepath.Dir(exe), "conpty")
		}
	}
	return &Manager{opts: opts, tabs: map[string]*Session{}}
}

// Start opens a tab running spec's program.
func (m *Manager) Start(spec Spec) (*Session, error) {
	if len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return nil, errors.New("nothing to run")
	}
	for _, a := range spec.Argv {
		if strings.ContainsRune(a, 0) {
			return nil, errors.New("an argument holds a NUL byte")
		}
	}
	if spec.Private && spec.Capture != CaptureNone {
		return nil, errors.New("a sign-in tab cannot capture its output")
	}
	if spec.Dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		spec.Dir = home
	}
	if !filepath.IsAbs(spec.Dir) {
		return nil, fmt.Errorf("the tab's folder %q is not an absolute path", spec.Dir)
	}
	if fi, err := os.Stat(spec.Dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("the tab's folder %s is not there", spec.Dir)
	}
	cols, rows := clampSize(spec.Cols, spec.Rows)
	if spec.Cols == 0 && spec.Rows == 0 {
		cols, rows = 100, 30
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	if len(m.tabs) >= m.opts.MaxTabs {
		m.mu.Unlock()
		return nil, fmt.Errorf("%d terminal tabs are open already; close one first", len(m.tabs))
	}
	id := newID()
	s := newSession(m, id, spec, cols, rows)
	m.tabs[id] = s
	m.order = append(m.order, id)
	m.mu.Unlock()

	conpty := m.opts.ConptyDir
	if spec.SystemConsole {
		conpty = "" // no bundled pair: the system's pseudoconsole
	}
	be, err := start(spec.Argv, spec.Dir, spec.Env.Build(m.opts.Version), cols, rows, conpty)
	if err != nil {
		m.remove(id)
		return nil, fmt.Errorf("starting %s: %w", filepath.Base(spec.Argv[0]), err)
	}
	s.run(be)
	m.changed(s.Info())
	return s, nil
}

// BundledConsole reports whether tabs run on the newer pseudoconsole the app carries
// (Windows; false elsewhere).
func (m *Manager) BundledConsole() bool { return bundledConsole(m.opts.ConptyDir) }

// Get is the open tab id.
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.tabs[id]
	return s, ok
}

// List is every open tab, oldest first.
func (m *Manager) List() []Info {
	m.mu.Lock()
	ss := make([]*Session, 0, len(m.order))
	for _, id := range m.order {
		ss = append(ss, m.tabs[id])
	}
	m.mu.Unlock()
	out := make([]Info, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Info())
	}
	return out
}

// Running is how many tabs' programs have not ended (the app asks before quitting them).
func (m *Manager) Running() int {
	n := 0
	for _, i := range m.List() {
		if i.State != Exited {
			n++
		}
	}
	return n
}

// Close ends tab id's program if it still runs, waits for it (a few seconds at most),
// and forgets the tab and everything it kept.
func (m *Manager) Close(id string) error {
	s, ok := m.Get(id)
	if !ok {
		return errors.New("no such tab")
	}
	s.end()
	m.remove(id)
	return nil
}

// CloseAll closes every tab and refuses new ones (the app is quitting).
func (m *Manager) CloseAll() {
	m.mu.Lock()
	m.closed = true
	ids := slices.Clone(m.order)
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Close(id)
		}()
	}
	wg.Wait()
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	s, ok := m.tabs[id]
	delete(m.tabs, id)
	m.order = slices.DeleteFunc(m.order, func(x string) bool { return x == id })
	m.mu.Unlock()
	if ok {
		s.forget()
		m.changed(Info{ID: id})
	}
}

func (m *Manager) changed(i Info) {
	if m.opts.OnChange != nil {
		m.opts.OnChange(i)
	}
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// clampSize keeps a terminal's size within what both kinds of pseudo-terminal take.
func clampSize(cols, rows int) (int, int) {
	return min(max(cols, 2), 1000), min(max(rows, 1), 1000)
}

// StepOutput is what a CaptureStep tab's program printed, for the module's step reader
// (agent.CloudStepReader), once it ended: the capture as plain text, the terminal's width
// and the exit code. It forgets the capture; a second call, or one before the program
// ended or on a tab without capture, returns false.
func (s *Session) StepOutput() (agent.StepOutput, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.State != Exited || s.capture == nil {
		return agent.StepOutput{}, false
	}
	out := agent.StepOutput{Text: s.capture.Text(), Width: s.info.Cols, Code: s.info.Code}
	s.capture.Reset()
	s.capture = nil
	return out, true
}
