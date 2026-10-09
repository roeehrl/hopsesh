package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// The installed client must send the remote command verbatim on every OS.
// A real SSH server records the wire command; no local shell stand-in can prove
// Windows' command-line argument handling or native stderr capture.
func TestNativeSSHCommandAndDiagnostics(t *testing.T) {
	bin, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("native SSH is not installed")
	}
	version, _ := exec.Command(bin, "-V").CombinedOutput()
	t.Logf("native client %s: %s", bin, strings.TrimSpace(string(version)))
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, command string
		code          uint32
	}{
		{"successful OS detection", "uname -s", 0},
		{"successful POSIX quoting", "sh -c 'printf \"a\\nb\\n\"'", 0},
		{"successful PowerShell encoding", PowerShellCommand("Write-Output 'Darwin'; exit 0"), 0},
		{"failed OS detection", "uname -s", 17},
		{"failed POSIX quoting", "sh -c 'printf \"a\\nb\\n\"'", 17},
		{"failed PowerShell encoding", PowerShellCommand("Write-Error 'probe failed'; exit 17"), 17},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			observed := make(chan string, 1)
			done := make(chan error, 1)
			go func() { done <- recordSSHCommand(listener, signer, observed, test.code) }()
			t.Cleanup(func() {
				_ = listener.Close()
				if err := <-done; err != nil {
					t.Error(err)
				}
			})
			c := &Conn{Dest: "ssh://hopsesh-command-fixture@" + listener.Addr().String(), StateDir: t.TempDir(), sshBinary: bin, Timeout: 5 * time.Second}
			host, port, err := net.SplitHostPort(listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			keys := parseHostKeys([]byte(fmt.Sprintf("[%s]:%s %s", host, port, ssh.MarshalAuthorizedKey(signer.PublicKey()))), "["+host+"]:"+port)
			if err := c.Trust(keys); err != nil {
				t.Fatal(err)
			}
			out, err := c.Run(t.Context(), test.command)
			var remote *RemoteError
			if string(out) != "Darwin\n" {
				t.Fatalf("native stdout lost: stdout=%q error=%v", out, err)
			}
			if test.code == 0 && err != nil {
				t.Fatalf("successful remote command reported failure: %v", err)
			}
			if test.code != 0 && (!errors.As(err, &remote) || remote.Code != int(test.code) || remote.Stderr != "#< CLIXML\nprobe failed after header") {
				t.Fatalf("native output/diagnostics lost: stdout=%q error=%v", out, err)
			}
			select {
			case actual := <-observed:
				if actual != test.command {
					t.Fatalf("remote command changed: want %q, received %q", test.command, actual)
				}
			default:
				t.Fatal("server did not record the native client's command")
			}
		})
	}
}

func recordSSHCommand(listener net.Listener, signer ssh.Signer, observed chan<- string, exitCode uint32) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	server, channels, requests, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return err
	}
	defer server.Close()
	go ssh.DiscardRequests(requests)
	for incoming := range channels {
		if incoming.ChannelType() != "session" {
			_ = incoming.Reject(ssh.UnknownChannelType, "fixture accepts sessions only")
			continue
		}
		channel, requests, err := incoming.Accept()
		if err != nil {
			return err
		}
		defer channel.Close()
		for request := range requests {
			if request.Type != "exec" {
				_ = request.Reply(false, nil)
				continue
			}
			var payload struct{ Command string }
			if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
				return err
			}
			observed <- payload.Command
			_ = request.Reply(true, nil)
			_, _ = fmt.Fprint(channel, "Darwin\n")
			_, _ = fmt.Fprint(channel.Stderr(), "#< CLIXML\n")
			_, _ = fmt.Fprint(channel.Stderr(), "probe failed after header\n")
			_, err = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{exitCode}))
			_ = channel.Close()
			_ = server.Wait() // let the native client close after consuming exit status
			return err
		}
	}
	return fmt.Errorf("client closed without exec: %s", strings.TrimSpace(string(server.ClientVersion())))
}
