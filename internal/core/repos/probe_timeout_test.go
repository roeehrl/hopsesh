package repos

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// The test binary is the stand-in git when HOPSESH_TEST_REAL_GIT is set.
func TestMain(m *testing.M) {
	if real := os.Getenv("HOPSESH_TEST_REAL_GIT"); real != "" {
		os.Exit(standInGit(real))
	}
	os.Exit(m.Run())
}

// standInGit (Windows) is git, except for a folder named "offloaded", where it never answers (like
// git waiting on files a cloud drive has not downloaded).
func standInGit(real string) int {
	for _, a := range os.Args[1:] {
		if strings.Contains(a, "offloaded") {
			time.Sleep(time.Minute)
			return 1
		}
	}
	cmd := exec.Command(real, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		return 127
	}
	return 0
}

// slowWorld puts the stand-in git first on PATH and makes three folders: a repository,
// one the stand-in never answers for, and a missing one.
func slowWorld(t *testing.T) (repo, offloaded, missing string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	repo = filepath.Join(root, "repo")
	offloaded = filepath.Join(root, "Cloud Drive", "offloaded")
	missing = filepath.Join(root, "missing")
	for _, d := range []string{repo, offloaded} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "remote", "add", "origin", "https://example.com/alice/demo.git")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "one")

	// The stand-in: a shell script where there is sh (fast), else this test binary. Not
	// under t.TempDir: on Windows a stand-in still running for the offloaded folder (the
	// POSIX probe cannot kill it there) keeps its file from being removed.
	bin, err := os.MkdirTemp("", "hopsesh-git-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bin) })
	old := ProbeTimeout
	t.Cleanup(func() { ProbeTimeout = old })
	if runtime.GOOS == "windows" {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		copyFile(t, self, filepath.Join(bin, "git.exe"))
		t.Setenv("HOPSESH_TEST_REAL_GIT", real)
		ProbeTimeout = 10 * time.Second // the stand-in starts slowly, and runs for every git call
	} else {
		script := "#!/bin/sh\ncase \"$*\" in *offloaded*) exec sleep 60 ;; esac\nexec '" + real + "' \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		ProbeTimeout = 2 * time.Second
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return repo, offloaded, missing
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// checkSlowStates: the offloaded folder is reported as unreadable, the others as usual.
func checkSlowStates(t *testing.T, states []GitState, took time.Duration, repo, offloaded, missing string) {
	t.Helper()
	// The stand-in sleeps a minute; the probe gives the folder ProbeTimeout.
	if took > ProbeTimeout+20*time.Second {
		t.Fatalf("the probe took %s", took)
	}
	if len(states) != 3 {
		t.Fatalf("got %d states: %+v", len(states), states)
	}
	r, o, m := states[0], states[1], states[2]
	if r.Dir != repo || !r.IsRepo || r.Error != "" || r.Branch != "main" || r.Identity != "example.com/alice/demo" {
		t.Errorf("repository: %+v", r)
	}
	if o.Dir != offloaded || o.IsRepo || o.Error != TimeoutError(strconv.Itoa(probeSeconds())) {
		t.Errorf("offloaded folder: %+v", o)
	}
	if m.Dir != missing || m.Exists || m.Error != "" {
		t.Errorf("missing folder: %+v", m)
	}
}

// A folder git does not answer for no longer holds up the probe: it gets its own state,
// and the folders after it are probed as usual.
func TestProbeLocalSlowFolder(t *testing.T) {
	repo, offloaded, missing := slowWorld(t)
	start := time.Now()
	states, err := ProbeLocal(context.Background(), []string{repo, offloaded, missing}, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkSlowStates(t, states, time.Since(start), repo, offloaded, missing)
}

// The same for the PowerShell probe Windows machines run.
func TestPowerShellProbeSlowFolder(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("runs Windows PowerShell")
	}
	repo, offloaded, missing := slowWorld(t)
	script := "$ProgressPreference='SilentlyContinue';" + PowerShellProbe([]string{repo, offloaded, missing}, nil)
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[2*i], b[2*i+1] = byte(v), byte(v>>8)
	}
	start := time.Now()
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(b))
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	checkSlowStates(t, WindowsPaths(ParseProbe(out, nil)), time.Since(start), repo, offloaded, missing)
}

// A folder that ran out of time keeps only the reason, whatever it printed before.
func TestParseProbeTimeout(t *testing.T) {
	out := "@@dir\t/w/a\nexists\t1\nrepo\t1\ntop\t/w/a\nwt\tworktree /w/a\n" +
		"timeout\t5\n" +
		"@@dir\t/w/b\nexists\t1\nerror\tgit was not found\n" +
		"@@dir\t/w/c\nexists\t1\nrepo\t0\n"
	got := ParseProbe([]byte(out), nil)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	a := got[0]
	if a.Dir != "/w/a" || a.IsRepo || a.Exists || a.Toplevel != "" || len(a.Worktrees) != 0 ||
		a.Error != "git did not answer within 5 seconds; the folder may be offloaded to iCloud or OneDrive, or on a slow disk" {
		t.Errorf("timed out: %+v", a)
	}
	if got[1].Error != "git was not found" {
		t.Errorf("error: %+v", got[1])
	}
	if c := got[2]; c.Error != "" || !c.Exists || c.IsRepo {
		t.Errorf("after: %+v", c)
	}
}

// The limit reaches both scripts, in whole seconds.
func TestProbeScriptsCarryTheLimit(t *testing.T) {
	old := ProbeTimeout
	t.Cleanup(func() { ProbeTimeout = old })
	ProbeTimeout = 1500 * time.Millisecond
	sh, _ := ProbeScript([]string{"/w/a"}, nil)
	ps := PowerShellProbe([]string{`C:\w\a`}, nil)
	if !strings.Contains(sh, "hp_limit=2\n") || !strings.Contains(ps, "$hpLimit = 2\n") {
		t.Fatalf("limit missing:\n%s\n%s", sh, ps)
	}
	if strings.Contains(sh, "@LIMIT@") || strings.Contains(ps, "@LIMIT@") || strings.Contains(sh, "@EXCLUDES@") || strings.Contains(ps, "@EXCLUDES@") {
		t.Fatal("a placeholder was left")
	}
}
