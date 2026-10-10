package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/peer"
)

func TestPushRevalidatesBrowsingSourceBeforeConnecting(t *testing.T) {
	for _, scenario := range []string{"cached", "hostless", "discovering", "deleted"} {
		t.Run(scenario, func(t *testing.T) {
			a := catalogApp(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			inv := a.Scan(ctx, ScanOptions{NoCache: true, SkipGit: true})
			defer inv.Close()
			var e Entry
			for _, candidate := range inv.Entries {
				if candidate.Agent == "claude" {
					e = candidate
					break
				}
			}
			if e.Session.Path == "" {
				t.Fatal("missing fixture session")
			}
			original := e.Session
			if scenario == "deleted" {
				if err := os.Remove(filepath.FromSlash(original.Path)); err != nil {
					t.Fatal(err)
				}
			}
			e.Session.Title = "stale browsing title"
			e.Session.Path = filepath.Join(t.TempDir(), "stale-path.jsonl")
			switch scenario {
			case "cached", "deleted":
				e.Cached = true
			case "hostless":
				inv.Local().host = nil
			case "discovering":
				inv.Discovering = true
			}
			requests := make(chan peer.PlanRequest, 1)
			dials := 0
			a.PeerDial = func(ctx context.Context, _ config.Host) (*PeerConn, error) {
				dials++
				cr, sw := io.Pipe()
				sr, cw := io.Pipe()
				done := make(chan struct{})
				go func() {
					defer close(done)
					defer sw.Close()
					_ = peer.Serve(ctx, sr, sw, func(_ context.Context, method string, raw json.RawMessage) (any, error) {
						switch method {
						case peer.MethodHello:
							return peer.HelloReply{Protocol: peer.Protocol, Receive: true, Agents: []peer.AgentInfo{{ID: "claude", Present: true}}}, nil
						case peer.MethodPlan:
							var req peer.PlanRequest
							if err := json.Unmarshal(raw, &req); err != nil {
								return nil, err
							}
							requests <- req
							return peer.PlanReply{Plan: &move.Plan{}}, nil
						default:
							return nil, errors.New("unexpected peer method")
						}
					})
				}()
				return &PeerConn{Out: cr, In: cw, Close: func() {
					_ = cw.Close()
					_ = sr.Close()
					_ = cr.Close()
					<-done
				}}, nil
			}
			p, err := a.StartPush(ctx, inv, e, config.Host{Name: "destination"}, "claude", move.Options{})
			if p != nil {
				defer p.Close()
			}
			if scenario == "deleted" {
				if err == nil || !strings.Contains(err.Error(), "no longer available") || dials != 0 {
					t.Fatal("deleted source was not rejected before connecting", err, dials)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case req := <-requests:
				if req.Package.Session.Key != original.Key || req.Package.Session.Path != original.Path || req.Package.Session.Title != original.Title || len(req.Package.Files) == 0 || p.source.Machine == nil {
					t.Fatal("push did not bind the freshly validated native source", req.Package.Session)
				}
			case <-ctx.Done():
				t.Fatal("no package reached the peer")
			}
			p.Close()
			p.Close() // owned refresh and peer close exactly once
		})
	}
}

// hopsesh peer starts directly in the form the Windows machine's ssh shell takes.
func TestPeerCommandWindows(t *testing.T) {
	exe := `C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe`
	for _, tc := range []struct{ shell, want string }{
		{"", `"C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe" peer --stdio`},
		{`C:\Windows\System32\cmd.exe`, `"C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe" peer --stdio`},
		{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, `& 'C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe' peer --stdio`},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, `& 'C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe' peer --stdio`},
		{`C:\Program Files\Git\bin\bash.exe`, `'C:/Users/Sam Doe/AppData/Local/Programs/hopsesh/hopsesh.exe' peer --stdio`},
	} {
		if got := peerCommandWindows(exe, tc.shell); got != tc.want {
			t.Errorf("shell %q:\n got %s\nwant %s", tc.shell, got, tc.want)
		}
	}
}

// A push to a machine with hopsesh 0.3 (peer protocol 1) stops at hello and names the
// machine to update, instead of sending it a package it would read without its lineage.
func TestPushToOlderPeer(t *testing.T) {
	a := &App{PeerDial: func(context.Context, config.Host) (*PeerConn, error) {
		cr, sw := io.Pipe()
		sr, cw := io.Pipe()
		go func() {
			defer sw.Close()
			line, _ := bufio.NewReader(sr).ReadString('\n')
			if !strings.Contains(line, `"method":"hello"`) {
				return
			}
			// hopsesh 0.3's refusal, byte for byte.
			_, _ = io.WriteString(sw, `{"id":1,"error":{"code":"protocol","message":"hopsesh here speaks protocol 1, the other machine 2: update hopsesh so both are the same version"}}`+"\n")
		}()
		return &PeerConn{Out: cr, In: cw, Close: func() { cw.Close(); sr.Close() }}, nil
	}}
	_, _, _, err := a.dialPeer(context.Background(), config.Host{Name: "old-box"})
	if !errors.Is(err, peer.ErrProtocol) {
		t.Fatalf("hello to an older peer: %v", err)
	}
	if want := "old-box runs an older hopsesh (peer protocol 1)"; !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "update hopsesh on old-box to "+peer.FirstVersion+" or later") {
		t.Fatalf("%q must name old-box as the one to update", err)
	}
}

func TestReceiveOptionsRetainsTransferIdentityAndSelectedReplica(t *testing.T) {
	a := &App{Cfg: config.Defaults()}
	got := a.receiveOptions(move.Options{OperationID: "same-transfer", TargetSession: "claude/selected", CarryRules: true, RuleFiles: []string{"/source/project/CLAUDE.md"}})
	if got.OperationID != "same-transfer" || got.TargetSession != "claude/selected" {
		t.Fatalf("peer lost identity or destination: %+v", got)
	}
	if !got.CarryRules || len(got.RuleFiles) != 1 || got.RuleFiles[0] != "/source/project/CLAUDE.md" {
		t.Fatalf("peer lost the selected instruction subset: %+v", got)
	}
	for _, o := range []move.Options{{NewReplica: true}, {Bounded: true}} {
		got = a.receiveOptions(o)
		if got.NewReplica != o.NewReplica || got.Bounded != o.Bounded {
			t.Fatalf("peer lost explicit new-session intent: %+v", got)
		}
	}
}
