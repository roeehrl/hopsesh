// Command fakeagent is the stand-in agent as a program: built as codex (or codex.exe) it
// answers like Codex, built as claude like Claude Code, and built as fakecloud it plays the
// stand-in clouds' agent (fakecloud work <id>). The Windows screenshots in CI put it on PATH
// so the window shows Codex as installed without the real one.
package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/roeehrl/hopsesh/internal/testkit/fakeagent"
)

func main() {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	if strings.HasPrefix(name, "claude") {
		os.Exit(fakeagent.Claude())
	}
	if strings.HasPrefix(name, "fakecloud") {
		os.Exit(fakeagent.Cloud())
	}
	os.Exit(fakeagent.Codex())
}
