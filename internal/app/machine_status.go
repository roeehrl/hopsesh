package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/core/serve"
	"github.com/roeehrl/hopsesh/internal/version"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// What hopsesh can do on each machine, check by check: whether it is installed and how its
// version compares with the newest on your machines, whether it runs in the login session
// and receives sessions, whether its command is on PATH, and each agent's hook and skill.
// One model for the app, the command line, the TUI and the planner ("can a session moved
// from there block its original?").

// States of a check. Gray (off, unknown) is never a failure; warn works but needs
// attention (an older hopsesh, an outdated skill); error does not work.
const (
	CheckOK      = "ok"
	CheckWarn    = "warn"
	CheckError   = "error"
	CheckOff     = "off"
	CheckUnknown = "unknown"
)

// Fixes a check can offer (Check.Fix): the action that puts it right.
const (
	FixSetUp   = "setup"   // set hopsesh up there (hosts setup)
	FixUpdate  = "update"  // update hopsesh there (hosts setup)
	FixReceive = "receive" // turn receiving on
	FixHooks   = "hooks"   // install the movement hooks
	FixSkill   = "skill"   // install or update the skill
	FixCLI     = "cli"     // put the hopsesh command on PATH
	FixApprove = "approve" // the user approves the hook in the agent (hopsesh never does)
)

// MinBlockVersion is the first hopsesh that can block or warn an original a 0.6+ hopsesh
// moved: older ones cannot read the lineage files (lineage.FileFormat) that say where the
// session went.
const MinBlockVersion = lineage.FirstFileFormatVersion

// Check is one thing hopsesh checked on a machine.
type Check struct {
	State  string `json:"state"`            // Check*
	Text   string `json:"text"`             // what it found, in a few words
	Reason string `json:"reason,omitempty"` // why it is not ok
	Fix    string `json:"fix,omitempty"`    // Fix*: what puts it right
}

func (c Check) ok() bool { return c.State == CheckOK }

// AgentSetup is one agent folder's hook and skill on a machine.
type AgentSetup struct {
	Agent        agent.ID `json:"agent"`
	Name         string   `json:"name"`              // "Claude Code"
	Profile      string   `json:"profile,omitempty"` // "" for the default folder
	ProfileLabel string   `json:"profileLabel,omitempty"`
	Hook         Check    `json:"hook"`
	Skill        Check    `json:"skill"`
}

// MachineStatus is what hopsesh can do on one machine.
type MachineStatus struct {
	Machine string `json:"machine"`
	Local   bool   `json:"local"`
	// Version is hopsesh's version there ("" when it is not installed); Newest is the
	// newest on any of your machines, and NewestOn the machine that has it.
	Version   string       `json:"version,omitempty"`
	Newest    string       `json:"newest,omitempty"`
	NewestOn  string       `json:"newestOn,omitempty"`
	Reach     Check        `json:"reach"`
	Hopsesh   Check        `json:"hopsesh"`
	Login     Check        `json:"login"`
	Receiving Check        `json:"receiving"`
	CLI       Check        `json:"cli"`
	Agents    []AgentSetup `json:"agents"`
	// Todo counts the checks that have a fix; Total every check there is.
	Todo  int `json:"todo"`
	Total int `json:"total"`
	// CheckedAt is when the machine was reached; DetailsAt when its hooks, skill and
	// command were read (they are read less often). Stale: they are older than
	// statusStale and could not be read again, so they are shown as unknown.
	CheckedAt time.Time `json:"checkedAt"`
	DetailsAt time.Time `json:"detailsAt,omitempty"`
	Stale     bool      `json:"stale,omitempty"`
}

// How long a machine's hooks, skill and command are trusted before they are read again,
// and after how long a reading that could not be repeated turns unknown.
const (
	statusDetailsTTL = 10 * time.Minute
	statusStale      = 3 * statusDetailsTTL
)

