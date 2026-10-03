// Package archtest checks the layering: sdk (the contract) ← agents (modules) ←
// internal/core ← internal/app ← internal/ui, wired together only in internal/agents/all
// and cmd.
package archtest

import (
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
)

const mod = "github.com/roeehrl/hopsesh/"

// rule: packages under from may not import packages under any of deny (test files are
// exempt: tests may wire layers together).
type rule struct {
	from string
	deny []string
	why  string
}

var rules = []rule{
	{"sdk/", []string{"internal/", "agents/", "cmd/"}, "the SDK is the contract; it depends on nothing of hopsesh"},
	{"agents/", []string{"internal/", "cmd/"}, "modules see machines only through the SDK"},
	{"internal/core/", []string{"internal/app", "internal/ui/", "internal/agents/", "agents/", "cmd/"}, "the core is agent-agnostic and below the use cases"},
	{"internal/app", []string{"internal/ui/", "internal/agents/", "agents/", "cmd/"}, "use cases get modules from the registry, never by name"},
	{"internal/ui/", []string{"agents/", "internal/agents/", "cmd/"}, "front ends get the registry from the composition root"},
	{"internal/config", []string{"internal/app", "internal/ui/", "internal/core/", "agents/"}, "configuration is a leaf"},
}

type pkg struct {
	ImportPath string
	Imports    []string
}

func TestImportBoundaries(t *testing.T) {
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	n := 0
	for {
		var p pkg
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		n++
		rel := strings.TrimPrefix(p.ImportPath, mod)
		for _, r := range rules {
			if !strings.HasPrefix(rel, r.from) {
				continue
			}
			for _, imp := range p.Imports {
				irel, ours := strings.CutPrefix(imp, mod)
				if !ours {
					continue
				}
				for _, d := range r.deny {
					if strings.HasPrefix(irel, d) {
						t.Errorf("%s imports %s: %s", rel, irel, r.why)
					}
				}
			}
		}
	}
	if n < 20 {
		t.Fatalf("go list found only %d packages", n)
	}
}
