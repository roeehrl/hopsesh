//go:build !windows

package repos

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A canceled scan must stop both the stuck Git child and its watchdog sleep,
// rather than leaving them to wake in the background until the probe deadline.
func TestProbeCancellationStopsChildren(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOPSESH_PROBE_TEST_ROOT", root)
	t.Setenv("HOPSESH_PROBE_TEST_SLEEP", sleep)
	for name, script := range map[string]string{
		"git":   "#!/bin/sh\nprintf '%s' \"$$\" > \"$HOPSESH_PROBE_TEST_ROOT/git.pid\"\nexec \"$HOPSESH_PROBE_TEST_SLEEP\" 60\n",
		"sleep": "#!/bin/sh\nprintf '%s' \"$PPID\" > \"$HOPSESH_PROBE_TEST_ROOT/watchdog-parent.pid\"\nprintf '%s' \"$$\" > \"$HOPSESH_PROBE_TEST_ROOT/watchdog.pid\"\nexec \"$HOPSESH_PROBE_TEST_SLEEP\" 60\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := ProbeLocal(ctx, []string{root}, nil)
		done <- err
	}()
	pids := make([]int, 0, 2)
	for _, name := range []string{"git", "watchdog", "watchdog-parent"} {
		deadline := time.Now().Add(5 * time.Second)
		for {
			b, _ := os.ReadFile(filepath.Join(root, name+".pid"))
			pid, err := strconv.Atoi(string(b))
			if err == nil && pid > 0 {
				pids = append(pids, pid)
				// Only the fixture's recorded child, should the regression fail.
				t.Cleanup(func() { p, _ := os.FindProcess(pid); _ = p.Kill() })
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s child never started", name)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled probe succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled probe did not return")
	}
	for _, pid := range pids {
		deadline := time.Now().Add(time.Second)
		for {
			state, err := exec.Command("ps", "-p", fmt.Sprint(pid), "-o", "stat=").Output()
			if err != nil || strings.TrimSpace(string(state)) == "" || strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
				break
			}
			if time.Now().After(deadline) {
				t.Errorf("probe child %d still runs after cancellation: %s", pid, state)
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