// CanBlock reports whether a session of agent (in profile) moved from this machine can
// block or warn its original there, and why not: hopsesh there must be new enough to read
// where the session went, and that agent folder's hook must be installed.
func (s MachineStatus) CanBlock(id agent.ID, profile string) (bool, []string) {
	var why []string
	switch {
	case s.Version == "":
		why = append(why, "hopsesh isn't installed there")
	case !atLeast(s.Version, MinBlockVersion):
		why = append(why, fmt.Sprintf("hopsesh there is %s; blocking needs %s or newer", s.Version, MinBlockVersion))
	}
	if s.Version != "" {
		hook, found := s.hookFor(id, profile)
		switch {
		case !found && s.DetailsAt.IsZero():
			why = append(why, "its hooks haven't been checked yet")
		case !found:
			why = append(why, "that agent folder has no hook there")
		case hook.State == CheckUnknown:
			why = append(why, "its hook couldn't be checked: "+nonEmptyStr(hook.Reason, "unknown"))
		case hook.State == CheckOff || hook.State == CheckError:
			why = append(why, "its hook isn't installed: "+nonEmptyStr(hook.Reason, hook.Text))
		}
	}
	return len(why) == 0, why
}

// hookFor is the hook of agent id's folder for profile: the folder with that profile, else
// the agent's only folder, else its default one (another machine names its profiles itself).
func (s MachineStatus) hookFor(id agent.ID, profile string) (Check, bool) {
	var mine []AgentSetup
	for _, a := range s.Agents {
		if a.Agent != id {
			continue
		}
		if a.Profile == profile && profile != "" {
			return a.Hook, true
		}
		mine = append(mine, a)
	}
	if len(mine) == 1 {
		return mine[0].Hook, true
	}
	for _, a := range mine {
		if a.ProfileLabel == "" {
			return a.Hook, true
		}
	}
	return Check{}, false
}

// Guard is what a machine's checks say about blocking or warning a session of key's agent
// left there once it moves. nil when nothing more than whether hopsesh is there is known
// (its hooks were never read), so the planner decides from that alone.
func (a *App) Guard(ctx context.Context, src *Machine, key agent.SessionKey) *move.Guard {
	if src == nil || src.host == nil {
		return nil
	}
	ss := a.MachineStatuses(ctx, &Inventory{Machines: []*Machine{src}}, StatusOptions{})
	if len(ss) == 0 {
		return nil
	}
	s := ss[0]
	ok, why := s.CanBlock(key.Agent, key.Profile)
	if s.DetailsAt.IsZero() && (s.Version == "" || atLeast(s.Version, MinBlockVersion)) {
		return nil
	}
	return &move.Guard{Ready: ok, Why: why}
}

// atLeast compares hopsesh versions; a development build counts as the newest.
func atLeast(v, min string) bool {
	if v == "dev" || strings.HasPrefix(v, "dev") {
		return true
	}
	c, ok := agent.CompareVersions(v, min)
	return ok && c >= 0
}

// newer reports whether a is a newer hopsesh than b (development builds never are: they
// are not a baseline anyone can update to).
func newer(a, b string) bool {
	if a == "" || a == "installed" || strings.HasPrefix(a, "dev") {
		return false
	}
	if b == "" || b == "installed" || strings.HasPrefix(b, "dev") {
		return true
	}
	c, ok := agent.CompareVersions(a, b)
	return ok && c > 0
}

// Newest is the newest hopsesh among ss and the machine that has it.
func Newest(ss []MachineStatus) (string, string) {
	v, on := "", ""
	for _, s := range ss {
		if newer(s.Version, v) {
			v, on = s.Version, s.Machine
		}
	}
	return v, on
}

// statusDetails are the slower readings of a machine, cached in the state folder.
type statusDetails struct {
	Version string       `json:"version"` // hopsesh there when they were read
	CLI     Check        `json:"cli"`
	Agents  []AgentSetup `json:"agents"`
	At      time.Time    `json:"at"`
}

type statusCache struct {
	mu   sync.Mutex
	path string
}

func (a *App) statusCache() *statusCache {
	return &statusCache{path: filepath.Join(a.StateDir, "machine-status.json")}
}

var statusFileMu sync.Mutex

func (c *statusCache) load() map[string]statusDetails {
	statusFileMu.Lock()
	defer statusFileMu.Unlock()
	out := map[string]statusDetails{}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (c *statusCache) save(name string, d statusDetails) {
	statusFileMu.Lock()
	defer statusFileMu.Unlock()
	all := map[string]statusDetails{}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, &all)
	}
	all[name] = d
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(c.path), 0o700)
	tmp := c.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, c.path)
	}
}

// StatusOptions control MachineStatuses.
type StatusOptions struct {
	// Refresh reads every machine's hooks, skill and command again, however recent.
	Refresh bool
	// CachedOnly never reads them: what was read last, as it is (for a quick listing).
	CachedOnly bool
}

