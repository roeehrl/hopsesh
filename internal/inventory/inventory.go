// Package inventory scans machines for Claude Code sessions and joins each session with
// its live status and git state. It is shared by the CLI, TUI and GUI.
package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/fsys"
	"github.com/roeehrl/hopsesh/internal/core/link"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/sessions"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/engine"
)

// Status of a machine scan.
const (
	StatusOK          = "ok"
	StatusLocal       = "local"
	StatusUnreachable = "unreachable"
	StatusAuth        = "auth"
	StatusHostKey     = "host-key"
	StatusKeyChanged  = "host-key-changed"
	StatusTSCheck     = "tailscale-check"
	StatusLocalNet    = "local-network" // macOS local network privacy blocked it
	StatusNoClaude    = "no-claude"
	StatusError       = "error"
)

// Session is one session with its machine-side context.
type Session struct {
	sessions.Summary
	Live *sessions.LiveEntry `json:"live,omitempty"`
	Git  *repos.GitState     `json:"git,omitempty"`
}

// Machine is one scanned machine.
type Machine struct {
	Name        string           `json:"name"`
	Destination string           `json:"destination"`
	Local       bool             `json:"local"`
	Status      string           `json:"status"`
	Error       string           `json:"error,omitempty"`
	Hint        string           `json:"hint,omitempty"`
	Facts       *transport.Facts `json:"facts,omitempty"`
	Sessions    []Session        `json:"sessions"`

	conn *transport.Conn
	fs   fsys.FS
	auth *link.Auth
}

// Scanner scans machines.
type Scanner struct {
	StateDir string
	Log      *audit.Log
	Options  sessions.ListOptions
	// SkipGit skips the git probe (faster listing).
	SkipGit bool
}

