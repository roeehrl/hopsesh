package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/host"
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
			switch r.match(q, e) {
			case matchExact:
				exact = append(exact, e)
			case matchPrefix:
				prefix = append(prefix, e)
			case matchTitle:
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

// How a reference matches a session, best first.
const (
	matchNone = iota
	matchExact
	matchPrefix
	matchTitle
)

// match is how r names e (q is r.Query in lower case).
func (r Ref) match(q string, e Entry) int {
	if r.Machine != "" && e.Machine != r.Machine || r.Agent != "" && e.Agent != r.Agent {
		return matchNone
	}
	sid := string(e.Session.Key.Session)
	switch {
	case sid == r.Query || strings.ToLower(e.Session.Title) == q:
		return matchExact
	case len(r.Query) >= 4 && strings.HasPrefix(sid, r.Query):
		return matchPrefix
	case q != "" && strings.Contains(strings.ToLower(e.Session.Title), q):
		return matchTitle
	}
	return matchNone
}

// GitFor narrows a scan's git probe (ScanOptions.GitFor) to what working with the sessions
// r names needs: the folders of every session r could match, and of the sessions whose
// lineage names a cloud copy (a cloud session's repository and checkout come from them).
// Other folders are left alone, so one that git cannot read in time (offloaded to a cloud
// drive, say) does not hold up a command about another session.
func (a *App) GitFor(r Ref) func(Entry) bool {
	q := strings.ToLower(r.Query)
	return func(e Entry) bool {
		if r.match(q, e) != matchNone {
			return true
		}
		if e.Lineage != nil {
			for _, rep := range e.Lineage.Replicas {
				if a.IsCloud(rep.Location) {
					return true
				}
			}
		}
		return false
	}
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

// Plan works out how a session comes here, in the same agent or (target set) another. A
// cloud session is brought from its cloud (a fetch).
func (a *App) Plan(ctx context.Context, inv *Inventory, e Entry, target agent.ID, opt move.Options) (*move.Plan, move.Input, error) {
	if e.LineageError != "" && (e.CanArchiveLineage || !opt.Fork) {
		return nil, move.Input{}, fmt.Errorf("cannot transfer with unsupported or damaged lineage: %s; archive the sidecar explicitly to start a new family", e.LineageError)
	}
	if e.Location.IsCloud() {
		return a.planFetch(ctx, inv, e, target, opt)
	}
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
		GitErr:    e.GitError,
		Lineage:   e.Lineage,
		Target:    move.Side{Machine: here.host, Module: tm, Install: tin},
		Worktrees: a.Reg.Worktrees(),
		Push:      pushFunc(src),
		GitFetch:  gitFetchFunc(src),
	}
	related := map[agent.SessionKey]bool{e.Session.Key: true}
	if e.Lineage != nil {
		for _, r := range e.Lineage.Replicas {
			if r.Line != e.Lineage.Branch {
				continue
			}
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
	} else if nin, ok := here.Install(e.Agent); ok && src.Name != here.Name {
		// Continuing on another machine: keep the source agent's own copy here too.
		in.Source.Account = a.account(ctx, src, sm, sin)
		ns := &move.NativeSide{Target: move.Side{Machine: here.host, Module: sm, Install: nin, Account: a.account(ctx, here, sm, nin)}}
		for _, c := range inv.Entries {
			if c.Machine == here.Name && c.Agent == e.Agent && related[c.Session.Key] {
				ns.Copies = append(ns.Copies, move.Copy{Summary: c.Session, Live: c.Live, Lineage: c.Lineage})
			}
		}
		in.Native = ns
	}
	if rc := a.Cfg.Agents[string(target)].RemoteControl; rc && !opt.RemoteControl {
		opt.RemoteControl = true
	}
	switch opt.Via {
	case move.ViaHopsesh:
		opt.Via = ""
	case "":
		if imp, ok := tm.(agent.Importer); ok && target != e.Agent && a.Cfg.Agents[string(target)].Import && imp.CanImport(e.Agent) {
			opt.Via = move.ViaImport
		}
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
	if p.Kind == move.KindHop {
		return a.applyHop(ctx, p, progress)
	}
	return move.Apply(ctx, p, in, move.Env{StateDir: a.StateDir, Audit: a.Audit, Progress: progress, Step: a.Steps})
}

// gitFetchFunc is how to fetch from a repository on a machine over SSH (nil for this
// machine and for Windows).
func gitFetchFunc(m *Machine) func(string) *repos.FetchSource {
	if m.Local || m.host == nil || m.host.Conn == nil {
		return nil
	}
	conn, name := m.host.Conn, m.Name
	if m.OS == "windows" {
		return func(dir string) *repos.FetchSource {
			return &repos.FetchSource{Name: name, Bundle: windowsBundle(m.host, dir)}
		}
	}
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
			out, err := repos.Push(ctx, dir)
			if errors.Is(err, repos.ErrNoUpstream) {
				return "", err
			}
			return pushResult(out, err)
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

// windowsBundle fetches from a Windows machine through a git bundle: git over ssh does not
// work there (its OpenSSH runs commands through cmd.exe, which mangles git's quoting), so
// the machine writes a bundle of the ref with PowerShell, and it comes over SFTP.
func windowsBundle(h *host.Machine, dir string) func(context.Context, string) (string, func(), error) {
	return func(ctx context.Context, ref string) (string, func(), error) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
		script := "$f = Join-Path $env:TEMP ('hopsesh-' + [guid]::NewGuid() + '.bundle')\n" +
			"git -C " + q(dir) + " bundle create $f " + q(ref) + " 2>&1 | Out-Null\n" +
			"if ($LASTEXITCODE -ne 0) { exit 1 }\n" +
			"Write-Output $f\n"
		out, err := h.Conn.RunPowerShell(ctx, script)
		if err != nil {
			return "", nil, fmt.Errorf("git bundle on %s: %w", h.Name, err)
		}
		remote := strings.TrimSpace(string(out))
		fsys, err := h.FS(ctx)
		if err != nil {
			return "", nil, err
		}
		defer func() { _ = fsys.Remove(remote) }()
		b, err := fsys.ReadFile(remote, 1<<30)
		if err != nil {
			return "", nil, fmt.Errorf("reading the bundle from %s: %w", h.Name, err)
		}
		f, err := os.CreateTemp("", "hopsesh-*.bundle")
		if err != nil {
			return "", nil, err
		}
		if _, err := f.Write(b); err != nil {
			f.Close()
			os.Remove(f.Name())
			return "", nil, err
		}
		f.Close()
		return f.Name(), func() { os.Remove(f.Name()) }, nil
	}
}