// MachineStatuses is what hopsesh can do on each machine of inv, newest version first
// considered. Hooks, skill and command are read again when older than statusDetailsTTL
// (or with Refresh), in parallel; everything else comes from the scan.
func (a *App) MachineStatuses(ctx context.Context, inv *Inventory, o StatusOptions) []MachineStatus {
	cache := a.statusCache()
	cached := cache.load()
	out := make([]MachineStatus, len(inv.Machines))
	var wg sync.WaitGroup
	for i, m := range inv.Machines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, have := cached[m.Name]
			fresh := have && time.Since(d.At) < statusDetailsTTL && d.Version == m.Hopsesh
			if m.reached() && !o.CachedOnly && (o.Refresh || !fresh) {
				if nd, ok := a.readStatusDetails(ctx, m); ok {
					d, have = nd, true
					cache.save(m.Name, d)
				}
			}
			out[i] = a.machineStatus(m, d, have)
		}()
	}
	wg.Wait()
	newest, on := Newest(out)
	for i := range out {
		out[i].Newest, out[i].NewestOn = newest, on
		out[i].Hopsesh = hopseshCheck(out[i], out[i].Hopsesh)
		out[i].count()
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Local && !out[j].Local })
	return out
}

// reached reports whether the scan reached the machine (its connection is there).
func (m *Machine) reached() bool { return m.Status == StatusOK && (m.host != nil || !m.Local) }

// machineStatus puts a machine's scan and its cached details together.
func (a *App) machineStatus(m *Machine, d statusDetails, have bool) MachineStatus {
	s := MachineStatus{Machine: m.Name, Local: m.Local, Version: m.Hopsesh, CheckedAt: m.CheckedAt, Agents: []AgentSetup{}}
	if s.CheckedAt.IsZero() {
		s.CheckedAt = time.Now().UTC()
	}
	if m.Local {
		s.Version = version.Version
	}
	switch {
	case m.Status == StatusOK:
		s.Reach = Check{State: CheckOK, Text: map[bool]string{true: "this machine", false: "reached over SSH"}[m.Local]}
	case m.Status == "":
		s.Reach = Check{State: CheckUnknown, Text: "not checked yet"}
	default:
		s.Reach = Check{State: CheckError, Text: "not reached", Reason: nonEmptyStr(m.Error, m.Hint)}
	}
	reached := m.Status == StatusOK
	// hopsesh itself; its version against the newest is set once every machine is in.
	switch {
	case !reached && s.Version == "":
		s.Hopsesh = Check{State: CheckUnknown, Text: "not checked"}
	case s.Version == "":
		s.Hopsesh = Check{State: CheckOff, Text: "not installed", Reason: "sessions can still be brought from it over SSH", Fix: FixSetUp}
	default:
		s.Hopsesh = Check{State: CheckOK, Text: s.Version}
	}
	// The login session, and receiving.
	switch {
	case s.Version == "":
		s.Login = Check{State: CheckOff, Text: "–"}
		s.Receiving = Check{State: CheckOff, Text: "can't receive", Reason: "hopsesh isn't installed there"}
	case !reached:
		s.Login = Check{State: CheckUnknown, Text: "not checked"}
		s.Receiving = Check{State: CheckUnknown, Text: "not checked"}
	default:
		s.Login, s.Receiving = m.loginChecks(a)
	}
	// Hooks, skill and command, as last read.
	if s.Version == "" {
		s.CLI = Check{State: CheckOff, Text: "–"}
		return s
	}
	if !have {
		s.CLI = Check{State: CheckUnknown, Text: "not checked yet"}
		for _, st := range m.Agents {
			if st.Install.Present {
				s.Agents = append(s.Agents, AgentSetup{Agent: st.Agent, Name: st.Name, Profile: st.Install.ProfileID(), ProfileLabel: profileLabel(st.Install.Profile),
					Hook: Check{State: CheckUnknown, Text: "not checked yet"}, Skill: Check{State: CheckUnknown, Text: "not checked yet"}})
			}
		}
		return s
	}
	s.CLI, s.Agents, s.DetailsAt = d.CLI, append([]AgentSetup{}, d.Agents...), d.At
	if time.Since(d.At) > statusStale || !reached && time.Since(d.At) > statusDetailsTTL {
		s.Stale = true
		ago := "checked " + d.At.Local().Format("Jan 2 15:04")
		s.CLI = staled(s.CLI, ago)
		for i := range s.Agents {
			s.Agents[i].Hook, s.Agents[i].Skill = staled(s.Agents[i].Hook, ago), staled(s.Agents[i].Skill, ago)
		}
	}
	return s
}

