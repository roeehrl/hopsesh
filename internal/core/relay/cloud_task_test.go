package relay

import (
	"crypto/ed25519"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func taskIssuerFixture(t *testing.T) (AdmissionStore, Identity, Connection, *int) {
	t.Helper()
	owner, err := GenerateIdentity("native-cloud-task-owner")
	if err != nil {
		t.Fatal(err)
	}
	calls := new(int)
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		if r.ParseForm() != nil || r.URL.Path != "/v1/cloud/tickets" {
			t.Error("unexpected invitation request")
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ticket": strings.Repeat("a", 64), "provider": r.Form.Get("provider"), "session": r.Form.Get("session"), "lease_seconds": 600, "expires": time.Now().Add(10 * time.Minute).Unix()})
	}))
	t.Cleanup(s.Close)
	cert := filepath.Join(t.TempDir(), "ca.pem")
	if err = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	c := Connection{URL: s.URL, Device: owner.Public.ID, Token: "private-native-credential", Space: "cloud-task-test-space", Expires: time.Now().Add(time.Hour).Unix(), CAFile: cert}
	return AdmissionStore{Directory: filepath.Join(t.TempDir(), "admissions")}, owner, c, calls
}

func TestCloudTaskResumeIsExplicitAndSurvivesInvitationRetirement(t *testing.T) {
	s, owner, c, calls := taskIssuerFixture(t)
	first, err := s.IssueTask(t.Context(), owner, c, "claude-hosted", "same-native-id", 10*time.Minute, "")
	if err != nil {
		t.Fatal(err)
	}
	independent, err := s.IssueTask(t.Context(), owner, c, "claude-hosted", "same-native-id", 10*time.Minute, "")
	if err != nil || first.TaskID == independent.TaskID {
		t.Fatal("native ID automatically joined independent tasks", err)
	}
	resumed, err := s.IssueTask(t.Context(), owner, c, "claude-hosted", "changed-native-id", 10*time.Minute, first.TaskID)
	if err != nil || resumed.TaskID != first.TaskID || resumed.ID == first.ID {
		t.Fatal("resume lost logical identity or reused invitation", err)
	}
	for _, row := range []AdmissionRecord{first, independent, resumed} {
		b, err := os.ReadFile(row.Path)
		if err != nil {
			t.Fatal(err)
		}
		var ticket AdmissionTicket
		if json.Unmarshal(b, &ticket) != nil {
			t.Fatal("ticket decode")
		}
		ticket.Expires = time.Now().Add(-time.Hour).Unix()
		ticket.Signature = ed25519.Sign(owner.Signing, ticket.signed())
		b, _ = json.Marshal(ticket)
		if err = os.WriteFile(row.Path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	resumedAgain, err := s.IssueTask(t.Context(), owner, c, "claude-hosted", "rebuilt-native-id", 10*time.Minute, first.TaskID)
	if err != nil || resumedAgain.TaskID != first.TaskID {
		t.Fatal("invitation expiry erased task identity", err)
	}
	if _, err = os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatal("expired invitation not retired", err)
	}
	rows, err := s.Tasks()
	if err != nil || len(rows) != 2 {
		t.Fatal("task history lost or duplicated across rebuild", err)
	}
	before := *calls
	for _, invalid := range []struct {
		session string
		lease   time.Duration
	}{{"", 10 * time.Minute}, {"../invalid", 10 * time.Minute}, {"session", time.Second}} {
		if _, err = s.IssueTask(t.Context(), owner, c, "claude-hosted", invalid.session, invalid.lease, first.TaskID); err == nil {
			t.Fatal("invalid resume input accepted")
		}
	}
	unchanged, err := s.readTask(first.TaskID)
	if err != nil || unchanged.Generation != resumedAgain.Generation || *calls != before {
		t.Fatal("invalid input superseded an existing task generation", err)
	}
	foreign, err := GenerateIdentity("another-native-owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		owner          Identity
		provider, task string
	}{
		{owner, "codex-current", first.TaskID}, {foreign, "claude-hosted", first.TaskID}, {owner, "claude-hosted", "../escape"}, {owner, "claude-hosted", strings.Repeat("f", 32)},
	} {
		if _, err = s.IssueTask(t.Context(), change.owner, c, change.provider, "session", 10*time.Minute, change.task); err == nil {
			t.Fatal("foreign or absent task resumed")
		}
	}
	if *calls != before {
		t.Fatal("invalid task scope reached network")
	}
}

func TestCloudTaskSignatureCannotCarryForkProviderOrIssuerChanges(t *testing.T) {
	owner, err := GenerateIdentity("native-owner")
	if err != nil {
		t.Fatal(err)
	}
	task, err := newCloudTask(owner, "claude-hosted")
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*CloudTask){
		func(v *CloudTask) { v.ID = strings.Repeat("f", 32) }, func(v *CloudTask) { v.Provider = "codex-current" }, func(v *CloudTask) { v.Created++ }, func(v *CloudTask) { v.Schema++ }, func(v *CloudTask) { v.Owner.Endpoint = "cloud/claude-hosted/forged" },
	} {
		copy := task
		mutate(&copy)
		if copy.Verify(owner.Public.ID) == nil {
			t.Fatal("changed historical task accepted")
		}
	}
	if task.Verify("") == nil {
		t.Fatal("task had no expected issuer")
	}
}

