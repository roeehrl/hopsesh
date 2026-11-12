package gui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/appicon"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// GuardDTO is how the copy left behind by a move is protected, as the app shows it.
// Effective is false when hopsesh cannot make the agent honor the choice there: the hook
// is not installed, the agent has not trusted it (Codex), or it cannot be verified.
type GuardDTO struct {
	Mode string `json:"mode"` // block | advise | off | released
	// State is whether the hook on Machine keeps this copy from going on now: protected,
	// unprotected, unknown, off or released. Effective is State "protected".
	State     string `json:"state"`
	Effective bool   `json:"effective"`
	// Checking: the answer is being read now (a saved row, or that machine's hooks). It
	// ends in a definite State, or in unknown with Problem saying why.
	Checking bool `json:"checking,omitempty"`
	// Problem is a full sentence whenever State is unprotected or unknown; Fix says what
	// puts it right, and Action which button the window offers for it: setup (install the
	// hooks on Machine now), update (hopsesh there), approve (the hook, in the agent there),
	// machines (open Machines) or settings (Settings › General).
	Problem string `json:"problem,omitempty"`
	Fix     string `json:"fix,omitempty"`
	Action  string `json:"action,omitempty"`
	// Machine is the machine whose hook decides (the copy's own), and CheckedAt when its
	// hooks were last read (zero: never).
	Machine   string    `json:"machine,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitzero"`
	Operation string    `json:"operation,omitempty"`
	// NoHopsesh: the original is on a machine without hopsesh, where nothing can protect it.
	NoHopsesh bool `json:"noHopsesh,omitempty"`
}

// Guard states (GuardDTO.State); off and released are the modes' own.
const (
	guardProtected   = "protected"
	guardUnprotected = "unprotected"
	guardUnknown     = "unknown"
)

// Guard fixes (GuardDTO.Action).
const (
	actionSetUp    = "setup"
	actionUpdate   = "update"
	actionApprove  = "approve"
	actionMachines = "machines"
	actionSettings = "settings"
)

// HookHealthDTO is what keeps movement protection and the skill working on this machine.
type HookHealthDTO struct {
	CLI      integrate.CLIStatus      `json:"cli"`
	CLIReady bool                     `json:"cliReady"` // the hopsesh command is installed and works
	Hooks    []app.MovementHookStatus `json:"hooks"`
	Problems []HookProblem            `json:"problems"`
	Mode     string                   `json:"mode"` // config original
	Checked  time.Time                `json:"checked"`
	// Fingerprint names this set of problems; Dismissed: the user said "Not now" to it.
	Fingerprint string `json:"fingerprint"`
	Dismissed   bool   `json:"dismissed"`
}

// HookProblem is one agent profile whose protection hook cannot protect originals.
type HookProblem struct {
	Agent   agent.ID `json:"agent"`
	Profile string   `json:"profile"`
	Label   string   `json:"label"`
	State   string   `json:"state"` // not-installed | unsupported | needs-review | disabled | missing | unknown | settings
	Message string   `json:"message"`
	Fix     string   `json:"fix,omitempty"`
}

var health struct {
	mu      sync.Mutex
	dto     *HookHealthDTO
	app     *App // the app the check was for: another one (a fresh start) checks again
	reading int  // checks running now
}

// CLIReady reports whether the hopsesh command is installed where the skill and hooks
// can run it (a link to this or another copy of the app, or a separate install).
func CLIReady(st integrate.CLIStatus) bool {
	return st.State == integrate.CLIOurs || st.State == integrate.CLIOtherApp || st.State == integrate.CLIStandalone ||
		st.State != integrate.CLIDangling && integrate.LookLoginPath("hopsesh") != ""
}

// HookHealth checks the hopsesh command and every local protection hook, asking agents that
// review hooks (Codex) whether they trust them. force rechecks now; otherwise a check
// from the last five minutes is reused.
func (a *App) HookHealth(force bool) (HookHealthDTO, error) {
	health.mu.Lock()
	if !force && health.dto != nil && health.app == a && time.Since(health.dto.Checked) < 5*time.Minute {
		d := *health.dto
		health.mu.Unlock()
		return a.withDismissal(d), nil
	}
	health.reading++
	health.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	core := a.snapshot()
	d := HookHealthDTO{CLI: integrate.CheckCLI(), Mode: core.Cfg.OriginalGuard(), Checked: time.Now()}
	d.CLIReady = CLIReady(d.CLI)
	hooks, err := core.NoticeHooks(ctx)
	d.Hooks = hooks
	d.Problems = hookProblems(hooks, d.Mode)
	if err != nil && len(hooks) == 0 && d.CLIReady { // without the command, that card says it
		d.Problems = append(d.Problems, HookProblem{State: "unknown", Message: "hopsesh could not check the protection hooks: " + err.Error()})
	}
	d.Fingerprint = problemFingerprint(d)
	health.mu.Lock()
	before := health.dto
	health.dto, health.app = &d, a
	health.reading--
	health.mu.Unlock()
	// The originals here show what this check found: the first one, or a change.
	if before == nil || hooksPrint(*before) != hooksPrint(d) {
		go a.guardsChanged()
	}
	return a.withDismissal(d), nil
}