// staled is a check whose reading is too old to be shown as it was.
func staled(c Check, ago string) Check {
	if c.State == CheckOK {
		return Check{State: CheckUnknown, Text: c.Text, Reason: "last seen like this; " + ago}
	}
	return c
}

// loginChecks are whether hopsesh runs in the login session there and receives sessions.
func (m *Machine) loginChecks(a *App) (Check, Check) {
	if m.Local {
		listening := m.listening
		login := Check{State: CheckOff, Text: "not running", Reason: "hopsesh serve isn't running here; hopsesh serve install starts it at each login"}
		if listening {
			login = Check{State: CheckOK, Text: "in your login session"}
		}
		switch {
		case !a.Cfg.Peer.Receive:
			return login, Check{State: CheckOff, Text: "off", Reason: "receiving is turned off", Fix: FixReceive}
		case !listening:
			return login, Check{State: CheckOff, Text: "on, not listening", Reason: login.Reason}
		}
		return login, Check{State: CheckOK, Text: "on, listening"}
	}
	switch m.Presence {
	case PresenceSSH:
		return Check{State: CheckOff, Text: "–"}, Check{State: CheckOff, Text: "can't receive", Reason: "hopsesh isn't installed there"}
	case PresenceOlder:
		why := fmt.Sprintf("hopsesh %s there is too old to talk to this one", m.Hopsesh)
		return Check{State: CheckError, Text: "too old", Reason: why, Fix: FixUpdate}, Check{State: CheckError, Text: "can't receive", Reason: why, Fix: FixUpdate}
	case PresenceLogin:
		login := Check{State: CheckOK, Text: "in your login session"}
		if m.receive != nil && !*m.receive {
			return login, Check{State: CheckOff, Text: "off", Reason: "receiving is turned off there (hopsesh receive on)", Fix: FixReceive}
		}
		return login, Check{State: CheckOK, Text: "on, listening"}
	}
	why := nonEmptyStr(m.PresenceWhy, "hopsesh isn't running in a login session there")
	if strings.Contains(why, "not in a login session") || strings.Contains(why, "nobody") {
		why = "nobody is logged in there, or hopsesh isn't set up to start at login"
	}
	login := Check{State: CheckOff, Text: "not running", Reason: why}
	if m.receive != nil && !*m.receive {
		return login, Check{State: CheckOff, Text: "off", Reason: "receiving is turned off there", Fix: FixReceive}
	}
	return login, Check{State: CheckOff, Text: "not listening", Reason: "until hopsesh runs in a login session there"}
}

// hopseshCheck compares a machine's hopsesh with the newest on your machines.
func hopseshCheck(s MachineStatus, c Check) Check {
	if s.Version == "" || c.State != CheckOK {
		return c
	}
	if strings.HasPrefix(s.Version, "dev") {
		return Check{State: CheckOK, Text: s.Version, Reason: "a development build: not compared with releases"}
	}
	if s.Newest != "" && newer(s.Newest, s.Version) {
		c = Check{State: CheckWarn, Text: s.Version, Reason: fmt.Sprintf("%s is the newest, on %s", s.Newest, s.NewestOn), Fix: FixUpdate}
		if !atLeast(s.Version, peer.FirstVersion) {
			c.State, c.Reason = CheckError, c.Reason+"; this one is too old to talk to it"
		}
	}
	return c
}

// count tallies the checks and those that have a fix.
func (s *MachineStatus) count() {
	s.Todo, s.Total = 0, 0
	add := func(c Check) {
		if c.State == "" {
			return
		}
		s.Total++
		if c.Fix != "" && !c.ok() {
			s.Todo++
		}
	}
	for _, c := range []Check{s.Hopsesh, s.Receiving, s.CLI} {
		add(c)
	}
	for _, a := range s.Agents {
		add(a.Hook)
		add(a.Skill)
	}
}

