package clientapi

import (
	"encoding/json"
	"time"
)

// The types below are the API's contract: fields are only ever added. They are not
// hopsesh's own structures, which change between releases.

// CatalogParams chooses what a catalog covers.
type CatalogParams struct {
	// Local leaves the other machines and the clouds out (fast: no network).
	Local bool `json:"local,omitempty"`
	// Machines limits it to these machines ("" or none: every allowed one).
	Machines []string `json:"machines,omitempty"`
	// Cached answers from hopsesh's last catalog at once, when it has one, instead of
	// reading the machines first (rows then say cached).
	Cached bool `json:"cached,omitempty"`
}

// Catalog is the sessions and machines hopsesh knows.
type Catalog struct {
	Sessions []Session `json:"sessions"`
	Machines []Machine `json:"machines"`
	// Attention is what needs the user, from what hopsesh knows (an inbox's items).
	Attention []Attention `json:"attention"`
	// Complete is false while some machines are still being read (a watch sends the rest
	// when they are).
	Complete bool      `json:"complete"`
	At       time.Time `json:"at"`
}

// Attention kinds.
const (
	AttentionOldCopyOpen = "old-copy-open" // a copy a move left behind is open: it may be continued twice
	AttentionOldCopyUsed = "old-copy-used" // a copy a move left behind was used after the move
	AttentionMachine     = "machine"       // a machine can't be read, or hopsesh there needs something
)

// Attention is one thing that needs the user. ID stays the same while it does.
type Attention struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Machine string `json:"machine,omitempty"`
	Session string `json:"session,omitempty"` // the session's ref, when it is about one
	Title   string `json:"title"`
	Text    string `json:"text"`
	Fix     string `json:"fix,omitempty"`
}

// SessionKey names a session in its agent: unique together with the machine's endpoint.
type SessionKey struct {
	Agent   string `json:"agent"`
	Profile string `json:"profile,omitempty"` // the account folder there ("" its default)
	Session string `json:"session"`
}

// Session is one copy of a session, where it is.
type Session struct {
	// Ref names it in plan and terminal.open: "<machine>:<agent>[@profile]/<session>".
	Ref       string     `json:"ref"`
	Key       SessionKey `json:"key"`
	Machine   string     `json:"machine"`            // hopsesh's name for the machine, or the cloud
	Endpoint  string     `json:"endpoint,omitempty"` // the machine's identity ("" for a cloud, or not known yet)
	Cloud     bool       `json:"cloud,omitempty"`
	AgentName string     `json:"agentName"`
	Title     string     `json:"title"`
	Dir       string     `json:"dir,omitempty"` // its project folder there
	Branch    string     `json:"branch,omitempty"`
	Activity  time.Time  `json:"activity,omitempty"`
	Live      Live       `json:"live"`
	// Cached: from hopsesh's last catalog, not read just now. Plan reads it again.
	Cached bool `json:"cached,omitempty"`
	// Replica is this copy in the session's lineage: it follows the session across
	// moves, while Key can change.
	Replica string `json:"replica,omitempty"`
	// Hop is how this copy came to be here, when it came by a move or a continuation.
	Hop *Hop `json:"hop,omitempty"`
	// MovedTo is set on a copy a move left behind: the session goes on there. Continuing
	// this copy is refused unless the user insists.
	MovedTo *Hop `json:"movedTo,omitempty"`
	// Owner is the program running it, from its owner record (see docs/client-api.md).
	Owner *Owner `json:"owner,omitempty"`
}

// Live is whether the session is open now.
type Live struct {
	// State is "live", "ended" or "unknown".
	State  string `json:"state"`
	Status string `json:"status,omitempty"` // the agent's words: "busy", "waiting for input"
	PIDs   []int  `json:"pids,omitempty"`
	App    bool   `json:"app,omitempty"` // in the agent's desktop app
	// RemoteControl is "on", "off" or "" (the agent doesn't say); RemoteURL its page.
	RemoteControl string `json:"remoteControl,omitempty"`
	RemoteURL     string `json:"remoteUrl,omitempty"`
}

// Hop is one move or continuation in a session's lineage.
type Hop struct {
	Operation string    `json:"operation"`
	Kind      string    `json:"kind"` // moved, continued, fetched, handoff, returned, continuation
	Machine   string    `json:"machine,omitempty"`
	Agent     string    `json:"agent,omitempty"`
	At        time.Time `json:"at,omitempty"`
}

// Owner is another program that runs a session.
type Owner struct {
	Name string `json:"name"`
	PID  int    `json:"pid"`
	// Stop says how to ask it to stop the session (the record's own words).
	Stop string `json:"stop,omitempty"`
}

// Machine is a machine hopsesh reaches.
type Machine struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint,omitempty"`
	Local    bool   `json:"local,omitempty"`
	OS       string `json:"os,omitempty"`
	// Status is "ok", or why it can't be read now ("unreachable", "error", ...).
	Status  string `json:"status"`
	Problem string `json:"problem,omitempty"`
	Fix     string `json:"fix,omitempty"`
	Hopsesh string `json:"hopsesh,omitempty"` // the version there ("" when none was found)
	// Presence is how hopsesh works with it: "login" (hopsesh serve answers there),
	// "ssh" (over SSH only), or "none".
	Presence string    `json:"presence,omitempty"`
	Agents   []string  `json:"agents,omitempty"` // the agents found there
	Checked  time.Time `json:"checked,omitempty"`
}

