package relay

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestReplyRequiresCurrentDurableRequestAuthorization(t *testing.T) {
	for _, name := range []string{"valid", "roots changed", "endpoint changed", "revoked", "shortened lease", "expired intent", "overlong reply", "wrong peer", "wrong operation", "wrong method", "wrong permission", "missing scope", "missing permission", "request envelope"} {
		t.Run(name, func(t *testing.T) {
			a, b := identities(t)
			store := Store{Directory: privateTemp(t)}
			g := Grant{Peer: b.Public, Kind: "device", Endpoint: "reviewed-endpoint", Roots: []string{"/reviewed"}, SendMethods: []string{"export", "observe"}}
			intent := Record{Peer: b.Public.ID, Operation: "reply-scope-operation-1234", Method: "export", Authorization: "export", Scope: grantScope(g), Phase: "submitted", Expires: time.Now().Add(time.Hour).Unix()}
			body, err := json.Marshal(Reply{Method: "export", Outcome: Outcome{Result: json.RawMessage(`{"session":"reviewed"}`)}})
			if err != nil {
				t.Fatal(err)
			}
			e, err := sealKind(b, a.Public, "reply-scope-space-1234", intent.Operation, "response", body, time.Now(), 30*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "roots changed":
				g.Roots = []string{"/different"}
			case "endpoint changed":
				g.Endpoint = "different-endpoint"
			case "revoked":
				g.Revoked = true
			case "shortened lease":
				g.Expires = time.Now().Add(time.Minute).Unix()
			case "expired intent":
				intent.Expires = time.Now().Add(-time.Minute).Unix()
			case "overlong reply":
				intent.Expires = time.Now().Add(time.Minute).Unix()
			case "wrong peer":
				intent.Peer = a.Public.ID
			case "wrong operation":
				intent.Operation = "different-operation-1234"
			case "wrong method":
				intent.Method = "observe"
			case "wrong permission":
				intent.Authorization = "observe"
			case "missing scope":
				intent.Scope = nil
			case "missing permission":
				intent.Authorization = ""
			case "request envelope":
				e, err = Seal(b, a.Public, e.Space, e.Operation, body, time.Now(), 30*time.Minute)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Approve(t.Context(), g); err != nil {
				t.Fatal(err)
			}
			key := replyKey(b.Public.ID, e.Operation, "export")
			if err := writeJSON(filepath.Join(store.Directory, "outgoing-"+key+".json"), intent); err != nil {
				t.Fatal(err)
			}
			s := Service{Processor: Processor{Identity: a, Space: e.Space, Store: store}}
			err = s.receive(t.Context(), e)
			_, cached := os.Stat(filepath.Join(store.Directory, "reply-"+key+".json"))
			if name == "valid" {
				if err != nil || cached != nil {
					t.Fatal("authorized response was not retained", err, cached)
				}
			} else if err == nil || !permanent(err) || !os.IsNotExist(cached) {
				t.Fatal("response escaped changed authorization", err, cached)
			}
		})
	}
}

func TestOutgoingRetryRefusesChangedScopeBeforeNetwork(t *testing.T) {
	for _, name := range []string{"roots", "endpoint", "permission", "peer", "operation", "method", "missing scope"} {
		t.Run(name, func(t *testing.T) {
			a, b := identities(t)
			store := Store{Directory: privateTemp(t)}
			g := Grant{Peer: b.Public, Kind: "device", Endpoint: "reviewed-endpoint", Roots: []string{"/reviewed"}, SendMethods: []string{"export"}}
			params := json.RawMessage(`{"session":"reviewed"}`)
			body, _ := json.Marshal(Request{Method: "export", Params: params})
			digest := sha256.Sum256(body)
			op := "outgoing-scope-operation-1234"
			intent := Record{Peer: b.Public.ID, Operation: op, Method: "export", Authorization: "export", Scope: grantScope(g), Request: digest[:], Phase: "submitted", Expires: time.Now().Add(time.Hour).Unix()}
			switch name {
			case "roots":
				g.Roots = []string{"/different"}
			case "endpoint":
				g.Endpoint = "different-endpoint"
			case "permission":
				intent.Authorization = "observe"
			case "peer":
				intent.Peer = a.Public.ID
			case "operation":
				intent.Operation = "different-operation-1234"
			case "method":
				intent.Method = "observe"
			case "missing scope":
				intent.Scope = nil
			}
			if err := store.Approve(t.Context(), g); err != nil {
				t.Fatal(err)
			}
			if err := writeJSON(filepath.Join(store.Directory, "outgoing-"+replyKey(b.Public.ID, op, "export")+".json"), intent); err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			s := Service{Processor: Processor{Identity: a, Space: "outgoing-scope-space-1234", Store: store}, Transport: Transport{Base: server.URL, Space: "outgoing-scope-space-1234", Token: "test-private-token", AllowLoopback: true}}
			if _, err := s.callSmall(t.Context(), b.Public.ID, op, "export", "export", params); err == nil || requests.Load() != 0 {
				t.Fatal("changed request reached network", err, requests.Load())
			}
		})
	}
}
