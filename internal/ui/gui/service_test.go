package gui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
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
	if hosts := a.Discover(); hosts == nil {
		t.Fatal("no machines is an empty list for the window, not null")
	}
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

// Continue in Codex, work there, and continue back: the Claude Code original gets only the
// new work, keeps its title, and the Codex thread is marked in Codex's own list.
func TestWindowRoundTrip(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	scan, _ := a.Scan()
	e := findEntry(t, scan, "claude/"+sid)
	if _, err := a.Plan(e.Machine, e.Key, "codex", OptsDTO{Mark: true}); err != nil {
		t.Fatal(err)
	}
	there, err := a.Apply()
	if err != nil {
		t.Fatal(err)
	}
	rollouts, _ := filepath.Glob(filepath.Join(os.Getenv("HOME"), ".codex", "sessions", "*", "*", "*", "rollout-*.jsonl"))
	if len(rollouts) != 1 {
		t.Fatalf("rollouts: %v", rollouts)
	}
	f, _ := os.OpenFile(rollouts[0], os.O_APPEND|os.O_WRONLY, 0)
	ts := time.Now().UTC().Add(-time.Minute).Format("2006-01-02T15:04:05.000Z")
	for _, l := range []string{
		`{"timestamp":"2030-01-01T00:00:00.000Z","type":"event_msg","payload":{"type":"user_message","message":"Now in uppercase","images":[]}}`,
		`{"timestamp":"2030-01-01T00:00:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Now in uppercase"}]}}`,
		`{"timestamp":"2030-01-01T00:00:00.000Z","type":"event_msg","payload":{"type":"agent_message","message":"PELICAN"}}`,
		`{"timestamp":"2030-01-01T00:00:00.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"PELICAN"}]}}`,
	} {
		f.WriteString(strings.ReplaceAll(l, "2030-01-01T00:00:00.000Z", ts) + "\n")
	}
	f.Close()

	scan, _ = a.Scan()
	var cx EntryDTO
	for _, g := range scan.Groups {
		for _, x := range g.Entries {
			if x.Agent == "codex" {
				cx = x
			}
		}
	}
	if !strings.HasPrefix(cx.Title, there.Title) || cx.LastPrompt != "Now in uppercase" {
		t.Fatalf("the Codex thread has the session's title and its real last prompt: %+v", cx)
	}
	p, err := a.Plan(cx.Machine, cx.Key, "claude", OptsDTO{Mark: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Continue.Relation != "append" || p.Continue.AppendTo == "" {
		t.Fatalf("back: %+v", p.Continue)
	}
	back, err := a.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if back.Title != p.Continue.AppendTo || !strings.Contains(back.Command, sid) {
		t.Fatalf("done: %+v", back)
	}
	scan, _ = a.Scan()
	cl := findEntry(t, scan, "claude/"+sid)
	if cl.LastPrompt != "Now in uppercase" || !cl.HereNewest {
		t.Fatalf("the original is newest again, without hopsesh's briefing as its prompt: %+v", cl)
	}
	var marked bool
	for _, c := range cl.Copies {
		marked = marked || c.Agent == "codex" && c.Mark != nil && c.Mark.AgentName == "Claude Code"
	}
	if !marked {
		t.Fatalf("the Codex thread is marked as continued in Claude Code: %+v", cl.Copies)
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

// Sending a session from the window to another machine's hopsesh (a real `hopsesh peer
// --stdio` process with its own home), and undoing both sides from here.
func TestWindowSendsToAnotherMachine(t *testing.T) {
	if testing.Short() {
		t.Skip("builds hopsesh")
	}
	bin := filepath.Join(t.TempDir(), "hopsesh")
	build := exec.Command("go", "build", "-o", bin, "./cmd/hopsesh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	// The other machine: Claude Code has run there once, and it receives sessions.
	box := t.TempDir()
	box, _ = filepath.EvalSymlinks(box)
	boxRepo := filepath.Join(box, "git", "demo")
	os.MkdirAll(boxRepo, 0o700)
	os.MkdirAll(filepath.Join(box, ".claude", "projects"), 0o700)
	boxEnv := []string{"HOME=" + box, "PATH=/usr/bin:/bin", "HOPSESH_MACHINE=box", "HOPSESH_CONFIG_DIR=" + filepath.Join(box, "config"),
		"HOPSESH_STATE_DIR=" + filepath.Join(box, "state"), "CLAUDE_CONFIG_DIR=", "CODEX_HOME="}
	recv := exec.Command(bin, "receive", "on")
	recv.Env = boxEnv
	if out, err := recv.CombinedOutput(); err != nil {
		t.Fatalf("receive on: %v %s", err, out)
	}

	home(t)
	t.Setenv("HOPSESH_MACHINE", "here")
	a := NewApp(all.Registry())
	if err := a.AddHost("box", "box", false, false); err != nil {
		t.Fatal(err)
	}
	a.core.PeerDial = func(ctx context.Context, _ config.Host) (*app.PeerConn, error) {
		cmd := exec.Command(bin, "peer", "--stdio")
		cmd.Env = boxEnv
		in, _ := cmd.StdinPipe()
		out, _ := cmd.StdoutPipe()
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &app.PeerConn{Out: out, In: in, Close: func() { in.Close(); _ = cmd.Wait() }}, nil
	}
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	p, err := a.PushPlan("claude/"+sid, "box", "", OptsDTO{Mark: true, TargetDir: boxRepo})
	if err != nil {
		t.Fatal(err)
	}
	if p.Machine != "box" || p.TargetCWD != boxRepo || len(p.Blockers) > 0 {
		t.Fatalf("plan: %+v", p)
	}
	d, err := a.PushApply()
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(box, ".claude", "projects", claude.Slug(boxRepo), sid+".jsonl")
	if _, err := os.Stat(moved); err != nil || d.Machine != "box" || d.Journal == "" || !strings.Contains(d.Command, "claude") {
		t.Fatalf("done: %+v (%v)", d, err)
	}
	scan, _ := a.Scan()
	if e := findEntry(t, scan, "claude/"+sid); !strings.HasPrefix(e.Status, "moved to box") {
		t.Fatalf("the copy here is marked: %+v", e.Status)
	}
	if err := a.Undo(d.Journal); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(moved); !os.IsNotExist(err) {
		t.Fatal("undo removes the copy on box")
	}
	scan, _ = a.Scan()
	if e := findEntry(t, scan, "claude/"+sid); e.Status != "ended" {
		t.Fatalf("undo removes the mark here: %+v", e.Status)
	}
}