// PlanParams asks for a plan.
type PlanParams struct {
	// Session is a session's ref, from the catalog.
	Session string `json:"session"`
	// To is the machine it goes to: "" (or this machine's name) brings it here; another
	// machine's name sends it there, which only a session on this machine can be.
	To string `json:"to,omitempty"`
	// Agent continues it in another agent ("" keeps its own).
	Agent string `json:"agent,omitempty"`
	// Dir is the folder it goes to there ("" lets hopsesh choose: the same repository,
	// found or cloned).
	Dir string `json:"dir,omitempty"`
	// Fork keeps the original usable instead of blocking it.
	Fork bool `json:"fork,omitempty"`
}

// Plan is what a move or continuation would do. Nothing has changed yet.
type Plan struct {
	ID        string    `json:"id"` // apply or cancel it by this, on this connection
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Agent     string    `json:"agent"`
	From      PlanSide  `json:"from"`
	To        PlanSide  `json:"to"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	Repo      *PlanRepo `json:"repo,omitempty"`
	Conflict  string    `json:"conflict,omitempty"`
	Warnings  []string  `json:"warnings,omitempty"`
	Blockers  []string  `json:"blockers,omitempty"`
	NoWork    bool      `json:"noWork,omitempty"` // it is there already
	Protected bool      `json:"protected"`        // the original will be blocked from going on
	Owner     *Owner    `json:"owner,omitempty"`  // another program runs the original: it must stop it first
	Live      bool      `json:"live,omitempty"`   // the original is open now
}

// PlanSide is one end of a plan.
type PlanSide struct {
	Machine string     `json:"machine"`
	Key     SessionKey `json:"key"`
	Dir     string     `json:"dir,omitempty"`
	Account string     `json:"account,omitempty"`
}

// PlanRepo is what happens to the code there.
type PlanRepo struct {
	Action string `json:"action"` // use, clone, worktree, none ...
	Path   string `json:"path,omitempty"`
	Branch string `json:"branch,omitempty"`
	Remote string `json:"remote,omitempty"`
}

// ApplyParams applies a plan made on this connection.
type ApplyParams struct {
	Plan string `json:"plan"`
	// OwnerStopped says the program in the plan's owner stopped the session (or it was
	// asked to and confirmed); without it, a plan with an owner is refused.
	OwnerStopped bool `json:"ownerStopped,omitempty"`
}

// Step is an apply.step event. Steps are numbered from 1 in the order apply takes them;
// which steps a move needs is known only as it goes. Each starts "running" and ends
// "done", or "failed" when apply fails in it.
type Step struct {
	Plan  string `json:"plan"`
	N     int    `json:"n"`
	Title string `json:"title"`
	State string `json:"state"`
}

// Receipt is what an apply did.
type Receipt struct {
	Plan string `json:"plan"`
	// Journal undoes it (MethodUndo); "" when nothing was changed.
	Journal string     `json:"journal,omitempty"`
	Machine string     `json:"machine"` // where the session is now
	Key     SessionKey `json:"key"`
	Ref     string     `json:"ref"`
	Dir     string     `json:"dir,omitempty"`
	// Command resumes it there, as the user would type it.
	Command  string   `json:"command,omitempty"`
	Files    int      `json:"files"`
	Bytes    int64    `json:"bytes"`
	Cloned   bool     `json:"cloned,omitempty"`
	Worktree string   `json:"worktree,omitempty"`
	Pushed   string   `json:"pushed,omitempty"`  // the branch pushed first
	Stopped  bool     `json:"stopped,omitempty"` // the original was stopped
	Warnings []string `json:"warnings,omitempty"`
}

// UndoParams reverses an applied plan.
type UndoParams struct {
	Journal string `json:"journal"`
	// Force undoes it even when the session was used since (that work is lost).
	Force bool `json:"force,omitempty"`
}

// Undone reports an undo.
type Undone struct {
	Journal string `json:"journal"`
	Title   string `json:"title,omitempty"`
}

// TerminalParams runs a session on this machine in a terminal hopsesh owns.
type TerminalParams struct {
	Session string `json:"session"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	// Anyway continues a copy a move left behind; the user confirmed it.
	Anyway bool `json:"anyway,omitempty"`
}

// Terminal is a terminal hopsesh runs (attach with hopsesh terminals attach <id>).
type Terminal struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Dir     string `json:"dir,omitempty"`
	State   string `json:"state"`
	Started string `json:"started,omitempty"`
}

// WatchParams starts a catalog watch.
type WatchParams struct {
	CatalogParams
}

// Unwatch stops the watch a catalog.watch request started.
type Unwatch struct {
	Watch json.RawMessage `json:"watch"`
}
