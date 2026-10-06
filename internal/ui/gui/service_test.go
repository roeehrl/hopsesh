package gui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

const sid = "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a01"

// home is a machine with Claude Code (one session, from the fixtures) and Codex (no
// sessions yet), in temporary folders.
func home(t *testing.T) (repo string) {
	t.Helper()
	h := t.TempDir()
	h, _ = filepath.EvalSymlinks(h)
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h) // the home folder on Windows
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(h, "config"))
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(h, "state"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("PATH", testPath()) // git, and no real agent binaries
	repo = filepath.Join(h, "git", "demo")
	os.MkdirAll(repo, 0o700)
	os.MkdirAll(filepath.Join(h, ".codex", "sessions"), 0o700)
	fix := "../../../agents/claude/testdata/2.1.284"
	err := filepath.Walk(fix, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(fix, p)
		// The fixture process ID can belong to an unrelated real process on a
		// long-running developer machine. Live-state tests create their own registry.
		if strings.HasPrefix(filepath.ToSlash(rel), "sessions/") {
			return nil
		}
		b, _ := os.ReadFile(p)
		esc, _ := json.Marshal(repo)
		b = []byte(strings.ReplaceAll(string(b), "/home/u/git/demo", string(esc[1:len(esc)-1])))
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
	if m := a.Machines(); m.Machines == nil || m.Found == nil || m.Here.Name == "" {
		t.Fatalf("empty lists for the window, not null: %+v", m)
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

	if err := a.Undo(d.Journal, false); err != nil {
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
	if p.Continue.Relation != "new" || p.Continue.AppendTo != "" {
		t.Fatalf("back: %+v", p.Continue)
	}
	back, err := a.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(back.Command, sid) {
		t.Fatalf("done: %+v", back)
	}
	scan, _ = a.Scan()
	original, readErr := os.ReadFile(e.Path)
	if readErr != nil || strings.Contains(string(original), "Now in uppercase") {
		t.Fatal("portable return modified protected original", readErr)
	}
	var cl EntryDTO
	for _, group := range scan.Groups {
		for _, entry := range group.Entries {
			if entry.Agent == "claude" && entry.Session != sid && entry.LastPrompt == "Now in uppercase" {
				cl = entry
			}
		}
	}
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
	if len(cl.History) < 2 || !strings.HasPrefix(cl.History[1].What, "Continued in Codex") {
		t.Fatalf("history: %+v", cl.History)
	}

	// Activity: the way back can be undone; the first continuation was used since.
	act, err := a.Activity()
	if err != nil || len(act.Items) != 2 {
		t.Fatalf("activity: %+v %v", act, err)
	}
	newest, first := act.Items[0], act.Items[1]
	if newest.Kind != "continue" || !newest.CanUndo || first.CanUndo || first.Why == "" {
		t.Fatalf("activity items: %+v", act.Items)
	}
	if title, err := a.UndoLast(); err != nil || title != newest.Title {
		t.Fatalf("undo last: %q %v", title, err)
	}
	if err := a.Undo(first.ID, false); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("undoing work used since needs force: %v", err)
	}
	if err := a.Undo(first.ID, true); err != nil {
		t.Fatal(err)
	}
	act, _ = a.Activity()
	for _, x := range act.Items {
		if !x.Undone || x.CanUndo {
			t.Fatalf("after undo: %+v", act.Items)
		}
	}
	if _, err := a.UndoLast(); err == nil {
		t.Fatal("nothing left to undo")
	}
}

// The Machines screen: this machine receives only when asked, and machines are added and
// removed without connecting to them.
func TestWindowMachines(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	if m := a.Machines(); m.Here.Receive || a.Info().Receive || len(m.Machines) != 0 {
		t.Fatalf("fresh: %+v", m)
	}
	if err := a.SetReceive(true); err != nil {
		t.Fatal(err)
	}
	if !a.Machines().Here.Receive || !a.Info().Receive {
		t.Fatal("receive is on")
	}
	if err := a.AddHost("box", "me@box.invalid", false, false); err != nil {
		t.Fatal(err)
	}
	m := a.Machines()
	if len(m.Machines) != 1 || m.Machines[0].Name != "box" || m.Machines[0].Scanned || m.Machines[0].Auth != "key" {
		t.Fatalf("added: %+v", m.Machines)
	}
	if err := a.RemoveHost("box"); err != nil {
		t.Fatal(err)
	}
	if m := a.Machines(); len(m.Machines) != 0 || a.Info().HasHosts {
		t.Fatalf("removed: %+v", m.Machines)
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
	old, err := a.StartFresh(false)
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

// A configuration a newer hopsesh wrote is not set aside as if it were old: the window
// offers to update first, and StartFresh sets it aside only when asked to for a newer file.
func TestWindowKeepsNewerConfig(t *testing.T) {
	home(t)
	os.MkdirAll(config.Dir(), 0o700)
	newer := fmt.Sprintf("schema = %d\nrepos_dir = \"/x\"\n", config.Schema+1)
	os.WriteFile(config.Path(), []byte(newer), 0o600)
	a := NewApp(all.Registry())
	info := a.Info()
	if !info.ConfigNewer || !strings.Contains(info.ConfigError, "written by a newer hopsesh") || strings.Contains(info.ConfigError, "older hopsesh") {
		t.Fatalf("the newer file must be reported as newer: %+v", info.ConfigError)
	}
	if _, err := a.StartFresh(false); err == nil || !strings.Contains(err.Error(), "update hopsesh") {
		t.Fatalf("a newer file is not set aside as if it were old: %v", err)
	}
	if b, _ := os.ReadFile(config.Path()); string(b) != newer {
		t.Fatal("the newer file stays where it is")
	}
	if err := a.SetUpdateCheck(true); err == nil {
		t.Fatal("settings must not overwrite the newer file")
	}
	old, err := a.StartFresh(true)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(old); string(b) != newer {
		t.Fatal("set aside on request, the newer file is kept as it was")
	}
	if info := a.Info(); info.ConfigError != "" || info.ConfigNewer {
		t.Fatalf("after starting fresh: %+v", info)
	}
}

func findEntry(t *testing.T, s *ScanDTO, key string) EntryDTO {
	t.Helper()
	for _, g := range s.Groups {
		for _, e := range g.Entries {
			if e.Key == key || e.Profile != nil && e.Profile.Default && string(e.Agent)+"/"+e.Session == key {
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
	bin := filepath.Join(t.TempDir(), "hopsesh"+exeSuffix())
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
	boxEnv := []string{"HOME=" + box, "USERPROFILE=" + box, "PATH=" + testPath(), "HOPSESH_MACHINE=box", "HOPSESH_CONFIG_DIR=" + filepath.Join(box, "config"),
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
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	key := findEntry(t, scan, "claude/"+sid).Key
	if _, err := a.PushPlan(key, "box", "", OptsDTO{Mark: true, TargetDir: boxRepo}); err != nil {
		t.Fatal(err)
	}
	a.ClosePlan() // the window closed the plan: its connection ends
	if _, err := a.PushApply(); err == nil {
		t.Fatal("a closed plan cannot be applied")
	}
	p, err := a.PushPlan(key, "box", "", OptsDTO{Mark: true, TargetDir: boxRepo})
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
	files, _ := filepath.Glob(filepath.Join(box, ".claude", "projects", claude.Slug(boxRepo), "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("portable copies: %v", files)
	}
	moved := files[0]
	if _, err := os.Stat(moved); err != nil || d.Machine != "box" || d.Journal == "" || !strings.Contains(d.Command, "claude") {
		t.Fatalf("done: %+v (%v)", d, err)
	}
	scan, _ = a.Scan()
	if e := findEntry(t, scan, "claude/"+sid); !strings.Contains(e.Status, "box") {
		t.Fatalf("the copy here is marked: %+v", e.Status)
	}
	if err := a.Undo(d.Journal, false); err != nil {
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

// testPath keeps git but no agent binaries: the system folders only (on Windows, as is).
func testPath() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("PATH")
	}
	return "/usr/bin:/bin"
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// Agents are pictured by their installed desktop app's icon (on by default), else the
// module's own mark.
func TestWindowAgentIcons(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	icons := func() map[string]string {
		out := map[string]string{}
		for _, ag := range a.Info().Agents {
			out[string(ag.ID)] = ag.Icon
		}
		return out
	}
	installed := func(id string) bool { // a real app outside the test home (this machine's /Applications)
		m, _ := all.Registry().Get(agent.ID(id))
		for _, p := range m.Spec().Icon.Apps[runtime.GOOS] {
			if _, err := os.Stat(p); err == nil && !strings.HasPrefix(p, "~") {
				return true
			}
		}
		return false
	}
	for id, icon := range icons() {
		if !installed(id) && !strings.HasPrefix(icon, "data:image/svg+xml;base64,") {
			t.Fatalf("%s without its app is pictured by its mark: %.40q", id, icon)
		}
	}
	if runtime.GOOS != "darwin" {
		return
	}
	// A Claude desktop app in ~/Applications: its icon comes first.
	app := filepath.Join(os.Getenv("HOME"), "Applications", "Claude.app", "Contents")
	os.MkdirAll(filepath.Join(app, "Resources"), 0o700)
	os.WriteFile(filepath.Join(app, "Info.plist"), []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIconFile</key><string>app.icns</string></dict></plist>`), 0o600)
	png := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR"), 0, 0, 0, 128, 0, 0, 0, 128)
	entry := append(append([]byte("ic07"), 0, 0, 0, byte(8+len(png))), png...)
	os.WriteFile(filepath.Join(app, "Resources", "app.icns"), append(append([]byte("icns"), 0, 0, 0, byte(8+len(entry))), entry...), 0o600)
	a = NewApp(all.Registry())
	if got := icons(); !strings.HasPrefix(got["claude"], "data:image/png;base64,") || !installed("codex") && !strings.HasPrefix(got["codex"], "data:image/svg+xml") {
		t.Fatalf("the installed app's icon first: %.40q / %.40q", got["claude"], got["codex"])
	}
	s := a.Settings()
	if err := a.SaveSettings(SettingsInput{Layout: s.Layout, MarkMoved: s.MarkMoved, SyncCode: s.SyncCode, UpdateChk: "off", AppIcons: false}); err != nil {
		t.Fatal(err)
	}
	if got := icons(); !strings.HasPrefix(got["claude"], "data:image/svg+xml") {
		t.Fatal("with app icons off, the module's mark")
	}
}
