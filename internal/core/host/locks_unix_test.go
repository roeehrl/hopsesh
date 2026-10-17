//go:build !windows

package host

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestUnknownLockHoldersAreNotReportedFree(t *testing.T) {
	p := filepath.Join(t.TempDir(), "unreadable.lock")
	if err := os.Symlink(p, p); err != nil {
		t.Fatal(err)
	}
	l := localLocks{}
	states, err := l.Probe(t.Context(), []string{p})
	if err != nil || states[p] != agent.LockUnknown {
		t.Fatalf("fixture must have unknown lock state: %v, %v", states, err)
	}
	holders, err := l.Holders(t.Context(), []string{p})
	if !errors.Is(err, agent.ErrUnsupported) || holders != nil {
		t.Fatalf("unknown lock reported as free: holders=%v, error=%v", holders, err)
	}
}

// A process holding a flock is found as the holder, and named.
func TestLockHoldersAndNames(t *testing.T) {
	p := filepath.Join(t.TempDir(), "thread.lock")
	cmd := exec.Command("perl", "-e", `use Fcntl qw(:flock); open(my $f, ">", $ARGV[0]) or die; flock($f, LOCK_EX) or die; print "locked\n"; $| = 1; sleep 30`, p)
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Skip("perl is not available")
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if line, _ := bufio.NewReader(out).ReadString('\n'); !strings.HasPrefix(line, "locked") {
		t.Fatal("the lock was not taken")
	}
	ctx := context.Background()
	var l localLocks
	if st, _ := l.Probe(ctx, []string{p}); st[p] != agent.LockHeld {
		t.Fatal("probe should see the lock")
	}
	h, err := l.Holders(ctx, []string{p})
	if err != nil {
		t.Fatal(err)
	}
	if len(h[p]) != 1 || h[p][0] != cmd.Process.Pid {
		t.Fatalf("holders %v, want %d", h, cmd.Process.Pid)
	}
	names, err := localProcs{}.Names(ctx, h[p])
	if err != nil || names[cmd.Process.Pid] != "perl" {
		t.Fatalf("names %v %v", names, err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	time.Sleep(100 * time.Millisecond)
	if h, _ := l.Holders(ctx, []string{p}); len(h[p]) != 0 {
		t.Fatalf("a released lock has no holders: %v", h)
	}
}

func TestParseProcLocks(t *testing.T) {
	in := "1: FLOCK  ADVISORY  WRITE 4242 08:02:131 0 EOF\n1: -> FLOCK  ADVISORY  WRITE 5555 08:02:131 0 EOF\n2: POSIX  ADVISORY  READ 7 00:15:99 0 EOF\n"
	got := parseProcLocks(bufio.NewScanner(strings.NewReader(in)), 131)
	if len(got) != 1 || got[0] != 4242 {
		t.Fatalf("got %v", got)
	}
}
