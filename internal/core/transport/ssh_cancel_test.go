package transport

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSSHRunPreservesCancellation(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "ssh.go")
	// Partial output is not proof of successful execution. A stalled SSH client
	// killed by CommandContext yields ExitError (code 1 on Windows), which must
	// never be mistaken for the remote command's own status.
	body := `package main
import ("fmt"; "os"; "os/exec"; "time")
func main() {
 if len(os.Args) == 2 && os.Args[1] == "hold-pipe" { time.Sleep(3*time.Second); return }
 if len(os.Args) == 2 && os.Args[1] == "finish-pipe" { time.Sleep(250*time.Millisecond); fmt.Print("drained\n"); return }
 for _, arg := range os.Args[1:] { if arg == "-G" { fmt.Print("hostname 127.0.0.1\nport 22\n"); return } }
 mode := os.Args[len(os.Args)-1]
 if mode == "parent-exits" || mode == "parent-canceled" || mode == "parent-drains" {
  childMode := "hold-pipe"
  if mode == "parent-drains" { childMode = "finish-pipe" }
  child := exec.Command(os.Args[0], childMode)
  child.Stdout, child.Stderr = os.Stdout, os.Stderr
  if err := child.Start(); err != nil { panic(err) }
  child.Process.Release()
  if mode == "parent-exits" || mode == "parent-drains" { return }
 }
 fmt.Println("Linux"); time.Sleep(3*time.Second)
}
`
	if err := os.WriteFile(source, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "ssh")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", bin, source).CombinedOutput(); err != nil {
		t.Fatalf("build SSH fixture: %v\n%s", err, out)
	}
	for _, kind := range []string{"caller deadline", "connection deadline", "caller cancellation"} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			c := &Conn{Dest: "fixture", StateDir: dir, sshBinary: bin, Timeout: time.Minute}
			want := context.DeadlineExceeded
			switch kind {
			case "caller deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
			case "connection deadline":
				c.Timeout = 200 * time.Millisecond
			case "caller cancellation":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				timer := time.AfterFunc(200*time.Millisecond, cancel)
				defer timer.Stop()
				want = context.Canceled
			}
			start := time.Now()
			out, err := c.Run(ctx, "uname -s")
			var remote *RemoteError
			if !errors.Is(err, want) || errors.As(err, &remote) || time.Since(start) > 2*time.Second {
				t.Fatalf("%s misclassified: stdout=%q error=%v elapsed=%v", kind, out, err, time.Since(start))
			}
			if want == context.DeadlineExceeded && !errors.Is(err, ErrUnreachable) {
				t.Fatalf("deadline lost transport failure classification: %v", err)
			}
		})
	}
	t.Run("successful exit drains finite inherited output", func(t *testing.T) {
		c := &Conn{Dest: "fixture", StateDir: dir, sshBinary: bin, Timeout: time.Minute}
		started := time.Now()
		t.Cleanup(func() {
			if remaining := time.Second - time.Since(started); remaining > 0 {
				time.Sleep(remaining)
			}
		})
		out, err := c.Run(t.Context(), "parent-drains")
		if err != nil || string(out) != "drained\n" {
			t.Fatalf("successful SSH exit lost finite pending output: output=%q error=%v", out, err)
		}
	})
	for _, mode := range []string{"parent-exits", "parent-canceled"} {
		t.Run("retained output pipe/"+mode, func(t *testing.T) {
			c := &Conn{Dest: "fixture", StateDir: dir, sshBinary: bin, Timeout: time.Minute}
			want := exec.ErrWaitDelay
			if mode == "parent-canceled" {
				c.Timeout = 200 * time.Millisecond
				want = context.DeadlineExceeded
			}
			start := time.Now()
			// The independently exiting fixture child is deliberately not killed:
			// the transport must close its own pipes, not arbitrary descendants.
			// Leave time for that finite child to exit before TempDir removes its
			// executable (Windows prevents removing a running executable).
			t.Cleanup(func() {
				if remaining := 4*time.Second - time.Since(start); remaining > 0 {
					time.Sleep(remaining)
				}
			})
			_, err := c.Run(t.Context(), mode)
			if time.Since(start) > 2*time.Second || !errors.Is(err, want) {
				t.Fatalf("retained pipe escaped command bound: error=%v elapsed=%v", err, time.Since(start))
			}
		})
	}
}
