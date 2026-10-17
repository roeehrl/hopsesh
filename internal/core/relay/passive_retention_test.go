package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

func TestExpiredPassiveRequestRefreshesWithoutRetiringNativeActions(t *testing.T) {
	a, b := identities(t)
	store := Store{Directory: privateTemp(t)}
	if err := store.Approve(t.Context(), Grant{Peer: a.Public, Kind: "device", Methods: []string{"observe"}}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p := Processor{Identity: b, Space: "passive-retention-space", Store: store, Handle: func(context.Context, Grant, string, string, json.RawMessage) (any, error) {
		calls++
		return map[string]int{"observation": calls}, nil
	}}
	now := time.Now()
	process := func(at time.Time) Envelope {
		t.Helper()
		e, err := Seal(a, b.Public, p.Space, "passive-retention-operation", []byte(`{"method":"observe","params":{"refresh":true}}`), at, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		response, err := p.Process(t.Context(), e, at)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	first := process(now)
	if first.Expires > now.Add(90*time.Second).Unix() {
		t.Fatal("passive ciphertext acquired a long native-action retention lease")
	}
	if replay := process(now.Add(time.Second)); replay.ID != first.ID || calls != 1 {
		t.Fatal("live passive delivery was not deduplicated")
	}
	if refreshed := process(now.Add(2 * time.Minute)); refreshed.ID == first.ID || calls != 2 {
		t.Fatal("expired passive cache could not obtain fresh evidence")
	}
	paths, err := filepath.Glob(filepath.Join(store.Directory, "operation-*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatal(paths, err)
	}
	body, err := localstate.ReadPrivateFile(paths[0], MaxWireBytes)
	var record Record
	if err != nil || json.Unmarshal(body, &record) != nil || !record.Transient || record.Outcome != nil || record.Expires != record.Response.Expires {
		t.Fatal("passive read kept a permanent encrypted outcome", err)
	}
}

func TestProductionRecordQuotaRetiresOnlyExpiredCompletedPassiveReads(t *testing.T) {
	for _, prefix := range []string{"operation-", "outgoing-"} {
		t.Run(prefix, func(t *testing.T) {
			store := Store{Directory: privateTemp(t)}
			now := time.Now()
			phase := "completed"
			if prefix == "outgoing-" {
				phase = "submitted"
			}
			expired := Record{Transient: true, Method: "observe", Authorization: "observe", Phase: phase, Expires: now.Add(-time.Minute).Unix()}
			body, _ := json.Marshal(expired)
			for i := 0; i < MaxOperationRecords-4; i++ {
				if err := os.WriteFile(filepath.Join(store.Directory, fmt.Sprintf("%s%064x.json", prefix, i)), body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			kept := []Record{
				{Transient: true, Method: "observe", Authorization: "observe", Phase: phase, Expires: now.Add(time.Hour).Unix()},
				{Transient: true, Method: "observe", Authorization: "observe", Phase: "started", Expires: now.Add(-time.Hour).Unix()},
				{Transient: true, Method: "apply", Authorization: "apply", Phase: phase, Expires: now.Add(-time.Hour).Unix()},
				{Method: "export", Authorization: "export", Phase: phase, Expires: now.Add(-time.Hour).Unix()},
			}
			for i, record := range kept {
				if err := writeJSON(filepath.Join(store.Directory, fmt.Sprintf("%skept-%d.json", prefix, i)), record); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.withLock(t.Context(), func() error { return store.recoveryRecordSlot(prefix, now) }); err != nil {
				t.Fatal("expired passive observations exhausted the production count ceiling", err)
			}
			paths, _ := filepath.Glob(filepath.Join(store.Directory, prefix+"*.json"))
			if len(paths) != len(kept) {
				t.Fatal("quota retired live, incomplete or native action bindings", len(paths))
			}
			for i := range kept {
				if _, err := os.Stat(filepath.Join(store.Directory, fmt.Sprintf("%skept-%d.json", prefix, i))); err != nil {
					t.Fatal("required binding was retired", err)
				}
			}
		})
	}
}

func TestRecordCountBeyondBoundFailsBeforeReadingOrRetiringBindings(t *testing.T) {
	store := Store{Directory: privateTemp(t)}
	for i := 0; i <= MaxOperationRecords; i++ {
		if err := os.WriteFile(filepath.Join(store.Directory, fmt.Sprintf("operation-%064x.json", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.withLock(t.Context(), func() error { return store.recoveryRecordSlot("operation-", time.Now()) }); err == nil {
		t.Fatal("over-bound inventory was admitted")
	}
	paths, _ := filepath.Glob(filepath.Join(store.Directory, "operation-*.json"))
	if len(paths) != MaxOperationRecords+1 {
		t.Fatal("over-bound inventory was silently discarded")
	}
}