// hooksPrint is what of a check decides the protection of the originals here.
func hooksPrint(d HookHealthDTO) string {
	var keys []string
	for _, p := range d.Problems {
		keys = append(keys, "p\x00"+string(p.Agent)+"\x00"+p.Profile+"\x00"+p.State)
	}
	for _, h := range d.Hooks {
		if h.Installed {
			keys = append(keys, "h\x00"+string(h.Agent)+"\x00"+h.Profile)
		}
	}
	sort.Strings(keys)
	return fmt.Sprint(keys)
}

// problemFingerprint names what protection still needs: the command, then each agent
// profile's problem. Wording and check times do not change it.
func problemFingerprint(d HookHealthDTO) string {
	var keys []string
	if !d.CLIReady {
		keys = append(keys, "cli")
	}
	for _, p := range d.Problems {
		keys = append(keys, string(p.Agent)+"\x00"+p.Profile+"\x00"+p.State)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(fmt.Sprint(keys)))
	return hex.EncodeToString(sum[:8])
}

func (a *App) withDismissal(d HookHealthDTO) HookHealthDTO {
	a.mu.Lock()
	defer a.mu.Unlock()
	d.Dismissed = d.Fingerprint != "" && a.core.Cfg.ProtectPrompt == d.Fingerprint
	return d
}

// DismissProtection remembers "Not now" for this set of protection problems: the card
// comes back when the set changes (a new agent, a hook that stopped working).
func (a *App) DismissProtection(fingerprint string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.core.Cfg.ProtectPrompt = fingerprint
	return a.save()
}

// OpenAgentApp brings an agent's desktop app forward (to approve its hooks there).
func (a *App) OpenAgentApp(id string) (string, error) {
	m, ok := a.snapshot().Module(agent.ID(id))
	if !ok {
		return "", fmt.Errorf("unknown agent %s", id)
	}
	apps := m.Spec().Icon.Apps
	name := nonEmptyStr(appicon.Name(apps), m.Spec().Name)
	if appHook != nil {
		return name, appHook(name, agent.Command{})
	}
	home, _ := os.UserHomeDir()
	p := appicon.Installed(apps, home)
	if p == "" {
		return "", fmt.Errorf("hopsesh can't find the %s app on this machine", name)
	}
	return name, openApp(p)
}

func hookProblems(hooks []app.MovementHookStatus, mode string) []HookProblem {
	if mode == config.OriginalOff {
		return []HookProblem{}
	}
	out := []HookProblem{}
	for _, h := range hooks {
		p := HookProblem{Agent: h.Agent, Profile: h.Profile, Label: h.ProfileLabel}
		switch {
		case !h.Supported:
			p.State, p.Message = "unsupported", "Movement protection is not available here: "+h.Reason
		case h.Result == app.HookFailed:
			p.State, p.Message = "failed", "hopsesh could not check the protection hook: "+h.Reason
		case !h.Enabled:
			p.State, p.Message = "settings", h.Reason
		case !h.Installed:
			p.State, p.Message, p.Fix = "not-installed", "The protection hook is not installed, so originals are not protected.", "Install the hook in Settings › General."
		case h.Trust != nil && h.Trust.State != agent.HookTrusted:
			p.State, p.Fix = h.Trust.State, h.Trust.Fix
			p.Message = map[string]string{
				agent.HookNeedsReview: "The agent has not trusted the hopsesh hooks yet, so it skips them: originals are not blocked or advised.",
				agent.HookDisabled:    "The hopsesh hooks are turned off in the agent, so originals are not protected.",
				agent.HookMissing:     "The agent does not see the hopsesh hooks.",
				agent.HookUnknown:     "hopsesh could not ask the agent whether it trusts the hooks. " + h.Trust.Detail,
			}[h.Trust.State]
		default:
			continue
		}
		out = append(out, p)
	}
	return out
}

