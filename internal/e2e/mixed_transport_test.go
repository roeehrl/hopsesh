package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// These routes cross real authenticated SSH streams and the actual SQLite/R2
// relay, using the same native endpoints and causal receipts throughout. The
// loopback SSH server permits only the peer command; OS SSH server installation,
// remote discovery and mixed operating systems remain separate qualification.
func TestMixedTransportLineageSQLiteR2(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_RELAY_PLATFORM") != "1" {
		t.Skip("actual SSH and SQLite/R2 mixed transport journeys")
	}
	bin := buildHopsesh(t)
	for _, route := range []string{"ABABA", "ABCA", "ABCBCAB"} {
		for _, fork := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fork=%t", route, fork), func(t *testing.T) {
				ctx, _, origin, cert, client := startSQLiteRelayFixture(t, time.Minute)
				f := newRelayFleet(t, ctx, bin, origin, cert, client)
				original := f.seed(t, "claude")
				originalBytes, err := os.ReadFile(original.Path)
				if err != nil {
					t.Fatal(err)
				}
				current, sourceAgent := original, "claude"
				var sentinels []string
				var family, branch string
				for hop := 0; hop < len(route)-1; hop++ {
					from, to := route[hop], route[hop+1]
					// Fork creation itself carries existing work; subsequent work
					// belongs solely to the child and must not alter its parent.
					if !fork || hop > 0 {
						text := fmt.Sprintf("MIXED-TRANSPORT-WORK-%d", hop)
						sentinels = append(sentinels, text)
						f.appendWork(t, current, sourceAgent, text)
					}
					target := "claude"
					if to == 'B' {
						target = "codex"
					}
					operation := fmt.Sprintf("mixed-route-hop-%d-123456789", hop)
					if hop%2 == 0 {
						current = f.pushSSH(t, from, to, current, target, fork && hop == 0, operation)
					} else {
						current = f.transfer(t, from, to, current, target, "push", false, operation)
					}
					if hop == 1 {
						before, err := os.ReadFile(current.Path)
						if err != nil {
							t.Fatal(err)
						}
						f.stop[to]()
						f.start(t, to)
						repeated := f.transfer(t, from, to, f.lastSource, target, "push", false, operation)
						after, err := os.ReadFile(repeated.Path)
						if err != nil || repeated.Key != current.Key || !bytes.Equal(before, after) {
							t.Fatal("restart/retry after transport switch changed native history", err)
						}
					}
					sourceAgent = target
					f.assertText(t, to, current, target, sentinels, nil)
					graph, err := lineage.Read(host.LocalFS(), current.Path)
					if err != nil || graph == nil {
						t.Fatal("mixed arrival missing lineage", err)
					}
					if hop == 0 {
						family, branch = graph.Family, graph.Branch
					}
					transfers, returns := hop+1, strings.Count(route[1:hop+2], "A")
					if fork {
						transfers, returns = hop, strings.Count(route[2:hop+2], "B")
					}
					if graph.Family != family || graph.Branch != branch || graph.Journey().Fork != fork || graph.Journey().Transfers != transfers || graph.Journey().RoundTrips != returns {
						t.Fatalf("hop %d transport switch changed journey: %+v", hop, graph.Journey())
					}
					hops := graph.ActiveHops()
					if len(hops) != hop+1 {
						t.Fatal("mixed transport lost or duplicated a causal hop", len(hops), hop+1)
					}
					for i, arrived := range hops {
						if arrived.ID != fmt.Sprintf("mixed-route-hop-%d-123456789", i) || graph.Replica(arrived.From).Location != f.homes[route[i]].name || graph.Replica(arrived.To).Location != f.homes[route[i+1]].name {
							t.Fatalf("hop %d lost ordered endpoint ancestry: %+v", i, arrived)
						}
					}
				}
				if fork {
					// A sibling starts from the original after the first child has
					// travelled, then switches transports on its own return.
					sibling := f.transfer(t, 'A', 'C', original, "codex", "push", true, "mixed-sibling-create-123456789")
					f.appendWork(t, sibling, "codex", "INDEPENDENT-MIXED-SIBLING")
					sibling = f.pushSSH(t, 'C', 'A', sibling, "claude", false, "mixed-sibling-return-123456789")
					f.assertText(t, 'A', sibling, "claude", []string{"INDEPENDENT-MIXED-SIBLING"}, sentinels)
					sg, err := lineage.Read(host.LocalFS(), sibling.Path)
					if err != nil || sg == nil || sg.Family != family || sg.Branch == branch || !sg.Journey().Fork || sg.Journey().Transfers != 1 {
						t.Fatal("travelling siblings shared a branch or counter", err)
					}
					after, err := os.ReadFile(original.Path)
					if err != nil || !bytes.Equal(originalBytes, after) {
						t.Fatal("mixed journey altered original native conversation", err)
					}
					f.appendWork(t, original, "claude", "INDEPENDENT-MIXED-PARENT")
					parent := f.transfer(t, 'A', 'C', original, "codex", "push", false, "mixed-parent-move-123456789")
					f.assertText(t, 'C', parent, "codex", []string{"INDEPENDENT-MIXED-PARENT"}, append(sentinels, "INDEPENDENT-MIXED-SIBLING"))
					pg, err := lineage.Read(host.LocalFS(), parent.Path)
					if err != nil || pg == nil || pg.Family != family || pg.Branch == branch || pg.Branch == sg.Branch {
						t.Fatal("parent and travelling child lost independent lineage", err)
					}
				}
			})
		}
	}
}

