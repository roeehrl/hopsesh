// Package termapp opens hopsesh's launches in the user's own terminal app (iTerm2,
// Terminal, Windows Terminal, a Linux terminal), finds the tab a running session is in,
// and brings it forward. It never reads what a terminal shows and never types into one.
//
// Every launch runs only hopsesh itself, by an opaque ticket: `<hopsesh> terminal-open
// <id>` (or `terminal-step <id>` for a hand-off step). hopsesh's own verb then prints the
// tab's labels, records its terminal and its child's process id, and starts the agent's
// command from the ticket. So what goes to a terminal app (an AppleScript string, a
// PowerShell line) carries an absolute path and an id, never a session's title or a
// prompt.
//
// The only verb a terminal must have is Open. Finder (find a tab by its TTY), Focuser
// (bring a tab forward) and Watcher (learn that a tab's program ended) are optional, and
// found by interface assertion. Reading a terminal's output is deliberately not a
// capability: what a step printed reaches hopsesh only through the step's own relay.
package termapp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Kind is what a launch runs; it decides the labels and the bookkeeping.
type Kind string

const (
	// KindSession resumes an agent's session: labelled with its title, and recorded so
	// "Show" can find its tab.
	KindSession Kind = "session"
	// KindStep is a step a driver runs (a hand-off, a teleport): labelled, not recorded.
	KindStep Kind = "step"
	// KindSignIn is a vendor's sign-in: a fixed label, nothing recorded or read back.
	KindSignIn Kind = "sign-in"
	// KindShell is a plain shell: a fixed label, nothing recorded or read back.
	KindShell Kind = "shell"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case KindSession, KindStep, KindSignIn, KindShell:
		return true
	}
	return false
}

// Placement is where a launch opens; terminals that cannot do one open a new window.
type Placement string

const (
	// NewTab opens a tab in the terminal's front window (a new window when it has none).
	NewTab Placement = "tab"
	// NewWindow opens a new window.
	NewWindow Placement = "window"
	// Beside opens a split pane beside the session the user is in, where the terminal can
	// (iTerm2 with its Python API on); elsewhere it is NewTab.
	Beside Placement = "beside"
)

// Terminal ids.
const (
	IDITerm2          = "iterm2"
	IDTerminalApp     = "terminal-app"
	IDWindowsTerminal = "windows-terminal"
	IDLinux           = "linux"
)

// Launch is what a terminal opens: hopsesh's own program with a verb and a ticket.
type Launch struct {
	// Program is hopsesh's absolute path; Args its verb and the ticket's id (each a
	// plain word: letters, digits and dashes).
	Program string
	Args    []string
	// Dir is where the tab starts (the verb changes to the ticket's folder itself).
	Dir   string
	Kind  Kind
	Where Placement
}

// Handle names a tab (or window) a terminal opened or found. Ref is the terminal's own id
// for it (iTerm2's session id); TTY its terminal device ("/dev/ttys003"), when known.
type Handle struct {
	Terminal string `json:"terminal"`
	Ref      string `json:"ref,omitempty"`
	TTY      string `json:"tty,omitempty"`
	// Verb and Ticket are the hopsesh verb and id the tab runs ("terminal-open" and a
	// ticket, or "terminal-step" and a step), for its exit code (ExitSource).
	Verb   string `json:"verb,omitempty"`
	Ticket string `json:"ticket,omitempty"`
}

// ExitSource reads a launch's exit code from hopsesh's own records: the code its verb
// wrote when the agent or step ended (false: not ended, or not known). Terminals never
// report it (iTerm2's API says only that a tab closed, and with --hold the tab outlives
// the agent).
type ExitSource func(h Handle) (code int, ok bool)

// handleFor fills a handle's Verb and Ticket from the launch that opened it.
func handleFor(h Handle, l Launch) Handle {
	if len(l.Args) >= 2 {
		h.Verb, h.Ticket = l.Args[0], l.Args[1]
	}
	return h
}