// guardFor is the protection of entry e, a copy that moved on (nil otherwise). A copy on
// another machine is protected by the hook there, as hopsesh last read it (inv's machines;
// nothing is read for it here, but a reading that is missing or too old is started).
func guardFor(core *app.App, e app.Entry, inv *app.Inventory) *GuardDTO {
	n := e.Departure
	if n == nil || n.Status == "forked" {
		return nil
	}
	g := &GuardDTO{Operation: n.Operation, Machine: e.Machine, Mode: core.OriginalGuard(e.Session.Key, n.Operation, n.Status)}
	if core.Cfg.OriginalGuard() != config.OriginalOff && core.Released(e.Session.Key, n.Operation) {
		g.Mode = "released"
	}
	if g.Mode == config.OriginalOff || g.Mode == "released" {
		g.State = g.Mode
		return g
	}
	if e.Cached {
		g.State, g.Checking, g.Problem = guardUnknown, true, "Checking current protection; this session is shown from saved information."
		return g
	}
	if e.Machine != app.LocalName() {
		var m *app.Machine
		if inv != nil {
			m = inv.Machine(e.Machine)
		}
		st, ok := machineStatusOf(core, inv, e.Machine)
		remoteGuard(g, e, m, st, ok, func() bool {
			// A reading that is missing or too old, of a machine the scan reached: read it.
			return m.Status == app.StatusOK && m.Hopsesh != "" && readHooksOf(core, m)
		})
		return g
	}
	localGuard(g, e)
	return g
}

// localGuard is the protection of a copy left on this machine, from the last check of
// its hooks (HookHealth).
func localGuard(g *GuardDTO, e app.Entry) {
	health.mu.Lock()
	d, reading := health.dto, health.reading > 0
	health.mu.Unlock()
	g.State = guardUnknown
	if d == nil {
		if reading {
			g.Checking, g.Problem = true, "Checking the agent's hooks…"
			return
		}
		g.Problem = "The protection hooks on this machine haven't been checked yet, so hopsesh can't tell whether this copy is protected."
		g.Fix, g.Action = "Check them in Settings › General.", actionSettings
		return
	}
	g.CheckedAt = d.Checked
	for _, p := range d.Problems {
		if (p.Agent == e.Agent || p.Agent == "" && p.State == agent.HookUnknown) && (p.Profile == "" || p.Profile == e.Session.Key.Profile) {
			g.Problem, g.Fix = p.Message, p.Fix
			switch p.State {
			case agent.HookUnknown, "failed":
				g.Fix, g.Action = nonEmptyStr(p.Fix, "Check again in Settings › General."), actionSettings
				return
			case "not-installed":
				g.Action = actionSetUp
			case agent.HookNeedsReview, agent.HookDisabled:
				g.Action = actionApprove
			case "unsupported":
			default:
				g.Action = actionSettings
			}
			g.State = guardUnprotected
			return
		}
	}
	for _, h := range d.Hooks {
		if h.Agent == e.Agent && h.Profile == e.Session.Key.Profile && h.Installed {
			g.State, g.Effective = guardProtected, true
			return
		}
	}
	g.State = guardUnprotected
	g.Problem, g.Fix, g.Action = "No protection hook covers this account, so the original is not protected.", "Install the hooks in Settings › General.", actionSetUp
}

