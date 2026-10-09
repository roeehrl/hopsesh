package repos

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestProbeCancellationStopsChildren(t *testing.T) {
	w := slowWorld(t)
	marker := filepath.Join(t.TempDir(), "git.pid")
	t.Setenv("HOPSESH_TEST_PROBE_PID", marker)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := ProbeLocal(ctx, []string{w.offloaded}, nil)
		done <- err
	}()
	var child windows.Handle
	deadline := time.Now().Add(15 * time.Second)
	for {
		b, _ := os.ReadFile(marker)
		pid, err := strconv.ParseUint(string(b), 10, 32)
		if err == nil && pid > 0 {
			child, err = windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(child)
			defer windows.TerminateProcess(child, 1)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Git child never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled probe succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("canceled probe did not return")
	}
	status, err := windows.WaitForSingleObject(child, 1000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("Git child still runs after probe cancellation: status=%d err=%v", status, err)
	}
}
