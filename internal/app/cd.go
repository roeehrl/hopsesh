package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Changing a session's folder (hopsesh cd): the session continues in another folder on the
// same machine and account profile, and stays the same session: its id and every record
// its agent wrote. Each agent module does it its agent's own way (agent.DirChanger). A
// session that is open is changed from inside it when its agent can do that and keep the
// session (Claude Code's /cd): hopsesh says what to type there and watches for the result.

// DirOptions are the choices of a folder change.
type DirOptions struct {
	OtherRepo bool // allow a folder that is not a checkout of the session's repository
	StopLocal bool // quit the session first when it is open
}

// DirChange is a planned folder change.
type DirChange struct {
	Entry Entry         `json:"entry"`
	Plan  agent.DirPlan `json:"plan"`
	// Open: the session is open now. InSession is then what to type in it (the agent's own
	// folder change, which keeps the session), "" when the agent has none; StopFirst: hopsesh
	// quits it first (--stop-local).
	Open      bool   `json:"open"`
	InSession string `json:"inSession,omitempty"`
	StopFirst bool   `json:"stopFirst,omitempty"`

	mod agent.DirChanger
	m   *Machine
	in  agent.Install
}

// CanChangeDir reports whether a session's folder can be changed: its agent's module has
// the capability, and the session is on this machine or on one where hopsesh runs (it
// makes the change there).
func (a *App) CanChangeDir(inv *Inventory, e Entry) bool {
	mod, ok := a.Module(e.Agent)
	if !ok || e.Location.IsCloud() {
		return false
	}
	m := inv.Machine(e.Machine)
	_, ok = mod.(agent.DirChanger)
	return ok && m != nil && (m.Local || m.Hopsesh != "" && m.Presence != PresenceSSH && m.Presence != PresenceOlder)
}

// PlanChangeDir says what moving a session on this machine to dir does.
func (a *App) PlanChangeDir(ctx context.Context, inv *Inventory, e Entry, dir string, o DirOptions) (*DirChange, error) {
	mod, m, in, err := a.moduleOn(inv, e)
	if err != nil {
		return nil, err
	}
	dc, ok := mod.(agent.DirChanger)
	if !ok || e.Location.IsCloud() {
		return nil, fmt.Errorf("hopsesh can't change the folder of %s sessions", e.AgentName)
	}
	if !m.Local {
		return nil, fmt.Errorf("the session is on %s; hopsesh changes the folder of sessions on this machine (bring it here with hopsesh pull --to %s)", m.Name, dir)
	}
	to, err := realDir(dir)
	if err != nil {
		return nil, err
	}
	if from, err := filepath.EvalSymlinks(e.Session.CWD); err == nil && from == to || e.Session.CWD == to {
		return nil, fmt.Errorf("%q is in %s already", e.Session.Title, to)
	}
	h, err := m.host.For(ctx, mod.Spec(), in, nil)
	if err != nil {
		return nil, err
	}
	p, err := dc.PlanChangeDir(ctx, h, in, e.Session, to)
	if err != nil {
		return nil, err
	}
	c := &DirChange{Entry: e, Plan: p, mod: dc, m: m, in: in}
	a.checkRepo(ctx, c, o)
	if li, ok := a.liveNow(ctx, mod, h, in, e); ok && li.State == agent.Live {
		c.Open = true
		c.InSession = dc.InSession(in, to)
		switch {
		case o.StopLocal && agent.Has(mod, agent.CapStop):
			c.StopFirst, c.InSession = true, ""
			c.Plan.Notes = append(c.Plan.Notes, "hopsesh quits the open session first (--stop-local)")
		case c.InSession == "":
			c.Plan.Blockers = append(c.Plan.Blockers, fmt.Sprintf("the session is open (%s), and %s can't change its folder there without starting another session; quit it first, or let hopsesh quit it (--stop-local)", nonEmptyStatus(li.Status), e.AgentName))
		}
	}
	return c, nil
}

// realDir is dir as an absolute folder with its symbolic links resolved: agents key
// sessions by the real folder.
func realDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s does not exist", abs)
		}
		return "", err
	}
	fi, err := host.LocalFS().Stat(real)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s is not a folder", real)
	}
	return real, nil
}