// remoteGuard is the protection of a copy left on machine m, from st, what hopsesh last
// read of its hooks (ok false: nothing). read starts reading them again when the answer is
// unknown only for want of a current reading, and reports whether a reading runs.
func remoteGuard(g *GuardDTO, e app.Entry, m *app.Machine, st app.MachineStatus, ok bool, read func() bool) {
	where := e.Machine
	name := nonEmptyStr(e.AgentName, string(e.Agent))
	label := ""
	if p := e.Profile; p != nil && !p.Default {
		label = p.Name
	}
	folder, found := st.AgentFolder(e.Agent, e.Session.Key.Profile)
	if found {
		name, label = nonEmptyStr(folder.Name, name), nonEmptyStr(label, folder.ProfileLabel)
	}
	forFolder := ""
	if label != "" {
		forFolder = " for “" + label + "”"
	}
	const stops = ", so nothing stops new prompts in this copy."
	g.State = guardUnknown
	if m == nil || !ok {
		g.Problem = "Can't confirm protection: " + where + " wasn't scanned, so hopsesh doesn't know its hooks."
		g.Fix, g.Action = "Check "+where+" in Machines.", actionMachines
		return
	}
	g.CheckedAt = st.DetailsAt
	verdict, why, fix := st.OriginalProtection(e.Agent, e.Session.Key.Profile)
	hook := folder.Hook
	switch verdict {
	case app.ProtectedYes:
		g.State, g.Effective = guardProtected, true
		return
	case app.ProtectedNo:
		g.State = guardUnprotected
		if fix != app.FixSetUp && fix != app.FixUpdate && time.Since(st.DetailsAt) > app.StatusDetailsTTL {
			read() // a hook set up there since shows once it is read; this answer stands meanwhile
		}
		switch {
		case fix == app.FixSetUp:
			g.NoHopsesh = true
			g.Problem = "Not protected: " + where + " has no hopsesh" + stops
			g.Fix, g.Action = "Set up hopsesh on "+where+" in Machines.", actionMachines
		case fix == app.FixUpdate:
			g.Problem = fmt.Sprintf("Not protected: hopsesh %s on %s is too old to block this copy (blocking needs %s or newer)%s", st.Version, where, app.MinBlockVersion, stops)
			g.Fix, g.Action = "Update hopsesh on "+where+".", actionUpdate
		case fix == app.FixApprove:
			g.Problem = "The " + name + " hook on " + where + forFolder + " waits for your approval in " + name + ", which skips it until then" + stops
			g.Fix, g.Action = "Approve the hopsesh hook in "+name+" on "+where+".", actionApprove
			if hook.Reason != "" {
				g.Fix = "Approve the hopsesh hook in " + name + " on " + where + ": " + hook.Reason + "."
			}
		case fix == app.FixHooks && hook.State == app.CheckError:
			g.Problem = "The " + name + " hook on " + where + forFolder + " is broken (" + nonEmptyStr(hook.Reason, hook.Text) + ")" + stops
			g.Fix, g.Action = setUpFix(m)
		case fix == app.FixHooks:
			g.Problem = "The " + name + " hook isn't set up on " + where + forFolder + stops
			g.Fix, g.Action = setUpFix(m)
		case hook.State == app.CheckWarn:
			g.Problem = "The " + name + " hook on " + where + forFolder + " is turned off (" + hook.Reason + ")" + stops
			g.Fix, g.Action = "Turn it on again on "+where+".", actionMachines
		default:
			g.Problem = name + " on " + where + " can't run hopsesh's protection hook" + stops
			if hook.Reason != "" {
				g.Problem = name + " on " + where + " can't run hopsesh's protection hook (" + hook.Reason + ")" + stops
			}
		}
		return
	}
	// Unknown: say why, and read the hooks again when only a current reading is missing.
	switch {
	case fix == app.FixServe:
		g.Problem = "Can't confirm protection: " + why + " on " + where + "."
		g.Fix, g.Action = "Start hopsesh at login on "+where+" to check it.", actionMachines
		return
	case m.Status != app.StatusOK:
		g.Problem = "Can't confirm protection: " + where + " wasn't reached in the last scan, so its hooks couldn't be read."
	case st.DetailsAt.IsZero() || st.Stale:
		if read() {
			g.Checking, g.Problem = true, "Checking the hooks on "+where+"…"
			return
		}
		if st.DetailsAt.IsZero() {
			g.Problem = "Can't confirm protection: the hooks on " + where + " haven't been read yet."
		} else {
			g.Problem = "Can't confirm protection: the hooks on " + where + " were last read " + st.DetailsAt.Local().Format("2 Jan 15:04") + " and couldn't be read again."
		}
	case !found:
		g.Problem = "Can't confirm protection: this copy's " + name + " folder" + forFolder + " wasn't found among the agent folders on " + where + "."
	default:
		g.Problem = "Can't confirm protection on " + where + ": " + why + "."
	}
	g.Fix, g.Action = "Check "+where+" in Machines.", actionMachines
}

// setUpFix is how to install the hooks on m: from here when hopsesh runs in its login
// session (it answers), else there.
func setUpFix(m *app.Machine) (string, string) {
	if m.Presence == app.PresenceLogin {
		return "Set up protection on " + m.Name + ".", actionSetUp
	}
	return "Start hopsesh at login on " + m.Name + ", then set up its hooks.", actionMachines
}

// guardStatuses keeps what hopsesh last read of each machine (MachineStatuses CachedOnly:
// no machine is contacted) for one inventory while that reading is unchanged, so a window
// update reads it once, not once per copy left behind.
var guardStatuses struct {
	mu       sync.Mutex
	inv      *app.Inventory
	machines string // the machines as scanned
	dir      string
	stamp    time.Time
	at       time.Time
	by       map[string]app.MachineStatus
}