// Scan scans this machine (if includeLocal) and every allowed host concurrently. Machines
// that fail are returned with a status and a hint instead of an error.
func (s *Scanner) Scan(ctx context.Context, hosts []config.Host, includeLocal bool) []*Machine {
	var out []*Machine
	var mu sync.Mutex
	var wg sync.WaitGroup
	add := func(m *Machine) {
		mu.Lock()
		out = append(out, m)
		mu.Unlock()
	}
	if includeLocal {
		wg.Add(1)
		go func() { defer wg.Done(); add(s.scanLocal(ctx)) }()
	}
	for _, h := range hosts {
		if !h.Allowed {
			continue
		}
		h := h
		wg.Add(1)
		go func() { defer wg.Done(); add(s.ScanHost(ctx, h)) }()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Local != out[j].Local {
			return out[i].Local
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// LocalHostName is this machine's short name.
func LocalHostName() string {
	h, _ := os.Hostname()
	return strings.TrimSuffix(strings.Split(h, ".")[0], ".local")
}

// LocalFacts describes this machine.
func LocalFacts(ctx context.Context) *transport.Facts {
	home, _ := os.UserHomeDir()
	cfg, _ := sessions.LocalConfigDir()
	f := &transport.Facts{OS: runtime.GOOS, Arch: runtime.GOARCH, Home: home, ConfigDir: cfg}
	if p, v := LocalClaude(ctx); p != "" {
		f.ClaudePath, f.ClaudeVersion = p, v
	}
	_, err := exec.LookPath("git")
	f.HasGit = err == nil
	return f
}

// LocalTarget describes this machine as the destination of a move.
func LocalTarget(ctx context.Context) engine.Target {
	f := LocalFacts(ctx)
	t := engine.Target{Host: LocalHostName(), OS: f.OS, ConfigDir: f.ConfigDir, Home: f.Home, ClaudeVersion: f.ClaudeVersion, ClaudePath: f.ClaudePath}
	if f.ClaudePath != "" {
		t.Auth = localAuth(ctx, f.ClaudePath)
	}
	return t
}

func localAuth(ctx context.Context, claude string) *link.Auth {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, claude, "auth", "status", "--json").Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	a, err := link.ParseAuth(out)
	if err != nil {
		return nil
	}
	return a
}

// PlanSource is Source with the login there read too (once per scan), so a plan can
// tell whether both machines use the same Claude account.
func (m *Machine) PlanSource(ctx context.Context) engine.Source {
	src := m.Source()
	if m.auth == nil && m.Facts != nil && m.Facts.ClaudePath != "" {
		if m.Local {
			m.auth = localAuth(ctx, m.Facts.ClaudePath)
		} else if m.conn != nil {
			ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			var out []byte
			var err error
			if m.Facts.OS == "windows" {
				out, err = m.conn.RunPowerShell(ctx, "& '"+strings.ReplaceAll(m.Facts.ClaudePath, "'", "''")+"' auth status --json")
			} else {
				out, err = m.conn.Run(ctx, transport.ShQuote(m.Facts.ClaudePath)+" auth status --json")
			}
			if err == nil || len(out) > 0 {
				m.auth, _ = link.ParseAuth(out)
			}
		}
	}
	src.Auth = m.auth
	return src
}

var (
	helpMu    sync.Mutex
	helpCache = map[string]string{}
)

// ClaudeSupports reports whether this machine's claude lists a command-line flag in its
// help (feature detection, rather than guessing from version numbers).
func ClaudeSupports(ctx context.Context, claude, flag string) bool {
	if claude == "" {
		return false
	}
	helpMu.Lock()
	help, ok := helpCache[claude]
	helpMu.Unlock()
	if !ok {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, claude, "--help").Output()
		help = string(out)
		helpMu.Lock()
		helpCache[claude] = help
		helpMu.Unlock()
	}
	for _, f := range strings.Fields(help) {
		if strings.TrimRight(f, ",") == flag {
			return true
		}
	}
	return false
}

// LocalClaude finds the claude binary and its version on this machine.
func LocalClaude(ctx context.Context) (path, version string) {
	cands := []string{}
	if p, err := exec.LookPath("claude"); err == nil {
		cands = append(cands, p)
	}
	home, _ := os.UserHomeDir()
	cands = append(cands, filepath.Join(home, ".local", "bin", "claude"), filepath.Join(home, ".claude", "local", "claude"),
		"/opt/homebrew/bin/claude", "/usr/local/bin/claude", filepath.Join(home, ".local", "bin", "claude.exe"))
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			out, err := exec.CommandContext(ctx, c, "--version").Output()
			if err == nil {
				v := strings.TrimSpace(strings.Split(string(out), "\n")[0])
				v = strings.TrimSpace(strings.TrimSuffix(v, "(Claude Code)"))
				return c, v
			}
		}
	}
	return "", ""
}

func (s *Scanner) scanLocal(ctx context.Context) *Machine {
	m := &Machine{Name: LocalHostName(), Destination: "", Local: true, Status: StatusLocal, fs: fsys.Local{}}
	m.Facts = LocalFacts(ctx)
	loc := sessions.Locator{FS: fsys.Local{}, ConfigDir: m.Facts.ConfigDir}
	list, err := loc.List(s.Options)
	if err != nil && !os.IsNotExist(err) {
		m.Status, m.Error = StatusError, err.Error()
		return m
	}
	live, _ := loc.LiveRegistry(sessions.LocalAlive)
	var gitStates []repos.GitState
	if !s.SkipGit {
		gitStates, _ = repos.ProbeLocal(ctx, distinctCWDs(list))
	}
	m.Sessions = join(list, live, gitStates)
	return m
}

