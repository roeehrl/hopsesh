package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

func TestRecoveryPressureRetiresExpiredCiphertextWithoutForgettingNativeAction(t *testing.T) {
	a, b := identities(t)
	store := Store{Directory: privateTemp(t)}
	grant := Grant{Peer: a.Public, Kind: "device", Methods: []string{"apply"}}
	if err := store.Approve(t.Context(), grant); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p := Processor{Identity: b, Space: "retention-test-space", Store: store, Handle: func(context.Context, Grant, string, string, json.RawMessage) (any, error) {
		calls++
		return map[string]string{"journal": "one-native-journal"}, nil
	}}
	now := time.Now()
	body := []byte(`{"method":"apply","params":{}}`)
	op := "retention-operation-1234"
	env, err := Seal(a, b.Public, p.Space, op, body, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Process(t.Context(), env, now); err != nil {
		t.Fatal(err)
	}
	paths, _ := filepath.Glob(filepath.Join(store.Directory, "operation-*.json"))
	before, err := localstate.ReadPrivateFile(paths[0], MaxWireBytes)
	if err != nil {
		t.Fatal(err)
	}
	var original Record
	if err = json.Unmarshal(before, &original); err != nil {
		t.Fatal(err)
	}
	// The wire lease expires first. Retained owner ciphertext must still renew.
	later := now.Add(2 * time.Minute)
	if err = store.withLock(t.Context(), func() error { return store.recoverySpaceLimit("", 0, later, int64(len(before)-1)) }); err != nil {
		t.Fatal(err)
	}
	env, err = Seal(a, b.Public, p.Space, op, body, later, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Process(t.Context(), env, later); err != nil || calls != 1 {
		t.Fatal("wire retirement lost retained result", err, calls)
	}
	current, _ := localstate.ReadPrivateFile(paths[0], MaxWireBytes)
	// Past the owner's retention lease, strip ciphertext but keep the tombstone.
	expired := now.Add(MaxLifetime + time.Minute)
	if err = store.withLock(t.Context(), func() error { return store.recoverySpaceLimit("", 0, expired, int64(len(current)-1)) }); err != nil {
		t.Fatal(err)
	}
	after, err := localstate.ReadPrivateFile(paths[0], MaxWireBytes)
	if err != nil {
		t.Fatal(err)
	}
	var tombstone Record
	if err = json.Unmarshal(after, &tombstone); err != nil {
		t.Fatal(err)
	}
	if tombstone.Phase != "completed" || !bytes.Equal(original.Request, tombstone.Request) || !bytes.Equal(original.Scope, tombstone.Scope) || tombstone.Peer != original.Peer || tombstone.Operation != original.Operation || tombstone.Outcome != nil || len(tombstone.Response.Ciphertext) != 0 {
		t.Fatal("retirement changed the operation binding or kept expired payload")
	}
	p = Processor{Identity: b, Space: p.Space, Store: Store{Directory: store.Directory}, Handle: p.Handle, Recover: p.Handle}
	env, err = Seal(a, b.Public, p.Space, op, body, expired, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Process(t.Context(), env, expired); err == nil || calls != 1 {
		t.Fatal("retired action repeated after owner restart", err, calls)
	}
}

func TestRecoveryQuotaPreservesLiveResultsUncertainIntentsAndRequestBindings(t *testing.T) {
	now := time.Now()
	store := Store{Directory: privateTemp(t)}
	paths := []string{}
	for _, prefix := range []string{"operation-live-", "operation-started-", "outgoing-", "reply-"} {
		path := filepath.Join(store.Directory, prefix+"fixture.json")
		var value any = Record{Phase: "started", Operation: "retention-operation-1234", Request: []byte("digest")}
		if prefix == "operation-live-" {
			value = Record{Phase: "completed", Response: Envelope{Expires: now.Add(time.Hour).Unix(), Ciphertext: bytes.Repeat([]byte{1}, 4096)}, Outcome: &retainedOutcome{Expires: now.Add(MaxLifetime).Unix(), Ciphertext: []byte{2}}}
		}
		if prefix == "reply-" {
			value = Envelope{Expires: now.Add(time.Hour).Unix(), Ciphertext: []byte{3}}
		}
		if err := writeJSON(path, value); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	before := map[string][]byte{}
	for _, path := range paths {
		before[path], _ = os.ReadFile(path)
	}
	used, err := recoveryBytes(paths, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.withLock(t.Context(), func() error { return store.recoverySpaceLimit("", 1, now, used) }); !errors.Is(err, ErrRecoveryQuota) {
		t.Fatal("live cache evicted or quota ignored", err)
	}
	for _, path := range paths {
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before[path], after) {
			t.Fatal("quota changed a live result or uncertain intent")
		}
	}
	// Only the expired reply can be deleted; its outgoing binding remains.
	if err = store.withLock(t.Context(), func() error { return store.recoverySpaceLimit("", 1, now.Add(2*time.Hour), used) }); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(paths[3]); !os.IsNotExist(err) {
		t.Fatal("expired wire reply retained", err)
	}
	for _, path := range paths[1:3] {
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before[path], after) {
			t.Fatal("uncertain or outgoing binding removed")
		}
	}
}

func TestActualAggregateRecoveryQuotaRefusesBeforeNativeIntent(t *testing.T) {
	a, b := identities(t)
	store := Store{Directory: privateTemp(t)}
	if err := store.Approve(t.Context(), Grant{Peer: a.Public, Kind: "device", Methods: []string{"apply"}}); err != nil {
		t.Fatal(err)
	}
	// Valid bounded records with padding exercise the production aggregate limit
	// without performing hundreds of irreversible native operations.
	seed := bytes.Repeat([]byte{' '}, MaxWireBytes)
	copy(seed, []byte(`{"phase":"started","operation":"uncertain-operation-1234"}`))
	for i := 0; int64(i+1)*MaxWireBytes <= MaxRecoveryBytes; i++ {
		if err := os.WriteFile(filepath.Join(store.Directory, fmt.Sprintf("operation-seed-%02d.json", i)), seed, 0600); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	p := Processor{Identity: b, Space: "quota-test-space-1234", Store: store, Handle: func(context.Context, Grant, string, string, json.RawMessage) (any, error) { calls++; return nil, nil }}
	now := time.Now()
	env, err := Seal(a, b.Public, p.Space, "quota-new-operation-1234", []byte(`{"method":"apply"}`), now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Process(t.Context(), env, now); !errors.Is(err, ErrRecoveryQuota) || calls != 0 {
		t.Fatal("quota reached after native action", err, calls)
	}
	paths, _ := filepath.Glob(filepath.Join(store.Directory, "operation-*.json"))
	if len(paths) != int(MaxRecoveryBytes/MaxWireBytes) {
		t.Fatal("rejected action allocated durable intent")
	}
}

func TestRecoveryAccountingRefusesLinkedAndOversizedEntries(t *testing.T) {
	for _, mode := range []string{"linked", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			store := Store{Directory: privateTemp(t)}
			path := filepath.Join(store.Directory, "operation-fixture.json")
			if mode == "linked" {
				target := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(target, []byte("outside"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skip("symlink privilege unavailable")
				}
			} else {
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				err = f.Truncate(MaxWireBytes + 1)
				_ = f.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := store.withLock(t.Context(), func() error { return store.recoverySpace("", 1, time.Now()) }); err == nil {
				t.Fatal("unsafe entry accepted")
			}
		})
	}
}
