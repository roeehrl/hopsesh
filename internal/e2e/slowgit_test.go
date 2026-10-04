package e2e

import (
	"context"
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
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/repos"
)

// A session folder git does not answer for (its files offloaded to a cloud drive, say)
// no longer hangs the scan: the folder gets its own reason, which blocks moving that
// session, and the other sessions are read as usual. A command about one session does not
// ask git about the other folders at all.
func TestSlowGitFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a POSIX stand-in git; the repos package tests the PowerShell probe")
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	here := newMachineHome(t, root, "here", true)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "https://example.com/alice/demo.git"},
		{"-c", "user.name=Alice", "-c", "user.email=alice@example.com", "commit", "-q", "--allow-empty", "-m", "one"}} {
		if out, err := exec.Command("git", append([]string{"-C", here.repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	// The fixture's second session moves to a folder git never answers for.
	const slowID = "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a02"
	slow := filepath.Join(here.home, "Cloud Drive", "offloaded")
	projects := filepath.Join(here.home, ".claude", "projects")
	b, err := os.ReadFile(filepath.Join(projects, claude.Slug(here.repo), slowID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projects, claude.Slug(slow)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(slow, ".claude", "worktrees", "feat"), 0o700); err != nil { // the session works in a worktree
		t.Fatal(err)
	}
	b = []byte(strings.ReplaceAll(string(b), jsonText(here.repo), jsonText(slow)))
	if err := os.WriteFile(filepath.Join(projects, claude.Slug(slow), slowID+".jsonl"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(projects, claude.Slug(here.repo), slowID+".jsonl")); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(root, "bin")
	asked := filepath.Join(root, "asked")
	stand := "#!/bin/sh\ncase \"$*\" in *offloaded*) echo \"$*\" >>'" + asked + "'; exec sleep 60 ;; esac\nexec '" + real + "' \"$@\"\n"
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(stand), 0o700); err != nil {
		t.Fatal(err)
	}
	here.writeConfig(t, config.Config{})
	t.Setenv("PATH", bin+":"+testPath())
	t.Setenv("HOPSESH_TAILSCALE", "off")
	old := repos.ProbeTimeout
	repos.ProbeTimeout = 2 * time.Second
	t.Cleanup(func() { repos.ProbeTimeout = old })
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(cfg, all.Registry(), config.StateDir(), nil)
	ctx := context.Background()

	start := time.Now()
	inv := a.Scan(ctx, app.ScanOptions{Hosts: []string{"here"}})
	defer inv.Close()
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("the scan took %s", took)
	}
	ok, err := inv.Find(app.ParseRef(sid))
	if err != nil {
		t.Fatal(err)
	}
	if ok.Git == nil || ok.Git.Identity != "example.com/alice/demo" || ok.GitError != "" {
		t.Fatalf("the repository's session: %+v %q", ok.Git, ok.GitError)
	}
	stuck, err := inv.Find(app.ParseRef(slowID))
	if err != nil {
		t.Fatal(err)
	}
	if stuck.Git != nil || stuck.GitError != repos.TimeoutError("2") {
		t.Fatalf("the slow folder's session: %+v %q", stuck.Git, stuck.GitError)
	}
	p, _, err := a.Plan(ctx, inv, stuck, "codex", move.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasBlocker(p, "hopsesh could not read the session's git checkout on here (git did not answer within 2 seconds") {
		t.Fatalf("blockers: %v", p.Blockers)
	}
	p, _, err = a.Plan(ctx, inv, ok, "codex", move.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if hasBlocker(p, "git") {
		t.Fatalf("the other session is blocked: %v", p.Blockers)
	}

	// A command about the repository's session leaves the slow folder alone.
	before, _ := os.ReadFile(asked)
	start = time.Now()
	inv = a.Scan(ctx, app.ScanOptions{Hosts: []string{"here"}, GitFor: a.GitFor(app.ParseRef(sid))})
	defer inv.Close()
	if took := time.Since(start); took >= repos.ProbeTimeout {
		t.Errorf("the narrowed scan took %s", took)
	}
	if after, _ := os.ReadFile(asked); string(after) != string(before) {
		t.Errorf("git was asked about the slow folder: %s", after)
	}
	if ok, _ = inv.Find(app.ParseRef(sid)); ok.Git == nil || ok.Git.Identity != "example.com/alice/demo" {
		t.Errorf("narrowed, the repository's session: %+v %q", ok.Git, ok.GitError)
	}
	if stuck, _ = inv.Find(app.ParseRef(slowID)); stuck.Git != nil || stuck.GitError != "" {
		t.Errorf("narrowed, the slow folder's session: %+v %q", stuck.Git, stuck.GitError)
	}
}
