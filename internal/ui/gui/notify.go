package gui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
)

// Desktop notifications for the terminal's tabs. hopsesh writes every word of them: a
// program's output is never shown, and a tab's title (which can come from a vendor's files)
// is cleaned of control and direction characters and shortened. At most one goes out every
// 10 seconds; those that come meanwhile are told as one ("2 programs are waiting for you").
// They go through the system's notification center on macOS (notify_darwin.go). On
// Windows the terminal window's taskbar button flashes instead: Windows' notifications
// would need a COM activator registered for the app, which hopsesh does not install.

// notifier sends desktop notifications, at most one per every.
type notifier struct {
	send  func(id, title, body string)
	every time.Duration
	// still reports whether a tab still waits (a coalesced notification leaves out those
	// that stopped).
	still func(id string) bool

	mu      sync.Mutex
	last    time.Time
	pending []note
	timer   *time.Timer
}

type note struct {
	id, body string
	wait     bool // the tab waits (left out once it stopped)
}

func newNotifier(send func(id, title, body string), every time.Duration) *notifier {
	return &notifier{send: send, every: every}
}

// waiting tells the user that a tab waits for them (now, or with the others once the
// pause is over).
func (n *notifier) waiting(id, body string) { n.add(note{id, body, true}) }

// now tells the user something about a tab (it ended), within the same pace.
func (n *notifier) now(id, body string) { n.add(note{id, body, false}) }

func (n *notifier) add(x note) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if wait := n.every - time.Since(n.last); wait > 0 {
		n.pending = append(n.pending, x)
		if n.timer == nil {
			n.timer = time.AfterFunc(wait, n.flush)
		}
		return
	}
	n.last = time.Now()
	go n.send(x.id, "hopsesh", x.body)
}

// flush sends what came during the pause, as one notification.
func (n *notifier) flush() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.timer = nil
	var left []note
	for _, x := range n.pending {
		if !x.wait || n.still == nil || n.still(x.id) {
			left = append(left, x)
		}
	}
	n.pending = nil
	if len(left) == 0 {
		return
	}
	n.last = time.Now()
	body := left[0].body
	if len(left) > 1 {
		waiting := 0
		for _, x := range left {
			if x.wait {
				waiting++
			}
		}
		body = fmt.Sprintf("%d programs are waiting for you", waiting)
		if waiting < len(left) {
			body = fmt.Sprintf("%d of hopsesh's tabs need a look", len(left))
		}
	}
	go n.send(left[0].id, "hopsesh", body)
}

// cleanTitle is a tab's title as a notification may show it: no control or direction
// characters, at most max runes.
func cleanTitle(s string, max int) string {
	s = termapp.Sanitize(s, 80)
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max-1])) + "…"
	}
	return s
}

// waitingText is the notification for a tab that waits.
func waitingText(m TabMeta, title string) string {
	who := m.Agent
	if who == "" {
		who = "A program"
	}
	return fmt.Sprintf("%s is waiting for you in “%s”", who, cleanTitle(title, 40))
}

// endedText is the notification for a step's or a sign-in's end.
func endedText(m TabMeta, i pty.Info) string {
	switch m.Kind {
	case TabStep:
		if i.Code == 0 {
			return "The hand-off to " + m.CloudTitle + " finished"
		}
		return fmt.Sprintf("The hand-off to %s ended with code %d", m.CloudTitle, i.Code)
	case TabBring:
		return "Bringing the session from " + m.CloudTitle + " ended"
	}
	if i.Code == 0 {
		return cleanTitle(i.Title, 40) + " finished"
	}
	return fmt.Sprintf("%s ended with code %d", cleanTitle(i.Title, 40), i.Code)
}
