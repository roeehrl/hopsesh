package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Ref names a session: [machine:][agent/]<id, id prefix or title>.
type Ref struct {
	Machine string
	Agent   agent.ID
	Query   string
}

// ParseRef reads a session reference.
func ParseRef(s string) Ref {
	var r Ref
	if m, rest, ok := strings.Cut(s, ":"); ok && !strings.ContainsAny(m, "/ ") {
		r.Machine, s = m, rest
	}
	if a, rest, ok := strings.Cut(s, "/"); ok && a != "" && !strings.ContainsAny(a, " ") && len(a) <= 16 && strings.ToLower(a) == a {
		r.Agent, s = agent.ID(a), rest
	}
	r.Query = strings.TrimSpace(s)
	return r
}

// ErrAmbiguous means a reference matches several sessions.
var ErrAmbiguous = errors.New("several sessions match")

// Find returns the session a reference names. Without a machine, the newest copy of the
// session is chosen.
func (inv *Inventory) Find(r Ref) (Entry, error) {
	q := strings.ToLower(r.Query)
	var exact, prefix, title []Entry
	for _, it := range inv.Items() {
		cands := []Entry{it.Entry}
		if r.Machine != "" || r.Agent != "" {
			cands = inv.copiesOf(it)
		}
		for _, e := range cands {
			if r.Machine != "" && e.Machine != r.Machine || r.Agent != "" && e.Agent != r.Agent {
				continue
			}
			sid := string(e.Session.Key.Session)
			switch {
			case sid == r.Query || strings.ToLower(e.Session.Title) == q:
				exact = append(exact, e)
			case len(r.Query) >= 4 && strings.HasPrefix(sid, r.Query):
				prefix = append(prefix, e)
			case q != "" && strings.Contains(strings.ToLower(e.Session.Title), q):
				title = append(title, e)
			}
		}
	}
	for _, set := range [][]Entry{exact, prefix, title} {
		switch len(set) {
		case 0:
			continue
		case 1:
			return set[0], nil
		}
		var names []string
		for _, e := range set {
			names = append(names, fmt.Sprintf("%s:%s (%s)", e.Machine, e.Session.Key, e.Session.Title))
		}
		return Entry{}, fmt.Errorf("%w: %s", ErrAmbiguous, strings.Join(names, ", "))
	}
	return Entry{}, fmt.Errorf("%w: no session matches %q", agent.ErrNotFound, r.Query)
}

// copiesOf returns every entry of an item (all its copies).
func (inv *Inventory) copiesOf(it Item) []Entry {
	if len(it.Copies) == 0 {
		return []Entry{it.Entry}
	}
	var out []Entry
	for _, c := range it.Copies {
		for _, e := range inv.Entries {
			if e.Machine == c.Machine && e.Session.Key == c.Key {
				out = append(out, e)
			}
		}
	}
	return out
}

// DefaultOptions are move options from the configuration.
func (a *App) DefaultOptions() move.Options {
	return move.Options{
		ReposDir: a.Cfg.ReposDir, GHQLayout: a.Cfg.Layout == "ghq", Mark: a.Cfg.MarkMovedOn(),
		SyncCode: a.Cfg.SyncCodeOn(), Push: a.Cfg.PushSource,
	}
}