// Terminal is a terminal app hopsesh can open launches in.
type Terminal interface {
	ID() string
	Name() string // for people: "iTerm2"
	// Available is nil when the app is installed here (ErrNotInstalled otherwise).
	Available(ctx context.Context) error
	// Open opens the launch. ErrDenied: macOS refused hopsesh the Automation permission.
	Open(ctx context.Context, l Launch) (Handle, error)
}

// Finder finds the tab whose terminal device is tty (false: none, or the app is not
// running; it is never started to look).
type Finder interface {
	FindTTY(ctx context.Context, tty string) (Handle, bool, error)
}

// Focuser brings a found tab forward.
type Focuser interface {
	Focus(ctx context.Context, h Handle) error
}

// Watcher reports a launch's exit code once it ends: the exit only, never output. The
// code comes from hopsesh's own records (ExitSource); the terminal adds that the tab was
// closed, which ends the launch with -1 when the agent left no code. Only iTerm2 with its
// Python API turned on by the user implements it; check CapsOf(t).Watch, which is false
// while the API is off.
type Watcher interface {
	Exited(ctx context.Context, h Handle) (<-chan int, error)
}

var (
	// ErrNotInstalled means the terminal app is not on this machine.
	ErrNotInstalled = errors.New("not installed")
	// ErrDenied means macOS did not let hopsesh control the app (Privacy & Security ›
	// Automation).
	ErrDenied = errors.New("macOS did not allow hopsesh to control it")
	// ErrNoTerminal means no terminal could be opened: copy the command instead.
	ErrNoTerminal = errors.New("no terminal could be opened")
	// ErrGone means the tab hopsesh found is no longer there (or no longer runs the same
	// terminal device).
	ErrGone = errors.New("the tab is no longer there")
)

// Caps is what a terminal can do besides Open.
type Caps struct {
	Find  bool `json:"find"`
	Focus bool `json:"focus"`
	Watch bool `json:"watch"`
	// Labels: the terminal shows hopsesh's labels (a badge and user variables).
	Labels bool `json:"labels"`
	// Tabs: it opens launches as tabs in its front window (else as windows).
	Tabs bool `json:"tabs"`
	// Hold: a launch's tab closes when its program ends unless hopsesh keeps it open.
	Hold bool `json:"hold"`
}

// CapsOf is what t can do.
func CapsOf(t Terminal) Caps {
	_, find := t.(Finder)
	_, focus := t.(Focuser)
	_, watch := t.(Watcher)
	c := Caps{Find: find, Focus: focus, Watch: watch}
	if d, ok := t.(interface{ caps() Caps }); ok {
		x := d.caps()
		c.Labels, c.Tabs, c.Hold = x.Labels, x.Tabs, x.Hold
		c.Watch = watch && x.Watch // a Watcher that can watch only now and then says when
	}
	return c
}

// Info describes one terminal for a picker.
type Info struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Caps      Caps   `json:"caps"`
}

// Set is the terminals this system has adapters for, best first.
type Set struct{ list []Terminal }

// System is the set for this operating system.
func System() Set {
	switch runtime.GOOS {
	case "darwin":
		return Set{list: []Terminal{ITerm2(), TerminalApp()}}
	case "windows":
		return Set{list: []Terminal{WindowsTerminal()}}
	}
	return Set{list: []Terminal{Linux()}}
}

// WithExits is the set with its terminals that watch launches reading exit codes from src.
func (s Set) WithExits(src ExitSource) Set {
	out := Set{list: make([]Terminal, len(s.list))}
	for i, t := range s.list {
		if w, ok := t.(interface{ withExits(ExitSource) Terminal }); ok {
			t = w.withExits(src)
		}
		out.list[i] = t
	}
	return out
}

// NewSet is a set of the given terminals, best first (tests).
func NewSet(ts ...Terminal) Set { return Set{list: ts} }

