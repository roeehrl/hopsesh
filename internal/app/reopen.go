package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A return may add to an original that is still open in its agent when hopsesh blocks
// that original: nobody can type there, so the history it holds in memory cannot grow.
// What it holds is now stale, so the original's hook keeps refusing prompts and asks for
// the session to be reopened, which loads the new work, until a fresh start or resume.

// openBlockedOriginal is the return candidate of e that is open on this machine and
// blocked by hopsesh through an installed (and, for agents that ask, trusted) hook; ""
// when there is none.
func (a *App) openBlockedOriginal(ctx context.Context, inv *Inventory, e Entry) string {
	for _, c := range e.Returns {
		if !c.Local {
			continue
		}
		for _, o := range inv.Entries {
			if o.Machine != c.Machine || o.Session.Key.String() != c.Key || o.Live.State != agent.Live || o.Departure == nil {
				continue
			}
			if a.OriginalGuard(o.Session.Key, o.Departure.Operation, o.Departure.Status) != GuardBlock {
				continue
			}
			statuses, err := a.NoticeHooksFor(ctx, o.Agent, o.Session.Key.Profile)
			if err != nil {
				continue
			}
			for _, st := range statuses {
				if st.Installed && st.Enabled && (st.Trust == nil || st.Trust.State == agent.HookTrusted) {
					return c.Key
				}
			}
		}
	}
	return ""
}

// reopenRecord says an original was written while open and must be reopened.
type reopenRecord struct {
	Key       agent.SessionKey `json:"key"`
	Operation string           `json:"operation"`
	From      string           `json:"from"` // the agent the new work came from
	At        time.Time        `json:"at"`
}

func (a *App) reopenPath(key agent.SessionKey) string {
	sum := sha256.Sum256([]byte(key.String()))
	return filepath.Join(a.StateDir, "movement-reopen", hex.EncodeToString(sum[:])+".json")
}

// noteReopen records a return that added to an open, blocked original.
func (a *App) noteReopen(p *move.Plan) {
	if p == nil || p.Continue == nil || p.Continue.AppendTo == nil || p.Options.OpenBlockedOriginal == "" || p.Options.OpenBlockedOriginal != p.Continue.AppendTo.Key.String() {
		return
	}
	rec := reopenRecord{Key: p.Placement.Key, Operation: p.OperationID, From: p.Continue.From, At: time.Now().UTC()}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	path := a.reopenPath(rec.Key)
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o600)
}

// ReopenNotice answers the original's hook while it must be reopened. A fresh start or
// resume loads the new work and clears the record; any other prompt is refused with how
// to reopen it. handled is false when there is nothing to reopen.
func (a *App) ReopenNotice(ctx context.Context, id agent.ID, profile, sessionID, event, source string) (text string, block, handled bool) {
	scope, err := a.noticeScope(ctx, id, profile)
	if err != nil {
		return "", false, false
	}
	key := agent.SessionKey{Agent: id, Profile: scope.ID, Session: agent.SessionID(sessionID)}
	path := a.reopenPath(key)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || err != nil {
		return "", false, false
	}
	var rec reopenRecord
	if json.Unmarshal(b, &rec) != nil || rec.Key != key {
		return "", false, false
	}
	name := a.agentName(id)
	if event == "SessionStart" && (source == "resume" || source == "startup") {
		_ = os.Remove(path)
		return "Hopsesh: this conversation now includes the work added from " + rec.From + ".", false, true
	}
	if event == "SessionStart" {
		return "", false, true // compaction or clear in the old window: still stale
	}
	return "Hopsesh added the work from " + rec.From + " to this conversation, and this open window does not show it. Close this conversation and reopen it in " + name +
		" to continue with everything (in a terminal: exit, then resume it). Nothing is lost.", true, true
}
