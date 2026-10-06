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
	"unicode"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/profiles"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func (a *App) Accounts() ([]agent.RuntimeProfile, error) { return a.accountStore().List() }
func (a *App) accountStore() profiles.Store              { return profiles.Store{Dir: a.StateDir} }
func (a *App) EditAccount(id, name string, tags []string, generation int) error {
	return a.accountStore().Edit(id, name, tags, generation)
}
func (a *App) ForgetAccount(id string, generation int) error {
	return a.accountStore().Forget(id, generation)
}

// RegisterAccount adopts a known root, or creates an empty private root on this machine.
// Authentication is a separate explicit vendor CLI action. No credentials are copied.
func (a *App) RegisterAccount(ctx context.Context, machine string, id agent.ID, name, root string, tags []string) (agent.RuntimeProfile, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 || strings.ContainsFunc(name, unicode.IsControl) {
		return agent.RuntimeProfile{}, errors.New("use an account name of 1–120 bytes without control characters")
	}
	var err error
	tags, err = profiles.Tags(tags)
	if err != nil {
		return agent.RuntimeProfile{}, err
	}
	m, err := a.accountMachine(ctx, machine)
	if err != nil {
		return agent.RuntimeProfile{}, err
	}
	defer m.Close()
	mod, ok := a.Module(id)
	if !ok || mod.Spec().Accounts == nil {
		return agent.RuntimeProfile{}, errors.New("account profiles support Claude Code and Codex")
	}
	endpoint, err := m.PrepareIdentity(ctx)
	if err != nil {
		return agent.RuntimeProfile{}, err
	}
	if err = m.CommitIdentity(ctx); err != nil {
		return agent.RuntimeProfile{}, err
	}
	p := agent.RuntimeProfile{Machine: m.Name, ID: profiles.ID(), Endpoint: endpoint, Agent: id, Name: name, Tags: tags}
	if root == "" {
		if !m.Local {
			return p, errors.New("create remote accounts using hopsesh accounts add on that machine, then adopt its root here")
		}
		p.Managed = true
		root = filepath.Join(a.StateDir, "profiles", p.ID, string(id))
		if err = os.MkdirAll(root, 0700); err != nil {
			return p, err
		}
		for file, contents := range mod.Spec().Accounts.InitialFiles {
			if err = os.WriteFile(filepath.Join(root, file), []byte(contents), 0600); err != nil {
				return p, err
			}
		}
	}
	if !m.Path().IsAbs(root) {
		return p, errors.New("account root must be an absolute path on the selected machine")
	}
	fsys, err := m.FS(ctx)
	if err != nil {
		return p, err
	}
	root, err = fsys.RealPath(root)
	if err != nil {
		return p, fmt.Errorf("account root: %w", err)
	}
	st, err := fsys.Stat(root)
	if err != nil {
		return p, err
	}
	if !st.IsDir() {
		return p, errors.New("account root must be a directory")
	}
	p.Root = m.Path().Clean(root)
	existing, err := a.Accounts()
	if err != nil {
		return p, err
	}
	if !m.Local {
		if err = a.discoverRemoteProfiles(ctx, m, endpoint, id, existing); err != nil {
			return p, err
		}
		existing, err = a.Accounts()
		if err != nil {
			return p, err
		}
		for _, old := range existing {
			if old.Endpoint == endpoint && old.Agent == id && rootsEqual(m, fsys, old.Root, p.Root) {
				if err = a.EditAccount(old.ID, name, tags, old.Generation); err != nil {
					return p, err
				}
				updated, e := a.Accounts()
				if e != nil {
					return p, e
				}
				for _, v := range updated {
					if v.ID == old.ID {
						return v, nil
					}
				}
			}
		}
		h, e := m.For(ctx, mod.Spec(), agent.Install{}, nil)
		if e != nil {
			return p, e
		}
		base, e := mod.Detect(ctx, h)
		if e != nil {
			return p, e
		}
		if !rootsEqual(m, fsys, base.Root("home"), p.Root) {
			return p, errors.New("register this custom root with hopsesh accounts add --root on its machine first, then scan accounts here")
		}
		p.ID = profiles.RootID(endpoint, id, p.Root)
		p.Default = true
	}
	for _, old := range existing {
		if old.Endpoint == endpoint && old.Agent == id && rootsEqual(m, fsys, old.Root, p.Root) {
			return p, fmt.Errorf("this root is already registered as %q", old.Name)
		}
	}
	return a.accountStore().Register(p)
}
func (a *App) accountMachine(ctx context.Context, name string) (*host.Machine, error) {
	if name == "" || name == "local" || name == LocalName() {
		return a.localMachine(ctx), nil
	}
	h := a.Cfg.FindHost(name)
	if h == nil || !h.Allowed {
		return nil, errors.New("machine is not allowed")
	}
	return a.Connect(ctx, *h)
}

