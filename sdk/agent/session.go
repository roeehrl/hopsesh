package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Summary describes one session, the same way for every agent.
type Summary struct {
	ContextOverflow bool       `json:"contextOverflow,omitempty"`
	NativeParent    SessionID  `json:"nativeParent,omitempty"` // module-declared parent; core verifies before linking lineage
	Key             SessionKey `json:"key"`
	Title           string     `json:"title"`
	TitleSource     string     `json:"titleSource,omitempty"` // custom | generated | prompt | reply
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
	// LegacyLabel is set when the title carried a label an older hopsesh wrote on a copy
	// left behind (Title is then the title without it).
	LegacyLabel *LegacyLabel `json:"legacyLabel,omitempty"`
	// Path is the session's main file on its machine (the lineage manifest sits beside it).
	Path string `json:"path"`
	// Account is the module's opaque account fingerprint for the session, when the agent
	// records one (bound reasoning cannot cross accounts).
	Account string `json:"account,omitempty"`
	// Owners are the accounts the agent recorded running the session, oldest first, with
	// consecutive runs under one account merged. Empty when the agent recorded none.
	Owners []OwnerRun `json:"owners,omitempty"`
	// Mirror is the vendor's cloud copy of this session while it runs here (Claude Code's
	// Remote Control), when the agent records one: the session is still this one.
	Mirror *CloudLink `json:"mirror,omitempty"`
	// Kinds are the Spec.Kinds this session is (Codex: "voice", "work"); the user
	// interfaces leave such sessions out unless the user shows that kind.
	Kinds []string `json:"kinds,omitempty"`
}

// LabelKind is what a legacy label said happened to a copy left behind.
type LabelKind string

const (
	LabelPrepared  LabelKind = "prepared"  // destination written; no inference claimed
	LabelMoved     LabelKind = "moved"     // the session moved to another location
	LabelContinued LabelKind = "continued" // the session continues in another agent
)

// LegacyLabel is what a title label written by an older hopsesh said (StripLegacyLabel).
type LegacyLabel struct {
	Kind     LabelKind `json:"kind"`
	Location string    `json:"location,omitempty"` // where it went: a machine's or a cloud's name
	Agent    ID        `json:"agent,omitempty"`    // the agent it continues in (LabelContinued)
	// AgentName is that agent's display name, as the label carried it (the core resolves
	// Agent from it).
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
	// Virtual: the module makes the file's bytes (Materializer) from a session the agent
	// keeps elsewhere, in a database; nothing at Root/Rel is read for it. Size is then an
	// estimate.
	Virtual bool `json:"virtual,omitempty"`
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
	// RenumberOrdinal rebases this top-level integer field after dropping records in a
	// newly installed copy. All other bytes stay intact; append never uses this policy.
	RenumberOrdinal string `json:"renumberOrdinal,omitempty"`
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
	// AppRunning is set by the core from current live detection, never by a CLI flag.
	// It distinguishes focusing an owned desktop session from importing a saved one.
	AppRunning    bool
	Fork          bool
	RemoteControl bool
	App           bool   // open in the agent's desktop app instead of the terminal
	Name          string // a name for Remote Control / the session list
	Prompt        string // a first message, "" for none
}

// Command is a program to run, never a shell string; the core quotes it for the user's
// shell.
type Command struct {
	// TTY requests a transient pseudo-terminal for a short-lived desktop launcher.
	// It never creates a visible tab or sends input; the caller waits for completion.
	TTY bool `json:"tty,omitempty"`
	// Wait requests error reporting from a short-lived desktop launcher.
	Wait bool     `json:"wait,omitempty"`
	Argv []string `json:"argv"`
	Dir  string   `json:"dir"`
	// Unset are environment variables the program must run without (a cloud driver's
	// Cloud.Unset); the core leaves them out of the shell line and the process.
	Unset []string `json:"unset,omitempty"`
	// Env are variables ("NAME=value") the program runs with on top of the user's
	// (CCR_FORCE_BUNDLE=1); never a credential.
	Env []string `json:"env,omitempty"`
}

// OwnedTabCommander adjusts the command that resumes a session in a terminal tab hopsesh
// owns on this machine (it started the process and can stop it): Codex runs its own
// server there instead of the shared one. getenv reads hopsesh's environment, which the
// tab inherits. It returns c as it is when nothing applies, and never changes c's Dir,
// Env or Unset; applying it twice changes nothing more.
type OwnedTabCommander interface {
	OwnedTabCommand(in Install, c Command, getenv func(string) string) Command
}

