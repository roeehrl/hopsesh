package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// This validates the orchestrator through real CLI processes, native transcript
// files and authenticated loopback SSH. It does not qualify mixed operating
// systems; journeysMain separately requires Linux, macOS and Windows hosts.
func TestJourneyCLIOverSSH(t *testing.T) {
	if os.Getenv("HOPSESH_JOURNEY_TEST") != "1" || runtime.GOOS == "windows" {
		t.Skip("opt-in Unix loopback SSH qualification of the journey orchestrator")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for name, pkg := range map[string]string{"hopsesh": "./cmd/hopsesh", "hsmatrix": "./internal/devtools/hsmatrix", "claude": "./internal/devtools/fakeagent"} {
		cmd := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(bin, name), pkg)
		cmd.Dir = "../../.."
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v %s", name, err, b)
		}
	}
	body, err := os.ReadFile(filepath.Join(bin, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(bin, "codex"), body, 0700); err != nil {
		t.Fatal(err)
	}
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		t.Fatal(err)
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "journey-fixture")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(root, "identity")
	if err = os.WriteFile(keyFile, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	var endpoints [3]side
	aliases := []string{"journey-a", "journey-b", "journey-c"}
	var sshConfig, knownHosts strings.Builder
	for i, alias := range aliases {
		home := filepath.Join(root, alias)
		if err := os.MkdirAll(home, 0700); err != nil {
			t.Fatal(err)
		}
		env := append(os.Environ(), "HOME="+home, "HOPSESH_CONFIG_DIR="+filepath.Join(home, "config"), "HOPSESH_STATE_DIR="+filepath.Join(home, "state"), "CLAUDE_CONFIG_DIR="+filepath.Join(home, "claude"), "CODEX_HOME="+filepath.Join(home, "codex"), "HSMATRIX_BASE="+filepath.Join(home, "repos"), "HOPSESH_MACHINE="+alias, "HOPSESH_TAILSCALE=off", "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		endpoint := journeyProcessSide{ctx: ctx, helper: filepath.Join(bin, "hsmatrix"), env: env, name: alias}
		endpoints[i] = endpoint
		address, hostKey := journeySSH(t, ctx, env, home, clientKey)
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sshConfig, "Host %s\n HostName 127.0.0.1\n Port %s\n User fixture\n IdentityFile %s\n IdentitiesOnly yes\n IdentityAgent none\n StrictHostKeyChecking yes\n UserKnownHostsFile %s\n GlobalKnownHostsFile /dev/null\n", alias, port, keyFile, filepath.Join(root, "known_hosts"))
		fmt.Fprintf(&knownHosts, "[127.0.0.1]:%s %s", port, ssh.MarshalAuthorizedKey(hostKey))
	}
	configPath := filepath.Join(root, "ssh_config")
	if err := os.WriteFile(configPath, []byte(sshConfig.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "known_hosts"), []byte(knownHosts.String()), 0600); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\nexec "+quote(sshBin)+" -F "+quote(configPath)+" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for i, endpoint := range endpoints {
		for j, alias := range aliases {
			if i == j {
				continue
			}
			for _, args := range [][]string{{"hosts", "add", alias, alias}, {"trust", alias, "--yes"}} {
				if err := endpoint.do("command", commandReq{Args: args}, &struct{}{}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, c := range journeyCases() {
		t.Run(c.Name, func(t *testing.T) {
			if err := runJourney(c, endpoints, aliases); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type journeyProcessSide struct {
	ctx          context.Context
	helper, name string
	env          []string
}

func (s journeyProcessSide) label() string { return s.name }
func (s journeyProcessSide) do(op string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(s.ctx, s.helper, "agent", op)
	cmd.Env = s.env
	cmd.Stdin = bytes.NewReader(b)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err = cmd.Output()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", s.name, op, err, stderr.String())
	}
	return json.Unmarshal(b, out)
}

func journeySSH(t *testing.T, ctx context.Context, env []string, home string, clientKey ssh.PublicKey) (string, ssh.PublicKey) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	server := &ssh.ServerConfig{PublicKeyCallback: func(meta ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if meta.User() != "fixture" || !bytes.Equal(k.Marshal(), clientKey.Marshal()) {
			return nil, fmt.Errorf("unknown fixture identity")
		}
		return nil, nil
	}}
	server.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[raw] = true
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer raw.Close()
				defer func() { mu.Lock(); delete(connections, raw); mu.Unlock() }()
				_ = raw.SetDeadline(time.Now().Add(5 * time.Second))
				conn, channels, requests, err := ssh.NewServerConn(raw, server)
				if err != nil {
					return
				}
				_ = raw.SetDeadline(time.Time{})
				defer conn.Close()
				go ssh.DiscardRequests(requests)
				for next := range channels {
					if next.ChannelType() != "session" {
						_ = next.Reject(ssh.Prohibited, "session only")
						continue
					}
					channel, reqs, err := next.Accept()
					if err != nil {
						return
					}
					for request := range reqs {
						var command struct{ Command string }
						if request.Type == "subsystem" && ssh.Unmarshal(request.Payload, &command) == nil && command.Command == "sftp" {
							_ = request.Reply(true, nil)
							fs, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(home))
							if err == nil {
								_ = fs.Serve()
								_ = fs.Close()
							}
							break
						}
						if request.Type != "exec" || ssh.Unmarshal(request.Payload, &command) != nil {
							_ = request.Reply(false, nil)
							continue
						}
						_ = request.Reply(true, nil)
						// Only the fixture's authenticated local client can execute
						// production discovery/peer commands, inside a temporary home.
						cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command.Command)
						cmd.Env, cmd.Dir = env, home
						cmd.Stdin, cmd.Stdout, cmd.Stderr = channel, channel, channel.Stderr()
						code := uint32(0)
						if err := cmd.Run(); err != nil {
							t.Logf("fixture command failed: %s: %v", command.Command, err)
							code = 1
						}
						_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{code}))
						break
					}
					_ = channel.Close()
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		for conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return listener.Addr().String(), signer.PublicKey()
}
