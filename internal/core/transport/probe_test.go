package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSSHConfigLookupIsBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stand-in SSH uses a POSIX shell")
	}
	fake := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Conn{Dest: "offline", sshBinary: fake}
	start := time.Now()
	_, err := c.Resolve(t.Context())
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 10*time.Second {
		t.Fatalf("unbounded settings lookup: %v after %v", err, time.Since(start))
	}
	// The optional local-network preflight obeys a shorter caller deadline too.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start = time.Now()
	if targets := c.localTargets(ctx); len(targets) != 0 || time.Since(start) > time.Second {
		t.Fatalf("preflight ignored cancellation: %v after %v", targets, time.Since(start))
	}
	// Cleanup has no caller context and previously hung even after the scan's
	// connection deadline expired. It must still release local password state.
	c.controlDir = t.TempDir()
	released := false
	c.pwRelease = func() { released = true }
	start = time.Now()
	c.Close()
	if !released || time.Since(start) > 5*time.Second {
		t.Fatalf("cleanup stalled or did not release credentials after %v", time.Since(start))
	}
}

// When ssh-keyscan returns no keys (Windows' does that for some OpenSSH servers), the key
// comes from one ssh connection with a throwaway known_hosts: what ssh accepted into it.
func TestHostKeyViaSSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in ssh is a shell script")
	}
	dir := t.TempDir()
	// A made-up ed25519 public key blob: the type name, then 32 bytes of 0x07.
	blob := append([]byte("\x00\x00\x00\x0bssh-ed25519\x00\x00\x00\x20"), bytes.Repeat([]byte{7}, 32)...)
	key := base64.StdEncoding.EncodeToString(blob)
	fake := filepath.Join(dir, "ssh")
	// Like ssh with StrictHostKeyChecking=accept-new: write the key, then fail the login.
	script := "#!/bin/sh\nfor a; do case \"$a\" in UserKnownHostsFile=*) f=\"${a#UserKnownHostsFile=}\";; esac; done\n" +
		"echo \"[studio]:2222 ssh-ed25519 " + key + "\" > \"$f\"\necho 'Permission denied' >&2\nexit 255\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Conn{Dest: "studio", sshBinary: fake}
	keys := parseHostKeys(c.hostKeyViaSSH(t.Context()), "[studio]:2222")
	if len(keys) != 1 || keys[0].Type != "ssh-ed25519" || keys[0].Line != "[studio]:2222 ssh-ed25519 "+key {
		t.Fatalf("keys: %+v", keys)
	}
	if got := parseHostKeys([]byte("# studio:2222 SSH-2.0-OpenSSH_9.6p1 Ubuntu\n"), "studio"); len(got) != 0 {
		t.Fatalf("a banner is not a key: %+v", got)
	}
}