// readStatusDetails reads a machine's hooks, skill and command: on this machine directly,
// through hopsesh in the login session there when it runs (it answers exactly), else from
// the files over SSH. false: nothing could be read (an SSH-only machine).
func (a *App) readStatusDetails(ctx context.Context, m *Machine) (statusDetails, bool) {
	d := statusDetails{Version: m.Hopsesh, At: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	switch {
	case m.Local:
		d.Version = version.Version
		st := integrate.CheckCLI()
		d.CLI = cliCheck(st.Resolves, st.Path)
		d.Agents = a.protectionSetups(a.LocalProtection(ctx), m)
		return d, true
	case m.Hopsesh == "":
		return d, false
	}
	// A connection of its own: a long-lived inventory's may have been closed since.
	hm, err := a.accountMachine(ctx, m.Name)
	if err != nil {
		return d, false
	}
	defer hm.Close()
	d.CLI = remoteCLI(ctx, hm)
	if m.Presence == PresenceLogin {
		if p, err := a.MachineProtection(ctx, m.Name); err == nil {
			d.Agents = a.protectionSetups(p, m)
			return d, true
		}
	}
	d.Agents = a.fileSetups(ctx, hm, m)
	return d, true
}

func cliCheck(resolves, fallback string) Check {
	if resolves != "" {
		return Check{State: CheckOK, Text: "on PATH · " + tildePath(resolves)}
	}
	return Check{State: CheckOff, Text: "not on PATH", Reason: nonEmptyStr(tildePath(fallback), "hopsesh") + " isn't found by your shell", Fix: FixCLI}
}

// tildePath shortens a path in a home folder to ~/….
func tildePath(p string) string {
	if p == "" {
		return ""
	}
	for _, home := range []string{"/Users/", "/home/"} {
		if strings.HasPrefix(p, home) {
			if i := strings.Index(p[len(home):], "/"); i >= 0 {
				return "~" + p[len(home)+i:]
			}
		}
	}
	return p
}

// remoteCLI asks the user's own shell there (interactive, as they use it) where hopsesh
// is: an SSH command runs a plain shell, whose PATH often lacks ~/.local/bin.
func remoteCLI(ctx context.Context, hm *host.Machine) Check {
	installed := hm.Facts.Binaries[host.Hopsesh.Name].Path
	if hm.Facts.OS == "windows" {
		// The probe asked PowerShell, which reads the user's PATH.
		return cliCheck(installed, installed)
	}
	r, err := hm.Exec().Run(ctx, []string{"sh", "-c", loginWhich}, agent.RunOptions{Timeout: 10 * time.Second})
	if err != nil {
		return Check{State: CheckUnknown, Text: "not checked", Reason: err.Error()}
	}
	got := ""
	for _, l := range strings.Split(strings.TrimSpace(string(r.Stdout)), "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "/") {
			got = l
		}
	}
	return cliCheck(got, installed)
}

// loginWhich prints where the user's own shell, started as at a terminal, finds hopsesh.
const loginWhich = `s="${SHELL:-/bin/sh}"; "$s" -lic 'command -v hopsesh' </dev/null 2>/dev/null | tail -n 1`

// protectionSetups turns hopsesh's own report of a machine's hooks and skill into one
// line per agent folder.
func (a *App) protectionSetups(p Protection, m *Machine) []AgentSetup {
	out := []AgentSetup{}
	index := map[string]int{}
	for _, h := range p.Hooks {
		key := string(h.Agent) + "\x00" + h.Profile
		if _, dup := index[key]; dup {
			continue
		}
		index[key] = len(out)
		out = append(out, AgentSetup{Agent: h.Agent, Name: a.agentName(h.Agent), Profile: h.Profile, ProfileLabel: h.ProfileLabel, Hook: hookCheck(h), Skill: Check{State: CheckUnknown, Text: "not checked"}})
	}
	// The skill report names the agents each copy serves ("Claude Code", "Claude Code (work)").
	for _, c := range p.Skill.Copies {
		sk := skillCheck(c.Status.State, c.Status.Version)
		for _, name := range c.Agents {
			for i := range out {
				if skillServes(name, out[i]) {
					out[i].Skill = sk
				}
			}
		}
	}
	// An agent folder without a hook (its agent takes none) still has its skill.
	for _, st := range m.Agents {
		if !st.Install.Present {
			continue
		}
		key := string(st.Agent) + "\x00" + st.Install.ProfileID()
		if _, ok := index[key]; ok {
			continue
		}
		if p.Local || p.Hooks != nil {
			if _, ok := a.Module(st.Agent); ok {
				// Listed by the module but absent from the report: the agent takes no hook.
				as := AgentSetup{Agent: st.Agent, Name: st.Name, Profile: st.Install.ProfileID(), ProfileLabel: profileLabel(st.Install.Profile),
					Hook: Check{State: CheckOff, Text: "not supported"}, Skill: Check{State: CheckUnknown, Text: "not checked"}}
				for _, c := range p.Skill.Copies {
					for _, name := range c.Agents {
						if skillServes(name, as) {
							as.Skill = skillCheck(c.Status.State, c.Status.Version)
						}
					}
				}
				index[key] = len(out)
				out = append(out, as)
			}
		}
	}
	return out
}

