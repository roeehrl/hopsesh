package transport

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestSFTPStartupAndOwnedLifetime(t *testing.T) {
	// A native subprocess exercises pipe cancellation on Windows as well as
	// Unix. The healthy mode speaks real SFTP; the stalled mode never replies.
	dir := t.TempDir()
	source := filepath.Join(dir, "ssh.go")
	body := `package main
import ("io"; "os"; "time"; "github.com/pkg/sftp")
type stdio struct{}
func (stdio) Read(p []byte) (int,error) { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int,error) { return os.Stdout.Write(p) }
func (stdio) Close() error { return nil }
func main() {
 if os.Getenv("HOPSESH_SFTP_TEST_MODE") == "stall" { time.Sleep(3*time.Second); return }
 server, err := sftp.NewServer(stdio{}); if err != nil { panic(err) }
 if err := server.Serve(); err != nil && err != io.EOF { panic(err) }
 // Deliberately ignore stdin EOF long enough to reveal unbounded Close.
 time.Sleep(3*time.Second)
}`
	if err := os.WriteFile(source, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "ssh")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if b, err := exec.CommandContext(t.Context(), "go", "build", "-o", bin, source).CombinedOutput(); err != nil {
		t.Fatalf("build SFTP fixture: %v %s", err, b)
	}
	for _, kind := range []string{"caller deadline", "connection deadline"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("HOPSESH_SFTP_TEST_MODE", "stall")
			ctx := t.Context()
			c := &Conn{Dest: "fixture", StateDir: dir, sshBinary: bin, Timeout: 100 * time.Millisecond}
			if kind == "caller deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
				c.Timeout = time.Minute
			}
			start := time.Now()
			r, err := c.OpenSFTP(ctx, false)
			if r != nil {
				_ = r.Close()
			}
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
				t.Fatalf("SFTP ignored %s: error=%v elapsed=%v", kind, err, time.Since(start))
			}
		})
	}
	t.Run("successful session survives caller and closes promptly", func(t *testing.T) {
		t.Setenv("HOPSESH_SFTP_TEST_MODE", "serve")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		c := &Conn{Dest: "fixture", StateDir: dir, sshBinary: bin, Timeout: 5 * time.Second}
		r, err := c.OpenSFTP(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Close() })
		cancel()
		if _, err := r.Client().Stat(filepath.ToSlash(source)); err != nil {
			t.Fatalf("caller cancellation killed owned session: %v", err)
		}
		start := time.Now()
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("close waited for uncooperative child: %v", time.Since(start))
		}
		if r.cmd.ProcessState == nil {
			t.Fatal("owned SSH process was not reaped")
		}
		var repeated sync.WaitGroup
		for range 8 {
			repeated.Go(func() {
				if err := r.Close(); err != nil {
					t.Errorf("repeated close: %v", err)
				}
			})
		}
		repeated.Wait()
	})
}