// RemoteControlTyper is what to type in an open, idle session to turn its agent's remote
// control on there ("/remote-control"); optional beside CapRemoteControl.
type RemoteControlTyper interface {
	RemoteControlInSession() string
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
	// App is true when the agent's own desktop app runs it (Claude Code in the Claude
	// app), not a terminal: there is no terminal tab to show.
	App bool `json:"app,omitempty"`
	// Procs are every process that has the session open (Claude Code can run one session
	// in several), PID among them; empty when the module can't say.
	Procs []LiveProc `json:"procs,omitempty"`
	// Name is the session's name as the running agent reports it (the Claude app names
	// sessions), "" when none.
	Name string `json:"name,omitempty"`
	// RemoteControl is whether a process has the session connected to the agent's
	// Remote Control (reachable from its website and phone app): RemoteOn, RemoteOff, or
	// "" when the agent doesn't say.
	RemoteControl string `json:"remoteControl,omitempty"`
	// RemoteURL is the page Remote Control reaches the session at, when it is on.
	RemoteURL string `json:"remoteUrl,omitempty"`
}

// Remote Control states (LiveInfo.RemoteControl).
const (
	RemoteOn  = "on"
	RemoteOff = "off"
)

// LiveProc is one process that has a session open.
type LiveProc struct {
	ObservedAt time.Time `json:"observedAt,omitempty"` // registry evidence timestamp, not process start time
	PID        int       `json:"pid"`
	// App is true when the agent's desktop app runs it.
	App bool `json:"app,omitempty"`
	// Waiting is true when it waits for the person (a question, a permission).
	Waiting bool `json:"waiting,omitempty"`
	// RemoteControl is this process's Remote Control state, as LiveInfo.RemoteControl.
	RemoteControl string `json:"remoteControl,omitempty"`
}

// OwnerRun is an account a session ran under, as recorded. Key is the module's account
// fingerprint (comparable with Account.Key); Person, when recorded, the user's (comparable
// with Account.Person). Source says who recorded it: "agent" or "hopsesh".
type OwnerRun struct {
	Key    string `json:"key"`
	Person string `json:"person,omitempty"`
	Source string `json:"source,omitempty"`
}

// LocalIdentifier reads which account a root on this machine is signed in to now from the
// agent's own public identity record (never credentials, never by running the agent), fast
// enough for a prompt hook. native is true when the root is the agent's default, found
// without its override variable.
type LocalIdentifier interface {
	LocalIdentity(root string, native bool) (OwnerRun, bool)
}

// AddOwner appends a run, merging it into the last one when it is the same account.
func AddOwner(runs []OwnerRun, r OwnerRun) []OwnerRun {
	if r.Key == "" && r.Person == "" {
		return runs
	}
	if n := len(runs); n > 0 {
		l := &runs[n-1]
		if (r.Key == "" || l.Key == "" || l.Key == r.Key) && (r.Person == "" || l.Person == "" || l.Person == r.Person) {
			if l.Key == "" {
				l.Key = r.Key
			}
			if l.Person == "" {
				l.Person = r.Person
			}
			return runs
		}
	}
	return append(runs, r)
}

// Account contains public vendor login metadata, never credentials. Key is legacy
// Remote Control ownership metadata; it does not prove cross-profile identity.
type Account struct {
	IsolationWhy string `json:"isolationWhy,omitempty"`
	LoggedIn     bool   `json:"loggedIn"`
	Provider     string `json:"provider,omitempty"`
	Email        string `json:"email,omitempty"`
	Confidence   string `json:"confidence,omitempty"` // verified, limited, unknown
	// Observation detects visible login changes; never proves account equality.
	Observation string `json:"observation,omitempty"`
	Key         string `json:"key"` // never sufficient for native cross-account replay
	// Person fingerprints the signed-in user within Key's account, when the agent's own
	// public identity file names one (Claude Code's accountUuid, Codex's user id).
	Person string `json:"person,omitempty"`
	Label  string `json:"label,omitempty"` // for people: "Max plan", "Team org"
	// RemoteControl is whether this login can use the agent's own remote control.
	RemoteControl bool   `json:"remoteControl"`
	Why           string `json:"why,omitempty"` // why not
}

// Materializer makes the bytes of a session's Virtual bundle files: the session as the
// module's own file format (JSON lines), read from where the agent keeps it. The core
// stages, rewrites, packages and installs those bytes like any other file. A module with
// Virtual files implements it.
type Materializer interface {
	Materialize(ctx context.Context, h Host, in Install, s Summary, f BundleFile) ([]byte, error)
}

// VirtualSessions marks a module whose sessions are not one growing file: Summary.Size and
// ir.Cursor.Offset count the session's records (settled messages), not bytes, and
// Summary.Path is an anchor file hopsesh keeps, which may be missing. The core then
// compares stamps (SessionStamp) instead of file sizes and times.
type VirtualSessions interface {
	VirtualSessions()
}

// IsVirtual reports whether a module keeps virtual sessions.
func IsVirtual(m Module) bool {
	_, ok := m.(VirtualSessions)
	return ok
}

