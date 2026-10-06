package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Handing sessions off to vendor clouds: which clouds a session can go to (and why not),
// the plan, follow-ups, and git on the session's machine for the snapshot and its undo.

// HandoffTarget is a cloud a session can be handed off to, or the reason it cannot.
type HandoffTarget struct {
	Cloud string `json:"cloud"`
	Title string `json:"title"`
	Agent string `json:"agent"` // the agent that runs there
	OK    bool   `json:"ok"`
	Why   string `json:"why,omitempty"` // why not, in words
	// Note says what the cloud gets ("a briefing and the code on a branch"), and what it
	// cannot do here.
	Note   string `json:"note,omitempty"`
	Bundle bool   `json:"bundle,omitempty"` // it goes as an upload (the remote is not one the cloud clones)
	// Limits are what hopsesh cannot reach in that cloud (agent.Cloud.Limits), said beside it.
	Limits []string `json:"limits,omitempty"`
}

// HandoffTargets are the clouds a session could be handed off to, each enabled or with its
// reason: an option is refused with its reason, never hidden.
func (a *App) HandoffTargets(inv *Inventory, e Entry) []HandoffTarget {
	if e.Location.IsCloud() {
		return nil
	}
	var out []HandoffTarget
	for _, r := range a.clouds() {
		cl := r.cloud
		t := HandoffTarget{Cloud: cl.Name, Title: cl.Title, Agent: r.mod.Spec().Name, Limits: cl.Limits}
		c := inv.Cloud(cl.Name)
		src := inv.Machine(e.Machine)
		host := ""
		if e.Git != nil {
			host, _, _ = strings.Cut(e.Git.Identity, "/")
		}
		_, sends := r.mod.(agent.CloudSender)
		bundle := hasCodeWay(cl.CodeUp, agent.ViaBundle)
		switch {
		case !sends:
			t.Why = "hopsesh does not reach " + cl.Title + " yet"
		case c == nil || !c.Allowed:
			t.Why = "turned off. Turn it on in Machines."
		case c.Status == CloudCLIMissing, c.Status == CloudNotEligible:
			t.Why = nonEmpty(c.Hint, c.Error)
		case c.Status == CloudSignedOut:
			t.Why = trimSentinel(c.Error)
		case c.Status == CloudError:
			t.Why = nonEmpty(c.Error, "it could not be reached")
		case e.Git == nil || !e.Git.IsRepo || e.Git.Identity == "":
			t.Why = "this session isn't in a git repository with a remote"
		case src != nil && !src.Local && src.OS == "windows":
			t.Why = "hopsesh can't snapshot code on a Windows machine yet; bring the session here first"
		case !containsStr(cl.Hosts, host) && !bundle:
			t.Why = "this repository isn't on " + hostsWords(cl.Hosts)
		case !containsStr(cl.Hosts, host) && src != nil && !src.Local:
			t.Why = "the remote is " + host + ", so it goes as an upload, from this machine only; bring the session here first"
		case !containsStr(cl.Hosts, host):
			t.OK, t.Bundle = true, true
			t.Note = "Gets a briefing and an upload of the code · Results can't be pushed back: the remote is " + host
		default:
			t.OK, t.Note = true, "Gets a briefing and the code on a branch"
		}
		out = append(out, t)
	}
	return out
}

func trimSentinel(s string) string {
	for _, e := range []error{agent.ErrSignedOut, agent.ErrNotEligible, agent.ErrRepoUnsupported, agent.ErrUnsupported} {
		s = strings.TrimPrefix(s, e.Error()+": ")
	}
	return s
}

func hasCodeWay(ws []agent.CodeWay, w agent.CodeWay) bool {
	for _, x := range ws {
		if x == w {
			return true
		}
	}
	return false
}

