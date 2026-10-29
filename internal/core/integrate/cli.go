package integrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// States of the hopsesh command: on macOS and Linux a link at ~/.local/bin/hopsesh; on
// Windows the app's folder (with hopsesh.exe beside the app) on the user's PATH.
const (
	CLIMissing    = "missing"    // nothing there
	CLIOurs       = "ours"       // a link to this app's tool
	CLIOtherApp   = "other-app"  // a link into another copy of hopsesh.app
	CLIDangling   = "dangling"   // a link whose target is gone (the app moved or was deleted)
	CLIStandalone = "standalone" // a regular file (install script, go install)
	CLIForeign    = "foreign"    // a link somewhere else (e.g. Homebrew)
)

// CLIStatus describes the command-line tool link.
type CLIStatus struct {
	State      string `json:"state"`
	Path       string `json:"path"`             // ~/.local/bin/hopsesh; Windows: the app's hopsesh.exe
	Target     string `json:"target,omitempty"` // where the link points
	AppCLI     string `json:"appCli,omitempty"` // the tool inside this app
	DirOnPath  bool   `json:"dirOnPath"`        // ~/.local/bin (Windows: the app's folder) is on the login PATH
	Resolves   string `json:"resolves,omitempty"`
	Profile    string `json:"profile,omitempty"`   // file "Add to PATH" would change
	PathLine   string `json:"pathLine"`            // the line to add to Profile; Windows: the folder
	PathAdded  bool   `json:"pathAdded,omitempty"` // hopsesh's block is in the profile
	CanInstall string `json:"cannotInstall,omitempty"`
	// Version is what a hopsesh that isn't this app's says it is (WithVersion fills it).
	Version string `json:"version,omitempty"`
}

// WithVersion adds the version of a separately installed hopsesh at the command's place,
// so the choice to replace it can say what it replaces.
func (st CLIStatus) WithVersion() CLIStatus {
	if st.State != CLIStandalone && st.State != CLIForeign {
		return st
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := proc.CommandContext(ctx, st.Path, "version").Output()
	if f := strings.Fields(string(out)); len(f) >= 2 && f[0] == "hopsesh" {
		st.Version = f[1]
	}
	return st
}

// SkillBin is how agents should run hopsesh: plain "hopsesh" when the user's login shell
// finds it, otherwise an absolute path (the linked tool, the app's tool, or this program).
func SkillBin() string {
	if LookLoginPath("hopsesh") != "" {
		return "hopsesh"
	}
	if st := CheckCLI(); st.State == CLIOurs || st.State == CLIOtherApp || st.State == CLIStandalone {
		return st.Path
	}
	if cli, err := AppCLI(); err == nil {
		return cli
	}
	exe, err := os.Executable()
	if err != nil || isAppExe(exe) {
		return "hopsesh" // never the window program
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe
}