// ScanHost scans one remote machine.
func (s *Scanner) ScanHost(ctx context.Context, h config.Host) *Machine {
	m := &Machine{Name: h.Name, Destination: h.Destination}
	conn, err := transport.NewConn(h.Destination, s.StateDir, s.Log)
	if err != nil {
		m.Status, m.Error = StatusError, err.Error()
		return m
	}
	if h.TailscaleName != "" && h.TailscaleName != h.Destination {
		conn.Fallbacks = []string{h.TailscaleName}
	}
	routes := loadRoutes(s.StateDir)
	if r := routes[h.Destination]; r != "" {
		conn.Prefer(r)
	}
	m.conn = conn
	facts, err := conn.Probe(ctx)
	if conn.Override() != routes[h.Destination] {
		saveRoute(s.StateDir, h.Destination, conn.Override())
	}
	if err != nil {
		m.Status, m.Error, m.Hint = classifyErr(err, h)
		return m
	}
	m.Facts = facts
	rfs, err := conn.OpenSFTP(ctx, facts.OS == "windows")
	if err != nil {
		m.Status, m.Error = StatusError, "SFTP: "+err.Error()
		m.Hint = "the machine's SSH server must allow the sftp subsystem"
		return m
	}
	m.fs = rfs
	if h.Helper && h.HelperSHA256 != "" && facts.OS != "windows" {
		list, err := runHelper(ctx, conn, facts, h.HelperSHA256)
		if err == nil {
			m.Sessions, m.Status = list, StatusOK
			return m
		}
		m.Hint = "helper not used (" + err.Error() + "); scanned without it"
	}
	loc := sessions.Locator{FS: rfs, ConfigDir: facts.ConfigDir, Workers: 16}
	list, err := loc.List(s.Options)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "not exist") {
			m.Status = StatusOK
			m.Hint = strings.TrimPrefix(m.Hint+"; no Claude Code sessions yet ("+facts.ConfigDir+"/projects is missing)", "; ")
			return m
		}
		m.Status, m.Error = StatusError, err.Error()
		return m
	}
	live, _ := loc.LiveRegistry(func(p []int) map[int]bool { return conn.Alive(ctx, facts, p) })
	var gitStates []repos.GitState
	if !s.SkipGit {
		gitStates, _ = conn.GitProbe(ctx, facts, distinctCWDs(list))
	}
	m.Sessions = join(list, live, gitStates)
	m.Status = StatusOK
	if facts.ClaudeVersion == "" {
		m.Hint = strings.TrimPrefix(m.Hint+"; claude is not on the PATH of non-interactive SSH sessions there (sessions are still listed)", "; ")
	}
	return m
}

func classifyErr(err error, h config.Host) (status, msg, hint string) {
	var tc *transport.TailscaleCheckError
	var ln *transport.LocalNetworkError
	switch {
	case errors.As(err, &ln):
		hint := ln.Hint()
		if h.TailscaleName == "" {
			hint += " Adding the machine by its Tailscale name avoids this."
		}
		return StatusLocalNet, err.Error(), hint
	case errors.Is(err, transport.ErrHostKeyUnknown):
		return StatusHostKey, err.Error(), "confirm its host key: hopsesh trust " + h.Name
	case errors.Is(err, transport.ErrHostKeyChanged):
		return StatusKeyChanged, err.Error(), "the host key changed since you trusted it; verify the machine before fixing known_hosts"
	case errors.Is(err, transport.ErrAuth):
		return StatusAuth, err.Error(), "make sure `ssh " + h.Destination + "` works without a prompt (add your key to the agent)"
	case errors.As(err, &tc):
		return StatusTSCheck, err.Error(), "open the URL, approve, then refresh"
	case errors.Is(err, transport.ErrUnreachable):
		return StatusUnreachable, err.Error(), "is it awake and on the network? (Remote Login / SSH server must be on)"
	}
	return StatusError, err.Error(), ""
}

func distinctCWDs(list []*sessions.Summary) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range list {
		if s.CWD != "" && !seen[s.CWD] {
			seen[s.CWD] = true
			out = append(out, s.CWD)
		}
	}
	return out
}

func join(list []*sessions.Summary, live []sessions.LiveEntry, git []repos.GitState) []Session {
	liveBy := map[string]*sessions.LiveEntry{}
	for i := range live {
		liveBy[live[i].SessionID] = &live[i]
	}
	gitBy := map[string]*repos.GitState{}
	for i := range git {
		gitBy[git[i].Dir] = &git[i]
	}
	out := make([]Session, 0, len(list))
	for _, s := range list {
		out = append(out, Session{Summary: *s, Live: liveBy[s.ID], Git: gitBy[s.CWD]})
	}
	return out
}

// Source returns the engine source for a scanned machine.
func (m *Machine) Source() engine.Source {
	return engine.Source{Host: m.Name, OS: m.Facts.OS, FS: m.fs, ConfigDir: m.Facts.ConfigDir, Home: m.Facts.Home}
}