func skillServes(name string, as AgentSetup) bool {
	if as.ProfileLabel == "" {
		return name == as.Name
	}
	return name == as.Name+" ("+as.ProfileLabel+")"
}

// hookCheck is a hook's state as hopsesh there reported it.
func hookCheck(h MovementHookStatus) Check {
	switch {
	case !h.Supported:
		return Check{State: CheckOff, Text: "not supported", Reason: h.Reason}
	case !h.Installed:
		return Check{State: CheckOff, Text: "not installed", Reason: "originals moved from here aren't blocked or warned", Fix: FixHooks}
	case h.Trust != nil && (h.Trust.State == agent.HookNeedsReview || h.Trust.State == agent.HookDisabled):
		return Check{State: CheckWarn, Text: "approve in " + nonEmptyStr(agentDisplay(h.Agent), string(h.Agent)), Reason: nonEmptyStr(h.Trust.Fix, "the agent runs it only once you approve it"), Fix: FixApprove}
	case !h.Enabled && h.Reason != "":
		return Check{State: CheckWarn, Text: "installed, turned off", Reason: h.Reason}
	case h.Trust != nil && h.Trust.State == agent.HookTrusted:
		return Check{State: CheckOK, Text: "installed · trusted"}
	}
	return Check{State: CheckOK, Text: "installed"}
}

func agentDisplay(id agent.ID) string {
	return map[agent.ID]string{"claude": "Claude Code", "codex": "Codex"}[id]
}

// skillCheck is a skill copy's state.
func skillCheck(state, installed string) Check {
	switch state {
	case integrate.Current:
		return Check{State: CheckOK, Text: "installed"}
	case integrate.Stale:
		return Check{State: CheckWarn, Text: "outdated", Reason: "written by hopsesh " + nonEmptyStr(installed, "an older version"), Fix: FixSkill}
	case integrate.Absent, "":
		return Check{State: CheckOff, Text: "not installed", Reason: "this agent doesn't know hopsesh's commands", Fix: FixSkill}
	case integrate.Modified:
		return Check{State: CheckWarn, Text: "edited", Reason: "you edited it; hopsesh leaves it as it is"}
	case integrate.Foreign:
		return Check{State: CheckWarn, Text: "not hopsesh's", Reason: "a skill called hopsesh that hopsesh didn't write"}
	}
	return Check{State: CheckError, Text: state, Reason: "the skill's files are incomplete", Fix: FixSkill}
}

// noticeHookIn finds a hopsesh notice hook for agent id in a hook file, and the program
// it runs ("" when it has none).
var noticeHookIn = regexp.MustCompile(`'((?:[^'\\]|\\.)+)' notice-hook --agent '([a-z0-9-]+)'`)

// fileSetups reads each agent folder's hook file and skill over SSH, for a machine whose
// hopsesh does not run in the login session (so it cannot answer for itself). A hook is
// the hopsesh notice hook for that agent, running a program that is there; agent trust
// (Codex) is only known to hopsesh there.
func (a *App) fileSetups(ctx context.Context, hm *host.Machine, m *Machine) []AgentSetup {
	out := []AgentSetup{}
	fsys, err := hm.FS(ctx)
	if err != nil {
		return out
	}
	bin := nonEmptyStr(hm.Facts.Binaries[host.Hopsesh.Name].Path, "hopsesh")
	for _, st := range m.Agents {
		if !st.Install.Present {
			continue
		}
		mod, ok := a.Module(st.Agent)
		if !ok {
			continue
		}
		in := st.Install
		as := AgentSetup{Agent: st.Agent, Name: st.Name, Profile: in.ProfileID(), ProfileLabel: profileLabel(in.Profile),
			Hook: Check{State: CheckOff, Text: "not supported"}, Skill: Check{State: CheckOff, Text: "not supported"}}
		h, err := hm.For(ctx, mod.Spec(), in, nil)
		if err != nil {
			as.Hook = Check{State: CheckUnknown, Text: "not checked", Reason: err.Error()}
			out = append(out, as)
			continue
		}
		if hi, ok := mod.(agent.MovementHookIntegrator); ok {
			if f := hi.MovementHooks(h, in, bin).File; f != nil {
				as.Hook = remoteHook(fsys, f.Path, st.Agent)
			}
		}
		if ig, ok := mod.(agent.Integrator); ok {
			if dir := ig.Integration(h, in).SkillDir; dir != "" {
				as.Skill = remoteSkill(fsys, h.Path().Join(dir, integrate.SkillName), m.Hopsesh)
			}
		}
		out = append(out, as)
	}
	return out
}

