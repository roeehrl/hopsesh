package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func relayAckFixture(t *testing.T) (*App, relay.Grant, *relayExportRecord, relayPullAck) {
	t.Helper()
	root := t.TempDir()
	native := filepath.Join(root, "original.jsonl")
	if err := os.WriteFile(native, []byte("original native conversation\n"), 0600); err != nil {
		t.Fatal(err)
	}
	id, err := relay.GenerateIdentity("target-endpoint")
	if err != nil {
		t.Fatal(err)
	}
	key := agent.SessionKey{Agent: "claude", Session: "original-session"}
	r := &relayExportRecord{Peer: id.Public.ID, Endpoint: id.Public.Endpoint, Operation: "source-ack-operation-1234", Package: peer.Package{Agent: "claude", Facts: host.Facts{Endpoint: "source-endpoint"}, Session: agent.Summary{Key: key, CWD: root, Path: native}}}
	g := lineage.New("relay-pull-family")
	from := g.Upsert(lineage.Replica{Endpoint: r.Package.Facts.Endpoint, Location: LocalName(), Key: key})
	to := g.Upsert(lineage.Replica{Endpoint: id.Public.Endpoint, Location: "target", Key: agent.SessionKey{Agent: "codex", Session: "converted-session"}})
	segment := ir.Segment{Nodes: []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Text: "Continue the work"}}, Cursor: ir.Cursor{Head: "source-head", Offset: 28}}
	source, err := g.Observe(from, &segment)
	if err != nil {
		t.Fatal(err)
	}
	destination := g.Deliver(to, ir.Cursor{Head: "target-head", Offset: 40}, source.Projection, source.Heads, nil)
	if err = g.AppendHop(lineage.Hop{ID: r.Operation, From: from, To: to, Source: source.ID, Target: destination.ID, Kind: lineage.HopContinue}); err != nil {
		t.Fatal(err)
	}
	grant := relay.Grant{Peer: id.Public, Endpoint: id.Public.Endpoint, Kind: "device", Roots: []string{root}, Methods: []string{"export", "ack", "undo"}}
	a := New(config.Defaults(), nil, t.TempDir(), nil)
	path := a.relayTransferPath("export/"+id.Public.ID, r.Operation)
	if err = saveRelayRecord(path, r); err != nil {
		t.Fatal(err)
	}
	return a, grant, r, relayPullAck{Receipt: g.Encode()}
}

func TestRelayPullAcknowledgmentRestartRetryAndUndoDoNotWriteNativeHistory(t *testing.T) {
	a, grant, r, ack := relayAckFixture(t)
	before, err := os.ReadFile(r.Package.Session.Path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(ack)
	first, err := a.relayExport(t.Context(), grant, r.Operation, "ack", body)
	if err != nil {
		t.Fatal(err)
	}
	a = New(config.Defaults(), nil, a.StateDir, nil)
	second, err := a.relayExport(t.Context(), grant, r.Operation, "ack", body)
	if err != nil {
		t.Fatal(err)
	}
	x, _ := json.Marshal(first)
	y, _ := json.Marshal(second)
	if !bytes.Equal(x, y) {
		t.Fatal("ack retry created another journal")
	}
	js, err := journal.List(a.StateDir)
	if err != nil || len(js) != 1 {
		t.Fatal("source acknowledgment duplicated journal", len(js), err)
	}
	after, err := os.ReadFile(r.Package.Session.Path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("ack rewrote native conversation", err)
	}
	js[0].Undone = true
	if err = js[0].Save(); err != nil {
		t.Fatal(err)
	}
	if _, err = a.relayExport(t.Context(), grant, r.Operation, "ack", body); err == nil {
		t.Fatal("ack revived an undone source")
	}
}

func TestRelayPullAcknowledgmentRejectsOtherEndpointsSessionsAndPendingPaths(t *testing.T) {
	_, grant, record, ack := relayAckFixture(t)
	if err := validateRelayAck(record, grant, ack); err != nil {
		t.Fatal(err)
	}
	other := grant
	other.Endpoint = "unapproved-endpoint"
	if err := validateRelayAck(record, other, ack); err == nil {
		t.Fatal("changed destination endpoint accepted")
	}
	changed := *record
	changed.Package.Session.Key.Session = "another-session"
	if err := validateRelayAck(&changed, grant, ack); err == nil {
		t.Fatal("changed native session accepted")
	}
	ack.Mark = &agent.Mark{Kind: agent.MarkMoved, Location: "another-machine"}
	if err := validateRelayAck(record, grant, ack); err == nil {
		t.Fatal("mark targets another machine")
	}
	ack.Mark = nil
	ack.Owed = &lineage.Pending{Operation: record.Operation, Key: record.Package.Session.Key, Path: filepath.Join(t.TempDir(), "unrelated.jsonl")}
	if err := validateRelayAck(record, grant, ack); err == nil {
		t.Fatal("pending mark escaped the exported native path")
	}
}

func TestRelayObservationRedactsCachedProfilesAndHonorsApprovedRoots(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	p := &agent.RuntimeProfile{ID: "stable-id", Binding: "stable-binding", Name: "personal label", Tags: []string{"private"}, Account: &agent.Account{Email: "person@example.test"}}
	obs := Observation{WatchRoots: []string{other}, Problems: []string{"private path error"}, Agents: []AgentState{{Install: agent.Install{Profile: p}}}, Entries: []Entry{{Profile: p, Session: agent.Summary{CWD: root}, Live: agent.LiveInfo{PID: 123}}, {Profile: p, Session: agent.Summary{CWD: other}}}}
	redacted := relayObservation(obs, relay.Grant{Roots: []string{root}})
	if len(redacted.Entries) != 1 || len(redacted.WatchRoots) != 0 || len(redacted.Problems) != 0 {
		t.Fatal("unapproved metadata shared")
	}
	for _, profile := range []*agent.RuntimeProfile{redacted.Agents[0].Install.Profile, redacted.Entries[0].Profile} {
		if profile.Account != nil || profile.Name != "" || len(profile.Tags) != 0 || profile.ID != "stable-id" || profile.Binding != "stable-binding" {
			t.Fatal("profile redaction damaged binding or leaked private labels", profile)
		}
	}
	if redacted.Entries[0].Live.PID != 0 || p.Name != "personal label" || p.Account == nil {
		t.Fatal("redaction changed source state or exposed PID")
	}
}