// AnchorRel is where a virtual session's anchor file sits, relative to its install's main
// root (slash-separated): hopsesh/<agent>/sessions/<id>.jsonl. A module with
// VirtualSessions keeps Summary.Path there, so the core finds a session's anchor, and the
// lineage beside it, from the session's id alone (a notice hook gets no path).
func AnchorRel(agent ID, id SessionID) string {
	return "hopsesh/" + string(agent) + "/sessions/" + string(id) + ".jsonl"
}

// SessionStamp is a module's opaque mark of a session's state: two equal stamps mean
// nothing was added or changed in between. "" is no stamp. A stamp starts with its form
// and a colon ("v1:…"), and a module that changes how it stamps changes that prefix, so
// a stamp journaled before can be told apart from a session that changed since (the core
// then asks instead of refusing an undo for good).
type SessionStamp string

// Format is the stamp's form, the text before its first colon ("" when it has none).
func (s SessionStamp) Format() string {
	if i := strings.IndexByte(string(s), ':'); i > 0 {
		return string(s[:i])
	}
	return ""
}

// SessionStamper gives a session's stamp without listing every session, for caches and
// for guards that refuse to undo over later work. A missing session is ErrNotFound.
type SessionStamper interface {
	SessionStamp(ctx context.Context, h Host, in Install, id SessionID) (SessionStamp, error)
}

// SummaryStamp is the stamp of a listed virtual session: its record count and last
// activity.
func SummaryStamp(s Summary) SessionStamp {
	return SessionStamp(fmt.Sprintf("summary1:%d@%d", s.Size, s.LastActivity.UTC().UnixNano()))
}

// StampOf is a session's stamp from its module: SessionStamper's, else SummaryStamp for a
// virtual module. ok is false when the module has neither, and the core compares the main
// file's size and time instead. For SummaryStamp, s must be fresh (from a listing or a
// summary made now), never one from the listing cache, or a guard compares old with old.
func StampOf(ctx context.Context, m Module, h Host, in Install, s Summary) (stamp SessionStamp, ok bool, err error) {
	if st, is := m.(SessionStamper); is {
		stamp, err = st.SessionStamp(ctx, h, in, s.Key.Session)
		return stamp, true, err
	}
	if IsVirtual(m) {
		return SummaryStamp(s), true, nil
	}
	return "", false, nil
}

// SessionStore changes sessions the agent keeps in a database, through the agent's own
// program, never by writing the database. The core journals each change as a
// vendor-session operation and undoes it with these methods.
type SessionStore interface {
	SessionStamper
	// Backup saves the session as the agent exports it, with its child sessions (Delete
	// removes them too), to a file under the install's roots (through h.FS, journaled) and
	// returns that file and the stamp it was taken at.
	Backup(ctx context.Context, h Host, in Install, id SessionID) (backup string, stamp SessionStamp, err error)
	// Delete removes the session (and its child sessions) only while its stamp is still
	// expect; otherwise ErrDiverged, and nothing is deleted. A session already gone is
	// ErrNotFound. An empty expect is always refused (ErrDenied): a delete names the
	// state it deletes.
	Delete(ctx context.Context, h Host, in Install, id SessionID, expect SessionStamp) error
	// Restore imports a Backup again, keeping the session's id, and returns the new stamp.
	// It is idempotent: a session already there with the backup's content is left as it
	// is. A session there with other content (continued since) is ErrDiverged and left
	// as it is, so an undo never reports success over newer work.
	Restore(ctx context.Context, h Host, in Install, id SessionID, backup string) (SessionStamp, error)
}

// SessionIDMinter makes new session ids in the agent's own form (OpenCode: "ses_…"), for a
// copy kept beside the original or a session written from another agent. Without it the
// core makes a UUID.
type SessionIDMinter interface {
	NewSessionID(in Install, now time.Time) SessionID
}

// NewSessionID is a new id from the module's minter, else from fallback.
func NewSessionID(m Module, in Install, now time.Time, fallback func() string) SessionID {
	if mm, ok := m.(SessionIDMinter); ok {
		if id := mm.NewSessionID(in, now); id != "" {
			return id
		}
	}
	return SessionID(fallback())
}

// VendorInstaller is a PostInstaller whose AfterInstall is the install itself: the staged
// files are input for the agent's own importer (OpenCode's session import), not the
// session. When InstallsViaVendor is true the core runs AfterInstall, after placing a
// moved session's files, before it records lineage, treats its failure as the operation's
// failure, and runs it again when it recovers an interrupted operation, so AfterInstall
// must be idempotent. A session already at the id with other content than the placed copy
// is ErrDiverged, never success (it may be work done since a first, interrupted run).
//
// Such a module's Writer imports in Write itself, and its WriteRecoverer again when an
// interrupted Write had not: the core runs no AfterInstall after a write.
type VendorInstaller interface {
	PostInstaller
	InstallsViaVendor() bool
}

// InstallsViaVendor reports whether a module's sessions are installed by the agent itself.
func InstallsViaVendor(m Module) bool {
	v, ok := m.(VendorInstaller)
	return ok && v.InstallsViaVendor()
}