func (f *relayFleet) pushSSH(t *testing.T, from, to byte, source agent.Summary, target string, fork bool, operation string, expectRefused ...bool) agent.Summary {
	t.Helper()
	endpoint := newPeerSSH(t, f.ctx, f.bin, f.homes[to])
	m := f.homes[from]
	for _, kv := range m.env() {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(cfg, all.Registry(), filepath.Join(m.home, "state"), nil)
	defer a.Catalog.Close()
	a.PeerDial = endpoint.dial
	inv := a.Scan(f.ctx, app.ScanOptions{Hosts: []string{m.name}})
	defer inv.Close()
	e, err := inv.Find(app.ParseRef(string(source.Key.Agent) + "/" + string(source.Key.Session)))
	if err != nil {
		t.Fatal(err)
	}
	// Only the connection is supplied by the fixture. Production planning,
	// native application and both source/destination receipt writers execute.
	p, err := a.StartPush(f.ctx, inv, e, config.Host{Name: f.homes[to].name, Destination: endpoint.address, Allowed: true}, agent.ID(target), move.Options{TargetDir: f.homes[to].repo, Notify: true, Fork: fork, OperationID: operation})
	if len(expectRefused) > 0 && expectRefused[0] {
		if p != nil {
			p.Close()
		}
		if !errors.Is(err, peer.ErrRefused) || endpoint.executions.Load() != 1 {
			t.Fatal("disabled SSH receiver was not explicitly refused", err)
		}
		return agent.Summary{}
	}
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if len(p.Plan.Blockers) != 0 {
		t.Fatal("SSH movement blocked", p.Plan.Blockers)
	}
	if _, err = p.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if endpoint.executions.Load() != 1 {
		t.Fatal("movement did not execute through the authenticated SSH peer")
	}
	return f.find(t, to, target, string(p.Plan.Placement.Key.Session))
}

type peerSSH struct {
	address, keyFile, knownHosts string
	executions                   atomic.Int64
}

// newPeerSSH binds only loopback, uses fresh per-test client/server keys and
// accepts one fixed command. It never opens an OS login or reads user's SSH keys.
func newPeerSSH(t *testing.T, parent context.Context, bin string, machine machineHome) *peerSSH {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	_, serverPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(serverPrivate)
	if err != nil {
		t.Fatal(err)
	}
	server := &ssh.ServerConfig{PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if meta.User() != "fixture" || !bytes.Equal(key.Marshal(), clientKey.Marshal()) {
			return nil, errors.New("unknown fixture identity")
		}
		return nil, nil
	}}
	server.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s := &peerSSH{address: listener.Addr().String(), keyFile: filepath.Join(dir, "identity"), knownHosts: filepath.Join(dir, "known_hosts")}
	block, err := ssh.MarshalPrivateKey(private, "disposable peer fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(s.keyFile, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(s.address)
	if err = os.WriteFile(s.knownHosts, append([]byte("[127.0.0.1]:"+port+" "), ssh.MarshalAuthorizedKey(signer.PublicKey())...), 0600); err != nil {
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
						_ = next.Reject(ssh.Prohibited, "peer sessions only")
						continue
					}
					channel, reqs, err := next.Accept()
					if err != nil {
						return
					}
					for request := range reqs {
						var command struct{ Command string }
						if request.Type != "exec" || ssh.Unmarshal(request.Payload, &command) != nil || command.Command != "hopsesh peer --stdio" {
							_ = request.Reply(false, nil)
							continue
						}
						_ = request.Reply(true, nil)
						s.executions.Add(1)
						cmd := exec.CommandContext(ctx, bin, "peer", "--stdio")
						cmd.Env = machine.env()
						cmd.Stdin, cmd.Stdout, cmd.Stderr = channel, channel, channel.Stderr()
						code := uint32(0)
						if err := cmd.Run(); err != nil {
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
		cancel()
		mu.Lock()
		for conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return s
}

func (s *peerSSH) dial(ctx context.Context, _ config.Host) (*app.PeerConn, error) {
	_, port, _ := net.SplitHostPort(s.address)
	cmd := exec.CommandContext(ctx, "ssh", "-F", "none", "-o", "BatchMode=yes", "-o", "IdentityAgent=none", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", "GlobalKnownHostsFile=none", "-o", "UserKnownHostsFile="+s.knownHosts, "-i", s.keyFile, "-p", port, "fixture@127.0.0.1", "hopsesh peer --stdio")
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}
	return &app.PeerConn{In: in, Out: out, Close: func() { _ = in.Close(); _ = cmd.Wait() }}, nil
}
