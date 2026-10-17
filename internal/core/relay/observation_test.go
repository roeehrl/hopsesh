package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/observe"
)

func TestRejectedObservationAcknowledgesWithoutNativeQuarantine(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	acked := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/messages":
			_ = json.NewEncoder(w).Encode(Batch{Cursor: 1, Messages: []Delivery{{Sequence: 1, Envelope: Envelope{Kind: "observation"}}}})
		case "/v1/ack":
			w.WriteHeader(http.StatusNoContent)
			select {
			case acked <- struct{}{}:
			default:
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	dir := privateTemp(t)
	listener := Listener{Transport: Transport{Base: server.URL, Space: "observation-space-123", Token: "fixture", AllowLoopback: true}, Processor: Processor{Store: Store{Directory: dir}}, OnObservation: func(context.Context, Envelope) error { return reject(errors.New("revoked source")) }}
	done := make(chan struct{})
	go func() { defer close(done); _ = listener.Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-acked:
	case <-time.After(time.Second):
		t.Fatal("rejected replaceable inventory blocked mailbox drain")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatal("passive rejection consumed native recovery space", files, err)
	}
}

func TestObservationIsAuthenticatedReplaceableEvidenceNeverAnAction(t *testing.T) {
	a, b := identities(t)
	store := Store{Directory: privateTemp(t)}
	grant := Grant{Peer: b.Public, Kind: "device", SendMethods: []string{"observe"}}
	if err := store.Approve(t.Context(), grant); err != nil {
		t.Fatal(err)
	}
	called, actions := 0, 0
	s := &Service{Processor: Processor{Identity: a, Space: "observation-space-123", Store: store, Handle: func(context.Context, Grant, string, string, json.RawMessage) (any, error) { actions++; return nil, nil }}, OnObservation: func(_ context.Context, _ Grant, _ observe.Snapshot) error { called++; return nil }}
	now := time.Now().UTC()
	snapshot := observe.Snapshot{Epoch: "source-incarnation-123", Sequence: 3, AttemptedAt: now, ObservedAt: now, ExpiresAt: now.Add(ObservationLease), Data: json.RawMessage(`{"inventoryComplete":true}`)}
	seal := func(snapshot observe.Snapshot) Envelope {
		t.Helper()
		body, _ := json.Marshal(snapshot)
		e, err := sealKind(b, a.Public, s.Processor.Space, "observation-operation-123", "observation", body, time.Now(), ObservationLease)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	e := seal(snapshot)
	for range 3 {
		if err := s.receiveObservation(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	if called != 1 || actions != 0 {
		t.Fatal("duplicate observation escaped passive dispatch", called, actions)
	}
	if _, err := s.Processor.Process(t.Context(), e, time.Now()); err == nil {
		t.Fatal("observation dispatched as native request")
	}
	if err := s.receive(t.Context(), e); err == nil {
		t.Fatal("observation dispatched as operation reply")
	}
	snapshot.Sequence--
	if err := s.receiveObservation(t.Context(), seal(snapshot)); err != nil {
		t.Fatal(err)
	}
	snapshot.Epoch = "prior-incarnation-123"
	snapshot.AttemptedAt = now.Add(-time.Second)
	if err := s.receiveObservation(t.Context(), seal(snapshot)); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatal("old source incarnation renewed evidence")
	}
	snapshot.Epoch = "newer-incarnation-123"
	snapshot.AttemptedAt = now.Add(time.Millisecond)
	if err := s.receiveObservation(t.Context(), seal(snapshot)); err != nil {
		t.Fatal(err)
	}
	if called != 2 {
		t.Fatal("new incarnation was lost")
	}
	e.Signature[0] ^= 1
	if err := s.receiveObservation(t.Context(), e); err == nil {
		t.Fatal("forged source accepted")
	}
	grant.Revoked = true
	if err := store.Approve(t.Context(), grant); err != nil {
		t.Fatal(err)
	}
	if err := s.receiveObservation(t.Context(), seal(snapshot)); err == nil {
		t.Fatal("revoked source accepted")
	}
	files, err := os.ReadDir(store.Directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), "op-") || strings.HasPrefix(f.Name(), "reply-") || strings.HasPrefix(f.Name(), "outgoing-") {
			t.Fatal("passive update created a recovery record", f.Name())
		}
	}
	if s.observations[b.Public.ID].Data != nil {
		t.Fatal("replay index retains private inventory")
	}
}

func TestObservationEnforcesCurrentScopeRoleDirectionAndLifetime(t *testing.T) {
	for _, name := range []string{"valid", "cloud", "wrong direction", "revoked", "changed roots", "expired", "future evidence", "long lease", "oversized"} {
		t.Run(name, func(t *testing.T) {
			a, b := identities(t)
			store := Store{Directory: privateTemp(t)}
			grant := Grant{Peer: b.Public, Kind: "device", Roots: []string{"/approved"}, Methods: []string{"observe"}}
			expected := grant
			now := time.Now().UTC()
			snapshot := observe.Snapshot{Epoch: "observation-epoch-123", Sequence: 1, AttemptedAt: now, ObservedAt: now, ExpiresAt: now.Add(ObservationLease), Data: json.RawMessage(`{}`)}
			switch name {
			case "cloud":
				grant.Kind = "cloud-session"
				grant.Expires = now.Add(time.Hour).Unix()
				expected = grant
			case "wrong direction":
				grant.Methods = nil
				grant.SendMethods = []string{"observe"}
				expected = grant
			case "revoked":
				grant.Revoked = true
				expected = grant
			case "changed roots":
				grant.Roots = []string{"/different"}
			case "expired":
				grant.Expires = now.Add(-time.Minute).Unix()
				expected = grant
			case "future evidence":
				snapshot.ObservedAt = now.Add(time.Hour)
			case "long lease":
				snapshot.ExpiresAt = now.Add(time.Hour)
			case "oversized":
				snapshot.Data = json.RawMessage(`"` + strings.Repeat("x", MaxObservationBytes) + `"`)
			}
			if err := store.Approve(t.Context(), grant); err != nil {
				t.Fatal(err)
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(http.StatusCreated) }))
			defer server.Close()
			s := &Service{Processor: Processor{Identity: a, Space: "observation-space-123", Store: store}, Transport: Transport{Base: server.URL, Space: "observation-space-123", Token: "fixture", AllowLoopback: true}}
			err := s.PublishObservation(t.Context(), expected, snapshot)
			if name == "valid" {
				if err != nil || requests != 1 {
					t.Fatal(err, requests)
				}
			} else if err == nil || requests != 0 {
				t.Fatal("invalid source publication reached network", err, requests)
			}
		})
	}
}