// remoteHook reads one hook file over SSH.
func remoteHook(fsys host.FS, path string, id agent.ID) Check {
	b, err := fsys.ReadFile(path, 1<<20)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return Check{State: CheckOff, Text: "not installed", Reason: "originals moved from here aren't blocked or warned", Fix: FixHooks}
	case err != nil:
		return Check{State: CheckUnknown, Text: "not checked", Reason: err.Error()}
	}
	for _, m := range noticeHookIn.FindAllSubmatch(bytes.ReplaceAll(b, []byte(`\"`), []byte(`"`)), -1) {
		if string(m[2]) != string(id) {
			continue
		}
		prog := string(m[1])
		if _, err := fsys.Stat(prog); err != nil {
			return Check{State: CheckError, Text: "broken", Reason: "it runs " + tildePath(prog) + ", which isn't there", Fix: FixHooks}
		}
		if id == "codex" {
			return Check{State: CheckOK, Text: "installed · trust checked at login"}
		}
		return Check{State: CheckOK, Text: "installed"}
	}
	return Check{State: CheckOff, Text: "not installed", Reason: "originals moved from here aren't blocked or warned", Fix: FixHooks}
}

// remoteSkill reads one skill copy's install record over SSH and compares the version
// that wrote it with hopsesh there (it would write its own).
func remoteSkill(fsys host.FS, dir, there string) Check {
	b, err := fsys.ReadFile(dir+"/"+integrate.RecordFile, 1<<16)
	if errors.Is(err, os.ErrNotExist) {
		if _, serr := fsys.Stat(dir); serr == nil {
			return skillCheck(integrate.Foreign, "")
		}
		return skillCheck(integrate.Absent, "")
	}
	if err != nil {
		return Check{State: CheckUnknown, Text: "not checked", Reason: err.Error()}
	}
	var rec integrate.Record
	if json.Unmarshal(b, &rec) != nil {
		return skillCheck(integrate.Foreign, "")
	}
	if _, err := fsys.Stat(dir + "/SKILL.md"); err != nil {
		return skillCheck(integrate.Broken, rec.Version)
	}
	if newer(there, rec.Version) {
		return skillCheck(integrate.Stale, rec.Version)
	}
	return skillCheck(integrate.Current, rec.Version)
}

// serveRunning reports whether hopsesh serve answers on this machine (it listens for
// other machines in the login session).
func serveRunning(ctx context.Context) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return serve.Running(ctx, home) != ""
}

// Receiving reports whether hopsesh on m runs in the login session there and accepts
// sessions (the green mark beside a machine).
func (a *App) Receiving(m *Machine) bool {
	if m.Status != StatusOK {
		return false
	}
	_, r := m.loginChecks(a)
	return r.State == CheckOK
}

// HopseshVersion is hopsesh's version on m: this program's own on this machine.
func (m *Machine) HopseshVersion() string {
	if m.Local {
		return version.Version
	}
	return m.Hopsesh
}

// NewestHopsesh is the newest hopsesh on inv's machines ("" when none is a release).
func NewestHopsesh(inv *Inventory) string {
	v := ""
	for _, m := range inv.Machines {
		if newer(m.HopseshVersion(), v) {
			v = m.HopseshVersion()
		}
	}
	return v
}

// Behind reports whether version is older than newest (development builds never are).
func Behind(version, newest string) bool {
	return version != "" && !strings.HasPrefix(version, "dev") && newer(newest, version)
}
