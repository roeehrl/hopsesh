package cloudintegration

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/relay"
)

func sessionFixture(t *testing.T) (string, Scope) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "sessions"), filepath.Join(dir, "repo"), filepath.Join(dir, "native", "project")} {
		if err = os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	s := Scope{Provider: "claude-hosted", Session: "abc123", Workspace: filepath.Join(dir, "repo"), NativeRoot: filepath.Join(dir, "native"), Transcript: filepath.Join(dir, "native", "project", "abc123.jsonl"), ExportTranscript: true}
	if err = os.WriteFile(s.Transcript, []byte("{\"sessionId\":\"abc123\",\"message\":\"test\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "sessions"), s
}

func TestStartupReasonPersistsWithoutGrantingAccess(t *testing.T) {
	parent, scope := sessionFixture(t)
	scope.ExportTranscript = false
	for _, source := range []string{"startup", "resume", "clear", "compact", "fork", "manual"} {
		s, err := Begin(t.Context(), parent, scope, source, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := Current(t.Context(), parent, scope.Provider, scope.Session, scope.Workspace)
		if err != nil || loaded.ID != s.ID || loaded.Source != source || loaded.Scope != scope {
			t.Fatalf("startup reason or scope changed: %s, %+v, %v", source, loaded, err)
		}
		grant := relay.Grant{Kind: "cloud-session", Methods: []string{"observe", "export"}, Expires: time.Now().Add(time.Hour).Unix()}
		observation, err := loaded.Handler(t.Context(), grant, "", "observe", nil)
		if err != nil || observation.(Observation).Source != source || observation.(Observation).ExportAllowed {
			t.Fatal("startup metadata lost or widened observation", observation, err)
		}
		if _, err := loaded.Handler(t.Context(), grant, "", "export", nil); err == nil {
			t.Fatal("startup reason granted export access", source)
		}
		if _, err := Begin(t.Context(), parent, scope, "untrusted\nsecret", time.Hour); err == nil {
			t.Fatal("unknown startup reason accepted")
		}
		current, err := Current(t.Context(), parent, scope.Provider, scope.Session, scope.Workspace)
		if err != nil || current.ID != s.ID {
			t.Fatal("invalid startup replaced the current identity", err)
		}
	}
}

func TestCloudConnectorStopsAfterRoutingCredentialRevocation(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			parent, scope := sessionFixture(t)
			s, err := Begin(t.Context(), parent, scope, "manual", time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			var polls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/notifications" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if polls.Add(1) == 1 {
					_ = json.NewEncoder(w).Encode(relay.Batch{})
					return
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			ca := filepath.Join(parent, "relay-ca.pem")
			if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			store := relay.Store{Directory: s.Directory}
			if err = store.SetConnection(t.Context(), relay.Connection{URL: server.URL, Space: "cloud-revoke-test", Device: s.Public.ID, Token: "disposable-test-token", Expires: time.Now().Add(time.Minute).Unix(), CAFile: ca}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err = s.Run(ctx); !errors.Is(err, relay.ErrAuthorizationRefused) {
				t.Fatalf("connector did not stop with its authorization cause: %v", err)
			}
			if polls.Load() != 2 {
				t.Fatalf("refused credential was retried: %d polls", polls.Load())
			}
		})
	}
}

func TestCloudIncarnationsNeverReuseSetupKeysAcrossResumeRebuildAndFork(t *testing.T) {
	parent, scope := sessionFixture(t)
	seen := map[string]bool{}
	for _, source := range []string{"startup", "resume", "compact", "fork", "rebuild", "resume"} {
		current := scope
		if source == "fork" {
			current.Session = "fork456"
			current.Transcript = filepath.Join(filepath.Dir(scope.Transcript), "fork456.jsonl")
		}
		s, err := Begin(t.Context(), parent, current, "manual", time.Hour)
		if err != nil {
			t.Fatal(source, err)
		}
		if seen[s.Public.ID] || s.Public.Endpoint == "" {
			t.Fatal("cached identity reused", source)
		}
		seen[s.Public.ID] = true
		loaded, err := Load(t.Context(), s.Directory)
		if err != nil || loaded.ID != s.ID {
			t.Fatal("load", err)
		}
		currentInstance, err := Current(t.Context(), parent, current.Provider, current.Session, current.Workspace)
		if err != nil || currentInstance.ID != s.ID {
			t.Fatal("exact task lookup did not return its fresh incarnation", err)
		}
		if _, err = (relay.Store{Directory: s.Directory}).Connection(t.Context()); !os.IsNotExist(err) {
			t.Fatal("startup carried a setup credential", err)
		}
	}
}

func TestRestartSupersedesOldConnectorButForkKeepsOriginalAuthorized(t *testing.T) {
	parent, scope := sessionFixture(t)
	original, err := Begin(t.Context(), parent, scope, "manual", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	forkScope := scope
	forkScope.Session = "fork456"
	forkScope.Transcript = filepath.Join(filepath.Dir(scope.Transcript), "fork456.jsonl")
	if _, err = Begin(t.Context(), parent, forkScope, "manual", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(t.Context(), original.Directory); err != nil {
		t.Fatal("fork invalidated original", err)
	}
	if _, err = Begin(t.Context(), parent, scope, "manual", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(t.Context(), original.Directory); err == nil {
		t.Fatal("restart reused old authorization")
	}
}

func TestCloudHandlerCannotWidenSessionOrAccessDeviceOperations(t *testing.T) {
	parent, scope := sessionFixture(t)
	s, err := Begin(t.Context(), parent, scope, "manual", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := relay.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	g := relay.Grant{Peer: peer.Public, Kind: "cloud-session", Methods: []string{"observe", "export"}, Expires: time.Now().Add(time.Hour).Unix()}
	obs, err := s.Handler(t.Context(), g, "operation", "observe", nil)
	if err != nil {
		t.Fatal(err)
	}
	o := obs.(Observation)
	if o.Session != scope.Session || !o.TranscriptAvailable || o.LeaseExpires.Unix() != s.Expires.Unix() || o.LastWrite.IsZero() {
		t.Fatal(o)
	}
	result, err := s.Handler(t.Context(), g, "operation", "export", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(result)
	if !strings.Contains(string(b), "native-jsonl") || strings.Contains(string(b), "Encryption") {
		t.Fatal(string(b))
	}
	for _, method := range []string{"plan", "apply", "undo", "shell", "settings", "inventory"} {
		if _, err = s.Handler(t.Context(), g, "op", method, nil); err == nil {
			t.Fatal("device action allowed", method)
		}
	}
	if _, err = s.Handler(t.Context(), g, "op", "export", json.RawMessage(`{"session":"other","path":"/etc/passwd"}`)); err == nil {
		t.Fatal("wider scope allowed")
	}
	s.ExportTranscript = false
	if _, err = s.Handler(t.Context(), g, "op", "export", nil); err == nil {
		t.Fatal("missing local export consent ignored")
	}
	g.Revoked = true
	if _, err = s.Handler(t.Context(), g, "op", "observe", nil); err == nil {
		t.Fatal("revocation ignored")
	}
	g.Revoked = false
	s.Expires = time.Now().Add(-time.Second)
	if _, err = s.Handler(t.Context(), g, "op", "observe", nil); err == nil {
		t.Fatal("lease expiration ignored")
	}
}

func TestCloudObservationUsesEffectivePeerLeaseAndExportPermission(t *testing.T) {
	parent, scope := sessionFixture(t)
	s, err := Begin(t.Context(), parent, scope, "manual", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := relay.GenerateIdentity("native-cloud-owner")
	if err != nil {
		t.Fatal(err)
	}
	g := relay.Grant{Peer: peer.Public, Kind: "cloud-session", Methods: []string{"observe"}, Expires: time.Now().Add(45 * time.Second).Unix()}
	value, err := s.Handler(t.Context(), g, "bounded-lease", "observe", nil)
	if err != nil {
		t.Fatal(err)
	}
	observation := value.(Observation)
	if observation.LeaseExpires.Unix() != g.Expires || observation.ExportAllowed {
		t.Fatal("observation overstated sharing or routing lifetime")
	}
	if _, err = s.Handler(t.Context(), g, "bounded-lease", "export", nil); err == nil {
		t.Fatal("observe-only owner exported a transcript")
	}
}

func TestCloudScopeRefusesOtherSessionsLinksAndUnsupportedNativeVisibility(t *testing.T) {
	parent, scope := sessionFixture(t)
	for _, mutate := range []func(*Scope){
		func(s *Scope) { s.Session = "other" },
		func(s *Scope) { s.Transcript = filepath.Join(filepath.Dir(parent), "abc123.jsonl") },
		func(s *Scope) { s.Provider = "codex-current" },
		func(s *Scope) { s.Workspace = "." },
		func(s *Scope) { s.Transcript = "" },
	} {
		s := scope
		mutate(&s)
		if _, err := Begin(t.Context(), parent, s, "manual", time.Hour); err == nil {
			t.Fatal("invalid scope accepted", s)
		}
	}
	if err := os.Remove(scope.Transcript); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(scope.Workspace, "abc123.jsonl"), scope.Transcript); err != nil {
		t.Skip("symlink unavailable", err)
	}
	if _, err := Begin(t.Context(), parent, scope, "manual", time.Hour); err == nil {
		t.Fatal("linked transcript accepted")
	}
}

func TestSessionStartRequiresCloudMarkerAndDocumentedInput(t *testing.T) {
	in := `{"session_id":"abc123","cwd":"/repo","transcript_path":"/native/abc123.jsonl","hook_event_name":"SessionStart","source":"resume"}`
	for _, remote := range []string{"", "false", "1"} {
		if _, err := ReadSessionStart(strings.NewReader(in), remote); err == nil {
			t.Fatal("local hook executed", remote)
		}
	}
	if _, err := ReadSessionStart(strings.NewReader(in), "true"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{strings.Repeat("x", 65537), strings.Replace(in, "SessionStart", "Setup", 1), strings.Replace(in, "resume", "unknown", 1)} {
		if _, err := ReadSessionStart(strings.NewReader(body), "true"); err == nil {
			t.Fatal("bad hook input accepted")
		}
	}
}

func TestCloudLeaseCannotBeRefreshedByCachedCredentialsOrChangedIdentity(t *testing.T) {
	parent, scope := sessionFixture(t)
	s, err := Begin(t.Context(), parent, scope, "manual", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	store := relay.Store{Directory: s.Directory}
	if err = store.SetConnection(t.Context(), relay.Connection{URL: "https://relay.example.com", Space: "testspace12345678", Device: s.Public.ID, Token: "test-credential", Expires: s.Expires.Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err = s.Run(ctx); err == nil || !strings.Contains(err.Error(), "lease") {
		t.Fatal("overlong credential accepted", err)
	}
	s.Expires = time.Now().Add(-time.Minute)
	b, _ := json.Marshal(s)
	if err = os.WriteFile(filepath.Join(s.Directory, "session.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(t.Context(), s.Directory); err == nil {
		t.Fatal("expired session loaded")
	}
}
