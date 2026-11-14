// Package agent is the contract between the hopsesh core and its agent modules. A module
// knows one coding agent (Claude Code, Codex, ...): where it keeps sessions, how they are
// listed, moved, resumed and converted. The core knows none of that; it owns all I/O,
// transport, safety, conversion and the user interfaces, and hands each module a Host.
//
// A module is a Spec (data the core acts on before running module code) plus the Module
// interface, plus any optional capability interfaces it implements. Modules hold no state
// and never import os, os/exec, net, path/filepath or the core: everything goes through
// the Host, so every write is confined, journaled and undoable.
package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// ID names an agent module: lower case letters and digits, 2 to 16 characters.
type ID string

// SessionID is an agent's own id for a session, opaque to the core.
type SessionID string

// SessionKey identifies a session across agents: the only key the core, the lineage
// manifests and the user interfaces use.
type SessionKey struct {
	Agent   ID        `json:"agent"`
	Profile string    `json:"profile,omitempty"`
	Session SessionID `json:"session"`
}

func (k SessionKey) String() string {
	a := string(k.Agent)
	if k.Profile != "" {
		a += "@" + k.Profile
	}
	return a + "/" + string(k.Session)
}

// ParseKey parses "agent/session".
func ParseKey(s string) (SessionKey, error) {
	a, id, ok := strings.Cut(s, "/")
	if !ok || a == "" || id == "" {
		return SessionKey{}, fmt.Errorf("%q is not agent/session", s)
	}
	name, profile, scoped := strings.Cut(a, "@")
	if name == "" || scoped && profile == "" || strings.ContainsAny(profile, "/:@ \t\r\n") {
		return SessionKey{}, fmt.Errorf("invalid profile in %q", s)
	}
	return SessionKey{Agent: ID(name), Profile: profile, Session: SessionID(id)}, nil
}

// Stability says how far a module (or one of its capabilities) can be trusted.
type Stability string

const (
	Experimental Stability = "experimental"
	Stable       Stability = "stable"
)

// Spec is what a module declares as data. The core resolves roots, probes binaries,
// confines writes and refuses secret reads from it, before any module code runs.
type Spec struct {
	Accounts *ProfileSpec
	// ResumeAcrossAccounts: a session resumes under another account than the one that ran
	// it, the vendor dropping what is bound to that account (Claude Code: signed reasoning).
	// Without it such a session must continue as a cleaned copy.
	ResumeAcrossAccounts bool
	// DesktopScheme is the registered URL protocol a desktop resume uses.
	DesktopScheme string
	ID            ID
	Name          string // "Claude Code"
	Vendor        string // "Anthropic"
	Stability     Stability
	// Tested lists the agent versions this module was verified against, oldest first,
	// as version prefixes ("2.1"). Other versions work but are reported as untested.
	Tested []string
	// Binaries the module may run through Host.Exec (the first is the agent itself).
	Binaries []Binary
	// Roots are the agent's data folders. The first is the main one; writes are allowed
	// only under a root. A cloud-only module has none.
	Roots []Root
	// LoginEnv are environment variables a GUI app must adopt from the user's login shell
	// (an app started from Finder does not inherit them).
	LoginEnv []string
	// Secrets are root-relative paths ("{home}/auth.json") the Host never opens.
	Secrets []string
	// Worktrees are worktree folders the agent creates inside a repository
	// (".claude/worktrees"); git status ignores them.
	Worktrees []string
	// Instructions are the project instruction files the agent reads, in its order of
	// preference ("CLAUDE.md", "AGENTS.md"). A session continued in another agent is told
	// about files only the other agent read.
	Instructions []string
	// GlobalInstructions are the user's instructions for every project, root-relative
	// ("{home}/CLAUDE.md"). A continued session reports them, and carries them when asked.
	GlobalInstructions []string
	// Tools names the agent's own tools for people and for a continued session's briefing
	// ("shell, apply_patch, update_plan").
	Tools string
	// Features are capabilities without a method of their own: the module honours the
	// matching ResumeOptions (CapFork, CapRemoteControl).
	Features []Capability
	// Experimental marks capabilities that are not yet trustworthy; the user interfaces
	// say so and keep them opt-in.
	Experimental []Capability
	// Icon is how the user interfaces picture the agent (optional; its initials otherwise).
	Icon Icon
	// Desktop is how the agent's desktop app signs in and runs sessions, for the user
	// interfaces (nil: it has none, or nothing to say).
	Desktop *Desktop
	// Plans is what an account's plan is named after when the module's Account.Label is a
	// bare plan ("max" reads "Claude Max"); empty when the label names it ("ChatGPT Pro").
	Plans string
	// Clouds are the vendor clouds the module reaches through its agent's CLI. A module
	// with clouds may have no Roots (a cloud-only module, see NoLocal).
	Clouds []Cloud
	// Kinds are kinds of session the module marks (Summary.Kinds) that hopsesh hides
	// unless the user turns them on for this agent: conversations that are not coding
	// sessions to move (the Codex app's voice and Work threads).
	Kinds []SessionKind
	// ServiceTurns: the agent's terminal program attaches to its background service, which
	// runs the turns (OpenCode): a terminal that ends leaves a turn running there.
	ServiceTurns bool
	// TerminalEnv are variables ("NAME=value") the agent's programs get in hopsesh's own
	// terminal tabs, on top of the user's: settings that suit that terminal
	// (CLAUDE_CODE_FORCE_SYNC_OUTPUT=1). Never a credential; not used in other terminals.
	TerminalEnv []string
	// SQLTables are the database tables the module may read through the Host's SQL
	// (SQLHost), for an agent that keeps sessions in SQLite. Every other table is refused.
	SQLTables []string
	// SQLFiles are the database files SQL may open: paths or patterns (* ? [) with {root}
	// and ~ as Secrets have ("{data}/opencode*.db", "{db}" for an Install root that a
	// variable sets). A pattern whose root the install lacks matches nothing. Each file,
	// with its -wal, -shm and -journal companions, is a Secret too: no file read, copy or
	// package reaches it.
	SQLFiles []string
	// SQLDenied are tables that are never read, even through a view, and whose name
	// refuses any query that mentions it (credentials, accounts).
	SQLDenied []string
}

