package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/fsys"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/sessions"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeJSONL(t *testing.T, p string, recs ...map[string]any) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	var b strings.Builder
	for _, r := range recs {
		j, _ := json.Marshal(r)
		b.Write(j)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEndToEndCloneWorktreeRewriteUndo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixtures")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	root, _ := filepath.EvalSymlinks(t.TempDir())

	// Source machine: a repo with a remote, a Claude worktree on branch feat, and a session in it.
	origin := filepath.Join(root, "origin.git")
	srcHome := filepath.Join(root, "srchome")
	srcRepo := filepath.Join(srcHome, "code", "proj")
	gitRun(t, root, "init", "-q", "--bare", "-b", "main", origin)
	// A hosted-looking remote, redirected to the local bare repo for the test.
	const remoteURL = "https://example.test/owner/proj.git"
	gcfg := filepath.Join(root, "gitconfig")
	os.WriteFile(gcfg, []byte("[url \"file://"+origin+"\"]\n\tinsteadOf = "+remoteURL+"\n[protocol \"file\"]\n\tallow = always\n"), 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", gcfg)
	gitRun(t, root, "clone", "-q", remoteURL, srcRepo)
	os.WriteFile(filepath.Join(srcRepo, "a.go"), []byte("package a"), 0o644)
	gitRun(t, srcRepo, "add", ".")
	gitRun(t, srcRepo, "commit", "-qm", "1")
	gitRun(t, srcRepo, "push", "-q", "-u", "origin", "main")
	srcWT := filepath.Join(srcRepo, ".claude", "worktrees", "feat")
	gitRun(t, srcRepo, "worktree", "add", "-q", "-b", "feat", srcWT)
	gitRun(t, srcWT, "push", "-q", "-u", "origin", "feat")

	srcCfg := filepath.Join(srcHome, ".claude")
	id := "11111111-2222-3333-4444-555555555555"
	srcProj := filepath.Join(srcCfg, "projects", sessions.Slug(srcWT))
	transcript := filepath.Join(srcProj, id+".jsonl")
	writeJSONL(t, transcript,
		map[string]any{"type": "user", "uuid": "u1", "parentUuid": nil, "sessionId": id, "cwd": srcWT, "version": "2.1.200",
			"timestamp": "2026-10-01T10:00:00Z", "gitBranch": "feat", "message": map[string]any{"role": "user", "content": "edit " + srcWT + "/a.go"}},
		map[string]any{"type": "assistant", "uuid": "a1", "parentUuid": "u1", "sessionId": id, "cwd": srcWT, "timestamp": "2026-10-01T10:00:01Z",
			"message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "thinking", "thinking": "the file is " + srcWT + "/a.go", "signature": "SIG"},
				map[string]any{"type": "tool_use", "id": "t1", "name": "Read", "input": map[string]any{"file_path": srcWT + "/a.go"}}}}},
		map[string]any{"type": "user", "uuid": "u2", "parentUuid": "a1", "sessionId": id, "cwd": srcWT, "timestamp": "2026-10-01T10:00:02Z",
			"toolUseResult": map[string]any{"persistedOutputPath": srcProj + "/" + id + "/tool-results/x.txt"},
			"message":       map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "ok"}}}},
		map[string]any{"type": "bridge-session", "bridgeSessionId": "b"},
		map[string]any{"type": "custom-title", "customTitle": "Feature work"},
	)
	os.MkdirAll(filepath.Join(srcProj, id, "tool-results"), 0o755)
	os.WriteFile(filepath.Join(srcProj, id, "tool-results", "x.txt"), []byte("listing of "+srcWT), 0o600)
	writeJSONL(t, filepath.Join(srcProj, id, "subagents", "agent-1.jsonl"),
		map[string]any{"type": "user", "uuid": "s1", "sessionId": id, "cwd": srcWT, "isSidechain": true, "message": map[string]any{"role": "user", "content": "sub"}})
	os.MkdirAll(filepath.Join(srcCfg, "file-history", id), 0o755)
	os.WriteFile(filepath.Join(srcCfg, "file-history", id, "abc@v1"), []byte("old content mentioning "+srcWT), 0o600)

	states, err := repos.ProbeLocal(ctx, []string{srcWT})
	if err != nil {
		t.Fatal(err)
	}
	sum, err := sessions.Summarize(fsys.Local{}, transcript)
	if err != nil {
		t.Fatal(err)
	}

	// Target machine: different home and config dir, repo not cloned yet.
	tgtHome := filepath.Join(root, "tgthome")
	tgtCfg := filepath.Join(tgtHome, ".claude")
	reposDir := filepath.Join(tgtHome, "git")
	src := Source{Host: "studio", OS: "darwin", FS: fsys.Local{}, ConfigDir: srcCfg, Home: srcHome}
	tgt := Target{Host: "laptop.local", OS: "darwin", ConfigDir: tgtCfg, Home: tgtHome, ClaudeVersion: "2.1.284"}
	in := Input{Summary: sum, Git: &states[0]}

	p, err := BuildPlan(ctx, src, tgt, in, Options{ReposDir: reposDir})
	if err != nil {
		t.Fatal(err)
	}
	if p.Repo.Action != "needs-clone" || len(p.Blockers) == 0 {
		t.Fatalf("expected a clone blocker: %+v", p.Repo)
	}
	if _, err := Apply(ctx, p, src, Env{StateDir: filepath.Join(root, "state")}); err == nil {
		t.Fatal("Apply must refuse a blocked plan")
	}

	p, err = BuildPlan(ctx, src, tgt, in, Options{ReposDir: reposDir, Clone: true, Worktree: WorktreeAuto, NotifyOld: true, RemoteCtl: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Repo.Identity != "example.test/owner/proj" || p.Repo.LocalPath != filepath.Join(reposDir, "proj") {
		t.Errorf("repo plan: %+v", p.Repo)
	}
	if !p.Repo.InWorktree || !p.Repo.ClaudeWT || p.Repo.SourceBranch != "feat" || p.Repo.Worktree == "" {
		t.Fatalf("worktree plan: %+v", p.Repo)
	}
	if !strings.HasSuffix(p.TargetCWD, filepath.Join(".claude", "worktrees", "feat")) {
		t.Errorf("target cwd %q", p.TargetCWD)
	}
	if p.TargetDir != filepath.Join(tgtCfg, "projects", sessions.Slug(resolveIntended(p.TargetCWD))) {
		t.Errorf("target dir %q", p.TargetDir)
	}
	if !strings.Contains(p.Resume.StartPrompt, "worktree") || !strings.Contains(p.Resume.StartPrompt, "studio") {
		t.Errorf("start prompt lacks move facts:\n%s", p.Resume.StartPrompt)
	}
	if p.NewName != "feature-work@laptop" {
		t.Errorf("new name %q", p.NewName)
	}
	kinds := map[string]int{}
	for _, f := range p.Files {
		kinds[f.Kind]++
	}
	if kinds["transcript"] != 1 || kinds["subagent"] != 1 || kinds["tool-result"] != 1 || kinds["file-history"] != 1 {
		t.Errorf("files: %v", kinds)
	}

	var steps []string
	res, err := Apply(ctx, p, src, Env{StateDir: filepath.Join(root, "state"), Progress: func(s string) { steps = append(steps, s) }})
	if err != nil {
		t.Fatalf("apply: %v (steps %v)", err, steps)
	}
	if !res.Cloned || res.Worktree == "" || res.Copied != 4 {
		t.Errorf("result: %+v", res)
	}
	if b := repos.CurrentBranch(ctx, p.Repo.Worktree); b != "feat" {
		t.Errorf("worktree branch %q", b)
	}
	data, err := os.ReadFile(p.TargetFile)
	if err != nil {
		t.Fatal(err)
	}
	txt := string(data)
	if strings.Contains(txt, `"bridge-session"`) {
		t.Error("bridge-session not stripped")
	}
	if !strings.Contains(txt, `"thinking":"the file is `+srcWT+`/a.go"`) {
		t.Error("thinking block must stay byte-identical")
	}
	if strings.Count(txt, srcWT) != 1 { // only inside the thinking block
		t.Errorf("source worktree path left outside thinking: %d", strings.Count(txt, srcWT))
	}
	if !strings.Contains(txt, `"file_path":"`+p.TargetCWD+`/a.go"`) {
		t.Error("tool input not rewritten")
	}
	if !strings.Contains(txt, tgtCfg+"/projects/") {
		t.Error("config dir path not rewritten")
	}
	sc := bufio.NewScanner(strings.NewReader(txt))
	var last string
	for sc.Scan() {
		last = sc.Text()
	}
	if !strings.Contains(last, `"type":"relocated"`) || !strings.Contains(last, p.TargetCWD) {
		t.Errorf("relocated record: %s", last)
	}
	tr, _ := os.ReadFile(filepath.Join(p.TargetDir, id, "tool-results", "x.txt"))
	if string(tr) != "listing of "+p.TargetCWD {
		t.Errorf("tool result text: %q", tr)
	}
	fh, _ := os.ReadFile(filepath.Join(tgtCfg, "file-history", id, "abc@v1"))
	if string(fh) != "old content mentioning "+srcWT {
		t.Errorf("file-history blobs must be copied unchanged: %q", fh)
	}
	got, err := sessions.Summarize(fsys.Local{}, p.TargetFile)
	if err != nil || got.CWD != p.TargetCWD || got.Title != "Feature work" {
		t.Errorf("summary after transport: %+v %v", got, err)
	}

	// Undo removes the installed copy.
	u, err := Undo(filepath.Join(root, "state"), id[:8], nil)
	if err != nil || u == nil {
		t.Fatalf("undo: %v", err)
	}
	if _, err := os.Stat(p.TargetFile); !os.IsNotExist(err) {
		t.Error("undo left the transcript in place")
	}
	if _, err := Undo(filepath.Join(root, "state"), id[:8], nil); err == nil {
		t.Error("second undo must find nothing")
	}
}

func TestImportSameMachineRelocatesAndSetsAsideOriginal(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(root, "home")
	cfg := filepath.Join(home, ".claude")
	oldDir := filepath.Join(home, "old")
	newDir := filepath.Join(home, "new")
	os.MkdirAll(oldDir, 0o755)
	os.MkdirAll(newDir, 0o755)
	id := "aaaaaaaa-0000-0000-0000-000000000000"
	orig := filepath.Join(cfg, "projects", sessions.Slug(oldDir), id+".jsonl")
	writeJSONL(t, orig, map[string]any{"type": "user", "uuid": "u", "sessionId": id, "cwd": oldDir, "timestamp": "2026-10-01T10:00:00Z",
		"message": map[string]any{"role": "user", "content": "hello " + oldDir}})
	sum, _ := sessions.Summarize(fsys.Local{}, orig)
	src := Source{Host: "here", OS: runtime.GOOS, FS: fsys.Local{}, ConfigDir: cfg, Home: home}
	tgt := Target{Host: "here", OS: runtime.GOOS, ConfigDir: cfg, Home: home, ClaudeVersion: "2.1.284"}
	p, err := BuildPlan(context.Background(), src, tgt, Input{Summary: sum}, Options{TargetDir: newDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Duplicates) != 1 || p.Duplicates[0] != orig {
		t.Fatalf("original must be listed as duplicate: %v", p.Duplicates)
	}
	res, err := Apply(context.Background(), p, src, Env{StateDir: filepath.Join(root, "state")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orig); !os.IsNotExist(err) {
		t.Error("original copy must be set aside so only one copy exists")
	}
	loc := sessions.Locator{FS: fsys.Local{}, ConfigDir: cfg}
	if found, _ := loc.Find(id); len(found) != 1 || found[0] != p.TargetFile {
		t.Errorf("exactly one copy expected: %v", found)
	}
	if len(res.SetAside) == 0 {
		t.Error("set-aside not reported")
	}
	if _, err := Undo(filepath.Join(root, "state"), id, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orig); err != nil {
		t.Error("undo must restore the original")
	}
}

func TestSameMachineSameFolderIsBlocked(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(root, "home")
	cfg := filepath.Join(home, ".claude")
	dir := filepath.Join(home, "proj")
	os.MkdirAll(dir, 0o755)
	id := "bbbbbbbb-0000-0000-0000-000000000000"
	f := filepath.Join(cfg, "projects", sessions.Slug(dir), id+".jsonl")
	writeJSONL(t, f, map[string]any{"type": "user", "uuid": "u", "sessionId": id, "cwd": dir, "message": map[string]any{"role": "user", "content": "x"}})
	sum, _ := sessions.Summarize(fsys.Local{}, f)
	src := Source{Host: "here", FS: fsys.Local{}, ConfigDir: cfg, Home: home}
	tgt := Target{Host: "here", ConfigDir: cfg, Home: home, ClaudeVersion: "2.1.284"}
	p, err := BuildPlan(context.Background(), src, tgt, Input{Summary: sum}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) == 0 || !strings.Contains(p.Blockers[0], "already here") {
		t.Fatalf("expected already-here blocker: %v", p.Blockers)
	}
	// A running session on this machine must never be set aside.
	os.MkdirAll(filepath.Join(cfg, "sessions"), 0o755)
	os.WriteFile(filepath.Join(cfg, "sessions", "1.json"), []byte(`{"pid":`+itoa(os.Getpid())+`,"sessionId":"`+id+`","status":"busy"}`), 0o600)
	p, _ = BuildPlan(context.Background(), src, tgt, Input{Summary: sum}, Options{TargetDir: filepath.Join(home, "elsewhere")})
	found := false
	for _, b := range p.Blockers {
		if strings.Contains(b, "running on this machine") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected live blocker: %v", p.Blockers)
	}
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func TestCrossOSMappingsConvertSeparators(t *testing.T) {
	p := &Plan{SourceCWD: `C:\Users\alice\proj`, TargetCWD: "/home/bob/proj", Repo: RepoPlan{Action: "none"}}
	ms := buildMappings(p, Source{OS: "windows", Home: `C:\Users\alice`}, Target{OS: "linux", Home: "/home/bob"})
	if len(ms) != 2 {
		t.Fatalf("mappings: %+v", ms)
	}
	for _, m := range ms {
		if m.ToSep != "/" {
			t.Errorf("Windows → Linux mapping %q should convert separators to /", m.From)
		}
	}
	ms = buildMappings(p, Source{OS: "darwin", Home: "/Users/a"}, Target{OS: "linux", Home: "/home/b"})
	for _, m := range ms {
		if m.ToSep != "" {
			t.Errorf("Unix → Unix must not convert separators: %+v", m)
		}
	}
}