// All lists the set's terminals.
func (s Set) All() []Terminal { return append([]Terminal(nil), s.list...) }

// ByID is the set's terminal with this id.
func (s Set) ByID(id string) (Terminal, bool) {
	for _, t := range s.list {
		if t.ID() == id {
			return t, true
		}
	}
	return nil, false
}

// Detect lists the set's terminals and whether each is installed.
func (s Set) Detect(ctx context.Context) []Info {
	out := make([]Info, 0, len(s.list))
	for _, t := range s.list {
		out = append(out, Info{ID: t.ID(), Name: t.Name(), Installed: t.Available(ctx) == nil, Caps: CapsOf(t)})
	}
	return out
}

// Choose is the terminal for a configured id: that one when it is installed, else the
// best installed one ("" picks the best: iTerm2 before Terminal on macOS).
func (s Set) Choose(ctx context.Context, id string) Terminal {
	if t, ok := s.ByID(id); ok && t.Available(ctx) == nil {
		return t
	}
	for _, t := range s.list {
		if t.Available(ctx) == nil {
			return t
		}
	}
	if len(s.list) > 0 {
		return s.list[len(s.list)-1]
	}
	return nil
}

// Opened is how a launch was opened: in which terminal, and whether hopsesh fell back
// from the chosen one (Reason says why).
type Opened struct {
	Handle   Handle `json:"handle"`
	Terminal string `json:"terminal"` // the name of the one that opened it
	FellBack bool   `json:"fellBack,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Open opens l in t, and when t is not installed or macOS denies hopsesh control of it,
// in the set's next installed terminal (Terminal, for iTerm2). When none opens it, the
// error wraps ErrNoTerminal: the person copies the command instead.
func (s Set) Open(ctx context.Context, t Terminal, l Launch) (Opened, error) {
	if err := l.check(); err != nil {
		return Opened{}, err
	}
	var errs []string
	tried := map[string]bool{}
	try := func(t Terminal) (Opened, bool, error) {
		tried[t.ID()] = true
		if err := t.Available(ctx); err != nil {
			errs = append(errs, t.Name()+": "+err.Error())
			return Opened{}, false, nil
		}
		h, err := t.Open(ctx, l)
		if err == nil {
			return Opened{Handle: h, Terminal: t.Name()}, true, nil
		}
		errs = append(errs, t.Name()+": "+err.Error())
		if errors.Is(err, ErrDenied) || errors.Is(err, ErrNotInstalled) {
			return Opened{}, false, nil // try the next
		}
		return Opened{}, false, err
	}
	if t != nil {
		o, ok, err := try(t)
		if ok || err != nil {
			return o, err
		}
	}
	for _, next := range s.list {
		if tried[next.ID()] {
			continue
		}
		o, ok, err := try(next)
		if err != nil {
			return o, err
		}
		if ok {
			o.FellBack = t != nil
			o.Reason = strings.Join(errs, "; ")
			return o, nil
		}
	}
	return Opened{}, fmt.Errorf("%w (%s)", ErrNoTerminal, strings.Join(errs, "; "))
}

// word is what a launch's argument may be: hopsesh's verbs, flags and ticket ids.
var word = regexp.MustCompile(`^-{0,2}[a-z0-9][a-z0-9-]*$`)

// check refuses a launch that would put anything but hopsesh's absolute path and plain
// words on a terminal's command line.
func (l Launch) check() error {
	if !filepath.IsAbs(l.Program) || strings.ContainsFunc(l.Program, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return fmt.Errorf("hopsesh's program is not a plain absolute path: %q", l.Program)
	}
	if len(l.Args) == 0 {
		return errors.New("a launch needs hopsesh's verb")
	}
	for _, a := range l.Args {
		if !word.MatchString(a) {
			return fmt.Errorf("a launch's argument is not a plain word: %q", a)
		}
	}
	if !l.Kind.Valid() {
		return fmt.Errorf("unknown launch kind %q", l.Kind)
	}
	return nil
}
