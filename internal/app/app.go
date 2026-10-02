// Package app is what hopsesh does, for every front end (command line, terminal UI,
// desktop app, the agents' skill): scan machines for sessions of every agent, plan and
// carry out moves and continuations, undo them, and keep the agents' integration current.
package app

import (
	"log/slog"
	"os"
	"strings"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// App holds what every use case needs.
type App struct {
	Cfg      config.Config
	Reg      *registry.Registry // every compiled-in module
	StateDir string
	Audit    *audit.Log
	Log      *slog.Logger
	// Passwords answers ssh password questions for machines that log in with one (nil:
	// such machines are reported, not scanned).
	Passwords func(h config.Host) transport.PasswordFunc
}

// New returns an App for the modules and configuration.
func New(cfg config.Config, reg *registry.Registry, stateDir string, log *audit.Log) *App {
	return &App{Cfg: cfg, Reg: reg, StateDir: stateDir, Audit: log, Log: slog.Default()}
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

// LocalName is this machine's short name.
func LocalName() string {
	h, _ := os.Hostname()
	return strings.TrimSuffix(strings.Split(h, ".")[0], ".local")
}
