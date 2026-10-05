// Command driftmanifest prints, as JSON, what hopsesh's agent modules rely on in each
// agent: its spec (versions tested, programs, data folders, secrets, instruction files),
// its capabilities, its test fixtures and its source files. It also prints the targets
// the weekly upstream-drift check watches (targets.go): the agents, the vendor clouds the
// modules declare (Spec.Clouds) and the ones no module reaches yet, each with its docs,
// feeds, help commands, issues and code canaries. The probe reads the
// targets; the review reads all of it next to the vendors' changelogs and docs.
//
// `driftmanifest feed FILE SINCE` prints the items of an RSS or Atom file published
// after SINCE (RFC 3339) as Markdown, for the probe. `driftmanifest check SCHEMA FILE`
// validates a manifest that another repository wrote for the reusable workflow
// (ci/drift/manifest.schema.json, and the rules a schema cannot say). It is not shipped.
package main

import (
	"encoding/json"
	"fmt"
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

	clouds []agent.Cloud // become targets (targets.go)
}

// cloudOnly reports whether the module has no agent on machines (no data folders), only
// clouds.
func (m module) cloudOnly() bool { return len(m.Roots) == 0 && len(m.clouds) > 0 }

func main() {
	if len(os.Args) == 4 && os.Args[1] == "feed" {
		if err := printFeed(os.Stdout, os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, "driftmanifest feed:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "check" {
		if errs := checkFile(os.Args[2], os.Args[3], true); len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, "driftmanifest check:", e)
			}
			os.Exit(1)
		}
		return
	}
	out := modules()
	targets, err := buildTargets(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "driftmanifest:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"project": hopsesh(out), "modules": out, "targets": targets}); err != nil {
		os.Exit(1)
	}
}

// modules describes the compiled-in modules. Run from the repository root.
func modules() []module {
	var out []module
	for _, m := range all.Registry().All() {
		s := m.Spec()
		dir := filepath.Join("agents", string(s.ID))
		d := module{ID: s.ID, Name: s.Name, Vendor: s.Vendor, Stability: s.Stability, Tested: s.Tested, Binaries: s.Binaries,
			Roots: s.Roots, LoginEnv: s.LoginEnv, Secrets: s.Secrets, Worktrees: s.Worktrees, Instructions: s.Instructions,
			GlobalInstructions: s.GlobalInstructions, DesktopApps: s.Icon.Apps, Capabilities: agent.Capabilities(m),
			clouds: s.Clouds}
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
	return out
}
