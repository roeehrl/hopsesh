package transport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
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

// Exercise the installed SSH executable, including native Windows argv/path
// handling, against a real key exchange. Authentication deliberately fails:
// collecting a public host key must not require a successful login.
func TestHostKeyViaSSHNative(t *testing.T) {
	bin, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("native SSH is not installed")
	}
	root := filepath.Join(t.TempDir(), "host keys with spaces")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Setenv("TMP", root)
		t.Setenv("TEMP", root)
	} else {
		t.Setenv("TMPDIR", root)
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		cfg := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
			return nil, errors.New("fixture refuses authentication")
		}}
		cfg.AddHostKey(signer)
		_, _, _, _ = ssh.NewServerConn(conn, cfg)
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	c := &Conn{Dest: "ssh://hopsesh-key-fixture@" + listener.Addr().String(), sshBinary: bin}
	recorded, err := c.hostKeyViaSSH(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	keys := parseHostKeys(recorded, "fixture")
	want := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if len(keys) != 1 || keys[0].Key != want {
		t.Fatalf("wrong host key: %+v, want %s", keys, want)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary trust leaked: %v %v", entries, err)
	}
}

func TestHostKeyFallbackFailureDiagnostic(t *testing.T) {
	c := &Conn{Dest: "fixture", sshBinary: filepath.Join(t.TempDir(), "missing-ssh")}
	_, err := c.hostKeyViaSSH(t.Context())
	if err == nil || !strings.Contains(err.Error(), "missing-ssh") || !strings.Contains(err.Error(), "no host keys recorded") {
		t.Fatalf("missing failure evidence: %v", err)
	}
	var d hostKeyDiagnostic
	for range 3 {
		if n, err := fmt.Fprint(&d, strings.Repeat("x", 2000)); n != 2000 || err != nil {
			t.Fatalf("diagnostic writer interrupted SSH: %d %v", n, err)
		}
	}
	if d.Len() != 2048 {
		t.Fatalf("diagnostics not bounded: %d", d.Len())
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
	recorded, err := c.hostKeyViaSSH(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	keys := parseHostKeys(recorded, "[studio]:2222")
	if len(keys) != 1 || keys[0].Type != "ssh-ed25519" || keys[0].Line != "[studio]:2222 ssh-ed25519 "+key {
		t.Fatalf("keys: %+v", keys)
	}
	if got := parseHostKeys([]byte("# studio:2222 SSH-2.0-OpenSSH_9.6p1 Ubuntu\n"), "studio"); len(got) != 0 {
		t.Fatalf("a banner is not a key: %+v", got)
	}
}