// Desktop is an agent's desktop app as the user interfaces show it.
type Desktop struct {
	// Tool names the app beside the agent's command-line tool ("Claude desktop", "Codex
	// app").
	Tool string
	// Product is the app as it names itself, before its version ("Claude desktop",
	// "ChatGPT desktop").
	Product string
	// OwnSignIn: the app keeps a sign-in of its own (Install.DesktopAccount) and its
	// sessions go in the main root's default folder, whatever the environment says (Claude
	// desktop). Otherwise it shares that folder's sign-in with the terminal (the Codex app).
	OwnSignIn bool
	// Shared: one app process runs every session, so the app is shown and opened, never
	// quit (the Codex app).
	Shared bool
}

// Short is the agent's name where space is tight: the first word of its Name ("Claude").
func (s Spec) Short() string {
	if f := strings.Fields(s.Name); len(f) > 0 {
		return f[0]
	}
	return string(s.ID)
}

// SessionKind is a kind of session hidden by default (Spec.Kinds).
type SessionKind struct {
	ID   string // "voice"
	Name string // "Voice conversations"
	Note string // what they are, for the setting
}

// Icon pictures an agent. The installed desktop app's own icon comes first (unless the
// user turns that off), then the module's mark, then the agent's initials.
type Icon struct {
	// SVG is a mark drawn for hopsesh: a complete <svg> document with no scripts or
	// external references. It must not reproduce a vendor's logo or trademark.
	SVG string
	// Apps are where the agent's desktop app may be installed, by GOOS ("~" is the home
	// folder): a macOS .app bundle or a Windows .exe. Its icon is read from this machine
	// and never shipped.
	Apps map[string][]string
}

// Binary is a program the module may run.
type Binary struct {
	Name string // "claude"
	// Candidates are extra places to look besides PATH, by GOOS ("*" for all); "~" is the
	// home folder.
	Candidates  map[string][]string
	VersionArgs []string // ["--version"]
}

// Root is one data folder of the agent.
type Root struct {
	Name string   // "home"
	Env  []string // overriding variables, first set wins: ["CLAUDE_CONFIG_DIR"]
	// EnvSub are variables that set the root as a folder inside their value, tried after
	// Env, first set wins: {XDG_DATA_HOME, "opencode"} is $XDG_DATA_HOME/opencode.
	EnvSub []EnvSub
	// Default is the folder when no variable is set, by GOOS ("*" for all); "~" is the home
	// folder, "{name}" another root.
	Default map[string]string
}

// EnvSub is a variable that names a folder the root is inside (XDG base directories).
type EnvSub struct {
	Env string `json:"env"` // "XDG_DATA_HOME"
	Sub string `json:"sub"` // "opencode", slash-separated
}

// Vars are the environment variables that can set the root, in the order Locate tries
// them. Code that keys on or resolves a root's environment goes through Vars and Locate,
// never Env or EnvSub themselves.
func (r Root) Vars() []string {
	out := append([]string(nil), r.Env...)
	for _, e := range r.EnvSub {
		if !slices.Contains(out, e.Env) {
			out = append(out, e.Env)
		}
	}
	return out
}

// DefaultFor is the root's folder on goos when no variable sets it ("~/.claude").
func (r Root) DefaultFor(goos string) string {
	if p := r.Default[goos]; p != "" {
		return p
	}
	return r.Default["*"]
}

// Locate is the root's folder on a machine, before Expand: its first set Env variable,
// else the first set EnvSub variable with its subfolder, else its default for goos.
// getenv reads that machine's environment.
func (r Root) Locate(goos string, getenv func(string) string) string {
	for _, e := range r.Env {
		if v := getenv(e); v != "" {
			return v
		}
	}
	for _, e := range r.EnvSub {
		if v := getenv(e.Env); v != "" {
			return PathFor(goos).Join(v, e.Sub)
		}
	}
	return r.DefaultFor(goos)
}