// profileInstalls discovers only the module's known default and registered roots.
// Remote discovery never initializes an endpoint or creates a root.
func (a *App) profileInstalls(ctx context.Context, m *host.Machine, mod agent.Module, base agent.Install, force bool) ([]agent.Install, error) {
	if mod.Spec().Accounts == nil {
		return []agent.Install{base}, nil
	}
	endpoint, err := m.ReadIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if endpoint == "" && m.Local {
		endpoint, err = m.PrepareIdentity(ctx)
		if err == nil {
			err = m.CommitIdentity(ctx)
		}
		if err != nil {
			return nil, err
		}
	}
	if endpoint == "" {
		return []agent.Install{base}, nil
	}
	fsys, err := m.FS(ctx)
	if err != nil {
		return nil, err
	}
	ps, err := a.Accounts()
	if err != nil {
		return nil, err
	}
	if !m.Local {
		if err = a.discoverRemoteProfiles(ctx, m, endpoint, base.Agent, ps); err != nil {
			return nil, err
		}
		ps, err = a.Accounts()
		if err != nil {
			return nil, err
		}
	}
	root := base.Root("home")
	if base.Present && root != "" {
		root, err = fsys.RealPath(root)
		if err != nil {
			return nil, err
		}
		root = m.Path().Clean(root)
		found := false
		for _, p := range ps {
			if p.Endpoint == endpoint && p.Agent == base.Agent && rootsEqual(m, fsys, p.Root, root) {
				found = true
			}
		}
		if !found {
			p := agent.RuntimeProfile{Machine: m.Name, ID: profiles.RootID(endpoint, base.Agent, root), Endpoint: endpoint, Agent: base.Agent, Name: mod.Spec().Name + " default", Root: root, Default: true}
			// Another front end can discover the same root concurrently. Re-read after registration.
			_, regErr := a.accountStore().Register(p)
			ps, err = a.Accounts()
			if err != nil {
				return nil, err
			}
			found = false
			for _, q := range ps {
				if q.ID == p.ID {
					found = true
				}
			}
			if !found && regErr != nil {
				return nil, regErr
			}
		}
	}

	defaultID := ""
	if base.Present {
		for _, p := range ps {
			if p.Endpoint == endpoint && p.Agent == base.Agent && rootsEqual(m, fsys, p.Root, root) {
				defaultID = p.ID
				break
			}
		}
	}
	needsDefaultUpdate := false
	for _, p := range ps {
		if p.Endpoint == endpoint && p.Agent == base.Agent && p.Default != (p.ID == defaultID) {
			needsDefaultUpdate = true
		}
	}
	if needsDefaultUpdate {
		if err = a.accountStore().SetDefault(endpoint, base.Agent, defaultID); err != nil {
			return nil, err
		}
		ps, err = a.Accounts()
		if err != nil {
			return nil, err
		}
	}
	out := []agent.Install{}
	for _, p := range ps {
		if p.Endpoint != endpoint || p.Agent != base.Agent {
			continue
		}
		scoped := base
		scoped.Accounts = mod.Spec().Accounts
		scoped.Profile = &p
		scoped.Roots = map[string]string{"home": p.Root}
		// Detect recomputes derived roots against the pinned environment.
		h, e := m.For(ctx, mod.Spec(), scoped, nil)
		if e != nil {
			return nil, e
		}
		in, e := mod.Detect(ctx, h)
		if e != nil {
			return nil, e
		}
		in.Profile = &p
		canonical, e := fsys.RealPath(p.Root)
		if e != nil || m.Path().Clean(canonical) != p.Root {
			in.Present = false
			p.Error = "Account root is missing or its symbolic link changed; re-register it"
			if fresh, e := a.accountStore().Observe(p.ID, nil, p.Error); e == nil {
				p = fresh
				in.Profile = &p
			}
			out = append(out, in)
			continue
		}
		if ap, ok := mod.(agent.AccountProber); ok && in.Binary != "" && (force || p.CheckedAt.IsZero() || time.Since(p.CheckedAt) > 5*time.Minute) {
			ch, e := m.For(ctx, mod.Spec(), in, nil)
			var acct agent.Account
			if e == nil {
				acct, e = ap.Account(ctx, ch, in)
			}
			problem := ""
			var observed *agent.Account
			if e != nil {
				problem = e.Error()
			} else {
				observed = &acct
			}
			fresh, e := a.accountStore().Observe(p.ID, observed, problem)
			if e != nil {
				return nil, e
			}
			in.Profile = &fresh
		}
		out = append(out, in)
	}
	if len(out) == 0 {
		return []agent.Install{base}, nil
	}
	return out, nil
}

