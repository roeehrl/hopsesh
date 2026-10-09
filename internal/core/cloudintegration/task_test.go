package cloudintegration

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/relay"
)

func taskInvitationIssuer(t *testing.T) func(string, string) relay.AdmissionTicket {
	t.Helper()
	owner, err := relay.GenerateIdentity("native-task-fixture")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ParseForm() != nil || r.URL.Path != "/v1/cloud/tickets" {
			t.Error("unexpected task issuance")
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ticket": strings.Repeat("a", 64), "provider": r.Form.Get("provider"), "session": r.Form.Get("session"), "lease_seconds": 600, "expires": time.Now().Add(10 * time.Minute).Unix()})
	}))
	t.Cleanup(server.Close)
	cert := filepath.Join(t.TempDir(), "ca.pem")
	if err = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	c := relay.Connection{URL: server.URL, Device: owner.Public.ID, Token: "private-fixture-native-credential", Space: "task-incarnation-test-space", Expires: time.Now().Add(time.Hour).Unix(), CAFile: cert}
	s := relay.AdmissionStore{Directory: filepath.Join(t.TempDir(), "admissions")}
	return func(session, resume string) relay.AdmissionTicket {
		t.Helper()
		r, err := s.IssueTask(t.Context(), owner, c, "claude-hosted", session, 10*time.Minute, resume)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(r.Path)
		if err != nil {
			t.Fatal(err)
		}
		var ticket relay.AdmissionTicket
		if json.Unmarshal(b, &ticket) != nil {
			t.Fatal("invalid issued ticket")
		}
		return ticket
	}
}

func TestCloudLogicalTaskSupersedesChangedNativeIDsWithoutInvalidatingFork(t *testing.T) {
	parent, scope := sessionFixture(t)
	issue := taskInvitationIssuer(t)
	ticket := issue(scope.Session, "")
	original, err := Begin(t.Context(), parent, scope, "manual", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = original.AssociateTask(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	forkScope := scope
	forkScope.Session = "fork-native"
	forkScope.Transcript = filepath.Join(filepath.Dir(scope.Transcript), forkScope.Session+".jsonl")
	fork, err := Begin(t.Context(), parent, forkScope, "manual", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	forkTicket := issue(forkScope.Session, "")
	if err = fork.AssociateTask(t.Context(), forkTicket); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(t.Context(), original.Directory); err != nil {
		t.Fatal("fork invalidated original", err)
	}
	rebuiltScope := scope
	rebuiltScope.Session = "rebuilt-native"
	rebuiltScope.Transcript = filepath.Join(filepath.Dir(scope.Transcript), rebuiltScope.Session+".jsonl")
	rebuilt, err := Begin(t.Context(), parent, rebuiltScope, "manual", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	resumed := issue(rebuiltScope.Session, ticket.Task.ID)
	if resumed.Generation != ticket.Generation+1 {
		t.Fatal("task generation did not advance")
	}
	if err = rebuilt.AssociateTask(t.Context(), resumed); err != nil {
		t.Fatal(err)
	}
	for _, load := range []func(string) error{
		func(p string) error { _, e := Load(t.Context(), p); return e },
		func(p string) error { _, e := LoadForClaim(t.Context(), p); return e },
	} {
		if load(original.Directory) == nil {
			t.Fatal("older generation remained usable after native ID changed")
		}
	}
	if err = original.AssociateTask(t.Context(), ticket); err == nil {
		t.Fatal("old claim resurrected superseded incarnation")
	}
	if _, err = Load(t.Context(), fork.Directory); err != nil {
		t.Fatal("resume invalidated independent fork", err)
	}
	if _, err = Load(t.Context(), rebuilt.Directory); err != nil {
		t.Fatal(err)
	}
	// A freshly generated key cannot reuse an older generation even if its
	// native ID has a separate startup slot and the old association is absent.
	older, err := Begin(t.Context(), parent, scope, "manual", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = older.AssociateTask(t.Context(), ticket); err == nil {
		t.Fatal("old invitation generation admitted fresh key")
	}
}

func TestCloudTaskAssociationRepairsInterruptedPublicationWithoutRollback(t *testing.T) {
	parent, scope := sessionFixture(t)
	issue := taskInvitationIssuer(t)
	ticket := issue(scope.Session, "")
	instance, err := Begin(t.Context(), parent, scope, "manual", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a := taskAssociation{ticket.Task, ticket.ID, instance.ID, scope.Session, ticket.Generation}
	b, _ := json.Marshal(a)
	if err = os.WriteFile(filepath.Join(instance.Directory, "task.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(t.Context(), instance.Directory); err == nil {
		t.Fatal("partially published association was authorized")
	}
	if _, err = LoadForClaim(t.Context(), instance.Directory); err != nil {
		t.Fatal("exact interrupted claim could not recover", err)
	}
	if err = instance.AssociateTask(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(t.Context(), instance.Directory); err != nil {
		t.Fatal(err)
	}
	other, err := Begin(t.Context(), parent, scope, "manual", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	next := issue(scope.Session, ticket.Task.ID)
	if err = other.AssociateTask(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	if err = instance.AssociateTask(t.Context(), ticket); err == nil {
		t.Fatal("interrupted-repair path rolled back a newer generation")
	}
}

func TestCloudScopeWatchJoinsOnChangedNativeIDTaskRebuild(t *testing.T) {
	parent, scope := sessionFixture(t)
	issue := taskInvitationIssuer(t)
	ticket := issue(scope.Session, "")
	first, err := Begin(t.Context(), parent, scope, "manual", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.AssociateTask(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	join, err := first.watchScope(ctx, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(context.Canceled); join() }()
	// Unrelated mailbox and vendor writes cannot retire an unchanged scope.
	if err = os.WriteFile(filepath.Join(first.Directory, "unrelated.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(scope.Transcript, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("unrelated writes canceled connector")
	}
	rebuiltScope := scope
	rebuiltScope.Session = "changed-native-session"
	rebuiltScope.Transcript = filepath.Join(filepath.Dir(scope.Transcript), rebuiltScope.Session+".jsonl")
	rebuilt, err := Begin(t.Context(), parent, rebuiltScope, "manual", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	next := issue(rebuiltScope.Session, ticket.Task.ID)
	if err = rebuilt.AssociateTask(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("superseded connector did not receive scope change")
	}
	if !strings.Contains(context.Cause(ctx).Error(), "superseded") {
		t.Fatal("scope change lost its shutdown reason", context.Cause(ctx))
	}
}
