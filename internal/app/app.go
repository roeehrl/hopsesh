// Package app is what hopsesh does, for every front end (command line, terminal UI,
// desktop app, the agents' skill): scan machines for sessions of every agent, plan and
// carry out moves and continuations, undo them, and keep the agents' integration current.
package app

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/catalog"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/core/termapp"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// App holds what every use case needs.
type App struct {
	activity      *runtimeActivity
	Catalog       *catalog.Store
	movementReads *movementReadCache
	Cfg           config.Config
	Reg           *registry.Registry // every compiled-in module
	StateDir      string
	Audit         *audit.Log
	Log           *slog.Logger
	// Passwords answers ssh password questions for machines that log in with one (nil:
	// such machines are reported, not scanned).
	Passwords func(h config.Host) transport.PasswordFunc
	// PeerDial reaches hopsesh on a configured machine (nil: over SSH).
	PeerDial func(ctx context.Context, h config.Host) (*PeerConn, error)
	// CloudTimeout bounds one cloud's listing in a scan (0: DefaultCloudTimeout).
	CloudTimeout time.Duration
	// Steps runs a cloud driver's terminal step where the user answers it (the command
	// line's and the terminal UI's relay, the app's terminal window); nil: this front end
	// has none, and a hand-off to a cloud that needs one is blocked with the reason.
	Steps move.StepRunner
	// RunHere runs a driver's command in this front end's terminal and waits for it (the
	// teleport that brings a hop's session here); nil: the front end opens it itself, and the
	// hop waits for ContinueHop.
	RunHere RunHere
	// Terminals are the terminal apps launches open in (empty: this system's), and Procs
	// what finding a session's tab reads of the process table (nil: this machine's).
	Terminals termapp.Set
	Procs     termapp.Procs
	// tests keeps the clouds' recent read-only probes, so replanning a hand-off does not ask
	// the vendor again each time (shared by copies of the App; nil: never kept).
	tests *cloudTests
}

// New returns an App for the modules and configuration.
func New(cfg config.Config, reg *registry.Registry, stateDir string, log *audit.Log) *App {
	return &App{activity: &runtimeActivity{}, Catalog: catalog.New(stateDir), movementReads: &movementReadCache{entries: map[string]movementRead{}}, Cfg: cfg, Reg: reg, StateDir: stateDir, Audit: log, Log: slog.Default(), tests: &cloudTests{m: map[string]cloudTest{}}}
}

// Modules returns the enabled modules.
func (a *App) Modules() []agent.Module {
	var out []agent.Module
	for _, m := range a.Reg.All() {
		if a.Cfg.AgentEnabled(string(m.Spec().ID)) {
			out = append(out, m)
		}
	}
	return out
}

// Specs returns the enabled modules' Specs.
func (a *App) Specs() []agent.Spec {
	var out []agent.Spec
	for _, m := range a.Modules() {
		out = append(out, m.Spec())
	}
	return out
}

// Module returns an enabled module by id.
func (a *App) Module(id agent.ID) (agent.Module, bool) {
	for _, m := range a.Modules() {
		if m.Spec().ID == id {
			return m, true
		}
	}
	return nil, false
}

// LocalName is this machine's name in marks and lineage: $HOPSESH_MACHINE, else its short
// host name.
func LocalName() string {
	if n := strings.TrimSpace(os.Getenv("HOPSESH_MACHINE")); n != "" {
		return n
	}
	h, _ := os.Hostname()
	return strings.TrimSuffix(strings.Split(h, ".")[0], ".local")
}
