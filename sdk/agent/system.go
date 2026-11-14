package agent

import (
	"os"
	"testing"
)

// A test binary marks the whole test as a test machine, so that every hopsesh and agent
// stand-in it starts inherits HOPSESH_APP_RUNTIMES=off too, unless the test runs against
// the installed agents (HOPSESH_REAL_AGENTS=1) or sets HOPSESH_APP_RUNTIMES itself.
func init() {
	if testing.Testing() && os.Getenv("HOPSESH_APP_RUNTIMES") == "" && os.Getenv("HOPSESH_REAL_AGENTS") != "1" {
		_ = os.Setenv("HOPSESH_APP_RUNTIMES", "off")
	}
}

// SystemPrograms reports whether hopsesh may look for agent programs outside PATH and the
// home folder: in the system's program folders (/opt/homebrew/bin, /usr/local/bin), inside
// desktop apps, and on the login shell's PATH. A test must never run this machine's real
// agents, which share the person's real services and data, so it is false on a test
// machine (HOPSESH_APP_RUNTIMES=off, which every test binary sets; see init) and in any
// test binary, unless the test sets HOPSESH_APP_RUNTIMES=on or runs against the installed
// agents (HOPSESH_REAL_AGENTS=1).
func SystemPrograms() bool {
	switch os.Getenv("HOPSESH_APP_RUNTIMES") {
	case "off":
		return false
	case "on":
		return true
	}
	return !testing.Testing() || os.Getenv("HOPSESH_REAL_AGENTS") == "1"
}
