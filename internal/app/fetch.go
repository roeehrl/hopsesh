package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Bringing sessions home from vendor clouds: the links the user pasted, planning a fetch,
// adopting what the driver wrote, and the result in one shape for every front end.

// Pasted is a cloud session the user named by its link or id ("Paste a link…"), which
// hopsesh then lists with what it knows.
type Pasted struct {
	Cloud    string          `json:"cloud"`
	ID       agent.SessionID `json:"id"`
	Repo     string          `json:"repo,omitempty"`
	Checkout string          `json:"checkout,omitempty"`
	Title    string          `json:"title,omitempty"`
	Time     time.Time       `json:"time"`
}

func (a *App) pastedFile() string { return filepath.Join(a.StateDir, "clouds", "pasted.json") }

// Pasted returns the pasted cloud sessions.
func (a *App) Pasted() []Pasted {
	var out []Pasted
	if b, err := os.ReadFile(a.pastedFile()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// ParseCloudLink reads a cloud session's link or id with the modules that know their
// vendor's links: the module, the cloud and the id.
func (a *App) ParseCloudLink(s string) (agent.Module, string, agent.SessionID, bool) {
	for _, m := range a.Modules() {
		if l, ok := m.(agent.CloudLinker); ok {
			if cl, id, ok := l.ParseCloudLink(s); ok {
				if _, declared := m.Spec().FindCloud(cl); declared {
					return m, cl, id, true
				}
			}
		}
	}
	return nil, "", "", false
}

// Paste remembers a cloud session by its link or id, with the checkout here it works on
// ("" when not known yet), so scans list it.
func (a *App) Paste(ctx context.Context, link, checkout string) (Pasted, error) {
	_, cl, id, ok := a.ParseCloudLink(link)
	if !ok {
		return Pasted{}, fmt.Errorf("%q is not a link or id of a cloud session hopsesh knows", strings.TrimSpace(link))
	}
	p := Pasted{Cloud: cl, ID: id, Time: time.Now().UTC()}
	if checkout != "" {
		states, err := repos.ProbeLocal(ctx, []string{checkout}, a.Reg.Worktrees())
		if err != nil || len(states) == 0 || !states[0].IsRepo {
			return Pasted{}, fmt.Errorf("%s is not a git checkout", checkout)
		}
		p.Repo, p.Checkout = states[0].Identity, nonEmpty(states[0].MainWorktree, states[0].Toplevel)
	}
	list := a.Pasted()
	kept := list[:0]
	for _, x := range list {
		if x.Cloud != p.Cloud || x.ID != p.ID {
			kept = append(kept, x)
		}
	}
	kept = append(kept, p)
	b, _ := json.MarshalIndent(kept, "", "  ")
	if err := os.MkdirAll(filepath.Dir(a.pastedFile()), 0o700); err != nil {
		return p, err
	}
	return p, os.WriteFile(a.pastedFile(), b, 0o600)
}

// CloudEntry is a cloud session as an entry: the scanned one, or, for an id the scan did
// not list (a link pasted on the command line), one made from the id.
func (inv *Inventory) CloudEntry(a *App, cloud string, id agent.SessionID) (Entry, error) {
	for _, e := range inv.Entries {
		if e.Location.IsCloud() && e.Location.Name == cloud && (e.Session.Key.Session == id || id != "" && len(id) >= 12 && strings.HasPrefix(string(e.Session.Key.Session), string(id))) {
			return e, nil
		}
	}
	mod, cl, ok := a.cloudModule(cloud)
	if !ok {
		return Entry{}, fmt.Errorf("%w: no module reaches %s", agent.ErrNotFound, cloud)
	}
	s := agent.CloudSession{Key: agent.SessionKey{Agent: mod.Spec().ID, Session: id}, Cloud: cloud, State: agent.CloudUnknown}
	if l, ok := mod.(agent.CloudLinker); ok && id != "" {
		s.URL = l.CloudURL(cloud, id)
	}
	return cloudEntry(mod.Spec(), cl, s, nil), nil
}

// CloudModule is the enabled module that reaches a cloud, with the cloud's declaration.
func (a *App) CloudModule(name string) (agent.Module, agent.Cloud, bool) { return a.cloudModule(name) }

// cloudModule is the enabled module that reaches a cloud.
func (a *App) cloudModule(name string) (agent.Module, agent.Cloud, bool) {
	for _, r := range a.clouds() {
		if r.cloud.Name == name {
			return r.mod, r.cloud, true
		}
	}
	return nil, agent.Cloud{}, false
}

// IsCloud reports whether a name is a cloud's.
func (a *App) IsCloud(name string) bool {
	_, _, ok := a.cloudModule(name)
	return ok
}

// planFetch works out how a cloud session comes here: target "" (or its own agent) keeps
// it in its agent; another agent continues it there once it is here. opt.TargetDir
// chooses the repository's checkout here.
func (a *App) planFetch(ctx context.Context, inv *Inventory, e Entry, target agent.ID, opt move.Options) (*move.Plan, move.Input, error) {
	mod, cl, ok := a.cloudModule(e.Location.Name)
	if !ok || mod.Spec().ID != e.Agent {
		return nil, move.Input{}, fmt.Errorf("%s is not enabled", e.Location.Name)
	}
	here := inv.Local()
	if here == nil || here.host == nil {
		return nil, move.Input{}, errors.New("this machine was not scanned")
	}
	var in agent.Install
	for _, st := range here.Agents {
		if st.Agent == e.Agent {
			in = st.Install
		}
	}
	if in.Agent == "" {
		in.Agent = e.Agent
	}
	s := agent.CloudSession{Key: e.Session.Key, Cloud: cl.Name, Title: e.Session.Title, State: agent.CloudUnknown}
	if e.Cloud != nil {
		s = *e.Cloud
	}
	set := a.Cfg.CloudSettings(cl.Name)
	opt.RenameVendor = *set.RenameVendorBranches
	hand := move.FindHandoff(a.StateDir, cl.Name, s.Key.Session)
	if hand != nil && s.Key.Session != "" && s.Repo == "" {
		s.Repo = hand.Repo // a listing that names no repository (Amp's threads)
	}
	checkout := opt.TargetDir
	if checkout == "" {
		checkout = e.Checkout
	}
	if checkout == "" && s.Repo != "" {
		if found := repos.FindLocal(s.Repo, a.LocalRoots()); len(found) > 0 {
			checkout = found[0].Path
		}
	}
	h, _, err := cloudHost(ctx, here.host, mod, cl)
	if err != nil {
		return nil, move.Input{}, err
	}
	fin := move.FetchInput{Machine: here.host, Module: mod, Install: in, Host: h, Cloud: cl, Session: s, Checkout: checkout,
		Lineage: e.Lineage, Worktrees: a.Reg.Worktrees()}
	if ap, ok := mod.(agent.AccountProber); ok {
		if acct, err := ap.Account(ctx, h, in); err == nil {
			fin.Account = &acct
		}
	}
	if _, own := mod.(agent.Writer); !own && target == "" && !opt.CodeOnly {
		// A cloud-only module keeps no sessions here: its text goes into a local agent.
		target = a.BringTarget(inv, e)
	}
	if target != "" && target != e.Agent {
		tm, ok := a.Module(target)
		if !ok || !agent.Has(tm, agent.CapWrite) {
			return nil, move.Input{}, fmt.Errorf("there is no %q agent here that can take a session", target)
		}
		tin, ok := here.Install(target)
		if !ok {
			return nil, move.Input{}, fmt.Errorf("%w: %s has no data folder on this machine yet; start it here once, then try again", agent.ErrNotInstalled, tm.Spec().Name)
		}
		fin.Continue, fin.ContinueInstall = tm, tin
	}
	if hand != nil && s.Key.Session != "" {
		fin.Prompt = hand.Brief
	}
	if e.Original != "" {
		for _, x := range inv.Entries {
			if x.Machine+":"+x.Session.Key.String() == e.Original && x.Machine == here.Name {
				fin.Original = &move.Copy{Summary: x.Session, Live: x.Live, Lineage: x.Lineage}
			}
		}
	}
	for _, x := range inv.Entries {
		if mr := x.Session.Mirror; mr != nil && mr.Cloud == cl.Name && mr.ID == s.Key.Session {
			fin.MirrorOf, fin.Session.Mirror = x.Machine+":"+x.Session.Key.String(), true
			if fin.Session.Account == "" {
				fin.Session.Account = mr.Account
			}
		}
	}
	p, err := move.BuildFetch(ctx, fin, opt)
	if err == nil && !a.Cfg.CloudAllowed(cl.Name) {
		p.Blockers = append([]string{fmt.Sprintf("hopsesh leaves %s alone until you allow it (hopsesh clouds allow %s)", cl.Title, cl.Name)}, p.Blockers...)
	}
	return p, move.Input{}, err
}

// BringTarget is the local agent that gets a cloud-only module's session (Copilot's log,
// Amp's thread) when the user names none: the agent it was handed off from, by its lineage,
// else Claude Code, else the first agent here that takes sessions; "" when none here can.
func (a *App) BringTarget(inv *Inventory, e Entry) agent.ID {
	here := inv.Local()
	if here == nil {
		return ""
	}
	takes := func(id agent.ID) bool {
		m, ok := a.Module(id)
		if !ok || !agent.Has(m, agent.CapWrite) {
			return false
		}
		_, ok = here.Install(id)
		return ok
	}
	if l := e.Lineage; l != nil {
		for _, h := range l.Hops {
			if h.Kind != lineage.HopHandoff || h.From < 0 || h.From >= len(l.Replicas) || h.To < 0 || h.To >= len(l.Replicas) {
				continue
			}
			if to := l.Replicas[h.To]; to.Location == e.Location.Name && to.Key.Session == e.Session.Key.Session {
				if from := l.Replicas[h.From].Key.Agent; takes(from) {
					return from
				}
			}
		}
	}
	if takes("claude") {
		return "claude"
	}
	for _, st := range here.Agents {
		if takes(st.Agent) {
			return st.Agent
		}
	}
	return ""
}

// adoptWaiting adopts what drivers wrote for fetches on this machine since: the fetches it
// adopted now, and the ones still waiting.
func (a *App) adoptWaiting(ctx context.Context, lm *host.Machine) (adopted, waiting []*move.Fetch) {
	fs, err := move.LoadFetches(a.StateDir)
	if err != nil {
		return nil, nil
	}
	for _, f := range fs {
		if !f.Waiting() || f.Machine != lm.Name {
			continue
		}
		ad, err := a.adopt(ctx, lm, f, false)
		switch {
		case errors.Is(err, move.ErrUndone):
			continue
		case err != nil && time.Since(f.Time) > 7*24*time.Hour:
			continue
		case ad != nil:
			adopted = append(adopted, f)
		default:
			waiting = append(waiting, f)
		}
	}
	return adopted, waiting
}

func (a *App) adopt(ctx context.Context, lm *host.Machine, f *move.Fetch, exited bool) (*move.Adopted, error) {
	mod, ok := a.Module(f.Agent)
	if !ok {
		return nil, fmt.Errorf("%s is not enabled", f.Agent)
	}
	h, err := lm.For(ctx, mod.Spec(), agent.Install{}, nil)
	if err != nil {
		return nil, err
	}
	in, err := mod.Detect(ctx, h)
	if err != nil {
		return nil, err
	}
	if in.Agent == "" {
		in.Agent = mod.Spec().ID
	}
	return move.AdoptFetch(ctx, f, move.Side{Machine: lm, Module: mod, Install: in}, move.Env{StateDir: a.StateDir, Audit: a.Audit}, exited)
}

// Adopt looks for what the driver wrote for a fetch (by its journal) and adopts it once it
// is there; exited: the driver has ended. The fetch says whether it is still waiting.
func (a *App) Adopt(ctx context.Context, journal string, exited bool) (*move.Fetch, error) {
	f, err := move.LoadFetch(a.StateDir, journal)
	if err != nil {
		return nil, fmt.Errorf("no fetch %s: %w", journal, err)
	}
	if !f.Waiting() {
		return f, nil
	}
	_, err = a.adopt(ctx, a.localMachine(ctx), f, exited)
	return f, err
}

// KeepPartial records that the user keeps a partial copy as it is.
func (a *App) KeepPartial(journal string) error {
	f, err := move.LoadFetch(a.StateDir, journal)
	if err != nil {
		return err
	}
	f.Kept = true
	a.Audit.Write(audit.Entry{Action: "cloud.keep", Detail: map[string]any{"journal": journal}})
	return move.SaveFetch(a.StateDir, f)
}

// Fetches lists what was brought from clouds, newest first.
func (a *App) Fetches() ([]*move.Fetch, error) { return move.LoadFetches(a.StateDir) }

// TestCloud probes a cloud through its module, read-only: the login and the driver's
// flags. The result is kept for a little while for the hand-off plans.
func (a *App) TestCloud(ctx context.Context, name string) (agent.CloudTest, error) {
	mod, cl, ok := a.cloudModule(name)
	if !ok {
		return agent.CloudTest{}, fmt.Errorf("no enabled agent reaches %s", name)
	}
	t, ok := mod.(agent.CloudTester)
	if !ok {
		return agent.CloudTest{}, fmt.Errorf("%w: hopsesh cannot test %s", agent.ErrUnsupported, cl.Title)
	}
	h, in, err := cloudHost(ctx, a.localMachine(ctx), mod, cl)
	if err != nil {
		return agent.CloudTest{}, err
	}
	ct, err := t.TestCloud(ctx, h, in, name)
	a.tests.keep(name, ct, err)
	return ct, err
}

// cloudTestTTL is how long a probe stands for the hand-off plans (a scan forgets it).
const cloudTestTTL = 90 * time.Second

// cloudTests are recent probes by cloud.
type cloudTests struct {
	mu sync.Mutex
	m  map[string]cloudTest
}

type cloudTest struct {
	at  time.Time
	t   agent.CloudTest
	err error
}

func (c *cloudTests) keep(name string, t agent.CloudTest, err error) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[name] = cloudTest{time.Now(), t, err}
}

func (c *cloudTests) recent(name string) (cloudTest, bool) {
	if c == nil {
		return cloudTest{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	x, ok := c.m[name]
	return x, ok && time.Since(x.at) < cloudTestTTL
}

func (c *cloudTests) forget() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.m)
}

// recentTest is a probe of the cloud from the last little while, or a new one.
func (a *App) recentTest(ctx context.Context, name string, run func(context.Context) (agent.CloudTest, error)) (agent.CloudTest, error) {
	if x, ok := a.tests.recent(name); ok {
		return x.t, x.err
	}
	t, err := run(ctx)
	if ctx.Err() == nil {
		a.tests.keep(name, t, err)
	}
	return t, err
}

// Brought is a fetch from a cloud as every front end shows it (and the command line's
// --json prints it): waiting for the user's terminal, or what came back.
type Brought struct {
	Journal    string `json:"journal"`
	Cloud      string `json:"cloud"`
	CloudTitle string `json:"cloudTitle"`
	Session    string `json:"session,omitempty"` // the cloud session's id
	URL        string `json:"url,omitempty"`
	Title      string `json:"title"`
	Agent      string `json:"agent"` // the agent it is in here
	Worktree   string `json:"worktree"`
	// Outcome is waiting, complete, partial, empty, or code (the code only).
	Outcome  string `json:"outcome"`
	Restored int    `json:"restored,omitempty"`
	Expected int    `json:"expected,omitempty"`
	Stated   bool   `json:"stated,omitempty"`
	// Branch is the branch the code is on here; Renamed, the cloud's own name for it.
	Branch   string `json:"branch,omitempty"`
	Renamed  string `json:"renamed,omitempty"`
	NoBranch bool   `json:"noBranch,omitempty"`
	// Command is what to run next: the driver's command while waiting, the command that
	// resumes the copy once it is here.
	Command string        `json:"command,omitempty"`
	Run     agent.Command `json:"run"`
	Key     string        `json:"key,omitempty"` // the copy here (agent/session)
	// Message is the outcome in words; Issue, the upstream problem it points to.
	Message  string `json:"message"`
	Issue    string `json:"issue,omitempty"`
	IssueRef string `json:"issueRef,omitempty"`
	MirrorOf string `json:"mirrorOf,omitempty"`
	// Written: hopsesh wrote the copy from what the driver brought as text, at Fidelity;
	// Changes sums up the code; Loss is what stayed in the cloud; Noun, what the cloud calls
	// its sessions.
	Written  bool     `json:"written,omitempty"`
	Fidelity string   `json:"fidelity,omitempty"`
	Changes  string   `json:"changes,omitempty"`
	Loss     []string `json:"loss,omitempty"`
	Noun     string   `json:"noun,omitempty"`
	Continue string   `json:"continue,omitempty"` // the agent to continue in now (id)
	// ContinueName is that agent's name.
	ContinueName string   `json:"continueName,omitempty"`
	Appended     bool     `json:"appended,omitempty"`
	Kept         bool     `json:"kept,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

// BroughtOf describes a fetch; agentName is the module's name ("Claude Code").
func BroughtOf(f *move.Fetch, agentName string) Brought {
	b := Brought{Journal: f.Journal, Cloud: f.Cloud, CloudTitle: f.CloudTitle, Session: string(f.Session), URL: f.URL, Title: f.Title,
		Agent: agentName, Worktree: f.Worktree, Outcome: move.FetchWaiting, Command: f.Command, Run: f.Run, MirrorOf: f.MirrorOf,
		Continue: string(f.Continue), ContinueName: f.ContinueName, Kept: f.Kept}
	ad := f.Adopted
	if ad == nil {
		b.Message = fmt.Sprintf("Waiting for %s to finish copying…", agentName)
		return b
	}
	b.Outcome, b.Restored, b.Expected, b.Stated = ad.Outcome, ad.Restored, ad.Expected, ad.Stated
	b.Branch, b.Renamed, b.NoBranch, b.Appended, b.Warnings = ad.Branch, ad.Renamed, ad.NoBranch, ad.Appended, ad.Warnings
	b.Command, b.Run, b.Key, b.Issue = ad.Command, ad.Resume, ad.Key.String(), ad.Issue
	b.Written, b.Fidelity, b.Changes, b.Loss, b.Noun = ad.Written, ad.Fidelity, ad.Changes, f.Loss, f.Noun
	if f.ContinueName != "" && ad.Written {
		b.Agent = f.ContinueName // written straight into the agent it continues in
	}
	if ad.Issue != "" {
		b.IssueRef = move.IssueRef(ad.Issue)
	}
	known := ""
	if b.IssueRef != "" {
		known = fmt.Sprintf(" This is a known %s problem (%s).", agentName, b.IssueRef)
	}
	switch {
	case ad.Written:
		b.Message = fmt.Sprintf("“%s” is here in %s", f.Title, b.Agent)
		switch ad.Fidelity {
		case string(agent.FidCode):
			b.Message += fmt.Sprintf(": the %s's title and what came of it", nonEmpty(f.Noun, "session"))
			if ad.Branch != "" {
				b.Message += ", with its code on " + ad.Branch
			}
			b.Message += ". Its messages and steps stay in " + f.CloudTitle + "."
		default:
			b.Message += fmt.Sprintf(": %d messages, as text", ad.Restored)
			if ad.Branch != "" {
				b.Message += ", with its code on " + ad.Branch
			}
			b.Message += "."
		}
		return b
	}
	switch ad.Outcome {
	case move.FetchComplete:
		b.Message = fmt.Sprintf("“%s” is here in %s", f.Title, agentName)
		if ad.Stated {
			b.Message += fmt.Sprintf(": %d of %d messages, checked", ad.Restored, ad.Expected)
		} else {
			b.Message += fmt.Sprintf(": %d messages", ad.Restored)
		}
	case move.FetchPartial:
		b.Message = fmt.Sprintf("%s restored %d of %d messages.%s", agentName, ad.Restored, ad.Expected, known)
	case move.FetchEmpty:
		if f.Mirror {
			b.Message = fmt.Sprintf("This is a Remote Control session, and %s copies none of its messages right now.%s", agentName, known)
		} else {
			b.Message = fmt.Sprintf("%s copied none of the session's messages.%s", agentName, known)
		}
	}
	return b
}

// Brought describes a fetch with its module's name.
func (a *App) Brought(f *move.Fetch) Brought {
	name := string(f.Agent)
	if m, ok := a.Reg.Get(f.Agent); ok {
		name = m.Spec().Name
	}
	return BroughtOf(f, name)
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
