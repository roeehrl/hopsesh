package inventory

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/fsys"
	"github.com/roeehrl/hopsesh/internal/core/hops"
	"github.com/roeehrl/hopsesh/internal/core/moved"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/sessions"
	"github.com/roeehrl/hopsesh/internal/core/transport"
)

// Copy is one machine's copy of a session that exists on several machines.
type Copy struct {
	Machine    string    `json:"machine"`
	Local      bool      `json:"local,omitempty"`
	LastActive time.Time `json:"lastActive"`
	MovedTo    string    `json:"movedTo,omitempty"`
	Live       bool      `json:"live,omitempty"`
	Newest     bool      `json:"newest,omitempty"`
}

// keptWorking is how much newer a copy marked "moved" must be than the newest unmarked
// copy before it counts as the newest again (someone kept working on it after the move;
// a short reply to the move notice does not count).
const keptWorking = 10 * time.Minute

// newest picks the copy to show and move: the most recently active copy that was not
// left behind by a handoff, unless a left-behind copy kept being used well after that.
func newest(cs []Copy) int {
	best, bestMoved := -1, -1
	for i, c := range cs {
		if c.MovedTo == "" {
			if best < 0 || c.LastActive.After(cs[best].LastActive) || (c.LastActive.Equal(cs[best].LastActive) && c.Live) {
				best = i
			}
		} else if bestMoved < 0 || c.LastActive.After(cs[bestMoved].LastActive) {
			bestMoved = i
		}
	}
	switch {
	case best < 0:
		return bestMoved
	case bestMoved >= 0 && cs[bestMoved].LastActive.Sub(cs[best].LastActive) > keptWorking:
		return bestMoved
	}
	return best
}

// applyPendingMarks writes the "moved" marks that waited for this machine's copy of a
// session to stop running (see package hops).
func (s *Scanner) applyPendingMarks(m *Machine) {
	if s.StateDir == "" || m.fs == nil {
		return
	}
	ap, ok := m.fs.(fsys.Appender)
	if !ok {
		return
	}
	for _, h := range hops.Pending(s.StateDir, m.Name) {
		var sess *Session
		for i := range m.Sessions {
			if m.Sessions[i].ID == h.SessionID {
				sess = &m.Sessions[i]
			}
		}
		switch {
		case sess == nil:
			_ = hops.Update(s.StateDir, h.SessionID, h.From, func(x *hops.Hop) {
				x.Mark, x.MarkError = hops.MarkFailed, "the copy is no longer on "+m.Name
			})
			continue
		case sess.MovedTo != "":
			_ = hops.Update(s.StateDir, h.SessionID, h.From, func(x *hops.Hop) { x.Mark = hops.MarkDone })
			continue
		case sess.Live != nil:
			continue // still running: try again on a later scan
		}
		// New turns after the move mean someone kept working there: do not call it moved.
		// When it was told about the move, its reply to that notice does not count.
		allowed := 0
		if h.Notified {
			allowed = 4
		}
		if n, err := sessions.TurnsSince(m.fs, sess.File, h.Time); err == nil && n > allowed {
			_ = hops.Update(s.StateDir, h.SessionID, h.From, func(x *hops.Hop) {
				x.Mark, x.MarkError = hops.MarkDiverged, fmt.Sprintf("%d message(s) were added there after the move", n)
			})
			continue
		}
		err := ap.AppendKeepTime(sess.File, moved.Record(h.SessionID, h.To, sess.Title))
		_ = hops.Update(s.StateDir, h.SessionID, h.From, func(x *hops.Hop) {
			x.Tries++
			if err == nil {
				x.Mark, x.MarkError = hops.MarkDone, ""
			} else if x.Tries >= 5 {
				x.Mark, x.MarkError = hops.MarkFailed, err.Error()
			} else {
				x.MarkError = err.Error()
			}
		})
		if err == nil {
			sess.MovedTo = h.To
		}
		s.Log.Write(audit.Entry{Action: "hop.mark", Host: m.Name, Session: h.SessionID, Detail: map[string]any{"deferred": true, "ok": err == nil}})
	}
}

// gitFetchFunc returns how to fetch from a repository on this machine over SSH (nil for
// this machine itself and for Windows machines).
func (m *Machine) gitFetchFunc() func(string) *repos.FetchSource {
	if m.Local || m.conn == nil || m.Facts == nil || m.Facts.OS == "windows" {
		return nil
	}
	conn, name := m.conn, m.Name
	return func(dir string) *repos.FetchSource {
		env := append([]string{"GIT_SSH_COMMAND=" + conn.GitSSHCommand()}, conn.GitSSHEnv(context.Background())...)
		return &repos.FetchSource{Name: name, URL: conn.GitURL(dir), Env: env}
	}
}

// pushFunc returns how to push a branch on this machine (nil when it cannot).
func (m *Machine) pushFunc() func(context.Context, string) (string, error) {
	switch {
	case m.Local:
		return func(ctx context.Context, dir string) (string, error) {
			out, err := exec.CommandContext(ctx, "sh", "-c", repos.PushScript, "hopsesh", dir).CombinedOutput()
			return pushResult(string(out), err)
		}
	case m.conn != nil && m.Facts != nil && m.Facts.OS != "windows":
		conn := m.conn
		return func(ctx context.Context, dir string) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			out, err := conn.RunSh(ctx, repos.PushScript, dir)
			var re *transport.RemoteError
			if errors.As(err, &re) {
				return pushResult(re.Stderr+string(out), err)
			}
			return pushResult(string(out), err)
		}
	}
	return nil
}

func pushResult(out string, err error) (string, error) {
	out = strings.TrimSpace(out)
	if err == nil {
		return nonEmptyStr(out, "pushed"), nil
	}
	if strings.Contains(out, "no-upstream") {
		return "", errors.New("the branch has no upstream")
	}
	if out == "" {
		out = err.Error()
	}
	return "", fmt.Errorf("git push failed: %s", firstLineOf([]byte(out)))
}

func nonEmptyStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// NewestOf picks the newest copy among (machine, session) pairs of one session, by the
// same rule as the grouped listing.
func NewestOf(ms []*Machine, ss []*Session) int {
	cs := make([]Copy, len(ss))
	for i := range ss {
		cs[i] = Copy{Machine: ms[i].Name, Local: ms[i].Local, LastActive: ss[i].LastActivity, MovedTo: ss[i].MovedTo, Live: ss[i].Live != nil}
	}
	return newest(cs)
}