// MainRoot is the main data folder: the first declared root (zero for a cloud-only module).
func (s Spec) MainRoot() Root {
	if len(s.Roots) == 0 {
		return Root{}
	}
	return s.Roots[0]
}

// PinMain returns in with its main root set from the account folder path and no other
// root, before Detect resolves the other roots against the pinned environment. For an
// added account (in.Profile set, not the default) of an agent whose account folders hold
// its roots (ProfileSpec.RootVars), the main root is inside path (ProfileLocate); else it
// is path itself. Set in.Profile first.
func (s Spec) PinMain(in Install, path string) Install {
	in.MainRoot = s.MainRoot().Name
	if in.Profile != nil && !in.Profile.Default && s.Accounts != nil && len(s.Accounts.RootVars) > 0 {
		goos := in.OS
		if goos == "" && (WindowsPath{}).IsAbs(path) {
			goos = "windows"
		}
		// Only the account folder's own variables: a root they do not set is not in it.
		if main := s.ProfileLocate(in.MainRoot, path, goos, func(string) string { return "" }); PathFor(goos).IsAbs(main) {
			path = main
		}
	}
	in.Roots = map[string]string{in.MainRoot: path}
	return in
}

// Install is an agent as found on one machine.
type Install struct {
	// NativeDefaultRoot is launch metadata verified by the module from the detected
	// machine's home and original environment. Empty means native config lookup is
	// unverified or explicitly overridden. It grants no filesystem access.
	NativeDefaultRoot string       `json:"nativeDefaultRoot,omitempty"`
	Accounts          *ProfileSpec `json:"-"`
	OS                string       `json:"os,omitempty"`
	Desktop           string       `json:"desktop,omitempty"`
	DesktopVersion    string       `json:"desktopVersion,omitempty"`
	DesktopWhy        string       `json:"desktopWhy,omitempty"`
	// DesktopAccount is who the desktop app is signed in as, when it keeps its own
	// sign-in (Claude desktop): its account and user fingerprints, never more.
	DesktopAccount *OwnerRun `json:"desktopAccount,omitempty"`
	// DesktopEngine is the version of the agent the desktop app runs, its own copy
	// (Claude desktop's Claude Code, the Codex app's codex).
	DesktopEngine string            `json:"desktopEngine,omitempty"`
	Profile       *RuntimeProfile   `json:"profile,omitempty"`
	Agent         ID                `json:"agent"`
	Version       string            `json:"version,omitempty"` // "" when the binary was not found
	Binary        string            `json:"binary,omitempty"`  // absolute path
	Runtime       string            `json:"runtime,omitempty"` // RuntimeApp: Binary is the desktop app's bundled runtime, no CLI
	Roots         map[string]string `json:"roots"`             // name → absolute path on that machine
	// MainRoot names the main root in Roots (Spec.MainRoot); see Main.
	MainRoot string `json:"mainRoot,omitempty"`
	Present  bool   `json:"present"` // the main root exists
}

// RuntimeApp marks Install.Binary as the desktop app's bundled runtime.
const RuntimeApp = "app"

// Root returns the path of a named root.
func (in Install) Root(name string) string { return in.Roots[name] }

// Main returns the path of the main root. An install that does not name it (one decoded
// from an older peer) has a single root, which is the main one.
func (in Install) Main() string {
	if in.MainRoot != "" {
		return in.Roots[in.MainRoot]
	}
	if len(in.Roots) == 1 {
		for _, p := range in.Roots {
			return p
		}
	}
	return ""
}

// Module is what every agent module implements.
type Module interface {
	Spec() Spec

	// Detect turns the probe facts of a machine into an Install. Most modules return
	// DefaultInstall(spec, facts) with checks of their own.
	Detect(ctx context.Context, h Host) (Install, error)

	// List returns the sessions on the machine. One unreadable session goes in
	// Listing.Errors; only a failure to list at all returns an error.
	List(ctx context.Context, h Host, in Install) (Listing, error)

	// Bundle names the files that make up one session.
	Bundle(ctx context.Context, h Host, in Install, s Summary) (Bundle, error)

	// PlanMove places a bundle on the target (pure): where each file goes, how it is
	// rewritten, and records to append. src and dst are the same agent's installs.
	PlanMove(src, dst Install, s Summary, b Bundle, p Placement) (MovePlan, error)

	// Verify checks staged files (already rewritten, on the target machine) before they are
	// installed, for example that the session's working directory is the target's.
	Verify(ctx context.Context, h Host, mp MovePlan, staged map[string]string, p Placement) error

	// Resume is how the user continues a session on this machine.
	Resume(in Install, key SessionKey, p Placement, o ResumeOptions) Command
}

// Listing is the result of Module.List.
type Listing struct {
	Sessions []Summary
	Errors   []SessionError
}

// SessionError is one session that could not be read.
type SessionError struct {
	Path string
	Err  error
}

func (e SessionError) Error() string { return e.Path + ": " + e.Err.Error() }