// AccountLogin returns a vendor-owned login command; callers explicitly run it in a
// terminal. Desktop deep links are not used because they cannot select this profile.
func (a *App) AccountLogin(ctx context.Context, id string) (agent.Command, error) {
	ps, err := a.Accounts()
	if err != nil {
		return agent.Command{}, err
	}
	m := a.localMachine(ctx)
	defer m.Close()
	endpoint, err := m.ReadIdentity(ctx)
	if err != nil {
		return agent.Command{}, err
	}
	for _, p := range ps {
		if p.ID != id {
			continue
		}
		if p.Endpoint != endpoint {
			return agent.Command{}, errors.New("sign in on the account's machine using hopsesh accounts login " + id)
		}
		mod, ok := a.Module(p.Agent)
		if !ok {
			return agent.Command{}, errors.New("agent is disabled")
		}
		in := agent.Install{Accounts: mod.Spec().Accounts, Agent: p.Agent, Profile: &p, Roots: map[string]string{"home": p.Root}, Binary: m.Facts.Binaries[string(p.Agent)].Path}
		if in.Binary == "" {
			return agent.Command{}, fmt.Errorf("install %s first", mod.Spec().Name)
		}
		fsys, e := m.FS(ctx)
		if e != nil {
			return agent.Command{}, e
		}
		canonical, e := fsys.RealPath(p.Root)
		if e != nil || m.Path().Clean(canonical) != p.Root {
			return agent.Command{}, errors.New("account root changed; register it again")
		}
		if in.Accounts == nil {
			return agent.Command{}, errors.New("agent does not support isolated accounts")
		}
		args := append([]string{in.Binary}, in.Accounts.Login...)
		return in.ScopeCommand(agent.Command{Argv: args, Dir: m.Facts.Home}), nil
	}
	return agent.Command{}, errors.New("account not found")
}

// checkAccountRegistration prevents a cached GUI/TUI plan from using an edited or
// removed root. Public auth metadata is checked again by the move engine before writes.
func (a *App) checkAccountRegistration(in agent.Install) error {
	if in.Profile == nil {
		return nil
	}
	ps, err := a.Accounts()
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.ID != in.Profile.ID {
			continue
		}
		if p.Generation != in.Profile.Generation || p.Root != in.Profile.Root || p.Binding != in.Profile.Binding {
			return fmt.Errorf("account %q changed; scan and plan again", p.Name)
		}
		return nil
	}
	return errors.New("account was removed; scan and plan again")
}

func (a *App) discoverRemoteProfiles(ctx context.Context, m *host.Machine, endpoint string, id agent.ID, known []agent.RuntimeProfile) error {
	pa := m.Path()
	dir := m.Facts.Env["HOPSESH_STATE_DIR"]
	if dir == "" {
		if m.Facts.OS == "windows" && m.Facts.Env["LOCALAPPDATA"] != "" {
			dir = pa.Join(m.Facts.Env["LOCALAPPDATA"], "hopsesh")
		} else if m.Facts.Env["XDG_STATE_HOME"] != "" {
			dir = pa.Join(m.Facts.Env["XDG_STATE_HOME"], "hopsesh")
		} else {
			dir = pa.Join(m.Facts.Home, ".local", "state", "hopsesh")
		}
	}
	fsys, err := m.FS(ctx)
	if err != nil {
		return err
	}
	b, err := fsys.ReadFile(pa.Join(dir, "accounts.json"), 1<<20)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remote account registry: %w", err)
	}
	ps, err := profiles.DecodeRegistrations(b)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.Endpoint != endpoint || p.Agent != id || !pa.IsAbs(p.Root) {
			continue
		}
		root, err := fsys.RealPath(p.Root)
		if err != nil {
			continue
		}
		p.Root = pa.Clean(root)
		p.Machine = m.Name
		found := false
		for _, k := range known {
			if k.Endpoint == endpoint && k.Agent == id && rootsEqual(m, fsys, k.Root, p.Root) {
				p.Root = k.Root
				if err := a.accountStore().ImportObservation(k.ID, p); err != nil {
					return err
				}
				found = true
				break
			}
		}
		if found {
			continue
		}
		if _, err = a.accountStore().Register(p); err != nil {
			return err
		}
	}
	return nil
}

func rootsEqual(m *host.Machine, fsys host.FS, a, b string) bool {
	if a == b {
		return true
	}
	if m.Facts.OS == "windows" || m.Facts.OS == "darwin" {
		if strings.EqualFold(a, b) {
			return true
		}
	}
	if m.Local {
		ai, ae := fsys.Stat(a)
		bi, be := fsys.Stat(b)
		return ae == nil && be == nil && os.SameFile(ai, bi)
	}
	return false
}
