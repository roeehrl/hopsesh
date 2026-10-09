package repos

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
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

	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// The test binary is the stand-in git when HOPSESH_TEST_REAL_GIT is set.
func TestMain(m *testing.M) {
	if real := os.Getenv("HOPSESH_TEST_REAL_GIT"); real != "" {
		os.Exit(standInGit(real))
	}
	os.Exit(m.Run())
}

// standInGit (Windows) is git, except that it never answers for a folder named
// "offloaded" (like git waiting on files a cloud drive has not downloaded) and answers
// slowly for one named "busy" (a cold disk). It logs each call to HOPSESH_TEST_GIT_LOG.
func standInGit(real string) int {
	start := time.Now()
	defer func() {
		if f, err := os.OpenFile(os.Getenv("HOPSESH_TEST_GIT_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = fmt.Fprintf(f, "%s %s: %v\n", start.Format("15:04:05.000"), time.Since(start).Round(time.Millisecond), os.Args[1:])
			_ = f.Close()
		}
	}()
	for _, a := range os.Args[1:] {
		switch {
		case strings.Contains(a, "offloaded"):
			if marker := os.Getenv("HOPSESH_TEST_PROBE_PID"); marker != "" {
				_ = os.WriteFile(marker, []byte(strconv.Itoa(os.Getpid())), 0600)
			}
			time.Sleep(time.Minute)
			return 1
		case strings.Contains(a, "busy"):
			time.Sleep(busyCall)
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

// busyCall is how long each git call takes in the busy folder: well inside the limit, but
// the folder's dozen calls together take longer than it.
const busyCall = 500 * time.Millisecond

type slowFolders struct {
	repo, busy, offloaded, missing string
	log                            string // the stand-in's calls (Windows)
}

func (w slowFolders) dirs() []string { return []string{w.repo, w.busy, w.offloaded, w.missing} }

// slowWorld puts the stand-in git first on PATH and makes four folders: a repository, a
// repository the stand-in answers for slowly, a folder it never answers for, and a
// missing one.
func slowWorld(t *testing.T) slowFolders {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	w := slowFolders{repo: filepath.Join(root, "repo"), busy: filepath.Join(root, "busy"),
		offloaded: filepath.Join(root, "Cloud Drive", "offloaded"), missing: filepath.Join(root, "missing"), log: filepath.Join(root, "git.log")}
	for _, d := range []string{w.repo, w.busy, w.offloaded} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{w.repo, w.busy} {
		git(t, d, "init", "-q", "-b", "main")
		git(t, d, "remote", "add", "origin", "https://example.com/alice/demo.git")
		git(t, d, "commit", "-q", "--allow-empty", "-m", "one")
	}

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
		t.Setenv("HOPSESH_TEST_GIT_LOG", w.log)
		// Each stand-in is this race-instrumented test binary. Its default
		// one-second exit sleep is not Git latency and accumulates per probe.
		t.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
		ProbeTimeout = 15 * time.Second // the stand-in is a large program, started for every call
	} else {
		script := "#!/bin/sh\ncase \"$*\" in *offloaded*) exec sleep 60 ;; *busy*) sleep 0.5 ;; esac\nexec '" + real + "' \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		ProbeTimeout = 2 * time.Second
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return w
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

// check: only the offloaded folder is reported as unreadable; the busy one, slower in all
// than the limit, is read in full.
func (w slowFolders) check(t *testing.T, states []GitState, took time.Duration) {
	t.Helper()
	defer func() {
		if t.Failed() {
			b, _ := os.ReadFile(w.log)
			t.Logf("the stand-in's calls:\n%s", b)
		}
	}()
	// The stand-in sleeps a minute; the probe waits ProbeTimeout for it.
	if took > ProbeTimeout+40*time.Second {
		t.Errorf("the probe took %s", took)
	}
	if len(states) != 4 {
		t.Fatalf("got %d states: %+v", len(states), states)
	}
	r, b, o, m := states[0], states[1], states[2], states[3]
	for _, g := range []GitState{r, b} {
		if !g.IsRepo || g.Error != "" || g.Branch != "main" || g.Identity != "example.com/alice/demo" || g.Unpushed != 1 || g.RootCommit == "" || len(g.Worktrees) != 1 {
			t.Errorf("repository: %+v", g)
		}
	}
	if r.Dir != w.repo || b.Dir != w.busy {
		t.Errorf("folders: %s, %s", r.Dir, b.Dir)
	}
	if o.Dir != w.offloaded || o.IsRepo || o.Error != TimeoutError(strconv.Itoa(seconds(ProbeTimeout))) {
		t.Errorf("offloaded folder: %+v", o)
	}
	if m.Dir != w.missing || m.Exists || m.Error != "" {
		t.Errorf("missing folder: %+v", m)
	}
}

// A folder git does not answer for no longer holds up the probe: it gets its own state,
// and the folders after it are probed as usual. The limit is per git call: a folder that
// answers slowly is never cut off.
func TestProbeLocalSlowFolder(t *testing.T) {
	w := slowWorld(t)
	start := time.Now()
	states, err := ProbeLocal(context.Background(), w.dirs(), nil)
	if err != nil {
		t.Fatal(err)
	}
	w.check(t, states, time.Since(start))
}

// The same for the PowerShell probe Windows machines run.
func TestPowerShellProbeSlowFolder(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("runs Windows PowerShell")
	}
	w := slowWorld(t)
	script := "$ProgressPreference='SilentlyContinue';" + PowerShellProbe(w.dirs(), nil, ProbeTimeout)
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
	w.check(t, WindowsPaths(ParseProbe(out, nil)), time.Since(start))
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
	sh, _ := ProbeScript([]string{"/w/a"}, nil, 1500*time.Millisecond)
	ps := PowerShellProbe([]string{`C:\w\a`}, nil, 1500*time.Millisecond)
	if !strings.Contains(sh, "hp_limit=2\n") || !strings.Contains(ps, "$hpLimit = 2\n") {
		t.Fatalf("limit missing:\n%s\n%s", sh, ps)
	}
	if strings.Contains(sh, "@LIMIT@") || strings.Contains(ps, "@LIMIT@") || strings.Contains(sh, "@EXCLUDES@") || strings.Contains(ps, "@EXCLUDES@") {
		t.Fatal("a placeholder was left")
	}
}

// Fault-inject the lost TERM observed when a fast folder finishes while its
// watchdog is still installing the signal handler. Completion must not depend
// solely on that signal or consume the whole Git inactivity deadline.
func TestProbeWatchdogRecognizesCompletedFolderWithoutSignal(t *testing.T) {
	script, args := ProbeScript([]string{filepath.Join(t.TempDir(), "missing")}, nil, 30*time.Second)
	script = "kill() { case \"$1\" in -9) command kill \"$@\" ;; *) return 0 ;; esac; }\n" + script
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	sh, err := findSh()
	if err != nil {
		t.Fatal(err)
	}
	cmd := proc.CommandContext(ctx, sh, append([]string{"-c", script, "probe-test"}, args[1:]...)...)
	configureProbeCancellation(cmd)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("completed folder waited for the inactivity deadline: %v", err)
	}
	states := ParseProbe(out, nil)
	if len(states) != 1 || states[0].Exists || states[0].Error != "" {
		t.Fatalf("missing folder result changed: %+v", states)
	}
}
