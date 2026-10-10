package e2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/profiles"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Change the public account observation after a real relay plan is accepted.
// This qualifies persisted binding enforcement, not an actual vendor login or
// cross-OS-user boundary. Neither a fork nor owner restart can revive that plan.
func TestRelayAccountRebindingSQLiteR2(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_RELAY_PLATFORM") != "1" {
		t.Skip("actual SQLite/R2 account-rebinding qualification")
	}
	bin := buildHopsesh(t)
	for _, network := range []string{"unrestricted", "websocket-blocked"} {
		for _, side := range []byte{'A', 'B'} {
			for _, fork := range []bool{false, true} {
				for _, restart := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%c/fork-%t/restart-%t", network, side, fork, restart), func(t *testing.T) {
						ctx, _, origin, cert, client := startSQLiteRelayFixture(t, 2*time.Minute)
						f := newRelayFleet(t, ctx, bin, origin, cert, client)
						if network != "unrestricted" {
							f.applyNetworkPolicy(t, origin, client, network)
						}
						original := f.seed(t, "claude")
						before := movementBytes(t, original.Path)
						beforeLineage, err := lineage.Read(host.LocalFS(), original.Path)
						if err != nil {
							t.Fatal(err)
						}
						for _, kv := range f.homes['A'].env() {
							key, value, _ := strings.Cut(kv, "=")
							t.Setenv(key, value)
						}
						cfg, err := config.Load()
						if err != nil {
							t.Fatal(err)
						}
						a := app.New(cfg, all.Registry(), config.StateDir(), nil)
						defer a.Catalog.Close()
						inv := a.Scan(ctx, app.ScanOptions{Hosts: []string{f.homes['A'].name}, NoCache: true})
						var source app.Entry
						for _, e := range inv.Entries {
							if e.Machine == f.homes['A'].name && e.Session.Key.Agent == "claude" && e.Session.Key.Session == original.Key.Session {
								source = e
							}
						}
						if source.Session.Key.Session == "" {
							t.Fatal("disposable source missing")
						}
						var destination config.Host
						for _, h := range cfg.Hosts {
							if h.Name == f.homes['B'].name {
								destination = h
							}
						}
						push, err := a.StartPush(ctx, inv, source, destination, "codex", move.Options{
							TargetDir: f.homes['B'].repo, Fork: fork, OperationID: "account-rebinding-operation-12345678",
						})
						if err != nil {
							t.Fatal(err)
						}
						defer push.Close()
						if len(push.Plan.Blockers) != 0 {
							t.Fatal("fixture plan was blocked before rebinding", push.Plan.Blockers)
						}
						endpoint := push.Plan.Source
						if side == 'B' {
							endpoint = push.Plan.Target
						}
						if endpoint.Profile == "" {
							t.Fatal("accepted plan lacks a registered account")
						}
						store := profiles.Store{Dir: filepath.Join(f.homes[side].home, "state")}
						changed, err := store.Observe(endpoint.Profile, &agent.Account{
							Provider: "fixture", Observation: "changed-after-relay-plan", LoggedIn: true, Confidence: "limited",
						}, "")
						if err != nil || changed.Binding == endpoint.Binding {
							t.Fatal("public observation did not rotate the binding", err)
						}
						if restart {
							f.restartRuntime(t, side)
						}
						if _, err := push.Commit(ctx); err == nil || (!strings.Contains(err.Error(), "account") && !strings.Contains(err.Error(), "binding")) {
							t.Fatal("stale accepted plan was not refused for its changed account", err)
						}
						if !bytes.Equal(before, movementBytes(t, original.Path)) {
							t.Fatal("refused transfer changed source history")
						}
						afterLineage, err := lineage.Read(host.LocalFS(), original.Path)
						if err != nil || !reflect.DeepEqual(beforeLineage, afterLineage) {
							t.Fatal("refused transfer changed source lineage", err)
						}
						files, err := filepath.Glob(filepath.Join(f.homes['B'].home, ".codex", "sessions", "*", "*", "*", "*.jsonl"))
						if err != nil || len(files) != 0 {
							t.Fatal("refused transfer wrote a destination conversation", files, err)
						}
					})
				}
			}
		}
	}
}
