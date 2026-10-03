package agent

import "time"

// Summary describes one session, the same way for every agent.
type Summary struct {
	Key         SessionKey `json:"key"`
	Title       string     `json:"title"`
	TitleSource string     `json:"titleSource,omitempty"` // custom | generated | prompt
	// CWD is the session's project folder, as the agent resumes it.
	CWD          string    `json:"cwd"`
	LastPrompt   string    `json:"lastPrompt,omitempty"`
	LastActivity time.Time `json:"lastActive"`
	Size         int64     `json:"size"`
	GitBranch    string    `json:"gitBranch,omitempty"`
	AgentVersion string    `json:"agentVersion,omitempty"` // the agent version that last wrote it
	// WorktreeRoot is set when CWD is a worktree the agent itself manages inside a
	// repository (for example <repo>/.claude/worktrees/<name>).
	WorktreeRoot string `json:"worktreeRoot,omitempty"`
	Subagents    int    `json:"subagents,omitempty"`
	// Mark is set when the agent's own data says this copy was left behind by a move.
	Mark *Mark `json:"mark,omitempty"`
	// Path is the session's main file on its machine (the lineage manifest sits beside it).
	Path string `json:"path"`
	// Account is the module's opaque account fingerprint for the session, when the agent
	// records one (bound reasoning cannot cross accounts).
	Account string `json:"account,omitempty"`
}

// MarkKind says why a copy was left behind.
type MarkKind string

const (
	MarkMoved     MarkKind = "moved"     // the session moved to another location
	MarkContinued MarkKind = "continued" // the session continues in another agent
)

// Mark describes a copy left behind.
type Mark struct {
	Kind     MarkKind `json:"kind"`
	Location string   `json:"location,omitempty"` // where it went: a machine name (later also a cloud)
	Agent    ID       `json:"agent,omitempty"`    // the agent it continues in (MarkContinued)
	// AgentName is that agent's display name, as a mark title carries it (the core
	// resolves Agent from it).
	AgentName string `json:"agentName,omitempty"`
}

// FileRole says what a bundle file is.
type FileRole string

const (
	RoleMain FileRole = "main" // the session transcript the agent resumes
	RoleSide FileRole = "side" // anything else the session needs (subagents, tool output, history)
)

// RewriteKind says how the core rewrites paths in a file.
type RewriteKind string

const (
	RewriteJSONL RewriteKind = "jsonl" // JSON lines: string values only, with the module's policy
	RewriteText  RewriteKind = "text"  // plain text
	RewriteNone  RewriteKind = "none"  // copied byte for byte
)

// BundleFile is one file of a session, relative to a root of its install.
type BundleFile struct {
	Root     string      `json:"root"`
	Rel      string      `json:"rel"` // slash-separated
	Size     int64       `json:"size"`
	Role     FileRole    `json:"role"`
	Rewrite  RewriteKind `json:"rewrite"`
	Growable bool        `json:"growable,omitempty"` // may still be appended to while copied
}

// Bundle is every file of one session.
type Bundle struct {
	Files []BundleFile `json:"files"`
}

// Main returns the bundle's main file.
func (b Bundle) Main() (BundleFile, bool) {
	for _, f := range b.Files {
		if f.Role == RoleMain {
			return f, true
		}
	}
	return BundleFile{}, false
}

// Mapping replaces an absolute path prefix (as it appears on the source) with another.
type Mapping struct {
	From  string `json:"from"`
	To    string `json:"to"`
	ToSep string `json:"toSep,omitempty"` // also convert separators after a match ("/" or `\`)
}

// Placement is what the core decided about where a session lands on the target.
type Placement struct {
	Key      SessionKey `json:"key"` // the session's key on the target (renamed for keep-both)
	SourceID SessionID  `json:"sourceId"`
	CWD      string     `json:"cwd"`   // the working directory on the target
	Title    string     `json:"title"` // title to give the copy ("" keeps it)
	// Name is the session's own name, for agents that keep names outside the session's
	// files (they record it again after installing; "" when it has none).
	Name     string    `json:"name,omitempty"`
	Mappings []Mapping `json:"mappings"`
	Location string    `json:"location"` // where the session lands: this machine's name
	// OtherAccount: the target is signed in to another account than the source, so
	// account-bound content must be removed (the module's Sanitizer policy applies).
	OtherAccount bool `json:"otherAccount,omitempty"`
}

// PlacedFile is a bundle file with its destination.
type PlacedFile struct {
	From   BundleFile `json:"from"`
	ToRoot string     `json:"toRoot"`
	ToRel  string     `json:"toRel"`
	// Append are complete records (without newline) added after the rewrite.
	Append [][]byte `json:"-"`
}

// StagedKey names a placed file in Module.Verify's staged map.
func StagedKey(f PlacedFile) string { return f.ToRoot + "/" + f.ToRel }

// MovePlan is how a module places a bundle on the target.
type MovePlan struct {
	Files  []PlacedFile  `json:"files"`
	Policy RewritePolicy `json:"policy"`
}

// RewritePolicy is data the core's JSON rewriter applies to RewriteJSONL files.
type RewritePolicy struct {
	// Protect: string values under these object keys are never changed (signatures,
	// encrypted content, opaque blobs).
	Protect []string `json:"protect,omitempty"`
	// DropRecords removes whole records whose top-level field has one of the values.
	DropRecords []FieldMatch `json:"dropRecords,omitempty"`
	// DropElems removes array elements (only when Placement.OtherAccount, via Sanitizer).
	DropElems []ElemMatch `json:"dropElems,omitempty"`
	// Rename gives a keep-both copy a new session id ({"old","new"}; empty for none): the id
	// changes inside every rewritable string, and in the RenameKeys fields even when they
	// are protected.
	Rename     [2]string `json:"rename,omitempty"`
	RenameKeys []string  `json:"renameKeys,omitempty"`
}

// FieldMatch matches a record by a top-level string field: equal to one of Values, or
// starting with one of Prefixes.
type FieldMatch struct {
	Field    string   `json:"field"` // a dot path into the record ("payload.type")
	Values   []string `json:"values,omitempty"`
	Prefixes []string `json:"prefixes,omitempty"`
}

// ElemMatch matches elements of the array at Array (dot path) by a string field.
type ElemMatch struct {
	Array  string   `json:"array"` // "message.content"
	Field  string   `json:"field"`
	Values []string `json:"values"`
}

// ResumeOptions shape the resume command.
type ResumeOptions struct {
	Fork          bool
	RemoteControl bool
	App           bool   // open in the agent's desktop app instead of the terminal
	Name          string // a name for Remote Control / the session list
	Prompt        string // a first message, "" for none
}

// Command is a program to run, never a shell string; the core quotes it for the user's
// shell.
type Command struct {
	Argv []string `json:"argv"`
	Dir  string   `json:"dir"`
}

// Liveness says whether a session is open right now.
type Liveness string

const (
	Live    Liveness = "live"
	Ended   Liveness = "ended"
	Unknown Liveness = "unknown"
)

// LiveInfo describes a live session.
type LiveInfo struct {
	State  Liveness `json:"state"`
	PID    int      `json:"pid,omitempty"`
	Status string   `json:"status,omitempty"` // agent words: "busy", "waiting for input"
}

// Account is an opaque, hashed account identity (never a credential).
type Account struct {
	Key   string `json:"key"`             // compared for equality only
	Label string `json:"label,omitempty"` // for people: "Max plan", "Team org"
	// RemoteControl is whether this login can use the agent's own remote control.
	RemoteControl bool   `json:"remoteControl"`
	Why           string `json:"why,omitempty"` // why not
}
