package integrate

import (
	"os"
	"path/filepath"
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
