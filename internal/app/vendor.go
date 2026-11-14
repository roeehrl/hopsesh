package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// sessionStamp is a session's stamp for a cache, when its module keeps sessions in a
// database: its file is an anchor, which does not change when the session is used, and may
// be missing. ok is false when there is none to trust: a module without SessionStamper
// stamps from the summary, and a cached row's summary is old.
func sessionStamp(ctx context.Context, mod agent.Module, h agent.Host, in agent.Install, s agent.Summary, cached bool) (agent.SessionStamp, bool) {
	if !agent.IsVirtual(mod) {
		return "", false
	}
	if _, ok := mod.(agent.SessionStamper); !ok && cached {
		return "", false
	}
	st, ok, err := agent.StampOf(ctx, mod, h, in, s)
	return st, ok && err == nil && st != ""
}

// undoSessions reaches, for undo and its checks, the sessions agents keep in databases,
// through each module's SessionStore: on this machine, and on another one through connect
// (over SSH, as the move that changed them did). A check that only lists what can be
// undone (checkOnly) leaves another machine's to the undo.
type undoSessions struct {
	a         *App
	checkOnly bool
	connect   func(name string) (*host.Machine, error) // nil: no other machine is reached
	mu        sync.Mutex
	local     *host.Machine
	found     map[string]undoSession
}

type undoSession struct {
	mod agent.Module
	h   agent.Host
	in  agent.Install
}

func (a *App) undoSessions(checkOnly bool, connect func(string) (*host.Machine, error)) journal.Sessions {
	return &undoSessions{a: a, checkOnly: checkOnly, connect: connect, found: map[string]undoSession{}}
}

func (u *undoSessions) resolve(ctx context.Context, machine string, key agent.SessionKey) (undoSession, error) {
	remote := machine != LocalName()
	switch {
	case remote && u.checkOnly:
		return undoSession{}, fmt.Errorf("%w: the %s session %s is on %s", journal.ErrOutOfReach, key.Agent, key.Session, machine)
	case remote && u.connect == nil:
		return undoSession{}, fmt.Errorf("the session is on %s, which cannot be reached from here", machine)
	}
	mod, ok := u.a.Module(key.Agent)
	if !ok {
		return undoSession{}, fmt.Errorf("%s is turned off", u.a.agentName(key.Agent))
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	id := machine + "\x00" + string(key.Agent) + "\x00" + key.Profile
	if s, ok := u.found[id]; ok {
		return s, nil
	}
	var m *host.Machine
	if remote {
		var err error
		if m, err = u.connect(machine); err != nil {
			return undoSession{}, err
		}
	} else {
		if u.local == nil {
			u.local = u.a.localMachine(ctx)
		}
		m = u.local
	}
	base, err := m.For(ctx, mod.Spec(), agent.Install{}, nil)
	if err != nil {
		return undoSession{}, err
	}
	detected, err := mod.Detect(ctx, base)
	if err != nil {
		return undoSession{}, err
	}
	installs, err := u.a.profileInstalls(ctx, m, mod, detected, false)
	if err != nil {
		return undoSession{}, err
	}
	for _, in := range installs {
		if !in.Present || key.Profile == "" && in.Profile != nil && !in.Profile.Default || key.Profile != "" && in.ProfileID() != key.Profile {
			continue
		}
		h, err := m.For(ctx, mod.Spec(), in, nil)
		if err != nil {
			return undoSession{}, err
		}
		s := undoSession{mod: mod, h: h, in: in}
		u.found[id] = s
		return s, nil
	}
	return undoSession{}, fmt.Errorf("%w: %s", agent.ErrNotInstalled, mod.Spec().Name)
}

func (u *undoSessions) Stamp(ctx context.Context, machine string, key agent.SessionKey) (agent.SessionStamp, error) {
	s, err := u.resolve(ctx, machine, key)
	if err != nil {
		return "", err
	}
	st, ok, err := move.StampNow(ctx, s.mod, s.h, s.in, key)
	if err == nil && !ok {
		err = fmt.Errorf("%w: %s does not stamp its sessions", agent.ErrUnsupported, s.mod.Spec().Name)
	}
	return st, err
}

func (u *undoSessions) store(ctx context.Context, machine string, key agent.SessionKey) (agent.SessionStore, undoSession, error) {
	s, err := u.resolve(ctx, machine, key)
	if err != nil {
		return nil, s, err
	}
	store, ok := s.mod.(agent.SessionStore)
	if !ok {
		return nil, s, fmt.Errorf("%w: %s cannot change its sessions", agent.ErrUnsupported, s.mod.Spec().Name)
	}
	return store, s, nil
}

func (u *undoSessions) Delete(ctx context.Context, machine string, key agent.SessionKey, expect agent.SessionStamp) error {
	store, s, err := u.store(ctx, machine, key)
	if err != nil {
		return err
	}
	return store.Delete(ctx, s.h, s.in, key.Session, expect)
}

func (u *undoSessions) Restore(ctx context.Context, machine string, key agent.SessionKey, backup string) (agent.SessionStamp, error) {
	store, s, err := u.store(ctx, machine, key)
	if err != nil {
		return "", err
	}
	return store.Restore(ctx, s.h, s.in, key.Session, backup)
}