func TestCloudTaskProductionQuotaAllowsExplicitResumeWithoutNewAllocation(t *testing.T) {
	s, owner, c, calls := taskIssuerFixture(t)
	dir := filepath.Join(s.Directory, "tasks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	var first CloudTask
	for i := 0; i < 10000; i++ {
		task, err := newCloudTask(owner, "claude-hosted")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = task
		}
		b, _ := json.Marshal(cloudTaskRegistry{task, 1, "previous-session"})
		if err = os.WriteFile(filepath.Join(dir, task.ID+".json"), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.IssueTask(t.Context(), owner, c, "claude-hosted", "new-session", 10*time.Minute, ""); err == nil || *calls != 0 {
		t.Fatal("full task quota issued new authority")
	}
	r, err := s.IssueTask(t.Context(), owner, c, "claude-hosted", "rebuilt-session", 10*time.Minute, first.ID)
	if err != nil || r.TaskID != first.ID || r.Generation != 2 || *calls != 1 {
		t.Fatal("quota blocked an existing task's independent fresh lease", err)
	}
	rows, err := s.Tasks()
	if err != nil || len(rows) != 10000 {
		t.Fatal("resume changed task cardinality", err)
	}
}

func TestCloudTaskResolveRequiresTheExactCurrentApprovedClaim(t *testing.T) {
	owner, ticket, leaf, _ := signedAdmission(t, DefaultOrigin)
	status := AdmissionStatus{Status: "claimed", Provider: ticket.Provider, Session: ticket.Session, Expires: ticket.Expires, LeaseExpires: time.Now().Add(5 * time.Minute).Unix(), Public: &leaf.Public}
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _ = json.NewEncoder(w).Encode(status) }))
	defer server.Close()
	ticket.Origin = server.URL
	ticket.Signature = ed25519.Sign(owner.Signing, ticket.signed())
	cert := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	c := Connection{URL: server.URL, Device: owner.Public.ID, Token: "native-routing-secret", CAFile: cert}
	s := AdmissionStore{Directory: t.TempDir()}
	if err := os.Chmod(s.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.saveTask(ticket.Task, ticket.Generation, ticket.Session); err != nil {
		t.Fatal(err)
	}
	p, _ := s.path(ticket.ID)
	body, _ := json.Marshal(ticket)
	if err := os.WriteFile(p, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveTask(t.Context(), c, ticket.ID, ticket.Task, leaf.Public, ticket.Provider, ticket.Session, ticket.Generation); err != nil {
		t.Fatal(err)
	}
	before := calls
	other, err := newCloudTask(owner, ticket.Provider)
	if err != nil {
		t.Fatal(err)
	}
	if s.ResolveTask(t.Context(), c, ticket.ID, other, leaf.Public, ticket.Provider, ticket.Session, ticket.Generation) == nil || s.ResolveTask(t.Context(), c, ticket.ID, ticket.Task, leaf.Public, ticket.Provider, "other-session", ticket.Generation) == nil || calls != before {
		t.Fatal("unrelated task or session reached claim lookup")
	}
	for _, mode := range []string{"other-peer", "pending", "revoked", "expired"} {
		status.Status = "claimed"
		status.Public = &leaf.Public
		if mode == "other-peer" {
			status.Public = &owner.Public
		} else {
			status.Status = mode
		}
		if s.ResolveTask(t.Context(), c, ticket.ID, ticket.Task, leaf.Public, ticket.Provider, ticket.Session, ticket.Generation) == nil {
			t.Fatal("unverified or inactive claim became lineage", mode)
		}
	}
}
