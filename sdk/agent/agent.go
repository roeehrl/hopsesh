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
	Session SessionID `json:"session"`
}

func (k SessionKey) String() string { return string(k.Agent) + "/" + string(k.Session) }

// ParseKey parses "agent/session".
func ParseKey(s string) (SessionKey, error) {
	a, id, ok := strings.Cut(s, "/")
	if !ok || a == "" || id == "" {
		return SessionKey{}, fmt.Errorf("%q is not agent/session", s)
	}
	return SessionKey{Agent: ID(a), Session: SessionID(id)}, nil
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
	ID        ID
	Name      string // "Claude Code"
	Vendor    string // "Anthropic"
	Stability Stability
	// Tested lists the agent versions this module was verified against, oldest first,
	// as version prefixes ("2.1"). Other versions work but are reported as untested.
	Tested []string
	// Binaries the module may run through Host.Exec (the first is the agent itself).
	Binaries []Binary
	// Roots are the agent's data folders. The first is the main one; writes are allowed
	// only under a root.
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
	// Tools names the agent's own tools for people and for a continued session's briefing
	// ("shell, apply_patch, update_plan").
	Tools string
	// Features are capabilities without a method of their own: the module honours the
	// matching ResumeOptions (CapFork, CapRemoteControl) or start prompt (CapNotify).
	Features []Capability
	// Experimental marks capabilities that are not yet trustworthy; the user interfaces
	// say so and keep them opt-in.
	Experimental []Capability
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
	// Default is the folder when no variable is set, by GOOS ("*" for all); "~" is the home
	// folder, "{name}" another root.
	Default map[string]string
}

// Install is an agent as found on one machine.
type Install struct {
	Agent   ID                `json:"agent"`
	Version string            `json:"version,omitempty"` // "" when the binary was not found
	Binary  string            `json:"binary,omitempty"`  // absolute path
	Roots   map[string]string `json:"roots"`             // name → absolute path on that machine
	Present bool              `json:"present"`           // the main root exists
}

// Root returns the path of a named root.
func (in Install) Root(name string) string { return in.Roots[name] }

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
