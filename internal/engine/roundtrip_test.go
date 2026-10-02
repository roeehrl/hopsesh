package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/fsys"
	"github.com/roeehrl/hopsesh/internal/core/hops"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/sessions"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func localPush(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "sh", "-c", repos.PushScript, "hopsesh", dir).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// TestRoundTrip moves a session laptop → studio → laptop. Each move pushes the source's
// unpushed commit, fast-forwards the destination checkout, marks the copy left behind
// "moved", and the copy that comes home replaces the stale one without the mark.
func TestRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixtures")
	}
	ctx := context.Background()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	origin := filepath.Join(root, "origin.git")
	const remoteURL = "https://example.test/alice/app.git"
	gcfg := filepath.Join(root, "gitconfig")
	os.WriteFile(gcfg, []byte("[url \"file://"+origin+"\"]\n\tinsteadOf = "+remoteURL+"\n[protocol \"file\"]\n\tallow = always\n"), 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", gcfg)
	gitOut(t, root, "init", "-q", "--bare", "-b", "main", origin)

	type machine struct {
		name, home, repo, cfg, state string
	}
	mk := func(name string) machine {
		home := filepath.Join(root, name)
		m := machine{name, home, filepath.Join(home, "git", "app"), filepath.Join(home, ".claude"), filepath.Join(home, "state")}
		return m
	}
	laptop, studio := mk("laptop"), mk("studio")
	gitOut(t, root, "clone", "-q", remoteURL, laptop.repo)
	os.WriteFile(filepath.Join(laptop.repo, "a.txt"), []byte("1"), 0o644)
	gitOut(t, laptop.repo, "add", ".")
	gitOut(t, laptop.repo, "commit", "-qm", "one")
	gitOut(t, laptop.repo, "push", "-q", "-u", "origin", "main")
	gitOut(t, root, "clone", "-q", remoteURL, studio.repo)
	// Work on the laptop that has not been pushed yet.
	os.WriteFile(filepath.Join(laptop.repo, "a.txt"), []byte("2"), 0o644)
	gitOut(t, laptop.repo, "commit", "-qam", "two")
	two := gitOut(t, laptop.repo, "rev-parse", "HEAD")

	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	laptopFile := filepath.Join(laptop.cfg, "projects", sessions.Slug(laptop.repo), id+".jsonl")
	writeJSONL(t, laptopFile,
		map[string]any{"type": "user", "uuid": "u1", "parentUuid": nil, "sessionId": id, "cwd": laptop.repo, "timestamp": "2026-10-01T10:00:00Z", "message": map[string]any{"role": "user", "content": "start in " + laptop.repo}},
		map[string]any{"type": "custom-title", "customTitle": "Round trip", "sessionId": id})

	move := func(from, to machine, file string, push func(context.Context, string) (string, error)) (*Plan, *Result) {
		t.Helper()
		sum, err := sessions.Summarize(fsys.Local{}, file)
		if err != nil {
			t.Fatal(err)
		}
		st, err := repos.ProbeLocal(ctx, []string{sum.CWD})
		if err != nil {
			t.Fatal(err)
		}
		src := Source{Host: from.name, OS: "darwin", FS: fsys.Local{}, ConfigDir: from.cfg, Home: from.home, Push: push}
		tgt := Target{Host: to.name, OS: "darwin", ConfigDir: to.cfg, Home: to.home, ClaudeVersion: "2.1.284"}
		opt := Options{ReposDir: filepath.Join(to.home, "git"), MarkSource: true, SyncCode: true, PushSource: true}
		p, err := BuildPlan(ctx, src, tgt, Input{Summary: sum, Git: &st[0]}, opt)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Blockers) > 0 || p.Mark != MarkNow || !p.Push || p.Sync == "" {
			t.Fatalf("plan: blockers=%v mark=%s push=%v sync=%q", p.Blockers, p.Mark, p.Push, p.Sync)
		}
		res, err := Apply(ctx, p, src, Env{StateDir: to.state, Log: nil})
		if err != nil {
			t.Fatal(err)
		}
		return p, res
	}

	// laptop → studio
	p, res := move(laptop, studio, laptopFile, localPush)
	if res.PushError != "" || res.Sync == nil || res.Sync.State != repos.SyncFastForwarded || res.Mark != hops.MarkDone {
		t.Fatalf("first move: %+v sync=%+v", res, res.Sync)
	}
	if head := gitOut(t, studio.repo, "rev-parse", "HEAD"); head != two {
		t.Fatalf("studio checkout at %s, want %s", head, two)
	}
	left, _ := sessions.Summarize(fsys.Local{}, laptopFile)
	if left.MovedTo != "studio" || left.Title != "Round trip" {
		t.Fatalf("copy left on the laptop: movedTo=%q title=%q", left.MovedTo, left.Title)
	}
	studioFile := p.TargetFile
	here, _ := sessions.Summarize(fsys.Local{}, studioFile)
	if here.MovedTo != "" || here.Title != "Round trip" || here.CWD != studio.repo {
		t.Fatalf("studio copy: %+v", here)
	}
	if h, ok := hops.Last(studio.state, id); !ok || h.From != "laptop" || h.Mark != hops.MarkDone {
		t.Fatalf("ledger: %+v", h)
	}

	// Work continues on the studio: a new message and an unpushed commit.
	f, _ := os.OpenFile(studioFile, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"user","uuid":"u2","parentUuid":"u1","sessionId":"` + id + `","cwd":"` + studio.repo + `","timestamp":"2026-10-02T09:00:00Z","message":{"role":"user","content":"continued on the studio"}}` + "\n")
	f.Close()
	os.WriteFile(filepath.Join(studio.repo, "a.txt"), []byte("3"), 0o644)
	gitOut(t, studio.repo, "commit", "-qam", "three")
	three := gitOut(t, studio.repo, "rev-parse", "HEAD")

	// studio → laptop: the stale, marked copy on the laptop is set aside and replaced.
	p2, res2 := move(studio, laptop, studioFile, localPush)
	if len(p2.Duplicates) != 1 || len(res2.SetAside) == 0 {
		t.Fatalf("the stale laptop copy should be set aside: %v %v", p2.Duplicates, res2.SetAside)
	}
	if res2.Sync == nil || res2.Sync.State != repos.SyncFastForwarded || gitOut(t, laptop.repo, "rev-parse", "HEAD") != three {
		t.Fatalf("laptop checkout not brought to %s: %+v", three, res2.Sync)
	}
	back, _ := sessions.Summarize(fsys.Local{}, p2.TargetFile)
	if back.MovedTo != "" || back.Title != "Round trip" || back.LastPrompt != "continued on the studio" || back.CWD != laptop.repo {
		t.Fatalf("copy back on the laptop: %+v", back)
	}
	b, _ := os.ReadFile(p2.TargetFile)
	if strings.Contains(string(b), "moved to") {
		t.Fatal("the copy that came home still carries a moved mark")
	}
	stale, _ := sessions.Summarize(fsys.Local{}, studioFile)
	if stale.MovedTo != "laptop" {
		t.Fatalf("copy left on the studio: movedTo=%q", stale.MovedTo)
	}
}

// TestRoundTripLiveAndFork: a source that is still running is marked later; a fork is
// never marked.
func TestRoundTripLiveAndFork(t *testing.T) {
	p := &Plan{Live: true}
	planRoundTrip(p, Source{Host: "a"}, Target{Host: "b"}, Input{}, Options{MarkSource: true})
	if p.Mark != MarkWhenStopped {
		t.Fatalf("live: %s", p.Mark)
	}
	p = &Plan{Live: true}
	planRoundTrip(p, Source{Host: "a"}, Target{Host: "b"}, Input{}, Options{MarkSource: true, Fork: true})
	if p.Mark != MarkOff {
		t.Fatalf("fork: %s", p.Mark)
	}
	p = &Plan{}
	planRoundTrip(p, Source{Host: "a"}, Target{Host: "a"}, Input{}, Options{MarkSource: true})
	if p.Mark != MarkOff {
		t.Fatalf("same machine: %s", p.Mark)
	}
	st := t.TempDir()
	markCopyLeftBehind(&Plan{SessionID: "s", SourceHost: "a", Mark: MarkWhenStopped}, Source{FS: fsys.Local{}}, Env{StateDir: st}, &Result{})
	if got := hops.Pending(st, "a"); len(got) != 1 {
		t.Fatalf("pending: %+v", got)
	}
}
