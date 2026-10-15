// Package loginsvc registers hopsesh serve to start in the user's login session at each
// login: a LaunchAgent on macOS, a systemd user service on Linux, a task at sign-in on
// Windows. Nothing here needs administrator rights, and removing it leaves nothing behind.
package loginsvc

import (
	"context"
	"errors"
)

// Label names the service everywhere.
const Label = "io.github.roeehrl.hopsesh.serve"

// Status is what is registered and running.
type Status struct {
	Registered bool   `json:"registered"` // starts at each login
	Running    bool   `json:"running"`    // runs now
	Program    string `json:"program,omitempty"`
	// NoSession: nobody is logged in to the desktop, so it starts at the next login
	// (a Mac's login session; Windows' interactive one).
	NoSession bool   `json:"noSession,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// ErrUnsupported means this system has no login service hopsesh knows.
var ErrUnsupported = errors.New("hopsesh cannot start itself at login on this system")

// Service registers one program.
type Service struct {
	Home    string   // the user's home folder
	Program string   // absolute path of hopsesh
	Args    []string // ["serve"]
	Log     string   // where its errors go
	// Run runs a system command (launchctl, systemctl, schtasks) and returns its output.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Install registers the service and starts it when it can.
func (s Service) Install(ctx context.Context) (Status, error) { return s.install(ctx) }

// Uninstall stops and removes it.
func (s Service) Uninstall(ctx context.Context) error { return s.uninstall(ctx) }

// Check reports its status.
func (s Service) Check(ctx context.Context) Status { return s.check(ctx) }