// Plan works out how a session comes here, in the same agent or (target set) another.
func (a *App) Plan(ctx context.Context, inv *Inventory, e Entry, target agent.ID, opt move.Options) (*move.Plan, move.Input, error) {
	src := inv.Machine(e.Machine)
	here := inv.Local()
	if src == nil || src.host == nil {
		return nil, move.Input{}, fmt.Errorf("%s was not reached", e.Machine)
	}
	if here == nil || here.host == nil {
		return nil, move.Input{}, errors.New("this machine was not scanned")
	}
	if target == "" {
		target = e.Agent
	}
	sm, ok := a.Module(e.Agent)
	if !ok {
		return nil, move.Input{}, fmt.Errorf("%s is not enabled", e.Agent)
	}
	tm, ok := a.Module(target)
	if !ok {
		return nil, move.Input{}, fmt.Errorf("there is no %q agent module", target)
	}
	tin, ok := here.Install(target)
	if !ok {
		return nil, move.Input{}, fmt.Errorf("%w: %s has no data folder on this machine yet; start it here once, then try again", agent.ErrNotInstalled, tm.Spec().Name)
	}
	sin, _ := src.Install(e.Agent)
	in := move.Input{
		Source:    move.Side{Machine: src.host, Module: sm, Install: sin},
		Session:   e.Session,
		Live:      e.Live,
		Git:       e.Git,
		Lineage:   e.Lineage,
		Target:    move.Side{Machine: here.host, Module: tm, Install: tin},
		Worktrees: a.Reg.Worktrees(),
		Push:      pushFunc(src),
		GitFetch:  gitFetchFunc(src),
	}
	related := map[agent.SessionKey]bool{e.Session.Key: true}
	if e.Lineage != nil {
		for _, r := range e.Lineage.Replicas {
			related[r.Key] = true
		}
	}
	for _, c := range inv.Entries {
		if c.Machine == here.Name && c.Agent == target && related[c.Session.Key] {
			in.Copies = append(in.Copies, move.Copy{Summary: c.Session, Live: c.Live, Lineage: c.Lineage})
		}
	}
	if target == e.Agent {
		in.Source.Account, in.Target.Account = a.account(ctx, src, sm, sin), a.account(ctx, here, tm, tin)
	}
	if rc := a.Cfg.Agents[string(target)].RemoteControl; rc && !opt.RemoteControl {
		opt.RemoteControl = true
	}
	p, err := move.Build(ctx, in, opt)
	return p, in, err
}

func (a *App) account(ctx context.Context, m *Machine, mod agent.Module, in agent.Install) *agent.Account {
	if m.account != nil {
		return m.account
	}
	ap, ok := mod.(agent.AccountProber)
	if !ok {
		return nil
	}
	h, err := m.host.For(ctx, mod.Spec(), in, nil)
	if err != nil {
		return nil
	}
	acct, err := ap.Account(ctx, h, in)
	if err != nil {
		return nil
	}
	return &acct
}

// Apply carries out a plan.
func (a *App) Apply(ctx context.Context, p *move.Plan, in move.Input, progress func(string)) (*move.Result, error) {
	return move.Apply(ctx, p, in, move.Env{StateDir: a.StateDir, Audit: a.Audit, Progress: progress})
}

// gitFetchFunc is how to fetch from a repository on a machine over SSH (nil for this
// machine and for Windows).
func gitFetchFunc(m *Machine) func(string) *repos.FetchSource {
	if m.Local || m.host == nil || m.host.Conn == nil || m.OS == "windows" {
		return nil
	}
	conn, name := m.host.Conn, m.Name
	return func(dir string) *repos.FetchSource {
		env := append([]string{"GIT_SSH_COMMAND=" + conn.GitSSHCommand()}, conn.GitSSHEnv(context.Background())...)
		return &repos.FetchSource{Name: name, URL: conn.GitURL(dir), Env: env}
	}
}

// pushFunc is how to push a branch on a machine (nil when it cannot).
func pushFunc(m *Machine) func(context.Context, string) (string, error) {
	switch {
	case m.Local:
		return func(ctx context.Context, dir string) (string, error) {
			out, err := exec.CommandContext(ctx, "sh", "-c", repos.PushScript, "hopsesh", dir).CombinedOutput()
			return pushResult(string(out), err)
		}
	case m.host != nil && m.host.Conn != nil && m.OS != "windows":
		conn := m.host.Conn
		return func(ctx context.Context, dir string) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			out, err := conn.RunSh(ctx, repos.PushScript, dir)
			var re *transport.RemoteError
			if errors.As(err, &re) {
				return pushResult(re.Stderr+string(out), err)
			}
			return pushResult(string(out), err)
		}
	}
	return nil
}

func pushResult(out string, err error) (string, error) {
	out = strings.TrimSpace(out)
	if err == nil {
		if out == "" {
			return "pushed", nil
		}
		return out, nil
	}
	if strings.Contains(out, "no-upstream") {
		return "", errors.New("the branch has no upstream")
	}
	if out == "" {
		out = err.Error()
	}
	return "", fmt.Errorf("git push failed: %s", strings.SplitN(out, "\n", 2)[0])
}

// LocalRoots are folders searched for checkouts on this machine.
func (a *App) LocalRoots() []string {
	home, _ := os.UserHomeDir()
	return append([]string{a.Cfg.ReposDir}, repos.DefaultRoots(home)...)
}