func containsStr(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// PlanHandoff works out handing a session off to a cloud. Nothing changes.
func (a *App) PlanHandoff(ctx context.Context, inv *Inventory, e Entry, cloud string, opt move.Options) (*move.Plan, error) {
	if e.Location.IsCloud() {
		return nil, fmt.Errorf("%s is already in a cloud", e.Session.Key)
	}
	mod, cl, ok := a.cloudModule(cloud)
	if !ok {
		return nil, fmt.Errorf("unknown cloud %q (see hopsesh clouds)", cloud)
	}
	src, here := inv.Machine(e.Machine), inv.Local()
	if src == nil || src.host == nil {
		return nil, fmt.Errorf("%s was not reached", e.Machine)
	}
	if here == nil || here.host == nil {
		return nil, errors.New("this machine was not scanned")
	}
	sm, ok := a.Module(e.Agent)
	if !ok {
		return nil, fmt.Errorf("%s is not enabled", e.Agent)
	}
	sin, _ := src.InstallProfile(e.Agent, e.Session.Key.Profile)
	h, in, err := cloudHost(ctx, here.host, mod, cl)
	if err != nil {
		return nil, err
	}
	set := a.Cfg.CloudSettings(cloud)
	hin := move.HandoffInput{
		Source: move.Side{Machine: src.host, Module: sm, Install: sin}, Session: e.Session, Live: e.Live, Git: e.Git, GitErr: e.GitError,
		Lineage: e.Lineage, Runner: gitOn(src), Here: here.host, Module: mod, Install: in, Host: h, Cloud: cl,
		Settings: move.HandoffSettings{Code: set.Code, Untracked: set.Untracked, BranchPrefix: set.BranchPrefix, DeleteBranch: set.DeleteBranch},
		Allowed:  a.Cfg.CloudAllowed(cloud), Worktrees: a.Reg.Worktrees(), Terminal: a.Steps != nil, FromBranch: broughtBranch(e),
	}
	if t, ok := mod.(agent.CloudTester); ok {
		hin.Tester = func(ctx context.Context) (agent.CloudTest, error) {
			return a.recentTest(ctx, cloud, func(ctx context.Context) (agent.CloudTest, error) { return t.TestCloud(ctx, h, in, cloud) })
		}
	}
	if g := e.Git; g != nil && g.Identity != "" {
		hin.Folder = repos.HandoffFolder(a.StateDir, g.Identity)
		if needsEnv(cl) {
			hin.Env, hin.Envs = set.Environments[g.Identity], a.EnvChoices(inv, cloud, g.Identity)
		}
		if src.Local {
			hin.Checkout = nonEmpty(g.MainWorktree, g.Toplevel)
		} else if found := repos.FindLocal(g.Identity, a.LocalRoots()); len(found) > 0 {
			hin.Checkout = found[0].Path
		}
	}
	return move.BuildHandoff(ctx, hin, opt)
}

// HandoffDefaults are the options a hand-off starts with, from the configuration.
func (a *App) HandoffDefaults(cloud string) move.Options {
	o := a.DefaultOptions()
	set := a.Cfg.CloudSettings(cloud)
	o.HistoryFile, o.Bundle, o.Cleanup = set.HistoryFile, set.Code == "bundle", set.DeleteBranch
	return o
}

// RememberEnv records, after a hand-off worked, the environment it ran in as its
// repository's, when the configuration named none (the caller saves the configuration). It
// reports whether it changed anything.
func (a *App) RememberEnv(p *move.Plan) bool {
	hp := p.Handoff
	if hp == nil || !hp.Remember || hp.Env == "" || hp.Repo == "" || a.Cfg.CloudSettings(hp.Cloud).Environments[hp.Repo] != "" {
		return false
	}
	a.Cfg.SetCloudEnvironment(hp.Cloud, hp.Repo, hp.Env)
	return true
}

// FollowUp sends a message to a cloud session through its module (it starts a model turn
// there, on the user's plan).
func (a *App) FollowUp(ctx context.Context, cloud string, id agent.SessionID, text string) (agent.CloudSession, error) {
	mod, cl, ok := a.cloudModule(cloud)
	if !ok {
		return agent.CloudSession{}, fmt.Errorf("unknown cloud %q", cloud)
	}
	if !a.Cfg.CloudAllowed(cloud) {
		return agent.CloudSession{}, fmt.Errorf("hopsesh leaves %s alone until you allow it (hopsesh clouds allow %s)", cl.Title, cl.Name)
	}
	f, ok := mod.(agent.CloudFollower)
	if !ok {
		if cl.NoFollowUp != "" {
			return agent.CloudSession{}, fmt.Errorf("%w: %s", agent.ErrUnsupported, cl.NoFollowUp)
		}
		return agent.CloudSession{}, fmt.Errorf("%w: hopsesh cannot send %s sessions a message", agent.ErrUnsupported, cl.Title)
	}
	h, in, err := cloudHost(ctx, a.localMachine(ctx), mod, cl)
	if err != nil {
		return agent.CloudSession{}, err
	}
	cs, err := f.FollowUp(ctx, h, in, id, text)
	a.Audit.Write(audit.Entry{Action: "cloud.followup", Session: agent.SessionKey{Agent: mod.Spec().ID, Session: id}.String(),
		Detail: map[string]any{"cloud": cloud, "ok": err == nil, "chars": len(text)}})
	return cs, err
}

// gitOn is git on a scanned machine: here, or over SSH on a Unix-like one (nil otherwise).
func gitOn(m *Machine) repos.Git {
	switch {
	case m.Local:
		return repos.Here{}
	case m.host != nil && m.host.Conn != nil && m.OS != "windows":
		return sshGit{m.host}
	}
	return nil
}

// refsReach reaches git remotes through the checkouts an operation used, here or on a
// configured machine, for undo (journal.Refs).
func (a *App) refsReach(connect func(name string) (*host.Machine, error)) repos.Refs {
	return repos.Refs{Machines: func(_ context.Context, name string) (repos.Git, error) {
		if name == LocalName() {
			return repos.Here{}, nil
		}
		m, err := connect(name)
		if err != nil {
			return nil, err
		}
		if m.Conn == nil || m.Facts.OS == "windows" {
			return nil, fmt.Errorf("hopsesh cannot run git on %s", name)
		}
		return sshGit{m}, nil
	}}
}

// sshGit is git on another machine, over SSH (Unix-like machines).
type sshGit struct{ m *host.Machine }

const sshGitScript = `cd "$1" || exit 2
shift
exec env GIT_TERMINAL_PROMPT=0 GCM_INTERACTIVE=never GIT_SSH_COMMAND="${GIT_SSH_COMMAND:-ssh -o BatchMode=yes}" "$@"`

func (g sshGit) Run(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	argv := append(append(append([]string{dir}, env...), "git"), args...)
	out, err := g.m.Conn.RunSh(ctx, sshGitScript, argv...)
	if err != nil {
		var re *transport.RemoteError
		if errors.As(err, &re) {
			return "", &repos.GitError{Args: args, Stderr: re.Stderr, Err: err}
		}
		return "", err
	}
	return string(out), nil
}

const sshSizesScript = `cd "$1" || exit 2
shift
for f do
  if [ -L "$f" ]; then echo 0; elif [ -f "$f" ]; then wc -c < "$f" | tr -d ' '; else echo -1; fi
done`

func (g sshGit) Sizes(ctx context.Context, dir string, paths []string) ([]int64, error) {
	out, err := g.m.Conn.RunSh(ctx, sshSizesScript, append([]string{dir}, paths...)...)
	if err != nil {
		return nil, err
	}
	lines := strings.Fields(string(out))
	if len(lines) != len(paths) {
		return nil, fmt.Errorf("sizes on %s: %d answers for %d files", g.m.Name, len(lines), len(paths))
	}
	sizes := make([]int64, len(paths))
	for i, l := range lines {
		sizes[i], _ = strconv.ParseInt(l, 10, 64)
	}
	return sizes, nil
}

func (g sshGit) WriteFile(ctx context.Context, p string, b []byte) error {
	fsys, err := g.m.FS(ctx)
	if err != nil {
		return err
	}
	return fsys.WriteFile(p, b, 0o600)
}

func (g sshGit) Remove(ctx context.Context, p string) error {
	fsys, err := g.m.FS(ctx)
	if err != nil {
		return err
	}
	if err := fsys.Remove(p); err != nil {
		if _, serr := fsys.Stat(p); serr != nil {
			return nil // gone already
		}
		return err
	}
	return nil
}

// broughtBranch is the cloud branch a session's code came on, when it was brought here from
// a cloud ("" otherwise): its lineage's last fetch into this copy.
func broughtBranch(e Entry) string {
	l := e.Lineage
	if l == nil {
		return ""
	}
	b := ""
	for _, h := range l.Hops {
		if h.Kind != lineage.HopFetch || h.Code == nil || !l.HasReplica(h.To) {
			continue
		}
		if to := l.Replica(h.To); to.Key == e.Session.Key && to.Location == e.Machine {
			b = h.Code.Branch
		}
	}
	return b
}
