package move

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// InstructionSource is a declared source file, never a destination file to overwrite.
type InstructionSource struct {
	Path      string `json:"path"`
	Scope     string `json:"scope"`
	Text      string `json:"text"`
	Bytes     int    `json:"bytes"`
	Selected  bool   `json:"selected"`
	Shortened bool   `json:"shortened"`
	Error     string `json:"error,omitempty"`
}

// InstructionSources enumerates the supported files for preview and peer packaging.
// It deliberately does not follow imports, search unrelated directories, or include
// automatically generated memories, credentials, skills or settings.
func InstructionSources(spec agent.Spec, h agent.Host, in agent.Install, cwd string) []InstructionSource {
	var out []InstructionSource
	seen := map[string]bool{}
	add := func(path, scope string) {
		path = h.Path().Clean(path)
		if path != "" && !seen[path] {
			seen[path] = true
			out = append(out, InstructionSource{Path: path, Scope: scope})
		}
	}
	for _, g := range spec.GlobalInstructions {
		add(agent.Expand(g, h.Facts().Home, in.Roots, h.Path()), "Global")
	}
	if cwd != "" {
		for _, f := range spec.Instructions {
			add(h.Path().Join(cwd, f), "Project directory")
		}
	}
	return out
}

func instructionRules(p *Plan, h agent.Host, src Side, cwd string, opt Options) []convert.Rules {
	if p.Continue != nil {
		p.Continue.Instructions = nil
	}
	selected := map[string]bool{}
	for _, path := range opt.RuleFiles {
		selected[path] = true
	}
	known := map[string]bool{}
	var out []convert.Rules
	for _, file := range InstructionSources(src.Module.Spec(), h, src.Install, cwd) {
		known[file.Path] = true
		b, err := h.FS().ReadFile(file.Path, 1<<20)
		if errors.Is(err, fs.ErrNotExist) {
			if selected[file.Path] {
				p.Blockers = append(p.Blockers, "Selected instruction file no longer exists: "+file.Path)
			}
			continue
		}
		if err != nil {
			file.Error = "Cannot read this instruction file"
		} else {
			file.Bytes = len(b)
			file.Text = strings.TrimSpace(string(b))
			if file.Text == "" {
				continue
			}
			if len(file.Text) > maxRules {
				file.Text = strings.ToValidUTF8(file.Text[:maxRules], "") + "\n[shortened]"
				file.Shortened = true
			}
		}
		// The existing CLI flag keeps its global-only meaning. Explicit file selections
		// may additionally include project files; no client-provided arbitrary path is read.
		file.Selected = opt.CarryRules && (selected[file.Path] || len(opt.RuleFiles) == 0 && file.Scope == "Global")
		if !opt.CarryRules && file.Scope == "Global" && file.Error == "" {
			p.Warnings = append(p.Warnings, fmt.Sprintf("your instructions for every %s project (%s) do not carry over to %s; --carry-rules adds them to the briefing", src.Module.Spec().Name, file.Path, p.Agent))
		}
		if file.Selected {
			if file.Error != "" {
				p.Blockers = append(p.Blockers, fmt.Sprintf("Cannot carry instructions from %s: %s", file.Path, file.Error))
			} else {
				out = append(out, convert.Rules{File: file.Path, Text: file.Text})
			}
		}
		if p.Continue != nil {
			p.Continue.Instructions = append(p.Continue.Instructions, file)
		}
	}
	for path := range selected {
		if !known[path] {
			p.Blockers = append(p.Blockers, "Instruction selection is not a discovered source file: "+path)
		}
	}
	return out
}
