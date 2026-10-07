package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/presence"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Observation is local evidence, not an authorization to receive, resume or move.
// An empty Endpoint means the installation has never initialized its identity.
type Observation struct {
	Processes         presence.Table `json:"processes,omitempty"`
	OS                string         `json:"os"`
	InventoryComplete bool           `json:"inventoryComplete"`
	Machine           string         `json:"machine"`
	Endpoint          string         `json:"endpoint,omitempty"`
	Agents            []AgentState   `json:"agents"`
	Entries           []Entry        `json:"entries"`
	Problems          []string       `json:"problems,omitempty"`
	WatchRoots        []string       `json:"watchRoots"`
}

// ObserveLocal never adopts imports, registers accounts, changes login bindings,
// applies pending movement marks, or starts vendor programs. Existing bindings are
// cached registration metadata, not fresh observations of the logged-in account.
func (a *App) ObserveLocal(ctx context.Context) (Observation, error) {
	m := &host.Machine{Name: LocalName(), Local: true, Facts: host.ObserveLocal(ctx, a.Specs()), Log: a.Log}
	defer m.Close()
	out, err := a.observeMachine(ctx, m)
	if err != nil {
		out.InventoryComplete = false
	}
	return out, err
}

func (a *App) observeMachine(ctx context.Context, m *host.Machine) (Observation, error) {
	out := Observation{OS: runtime.GOOS, InventoryComplete: true, Machine: m.Name, Agents: []AgentState{}, Entries: []Entry{}, WatchRoots: []string{}}
	endpoint, err := m.ReadIdentity(ctx)
	if err != nil {
		return out, err
	}
	out.Endpoint = endpoint
	ps, err := a.Accounts()
	if err != nil {
		return out, err
	}
	fsys, err := m.FS(ctx)
	if err != nil {
		return out, err
	}
	for _, mod := range a.Modules() {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		spec := mod.Spec()
		h, err := m.For(ctx, spec, agent.Install{}, nil)
		if err != nil {
			return out, err
		}
		h = observationHost{h}
		base, err := mod.Detect(ctx, h)
		if err != nil {
			out.InventoryComplete = false
			out.Agents = append(out.Agents, AgentState{Agent: spec.ID, Name: spec.Name, Error: err.Error()})
			continue
		}
		installs := []agent.Install{}
		defaultRegistered := false
		for _, p := range ps {
			if endpoint == "" || p.Endpoint != endpoint || p.Agent != spec.ID {
				continue
			}
			if rootsEqual(m, fsys, p.Root, base.Root("home")) {
				defaultRegistered = true
			}
			scoped := base
			scoped.Accounts, scoped.Profile = spec.Accounts, &p
			scoped.Roots = map[string]string{"home": p.Root}
			ch, err := m.For(ctx, spec, scoped, nil)
			if err != nil {
				return out, err
			}
			in, err := mod.Detect(ctx, observationHost{ch})
			if err != nil {
				out.InventoryComplete = false
				out.Problems = append(out.Problems, fmt.Sprintf("%s/%s: %v", spec.ID, p.ID, err))
				continue
			}
			in.Accounts, in.Profile = spec.Accounts, &p
			canonical, err := fsys.RealPath(p.Root)
			if err != nil || m.Path().Clean(canonical) != p.Root {
				out.InventoryComplete = false
				in.Present = false
				in.Profile.Error = "Account root is missing or its symbolic link changed; re-register it"
			}
			installs = append(installs, in)
		}
		if !defaultRegistered {
			installs = append(installs, base)
		}
		for _, in := range installs {
			state := AgentState{Agent: spec.ID, Name: spec.Name, Install: in}
			if in.Profile != nil {
				state.Error = in.Profile.Error
			}
			out.Agents = append(out.Agents, state)
			for _, root := range in.Roots {
				if root != "" {
					out.WatchRoots = append(out.WatchRoots, root)
				}
			}
			if !in.Present {
				// DefaultInstall cannot distinguish an absent root from a failed stat.
				// Only confirmed absence may support a negative inventory result.
				if len(spec.Roots) > 0 {
					info, err := fsys.Stat(in.Root(spec.Roots[0].Name))
					if err != nil && !errors.Is(err, fs.ErrNotExist) {
						out.InventoryComplete = false
						out.Agents[len(out.Agents)-1].Error = err.Error()
					} else if err == nil && !info.IsDir() {
						out.InventoryComplete = false
						out.Agents[len(out.Agents)-1].Error = "Agent state root is not a directory"
					}
				}
				continue
			}
			ch, err := m.For(ctx, spec, in, nil)
			if err != nil {
				return out, err
			}
			ch = observationHost{ch}
			listing, err := mod.List(ctx, ch, in)
			if err != nil {
				out.InventoryComplete = false
				out.Agents[len(out.Agents)-1].Error = err.Error()
				continue
			}
			if len(listing.Errors) > 0 {
				out.Agents[len(out.Agents)-1].Error = "Some session files could not be read"
			}
			for _, problem := range listing.Errors {
				out.InventoryComplete = false
				out.Problems = append(out.Problems, problem.Error())
			}
			ids := make([]agent.SessionID, len(listing.Sessions))
			for i := range listing.Sessions {
				listing.Sessions[i].Key.Profile = in.ProfileID()
				ids[i] = listing.Sessions[i].Key.Session
			}
			live := map[agent.SessionID]agent.LiveInfo{}
			if detector, ok := mod.(agent.LiveDetector); ok && len(ids) > 0 {
				live, err = detector.Live(ctx, ch, in, ids)
				if err != nil {
					live = nil
					out.Problems = append(out.Problems, fmt.Sprintf("%s/%s presence: %v", spec.ID, in.ProfileID(), err))
				}
			}
			manifests, problems := readManifests(fsys, listing.Sessions)
			for i, s := range listing.Sessions {
				if manifests[i] == nil && problems[i] == "" && s.NativeParent != "" {
					problems[i] = "Native fork ancestry requires verification before transfer"
				}
				lv := live[s.Key.Session]
				if lv.State == "" {
					lv.State = agent.Unknown
				}
				out.Entries = append(out.Entries, Entry{Location: agent.MachineLocation(m.Name), Machine: m.Name, Agent: spec.ID, AgentName: spec.Name, Session: s, Live: lv, Profile: in.Profile, Lineage: manifests[i], LineageError: problems[i]})
			}
		}
	}
	// Repository probes are bounded, read-only Git queries owned by the source.
	// Modules still cannot execute vendor commands through observationHost.
	var dirs []string
	for _, e := range out.Entries {
		if d := e.Session.CWD; d != "" && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	states, gitErr := m.GitProbe(ctx, dirs, a.Reg.Worktrees())
	byDir := map[string]*repos.GitState{}
	for i := range states {
		byDir[states[i].Dir] = &states[i]
	}
	for i := range out.Entries {
		if d := out.Entries[i].Session.CWD; d != "" {
			setGit(&out.Entries[i], byDir[d], gitErr)
		}
	}
	table, tableErr := presence.Snapshot(ctx)
	if tableErr != nil {
		out.Problems = append(out.Problems, "Process places unavailable: "+tableErr.Error())
	}
	var pids []int
	for _, e := range out.Entries {
		if e.Live.PID > 0 {
			pids = append(pids, e.Live.PID)
		}
		for _, p := range e.Live.Procs {
			pids = append(pids, p.PID)
		}
	}
	out.Processes = table.Ancestors(pids)
	inv := &Inventory{Machines: []*Machine{{Kind: agent.AtMachine, Name: m.Name, Local: true, Status: StatusOK, OS: m.Facts.OS, Agents: out.Agents, host: m}}, Entries: out.Entries}
	a.enrichMovement(ctx, inv, true)
	out.Entries = inv.Entries
	for i := range out.Entries {
		if out.Entries[i].ObservedAt.IsZero() {
			out.Entries[i].ObservedAt = time.Now().UTC()
		}
	}
	slices.Sort(out.WatchRoots)
	out.WatchRoots = slices.Compact(out.WatchRoots)
	slices.SortFunc(out.Entries, func(a, b Entry) int {
		return strings.Compare(string(a.Agent)+"/"+a.Session.Key.String(), string(b.Agent)+"/"+b.Session.Key.String())
	})
	return out, ctx.Err()
}

// moduleFS already denies writes when no journal is supplied. Also deny execution
// and termination so a future module cannot accidentally turn observation into action.
type observationHost struct{ agent.Host }

func (h observationHost) Exec() agent.Exec   { return observationExec{} }
func (h observationHost) Procs() agent.Procs { return observationProcs{h.Host.Procs()} }

type observationExec struct{}

func (observationExec) Run(context.Context, []string, agent.RunOptions) (agent.Result, error) {
	return agent.Result{}, fmt.Errorf("%w: passive observation cannot execute vendor commands", agent.ErrDenied)
}

type observationProcs struct{ agent.Procs }

func (observationProcs) Terminate(context.Context, int) error {
	return fmt.Errorf("%w: passive observation cannot terminate processes", agent.ErrDenied)
}

// ObservationInventory reattaches local filesystem access for explicit preview and
// transfer actions without repeating listing or registration. Cached login identity
// is still checked by the existing transfer planner before committing anything.
func (a *App) ObservationInventory(ctx context.Context, o Observation) *Inventory {
	hm := &host.Machine{Name: o.Machine, Local: true, Facts: host.ObserveLocal(ctx, a.Specs()), Log: a.Log}
	hm.Facts.Endpoint = o.Endpoint
	return &Inventory{Machines: []*Machine{{Kind: agent.AtMachine, Name: o.Machine, Local: true, Status: StatusOK, OS: o.OS, Agents: o.Agents, host: hm}}, Entries: o.Entries}
}