// checkRepo compares the two folders' repositories: another repository (or none) is
// refused unless o.OtherRepo, since the conversation is about the other one's files.
func (a *App) checkRepo(ctx context.Context, c *DirChange, o DirOptions) {
	states, err := repos.ProbeLocal(ctx, []string{c.Plan.From, c.Plan.To}, nil)
	if err != nil || len(states) != 2 {
		c.Plan.Warnings = append(c.Plan.Warnings, "hopsesh could not read the folders' git state")
		return
	}
	from, to := states[0], states[1]
	same := from.IsRepo && to.IsRepo && (from.Identity != "" && from.Identity == to.Identity ||
		from.RootCommit != "" && from.RootCommit == to.RootCommit || from.Toplevel == to.Toplevel)
	if !same && !o.OtherRepo {
		what := "is not a checkout of the session's repository"
		if !to.IsRepo {
			what = "is not in a git repository"
		}
		c.Plan.Blockers = append(c.Plan.Blockers, fmt.Sprintf("%s %s, and the conversation is about the files in %s; pass --other-repo to change it anyway", c.Plan.To, what, c.Plan.From))
	}
	if same && from.Branch != "" && to.Branch != "" && from.Branch != to.Branch {
		c.Plan.Warnings = append(c.Plan.Warnings, fmt.Sprintf("%s is on %s; the session worked on %s", c.Plan.To, to.Branch, from.Branch))
	}
	if from.Dirty > 0 && from.Toplevel != to.Toplevel {
		c.Plan.Warnings = append(c.Plan.Warnings, fmt.Sprintf("%s has %d uncommitted changes; they stay there", from.Toplevel, from.Dirty))
	}
}

func nonEmptyStatus(s string) string {
	if s == "" {
		return "live"
	}
	return s
}

// liveNow asks the session's agent whether it is open now.
func (a *App) liveNow(ctx context.Context, mod agent.Module, h agent.Host, in agent.Install, e Entry) (agent.LiveInfo, bool) {
	ld, ok := mod.(agent.LiveDetector)
	if !ok {
		return e.Live, e.Live.State != ""
	}
	live, err := ld.Live(ctx, h, in, []agent.SessionID{e.Session.Key.Session})
	if err != nil {
		return e.Live, e.Live.State != ""
	}
	li, ok := live[e.Session.Key.Session]
	return li, ok
}

// ChangeDir carries out a planned folder change of a closed session (or one hopsesh quits
// first), in a journal (undo reverses it), and checks that the agent now finds the session
// in the new folder. It returns the journal's id.
func (a *App) ChangeDir(ctx context.Context, c *DirChange) (string, error) {
	if len(c.Plan.Blockers) > 0 {
		return "", errors.New(c.Plan.Blockers[0])
	}
	e, m, in := c.Entry, c.m, c.in
	mod, _ := a.Module(e.Agent)
	h, err := m.host.For(ctx, mod.Spec(), in, nil)
	if err != nil {
		return "", err
	}
	if li, ok := a.liveNow(ctx, mod, h, in, e); ok && li.State == agent.Live {
		if !c.StopFirst {
			if c.InSession != "" {
				return "", fmt.Errorf("the session is open; type %s in it", c.InSession)
			}
			return "", errors.New("the session is open; quit it first, or let hopsesh quit it (--stop-local)")
		}
		if err := mod.(agent.Stopper).Stop(ctx, h, in, e.Session, 15*time.Second); err != nil {
			return "", fmt.Errorf("quitting the open session: %w", err)
		}
	}
	j, err := journal.New(a.StateDir, journal.KindChangeDir, fmt.Sprintf("change the folder of “%s” to %s on %s", e.Session.Title, c.Plan.To, m.Name))
	if err != nil {
		return "", err
	}
	j.AddKey(e.Session.Key)
	jh, err := m.host.For(ctx, mod.Spec(), in, j)
	if err == nil {
		err = c.mod.ChangeDir(ctx, jh, in, e.Session, c.Plan)
	}
	if err == nil && c.Plan.Method == agent.DirAgent {
		err = j.ChangedDir(host.LocalFS(), m.Name, e.Session.Key, c.Plan.Main, c.Plan.From, c.Plan.To)
	}
	if err == nil && c.Plan.Main != e.Session.Path {
		// The lineage file beside the transcript goes with it: the same session, the same
		// replica.
		side := lineage.PathFor(e.Session.Path)
		if _, serr := host.LocalFS().Stat(side); serr == nil {
			err = jh.FS().Rename(side, lineage.PathFor(c.Plan.Main))
		}
	}
	if err == nil {
		err = verifyDir(ctx, mod, h, in, e.Session.Key, c.Plan.To)
	}
	if a.Audit != nil {
		a.Audit.Write(audit.Entry{Action: "cd", Host: m.Name, Session: e.Session.Key.String(), Detail: map[string]any{"ok": err == nil, "to": c.Plan.To, "method": string(c.Plan.Method)}})
	}
	if err != nil {
		if j.Changes() > 0 {
			return j.ID, fmt.Errorf("%w (hopsesh undo %s reverses what was done)", err, j.ID)
		}
		return "", err
	}
	return j.ID, nil
}