// machineStatusOf is what hopsesh last read of machine name in inv.
func machineStatusOf(core *app.App, inv *app.Inventory, name string) (app.MachineStatus, bool) {
	if inv == nil {
		return app.MachineStatus{}, false
	}
	stamp := core.StatusCachedAt()
	var machines strings.Builder
	for _, m := range inv.Machines {
		fmt.Fprintf(&machines, "%s\x00%s\x00%s\x00%s\x00%v\n", m.Name, m.Status, m.Hopsesh, m.Presence, m.Local)
	}
	c := &guardStatuses
	c.mu.Lock()
	defer c.mu.Unlock()
	// Stale turns with time: a minute old is recomputed even when nothing was read.
	if c.inv != inv || c.machines != machines.String() || c.dir != core.StateDir || !c.stamp.Equal(stamp) || time.Since(c.at) > time.Minute {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.by = map[string]app.MachineStatus{}
		for _, s := range core.MachineStatuses(ctx, inv, app.StatusOptions{CachedOnly: true}) {
			c.by[s.Machine] = s
		}
		c.inv, c.machines, c.dir, c.stamp, c.at = inv, machines.String(), core.StateDir, stamp, time.Now()
	}
	s, ok := c.by[name]
	return s, ok
}

// forgetStatuses makes the next guard read the machines' statuses again.
func forgetStatuses() {
	guardStatuses.mu.Lock()
	guardStatuses.inv = nil
	guardStatuses.mu.Unlock()
}

// guardReads reads a machine's hooks in the background when a copy left there would
// otherwise show its protection as unknown for want of a current reading: one reading per
// machine at a time, and none again within app.StatusDetailsTTL, so a machine that can't
// be read is never asked in a loop. The window is shown the result.
var guardReads struct {
	mu      sync.Mutex
	running map[string]bool
	started map[string]time.Time
	window  *App // the window's app (NewApp)
}

// readGuardStatus reads machine m's hooks for guardReads (tests replace it).
var readGuardStatus = func(ctx context.Context, core *app.App, m *app.Machine) {
	core.RefreshMachineStatus(ctx, m, false)
}

// readHooksOf starts reading m's hooks in the background, unless one runs or ran lately;
// true while one runs.
func readHooksOf(core *app.App, m *app.Machine) bool {
	r := &guardReads
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running[m.Name] {
		return true
	}
	if at, ok := r.started[m.Name]; ok && time.Since(at) < app.StatusDetailsTTL {
		return false
	}
	if r.running == nil {
		r.running, r.started = map[string]bool{}, map[string]time.Time{}
	}
	r.running[m.Name], r.started[m.Name] = true, time.Now()
	machine := *m // the inventory's may change under a later scan
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		readGuardStatus(ctx, core, &machine)
		forgetStatuses()
		r.mu.Lock()
		delete(r.running, machine.Name)
		w := r.window
		r.mu.Unlock()
		if w != nil {
			w.guardsChanged()
		}
	}()
	return true
}

// guardsChanged shows the window its sessions again after what decides the protection of
// the copies left behind changed (a reading of some machine's hooks, or of this one's,
// ended). Only a finished scan is shown again: one in progress shows it as it goes on,
// and saved sessions are only checking.
func (a *App) guardsChanged() {
	if a.Wails == nil && a.Emitter == nil {
		return
	}
	core := a.snapshot()
	a.mu.Lock()
	inv, at := a.inv, a.invAt
	ready := inv != nil && !inv.Discovering && !at.IsZero() && !a.closing
	a.mu.Unlock()
	if !ready {
		return
	}
	d := scanDTO(core, inv, at, at)
	d.Revision = a.scanRevision.Add(1)
	a.emit(DiscoveryEvent, d)
	a.publishQuick(d)
}

// ReleaseOriginal removes the block (or advice) from one original on this machine, for
// its current movement. The window confirms first: continuing there makes the copies
// diverge, and moving back then needs a comparison instead of a clean return.
func (a *App) ReleaseOriginal(machine, key string) error {
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if e.Machine != app.LocalName() {
		return errors.New("the original is on " + e.Machine + ": remove its block in hopsesh there")
	}
	if e.Departure == nil {
		return errors.New("this session was not moved away")
	}
	return core.ReleaseOriginal(e.Session.Key, e.Departure.Operation)
}

// RestoreBlock puts a removed block back on an original on this machine.
func (a *App) RestoreBlock(machine, key string) error {
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	return core.RestoreBlock(e.Session.Key)
}

// refuseOldCopy refuses to continue, move or plan from e when it is a copy a move left
// behind (app.OldCopy): its session goes on where it moved. The window offers to continue
// it anyway, confirmed, instead (ContinueOriginalAnyway, or ReleaseOriginal here).
func refuseOldCopy(core *app.App, e app.Entry) error {
	if core.OldCopy(e) {
		return app.OldCopyError(e)
	}
	return nil
}