// Close releases the machine's connections.
func (m *Machine) Close() {
	if rfs, ok := m.fs.(*transport.RemoteFS); ok {
		_ = rfs.Close()
	}
	if m.conn != nil {
		m.conn.Close()
	}
}

// Find locates a session on a machine by id prefix or exact/substring title match.
func (m *Machine) Find(query string) ([]*Session, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	var exact, prefix, title []*Session
	for i := range m.Sessions {
		s := &m.Sessions[i]
		switch {
		case s.ID == query:
			exact = append(exact, s)
		case strings.HasPrefix(s.ID, query) && len(query) >= 4:
			prefix = append(prefix, s)
		case strings.ToLower(s.Title) == q:
			exact = append(exact, s)
		case q != "" && strings.Contains(strings.ToLower(s.Title), q):
			title = append(title, s)
		}
	}
	for _, set := range [][]*Session{exact, prefix, title} {
		if len(set) > 0 {
			return set, nil
		}
	}
	return nil, errors.New("no session matches " + query)
}

// Group is sessions of one repository across machines.
type Group struct {
	Identity string         `json:"identity"`
	Name     string         `json:"name"`
	Remote   string         `json:"remote,omitempty"`
	Local    string         `json:"localCheckout,omitempty"`
	Entries  []GroupedEntry `json:"entries"`
}

// GroupedEntry is one session in a group.
type GroupedEntry struct {
	Machine string   `json:"machine"`
	Session *Session `json:"session"`
}

// GroupByRepo groups sessions by repository identity; sessions outside git go to a
// "no repository" group (identity ""). localRoots are searched for local checkouts.
func GroupByRepo(machines []*Machine, localRoots []string) []Group {
	idx := map[string]int{}
	var groups []Group
	for _, m := range machines {
		for i := range m.Sessions {
			s := &m.Sessions[i]
			id, remote, name := "", "", "No repository"
			if s.Git != nil && s.Git.Identity != "" {
				id, remote, name = s.Git.Identity, s.Git.Remote, repos.Name(s.Git.Identity)
			} else if s.Git != nil && s.Git.IsRepo {
				id, name = "local:"+m.Name+":"+s.Git.Toplevel, filepath.Base(s.Git.Toplevel)+" (no remote)"
			}
			gi, ok := idx[id]
			if !ok {
				g := Group{Identity: id, Name: name, Remote: remote}
				if id != "" && !strings.HasPrefix(id, "local:") {
					if found := repos.FindLocal(id, localRoots); len(found) > 0 {
						g.Local = found[0].Path
					}
				}
				groups = append(groups, g)
				gi = len(groups) - 1
				idx[id] = gi
			}
			groups[gi].Entries = append(groups[gi].Entries, GroupedEntry{Machine: m.Name, Session: s})
		}
	}
	for i := range groups {
		sort.SliceStable(groups[i].Entries, func(a, b int) bool {
			return groups[i].Entries[a].Session.LastActivity.After(groups[i].Entries[b].Session.LastActivity)
		})
	}
	sort.SliceStable(groups, func(a, b int) bool {
		if (groups[a].Identity == "") != (groups[b].Identity == "") {
			return groups[b].Identity == ""
		}
		return groups[a].Entries[0].Session.LastActivity.After(groups[b].Entries[0].Session.LastActivity)
	})
	return groups
}

var routesMu sync.Mutex

// routes remembers which host name reached a destination last time (e.g. its Tailscale
// name when the alias points at an unresolvable .local name), saving a failed lookup.
func loadRoutes(stateDir string) map[string]string {
	routesMu.Lock()
	defer routesMu.Unlock()
	m := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(stateDir, "routes.json")); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func saveRoute(stateDir, dest, via string) {
	routesMu.Lock()
	defer routesMu.Unlock()
	p := filepath.Join(stateDir, "routes.json")
	m := map[string]string{}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	if via == "" {
		delete(m, dest)
	} else {
		m[dest] = via
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = os.MkdirAll(stateDir, 0o700)
	_ = os.WriteFile(p, b, 0o600)
}
