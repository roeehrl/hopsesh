package gui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
)

const sid = "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a01"

// home is a machine with Claude Code (one session, from the fixtures) and Codex (no
// sessions yet), in temporary folders.
func home(t *testing.T) (repo string) {
	t.Helper()
	h := t.TempDir()
	h, _ = filepath.EvalSymlinks(h)
	t.Setenv("HOME", h)
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(h, "config"))
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(h, "state"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("PATH", "/usr/bin:/bin") // git, and no real agent binaries
	repo = filepath.Join(h, "git", "demo")
	os.MkdirAll(repo, 0o700)
	os.MkdirAll(filepath.Join(h, ".codex", "sessions"), 0o700)
	fix := "../../../agents/claude/testdata/2.1.284"
	err := filepath.Walk(fix, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(fix, p)
		b, _ := os.ReadFile(p)
		b = []byte(strings.ReplaceAll(string(b), "/home/u/git/demo", repo))
		rel = strings.ReplaceAll(rel, "-home-u-git-demo", claude.Slug(repo))
		dst := filepath.Join(h, ".claude", rel)
		os.MkdirAll(filepath.Dir(dst), 0o700)
		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// The window's whole path on one machine: scan both agents, continue the Claude Code
// session in Codex, see the copies merged on the next scan, and undo.
func TestWindowContinuesInAnotherAgent(t *testing.T) {
	repo := home(t)
	a := NewApp(all.Registry())
	if info := a.Info(); info.ConfigError != "" || len(info.Agents) < 2 {
		t.Fatalf("info: %+v", info)
	}
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	e := findEntry(t, scan, "claude/"+sid)
	if !e.HereNewest || len(e.ContinueIn) != 1 || e.ContinueIn[0].ID != "codex" {
		t.Fatalf("entry: %+v", e)
	}
	opts := OptsDTO{Mark: true, Fidelity: "history"}
	p, err := a.Plan(e.Machine, e.Key, "codex", opts)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != "continue" || p.Continue == nil || p.Continue.Relation != "new" || len(p.Blockers) > 0 || p.TargetCWD != repo {
		t.Fatalf("plan: %+v %+v", p, p.Continue)
	}
	if !strings.Contains(p.Continue.Briefing, "Claude Code") || p.Continue.Report.Summary == "" {
		t.Fatalf("the plan must show the briefing and what is carried: %+v", p.Continue)
	}
	d, err := a.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != "continue" || d.Agent != "Codex" || !strings.Contains(d.Command, "codex") || d.Journal == "" {
		t.Fatalf("done: %+v", d)
	}

	scan, err = a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	var cx *EntryDTO
	for _, g := range scan.Groups {
		for i := range g.Entries {
			if g.Entries[i].Agent == "codex" {
				cx = &g.Entries[i]
			}
			if g.Entries[i].Key == "claude/"+sid {
				t.Fatal("the continued session and its source are one item, shown by its newest copy")
			}
		}
	}
	if cx == nil || len(cx.Copies) != 2 || !cx.HereNewest {
		t.Fatalf("codex entry: %+v", cx)
	}

	if err := a.Undo(d.Journal); err != nil {
		t.Fatal(err)
	}
	scan, _ = a.Scan()
	e = findEntry(t, scan, "claude/"+sid)
	if e.Status != "ended" {
		t.Fatalf("undo must remove the mark too: %+v", e)
	}
	for _, g := range scan.Groups {
		for _, x := range g.Entries {
			if x.Agent == "codex" {
				t.Fatal("undo must remove the Codex session")
			}
		}
	}
}

// A configuration an older hopsesh wrote is never overwritten; StartFresh sets it aside.
func TestWindowStartsFreshFromOldConfig(t *testing.T) {
	home(t)
	os.MkdirAll(config.Dir(), 0o700)
	os.WriteFile(config.Path(), []byte("repos_dir = \"/x\"\n"), 0o600)
	a := NewApp(all.Registry())
	if a.Info().ConfigError == "" {
		t.Fatal("the old file must be reported")
	}
	if _, err := a.Scan(); !errors.Is(err, config.ErrOldConfig) {
		t.Fatalf("scan: %v", err)
	}
	if err := a.SetUpdateCheck(true); err == nil {
		t.Fatal("settings must not overwrite the old file")
	}
	old, err := a.StartFresh()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(old); string(b) != "repos_dir = \"/x\"\n" {
		t.Fatal("the old file is kept as it was")
	}
	if err := a.SetUpdateCheck(true); err != nil || a.Info().ConfigError != "" {
		t.Fatalf("after starting fresh: %v", err)
	}
}

func findEntry(t *testing.T, s *ScanDTO, key string) EntryDTO {
	t.Helper()
	for _, g := range s.Groups {
		for _, e := range g.Entries {
			if e.Key == key {
				return e
			}
		}
	}
	t.Fatalf("no %s in %+v", key, s)
	return EntryDTO{}
}
