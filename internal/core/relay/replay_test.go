package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestExpiredReplyRenewsWithoutRepeatingNativeAction(t *testing.T) {
	a, b := identities(t)
	store := Store{Directory: privateTemp(t)}
	if err := store.Approve(t.Context(), Grant{Peer: a.Public, Kind: "device", Methods: []string{"apply"}}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p := Processor{Identity: b, Space: "reply-renew-space-1234", Store: store, Handle: func(context.Context, Grant, string, string, json.RawMessage) (any, error) {
		calls++
		return map[string]string{"journal": "one-native-journal"}, nil
	}}
	now := time.Now()
	body := []byte(`{"method":"apply","params":{"session":"original"}}`)
	env, err := Seal(a, b.Public, p.Space, "reply-renew-operation-1234", body, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.Process(t.Context(), env, now)
	if err != nil {
		t.Fatal(err)
	}
	later := now.Add(2 * time.Minute)
	env, err = Seal(a, b.Public, p.Space, env.Operation, body, later, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p = Processor{Identity: b, Space: p.Space, Store: store, Handle: p.Handle}
	second, err := p.Process(t.Context(), env, later)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Open(a, b.Public, second, p.Space, later)
	if err != nil || !bytes.Contains(plain, []byte("one-native-journal")) || calls != 1 || first.ID == second.ID {
		t.Fatal("renewal duplicated or lost native outcome", calls, err)
	}
}

func TestCachedRelayOutcomeCannotEscapeChangedRootScope(t *testing.T) {
	a, b := identities(t)
	store := Store{Directory: privateTemp(t)}
	g := Grant{Peer: a.Public, Kind: "device", Endpoint: "approved-endpoint", Roots: []string{filepath.Join(t.TempDir(), "approved")}, Methods: []string{"export"}}
	if err := store.Approve(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p := Processor{Identity: b, Space: "scope-replay-space-1234", Store: store, Handle: func(context.Context, Grant, string, string, json.RawMessage) (any, error) {
		calls++
		return "approved-only-data", nil
	}}
	now := time.Now()
	body := []byte(`{"method":"export","params":{"session":"original"}}`)
	env, err := Seal(a, b.Public, p.Space, "scope-replay-operation-1234", body, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Process(t.Context(), env, now); err != nil {
		t.Fatal(err)
	}
	g.Roots = []string{filepath.Join(t.TempDir(), "different")}
	if err = store.Approve(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Process(t.Context(), env, now); err == nil {
		t.Fatal("cached data escaped changed authorization scope")
	}
	if calls != 1 {
		t.Fatal("scope change repeated native action")
	}
}

func TestSmallAndChunkedFormsShareOneCanonicalOperation(t *testing.T) {
	a, b := identities(t)
	store := Store{Directory: privateTemp(t)}
	if err := store.Approve(t.Context(), Grant{Peer: a.Public, Kind: "device", Methods: []string{"apply"}}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p := Processor{Identity: b, Space: "canonical-space-1234", Store: store, Handle: func(context.Context, Grant, string, string, json.RawMessage) (any, error) {
		calls++
		return "one-result", nil
	}}
	now := time.Now()
	params := json.RawMessage(`{"session":"original"}`)
	op := "canonical-operation-1234"
	process := func(req Request, operation string) Reply {
		body, _ := json.Marshal(req)
		env, err := Seal(a, b.Public, p.Space, operation, body, now, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		reply, err := p.Process(t.Context(), env, now)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := Open(a, b.Public, reply, p.Space, now)
		if err != nil {
			t.Fatal(err)
		}
		var out Reply
		if err = json.Unmarshal(plain, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	process(Request{Method: "apply", Params: params}, op)
	d := descriptor(op, "apply", "request", params, now.Add(time.Hour).Unix())
	part, _ := json.Marshal(blobRequest{Descriptor: d, Data: params})
	process(Request{Method: "blob.put", Params: part}, chunkOperation(d, "put", 0))
	invoke, _ := json.Marshal(blobRequest{Descriptor: d})
	r := process(Request{Method: "blob.invoke", Params: invoke}, op)
	if calls != 1 || r.Method != "blob.invoke" || string(r.Outcome.Result) != `"one-result"` {
		t.Fatal("transport form repeated canonical action", calls, r)
	}
}