// verifyDir lists the session again, as its agent finds it: it must be in dir.
func verifyDir(ctx context.Context, mod agent.Module, h agent.Host, in agent.Install, key agent.SessionKey, dir string) error {
	got, err := folderOf(ctx, mod, h, in, key)
	if err != nil {
		return err
	}
	if got != dir {
		return fmt.Errorf("the session's folder reads as %s, not %s", got, dir)
	}
	return nil
}

// folderOf is the folder a session is in now, from a fresh listing of its agent's sessions
// in that install.
func folderOf(ctx context.Context, mod agent.Module, h agent.Host, in agent.Install, key agent.SessionKey) (string, error) {
	ls, err := mod.List(ctx, h, in)
	if err != nil {
		return "", err
	}
	for _, s := range ls.Sessions {
		if s.Key.Session == key.Session {
			return s.CWD, nil
		}
	}
	return "", fmt.Errorf("the session %s is no longer listed", key.Session)
}

// WaitChangedDir waits until the open session's agent has moved it to the planned folder
// (the user typed c.InSession there), checking every interval until ctx ends.
func (a *App) WaitChangedDir(ctx context.Context, c *DirChange, interval time.Duration) error {
	mod, ok := a.Module(c.Entry.Agent)
	if !ok {
		return fmt.Errorf("%s is turned off", c.Entry.AgentName)
	}
	h, err := c.m.host.For(ctx, mod.Spec(), c.in, nil)
	if err != nil {
		return err
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		// The main file's new place is cheap to watch; a listing confirms the folder.
		if _, err := h.FS().Stat(c.Plan.Main); err == nil {
			if got, err := folderOf(ctx, mod, h, c.in, c.Entry.Session.Key); err == nil && got == c.Plan.To {
				if a.Audit != nil {
					a.Audit.Write(audit.Entry{Action: "cd", Host: c.m.Name, Session: c.Entry.Session.Key.String(), Detail: map[string]any{"ok": true, "to": c.Plan.To, "method": "in-session"}})
				}
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the session did not move to %s (%w)", c.Plan.To, ctx.Err())
		case <-t.C:
		}
	}
}

// undoDirs changes a session's folder back for undo, on this machine.
type undoDirs struct{ a *App }

func (a *App) undoDirs() journal.Dirs { return undoDirs{a} }

func (u undoDirs) ChangeDir(ctx context.Context, machine string, key agent.SessionKey, dir string) error {
	if machine != LocalName() {
		return fmt.Errorf("the session is on %s; undo it there", machine)
	}
	inv := u.a.Scan(ctx, ScanOptions{Hosts: []string{LocalName()}, NoCache: true, SkipGit: true})
	defer inv.Close()
	for _, e := range inv.Entries {
		if e.Session.Key != key || e.Location.IsCloud() {
			continue
		}
		mod, m, in, err := u.a.moduleOn(inv, e)
		if err != nil {
			return err
		}
		dc, ok := mod.(agent.DirChanger)
		if !ok {
			return fmt.Errorf("%s can't change a session's folder", e.AgentName)
		}
		h, err := m.host.For(ctx, mod.Spec(), in, nil)
		if err != nil {
			return err
		}
		if li, ok := u.a.liveNow(ctx, mod, h, in, e); ok && li.State == agent.Live {
			return errors.New("the session is open; quit it first")
		}
		p, err := dc.PlanChangeDir(ctx, h, in, e.Session, dir)
		if err != nil {
			return err
		}
		return dc.ChangeDir(ctx, h, in, e.Session, p)
	}
	return fmt.Errorf("the session %s is no longer listed", key.Session)
}

// cdPlan plans, here, the folder change another machine's hopsesh asks for: a session on
// this machine, found by its key, moved to a folder here.
func (s *peerSession) cdPlan(ctx context.Context, req peer.CdRequest) (*DirChange, error) {
	if s.cdInv != nil {
		s.cdInv.Close()
	}
	s.cd, s.cdInv = nil, nil
	if home, err := os.UserHomeDir(); err == nil && (req.Dir == "~" || strings.HasPrefix(req.Dir, "~/")) {
		req.Dir = filepath.Join(home, strings.TrimPrefix(req.Dir, "~"))
	}
	inv := s.a.Scan(ctx, ScanOptions{Hosts: []string{LocalName()}, NoCache: true, SkipGit: true})
	// The other machine names this machine's account folders its own way: the session is
	// its agent's session id here, in the folder of the same id when there are several.
	var found []Entry
	for _, e := range inv.Entries {
		k := e.Session.Key
		if e.Location.IsCloud() || k.Agent != req.Session.Agent || k.Session != req.Session.Session {
			continue
		}
		if k.Profile == req.Session.Profile {
			found = []Entry{e}
			break
		}
		found = append(found, e)
	}
	switch len(found) {
	case 0:
		inv.Close()
		return nil, fmt.Errorf("no session %s here", req.Session.Session)
	case 1:
	default:
		inv.Close()
		return nil, fmt.Errorf("session %s is in %d account folders here; move it from this machine (hopsesh cd)", req.Session.Session, len(found))
	}
	c, err := s.a.PlanChangeDir(ctx, inv, found[0], req.Dir, DirOptions{OtherRepo: req.OtherRepo, StopLocal: req.StopLocal})
	if err != nil {
		inv.Close()
		return nil, err
	}
	s.cd, s.cdInv = c, inv
	return c, nil
}

// RemoteDir is a folder change of a session on another machine, planned and carried out by
// hopsesh there (it writes nothing there over SSH itself): this machine keeps a journal
// that names the one hopsesh there keeps, so hopsesh undo here reverses it there.
type RemoteDir struct {
	Machine string
	Change  DirChange // as hopsesh there planned it

	a      *App
	client *peer.Client
	done   func()
}

// PlanChangeDirOn asks hopsesh on machine to plan moving its session key to dir there (dir
// is a folder there; ~ is that machine's home).
func (a *App) PlanChangeDirOn(ctx context.Context, machine string, key agent.SessionKey, dir string, o DirOptions) (*RemoteDir, error) {
	h, err := a.allowedHost(machine)
	if err != nil {
		return nil, err
	}
	c, hr, done, err := a.dialPeer(ctx, h)
	if err != nil {
		return nil, fmt.Errorf("hopsesh on %s: %w (set it up with: hopsesh hosts setup %s)", machine, err, machine)
	}
	r := &RemoteDir{Machine: machine, a: a, client: c, done: done}
	if err := c.Call(ctx, peer.MethodCdPlan, peer.CdRequest{Session: key, Dir: dir, OtherRepo: o.OtherRepo, StopLocal: o.StopLocal}, &r.Change); err != nil {
		r.Close()
		if strings.Contains(err.Error(), "unknown request") {
			return nil, fmt.Errorf("hopsesh %s on %s can't change a session's folder; update it (hopsesh hosts setup %s)", hr.Version, machine, machine)
		}
		return nil, err
	}
	return r, nil
}

// Apply has hopsesh there carry out the change, and records here which journal it keeps.
func (r *RemoteDir) Apply(ctx context.Context) (string, error) {
	if len(r.Change.Plan.Blockers) > 0 {
		return "", errors.New(r.Change.Plan.Blockers[0])
	}
	var reply peer.CdApplyReply
	if err := r.client.Call(ctx, peer.MethodCdApply, struct{}{}, &reply); err != nil {
		return "", fmt.Errorf("on %s: %w", r.Machine, err)
	}
	e := r.Change.Entry
	j, err := journal.New(r.a.StateDir, journal.KindChangeDir, fmt.Sprintf("change the folder of “%s” to %s on %s", e.Session.Title, r.Change.Plan.To, r.Machine))
	if err != nil {
		return reply.Journal, err
	}
	j.AddKey(e.Session.Key)
	if err := j.AddRemote(r.Machine, reply.Journal); err != nil {
		return j.ID, err
	}
	if r.a.Audit != nil {
		r.a.Audit.Write(audit.Entry{Action: "cd", Host: r.Machine, Session: e.Session.Key.String(), Detail: map[string]any{"ok": true, "to": r.Change.Plan.To, "remote": reply.Journal}})
	}
	return j.ID, nil
}

// Wait has hopsesh there wait, at most d, until the open session's agent has moved it (the
// user typed Change.InSession in it there).
func (r *RemoteDir) Wait(ctx context.Context, d time.Duration) error {
	return r.client.Call(ctx, peer.MethodCdWait, peer.CdWait{Seconds: int(d / time.Second)}, nil)
}

// Close ends the conversation with hopsesh there.
func (r *RemoteDir) Close() {
	if r.done != nil {
		r.done()
		r.done = nil
	}
}
