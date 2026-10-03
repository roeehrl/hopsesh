// Command driftmanifest prints, as JSON, what hopsesh's agent modules rely on in each
// agent: its spec (versions tested, programs, data folders, secrets, instruction files),
// its capabilities, its test fixtures and its source files. The weekly upstream-drift
// review reads it next to the agents' changelogs and docs. It is not shipped.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

type module struct {
	ID                 agent.ID            `json:"id"`
	Name               string              `json:"name"`
	Vendor             string              `json:"vendor"`
	Stability          agent.Stability     `json:"stability"`
	Tested             []string            `json:"testedVersions"`
	Binaries           []agent.Binary      `json:"binaries"`
	Roots              []agent.Root        `json:"dataFolders"`
	LoginEnv           []string            `json:"loginEnv"`
	Secrets            []string            `json:"secretsNeverOpened"`
	Worktrees          []string            `json:"worktreeFolders"`
	Instructions       []string            `json:"instructionFiles"`
	GlobalInstructions []string            `json:"globalInstructionFiles"`
	DesktopApps        map[string][]string `json:"desktopApps"`
	Capabilities       []agent.Capability  `json:"capabilities"`
	Fixtures           []string            `json:"fixtureVersions"`
	Sources            []string            `json:"sourceFiles"`
}

func main() {
	var out []module
	for _, m := range all.Registry().All() {
		s := m.Spec()
		dir := filepath.Join("agents", string(s.ID))
		d := module{ID: s.ID, Name: s.Name, Vendor: s.Vendor, Stability: s.Stability, Tested: s.Tested, Binaries: s.Binaries,
			Roots: s.Roots, LoginEnv: s.LoginEnv, Secrets: s.Secrets, Worktrees: s.Worktrees, Instructions: s.Instructions,
			GlobalInstructions: s.GlobalInstructions, DesktopApps: s.Icon.Apps, Capabilities: agent.Capabilities(m)}
		if es, err := os.ReadDir(filepath.Join(dir, "testdata")); err == nil {
			for _, e := range es {
				if e.IsDir() {
					d.Fixtures = append(d.Fixtures, e.Name())
				}
			}
		}
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if !strings.HasSuffix(f, "_test.go") {
				d.Sources = append(d.Sources, filepath.ToSlash(f))
			}
		}
		sort.Strings(d.Sources)
		out = append(out, d)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"modules": out}); err != nil {
		os.Exit(1)
	}
}
