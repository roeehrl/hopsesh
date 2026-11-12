package agent

import "context"

// CapChangeDir is DirChanger: a session continued in another folder on the same machine
// and account profile.
const CapChangeDir Capability = "change-dir"

// DirChanger continues a session in another working directory on the same machine and
// account profile, keeping the session itself: its id and every record the agent wrote.
// Each module does it the way its agent's own folder change does.
type DirChanger interface {
	// PlanChangeDir says what moving s to dir (an absolute, symlink-resolved folder) does.
	// It only reads through h.
	PlanChangeDir(ctx context.Context, h Host, in Install, s Summary, dir string) (DirPlan, error)
	// ChangeDir carries the plan out through h: files through h.FS (journaled), or the
	// agent's own program (DirAgent).
	ChangeDir(ctx context.Context, h Host, in Install, s Summary, p DirPlan) error
	// InSession is what to type in the open session to change its folder the agent's own
	// way, keeping the session ("/cd <dir>"), or "" when the agent cannot do that without
	// starting another session.
	InSession(in Install, dir string) string
}

// DirMethod is how a module changes a session's folder.
type DirMethod string

const (
	// DirRecord: hopsesh writes what the agent's own folder change writes (files moved,
	// records appended), journaled, so undo reverses it byte for byte.
	DirRecord DirMethod = "record"
	// DirAgent: the agent's own program records the change; undo changes the folder back.
	DirAgent DirMethod = "agent"
)

// DirPlan is what changing a session's folder does.
type DirPlan struct {
	From   string    `json:"from"` // the folder the session is in now
	To     string    `json:"to"`
	Method DirMethod `json:"method"`
	// Moves are the session's files that move (absolute paths on its machine); the first
	// is the main file when it moves.
	Moves []FileMove `json:"moves,omitempty"`
	// Main is the main file's path afterwards.
	Main     string   `json:"main"`
	Records  []string `json:"records,omitempty"` // what is added, in words
	Notes    []string `json:"notes,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Blockers []string `json:"blockers,omitempty"`
}

// FileMove is a file moved from one path to another.
type FileMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}
