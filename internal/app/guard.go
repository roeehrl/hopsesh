package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Protecting the original after a move. In block mode the notice hook refuses new
// prompts in the copy left behind, so the moved copy stays the one source of truth and
// moving back stays a clean return; in advise mode it only tells the agent and the
// user. Moving back clears both (the departure is gone). The user can remove the block
// from one original; that release is kept per movement, so a later move blocks again.

// Guard modes (config "original").
const (
	GuardBlock  = config.OriginalBlock
	GuardAdvise = config.OriginalAdvise
	GuardOff    = config.OriginalOff
)

// GuardBlocks reports whether a movement status keeps the original blocked: the work
// was prepared or continued elsewhere, or both copies already differ. A separate fork
// never blocks: the original is meant to go on.
func GuardBlocks(status string) bool {
	return status == "prepared" || status == "continued" || status == "diverged"
}

// Release is the user's choice to continue one original despite its movement.
type Release struct {
	Operation string    `json:"operation"`
	At        time.Time `json:"at"`
}

var releaseMu sync.Mutex

func (a *App) releasesPath() string { return filepath.Join(a.StateDir, "original-releases.json") }

func (a *App) readReleases() map[string]Release {
	out := map[string]Release{}
	b, err := os.ReadFile(a.releasesPath())
	if err == nil && len(b) <= 4<<20 {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// Released reports whether the user removed the block for this movement of key.
func (a *App) Released(key agent.SessionKey, operation string) bool {
	if operation == "" {
		return false
	}
	r, ok := a.readReleases()[key.String()]
	return ok && r.Operation == operation
}

// ReleaseOriginal removes the block (or advice) from one original for its current
// movement. Continuing there afterwards makes the copies diverge: moving back then
// needs a comparison or a separate fork instead of a clean return.
func (a *App) ReleaseOriginal(key agent.SessionKey, operation string) error {
	if operation == "" {
		return errors.New("this session has no movement to release")
	}
	return a.editReleases(func(m map[string]Release) { m[key.String()] = Release{Operation: operation, At: time.Now().UTC()} })
}

// RestoreBlock undoes ReleaseOriginal.
func (a *App) RestoreBlock(key agent.SessionKey) error {
	return a.editReleases(func(m map[string]Release) { delete(m, key.String()) })
}

func (a *App) editReleases(edit func(map[string]Release)) error {
	releaseMu.Lock()
	defer releaseMu.Unlock()
	m := a.readReleases()
	edit(m)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(a.StateDir, 0o700); err != nil {
		return err
	}
	tmp := a.releasesPath() + ".tmp"
	if err = os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.releasesPath())
}

// OriginalGuard is how the copy left behind is protected for this movement: block,
// advise or off (released originals are advised once, never blocked).
func (a *App) OriginalGuard(key agent.SessionKey, operation, status string) string {
	mode := a.Cfg.OriginalGuard()
	if mode == GuardOff {
		return GuardOff
	}
	if mode == GuardBlock && GuardBlocks(status) && !a.Released(key, operation) {
		return GuardBlock
	}
	return GuardAdvise
}

// BlockReason is what the agent shows when it refuses a prompt in a blocked original.
func BlockReason(notice string, key agent.SessionKey) string {
	return "Blocked by hopsesh. " + notice +
		" Continue in the moved copy, or move it back here to unblock this one. To continue here anyway, remove the block in hopsesh" +
		" (select this session, then Remove block…) or run: hopsesh unblock " + string(key.Session) +
		". Continuing here means moving back later needs a comparison instead of a clean return."
}

// Arrival is how this copy came to be where it is: moved or continued here from another
// place, brought back from a cloud, handed off, or moved back (returned) to a copy that
// had left. It is the moved copy's side of a Departure.
type Arrival struct {
	Operation string    `json:"operation"`
	Kind      string    `json:"kind"` // moved | continued | fetched | handoff | returned | continuation
	Fork      bool      `json:"fork,omitempty"`
	Machine   string    `json:"machine"`
	Agent     agent.ID  `json:"agent"`
	AgentName string    `json:"agentName"`
	At        time.Time `json:"at"`
}

func (a *App) arrival(g *lineage.Manifest, id lineage.ReplicaID) *Arrival {
	hop, ok := g.LastHopTo(id)
	if !ok {
		return nil
	}
	for _, c := range g.Compensations {
		if c.Operation == hop.ID {
			return nil
		}
	}
	from := g.Replica(hop.From)
	ar := &Arrival{Operation: hop.ID, Kind: "moved", Fork: hop.Fork, Machine: from.Location, Agent: from.Key.Agent, AgentName: a.agentName(from.Key.Agent), At: hop.Time}
	if _, cl, ok := a.cloudModule(from.Location); ok {
		ar.Machine = cl.Title
	}
	switch hop.Kind {
	case lineage.HopContinue:
		ar.Kind = "continued"
	case lineage.HopFetch:
		ar.Kind = "fetched"
	case lineage.HopHandoff:
		ar.Kind = "handoff"
	}
	if hop.Rollover != nil {
		ar.Kind = "continuation" // a bounded continuation: the original is kept and not blocked
		return ar
	}
	// Moved back: this copy had left earlier on the same line.
	for _, h := range g.OrderedHops() {
		if h.ID != hop.ID && h.From == id && h.Line == hop.Line && !h.Backup && h.Time.Before(hop.Time) {
			ar.Kind = "returned"
			break
		}
	}
	return ar
}

// RoleInfo is what a copy is in its session's journey, in the words and glyphs the
// command line and terminal UI share with the app (always a word with the glyph).
type RoleInfo struct {
	Kind  string `json:"kind"`  // blocked | moved-out | diverged | forked-out | moved | returned | fork
	Glyph string `json:"glyph"` // ◆ ◇ ! ⑂ ● ↩
	Word  string `json:"word"`  // Moved out, Moved copy, Returned, …
	Line  string `json:"line"`  // where it went or came from, and the protection
}

// Role describes entry e, or nil for a copy that never moved.
func (a *App) Role(e Entry) *RoleInfo {
	place := func(agentName string, agentID agent.ID, machine, cloud string) string {
		if cloud != "" {
			return cloud
		}
		if agentName == "" {
			agentName = string(agentID)
		}
		return agentName + " on " + machine
	}
	if n := e.Departure; n != nil {
		to := place(n.AgentName, n.Agent, n.Machine, n.Cloud)
		if n.Status == "forked" {
			return &RoleInfo{Kind: "forked-out", Glyph: "⑂", Word: "Fork made", Line: "a separate fork continues in " + to}
		}
		mode := a.OriginalGuard(e.Session.Key, n.Operation, n.Status)
		released := a.Cfg.OriginalGuard() != GuardOff && a.Released(e.Session.Key, n.Operation)
		switch {
		case n.Status == "diverged":
			return &RoleInfo{Kind: "diverged", Glyph: "!", Word: "Diverged", Line: "continued here after moving to " + to + "; moving back needs a comparison"}
		case released:
			return &RoleInfo{Kind: "diverged", Glyph: "!", Word: "Unblocked", Line: "moved to " + to + "; block removed, continuing here diverges"}
		case mode == GuardBlock:
			return &RoleInfo{Kind: "blocked", Glyph: "◆", Word: "Moved out", Line: "moved to " + to + "; blocked until you move back"}
		case mode == GuardAdvise:
			return &RoleInfo{Kind: "moved-out", Glyph: "◇", Word: "Moved out", Line: "moved to " + to + "; warns before you continue here"}
		}
		return &RoleInfo{Kind: "moved-out", Glyph: "◇", Word: "Moved out", Line: "moved to " + to}
	}
	if ar := e.Arrival; ar != nil {
		from := place(ar.AgentName, ar.Agent, ar.Machine, "")
		switch {
		case ar.Kind == "returned":
			return &RoleInfo{Kind: "returned", Glyph: "↩", Word: "Returned", Line: "moved back from " + from}
		case ar.Fork:
			return &RoleInfo{Kind: "fork", Glyph: "⑂", Word: "Fork", Line: "forked from " + from}
		case ar.Kind == "continuation":
			return &RoleInfo{Kind: "continuation", Glyph: "↳", Word: "Continuation", Line: "bounded continuation of " + from + "; the original is kept"}
		}
		return &RoleInfo{Kind: "moved", Glyph: "●", Word: "Moved copy", Line: "moved from " + from + "; the active copy"}
	}
	return nil
}
