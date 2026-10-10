package app

import (
	"errors"
	"testing"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestRelaySenderFreezesRequestAcrossNativeUpdatesAndRejectsReboundIntent(t *testing.T) {
	p := &Push{a: &App{StateDir: t.TempDir()}, to: config.Host{RelayID: "peer-123456789012"}, operation: "operation-123456"}
	request := peer.PlanRequest{Package: peer.Package{Agent: "claude", Session: agent.Summary{Key: agent.SessionKey{Agent: "claude", Session: "native123"}, Path: "/native/native123.jsonl"}, Facts: host.Facts{Endpoint: "native-endpoint"}, Files: []host.SnapshotFile{{Path: "/native/native123.jsonl", Data: []byte("original")}}}, Target: "codex", Options: move.Options{OperationID: p.operation}}
	if _, err := p.frozenRequest(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	request.Package.Files[0].Data = []byte("native updated after source acknowledgment")
	got, err := p.frozenRequest(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Package.Files[0].Data) != "original" {
		t.Fatal("reconstructed an unstable retry")
	}
	for _, change := range []func(*peer.PlanRequest){
		func(r *peer.PlanRequest) { r.Target = "claude" },
		func(r *peer.PlanRequest) { r.Package.Session.Key.Session = "another" },
		func(r *peer.PlanRequest) { r.Options.Fork = true },
		func(r *peer.PlanRequest) { r.Package.Facts.Endpoint = "another endpoint" },
	} {
		r := request
		change(&r)
		if _, err = p.frozenRequest(t.Context(), r); err == nil {
			t.Fatal("changed intent accepted")
		}
	}
}

func TestRelaySenderNeverRepeatsUncertainOrUndoneSourceWrites(t *testing.T) {
	p := &Push{a: &App{StateDir: t.TempDir()}, to: config.Host{RelayID: "peer-123456789012"}, operation: "operation-123456"}
	request := peer.PlanRequest{}
	if _, err := p.frozenRequest(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	r, err := readRelayOutgoing(p.outgoingPath())
	if err != nil {
		t.Fatal(err)
	}
	r.Phase, r.Journal = "source-started", "durable-journal"
	if err = saveRelayOutgoing(p.outgoingPath(), r); err != nil {
		t.Fatal(err)
	}
	if _, lock, err := p.beginSourceCommit(t.Context()); !errors.Is(err, relay.ErrUncertain) || lock != nil {
		t.Fatal("uncertain native write repeated", err)
	}
	j, err := journal.New(p.a.StateDir, journal.KindPush, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	j.TransferID = p.operation
	j.Undone = true
	if err = j.Save(); err != nil {
		t.Fatal(err)
	}
	r.Phase, r.Journal, r.Result = "completed", j.ID, &PushResult{Journal: j.ID}
	if err = saveRelayOutgoing(p.outgoingPath(), r); err != nil {
		t.Fatal(err)
	}
	if _, lock, err := p.beginSourceCommit(t.Context()); err == nil || lock != nil {
		t.Fatal("undone operation reported as applied")
	}
}
