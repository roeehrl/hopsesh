package relay

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signedAdmission(t *testing.T, origin string) (Identity, AdmissionTicket, Identity, CloudClaim) {
	t.Helper()
	owner, err := GenerateIdentity("native-admission-machine")
	if err != nil {
		t.Fatal(err)
	}
	scope := CloudClaim{Provider: "claude-hosted", Session: "native-session-123", Incarnation: strings.Repeat("a", 32), Expires: time.Now().Add(5 * time.Minute).Unix()}
	leaf, err := GenerateIdentity("cloud/" + scope.Provider + "/" + scope.Incarnation)
	if err != nil {
		t.Fatal(err)
	}
	task, err := newCloudTask(owner, scope.Provider)
	if err != nil {
		t.Fatal(err)
	}
	ticket := AdmissionTicket{Schema: 2, ID: strings.Repeat("e", 32), Task: task, Generation: 1, Origin: origin, Ticket: strings.Repeat("b", 64), Provider: scope.Provider, Session: scope.Session, Expires: time.Now().Add(10 * time.Minute).Unix(), LeaseSeconds: 600, Owner: owner.Public}
	ticket.Signature = ed25519.Sign(owner.Signing, ticket.signed())
	return owner, ticket, leaf, scope
}

func TestAdmissionInvitationPinsEveryFieldAndIndependentFingerprint(t *testing.T) {
	owner, ticket, _, _ := signedAdmission(t, DefaultOrigin)
	if err := ticket.Verify(owner.Public.ID); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*AdmissionTicket){
		func(v *AdmissionTicket) { v.Origin = "https://other.invalid" }, func(v *AdmissionTicket) { v.Session = "fork" }, func(v *AdmissionTicket) { v.Provider = "codex-current" },
		func(v *AdmissionTicket) { v.Generation++ }, func(v *AdmissionTicket) { v.ID = strings.Repeat("c", 32) }, func(v *AdmissionTicket) { v.Task.ID = strings.Repeat("c", 32) }, func(v *AdmissionTicket) { v.Expires++ }, func(v *AdmissionTicket) { v.LeaseSeconds++ }, func(v *AdmissionTicket) { v.Ticket = strings.Repeat("c", 64) },
		func(v *AdmissionTicket) { v.Schema++ }, func(v *AdmissionTicket) { v.Owner.Endpoint = "cloud/claude-hosted/reused" },
	} {
		copy := ticket
		mutate(&copy)
		if copy.Verify(owner.Public.ID) == nil {
			t.Fatal("tampered invitation accepted")
		}
	}
	for _, fingerprint := range []string{"", strings.Repeat("d", 64)} {
		if ticket.Verify(fingerprint) == nil {
			t.Fatal("independent fingerprint check omitted")
		}
	}
	// Re-signing cannot turn a personal key into a cloud issuing device.
	owner2, err := GenerateIdentity("cloud/claude-hosted/setup")
	if err != nil {
		t.Fatal(err)
	}
	ticket.Owner = owner2.Public
	ticket.Signature = ed25519.Sign(owner2.Signing, ticket.signed())
	if ticket.Verify(owner2.Public.ID) == nil {
		t.Fatal("cloud key became an invitation issuer")
	}
}

func TestAdmissionClaimProvesFreshKeysAndRejectsWidenedCredential(t *testing.T) {
	for _, mode := range []string{"valid", "short-remaining-lease", "wrong-device", "wrong-kind", "not-provisional", "other-url", "remote-ca", "too-long", "oversized-token", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var ticket AdmissionTicket
			var leaf Identity
			var scope CloudClaim
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.ParseForm() != nil || r.URL.Path != "/v1/cloud/claim" || r.Header.Get("Authorization") != "" {
					t.Error("invalid claim or leaked native credential")
				}
				message := "hopsesh-cloud-admission-v1\x00" + ticket.Origin
				for _, key := range []string{"ticket", "identity", "provider", "session", "incarnation", "lease_expires"} {
					message += "\x00" + r.Form.Get(key)
				}
				proof, _ := base64.StdEncoding.DecodeString(r.Form.Get("proof"))
				if !ed25519.Verify(leaf.Public.Signing, []byte(message), proof) || r.Form.Get("lease_expires") != strconv.FormatInt(scope.Expires, 10) {
					t.Error("fresh scope proof invalid")
				}
				out := map[string]any{"space": "admission-space-12345", "device": leaf.Public.ID, "token": "bounded-cloud-secret", "expires": scope.Expires, "kind": "cloud-session", "provisional": true}
				switch mode {
				case "wrong-device":
					out["device"] = ticket.Owner.ID
				case "wrong-kind":
					out["kind"] = "device"
				case "not-provisional":
					out["provisional"] = false
				case "other-url":
					out["url"] = "https://other.invalid"
				case "remote-ca":
					out["caFile"] = "remote.pem"
				case "too-long":
					out["expires"] = scope.Expires + 1
				case "oversized-token":
					out["token"] = strings.Repeat("x", 4097)
				case "redirect":
					http.Redirect(w, r, "https://other.invalid", http.StatusFound)
					return
				}
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer server.Close()
			owner, invitation, key, claim := signedAdmission(t, server.URL)
			ticket, leaf, scope = invitation, key, claim
			if mode == "short-remaining-lease" {
				scope.Expires = time.Now().Add(45 * time.Second).Unix()
			}
			connection, err := ClaimAdmission(t.Context(), ticket, owner.Public.ID, leaf, scope, server.Client())
			valid := mode == "valid" || mode == "short-remaining-lease"
			if (err == nil) != valid || calls != 1 {
				t.Fatalf("claim qualification: valid=%v error=%v calls=%d", valid, err, calls)
			}
			if valid && (connection.URL != server.URL || connection.Device != leaf.Public.ID) {
				t.Fatal("connection escaped signed origin or identity")
			}
		})
	}
}

func TestAdmissionRevokeDoesNotSendCredentialToAnotherOrigin(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; t.Error("credential sent to wrong relay") }))
	defer server.Close()
	owner, ticket, _, _ := signedAdmission(t, server.URL)
	c := Connection{URL: "https://other.invalid", Device: owner.Public.ID, Token: "native-routing-secret"}
	if RevokeAdmission(t.Context(), c, ticket) == nil || requests != 0 {
		t.Fatal("origin mismatch accepted")
	}
}

func TestAdmissionIssueAndRepeatedRevokeUseEnrolledOwner(t *testing.T) {
	owner, _, _, _ := signedAdmission(t, DefaultOrigin)
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer native-routing-secret" || r.ParseForm() != nil {
			t.Error("native routing authorization missing")
		}
		if r.URL.Path == "/v1/cloud/tickets" {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"ticket": strings.Repeat("a", 64), "provider": r.Form.Get("provider"), "session": r.Form.Get("session"), "lease_seconds": 600, "expires": time.Now().Add(10 * time.Minute).Unix()})
			return
		}
		if r.Form.Get("ticket") != strings.Repeat("a", 64) {
			t.Error("another admission revoked")
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"revoked": true})
	}))
	defer server.Close()
	certFile := filepath.Join(t.TempDir(), "relay.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	c := Connection{URL: server.URL, Device: owner.Public.ID, Token: "native-routing-secret", Space: "admission-space-12345", Expires: time.Now().Add(time.Hour).Unix(), CAFile: certFile}
	store := AdmissionStore{Directory: filepath.Join(t.TempDir(), "invitations")}
	record, err := store.IssueTask(t.Context(), owner, c, "claude-hosted", "native-session-123", 10*time.Minute, "")
	if err != nil {
		t.Fatal("private invitation issue failed", err)
	}
	body, err := os.ReadFile(record.Path)
	if err != nil {
		t.Fatal(err)
	}
	var ticket AdmissionTicket
	if json.Unmarshal(body, &ticket) != nil || ticket.Verify(owner.Public.ID) != nil {
		t.Fatal("issued capsule invalid")
	}
	rows, err := store.List()
	if err != nil || len(rows) != 1 || rows[0].ID != record.ID {
		t.Fatal("saved invitation not listed", err)
	}
	publicJSON, _ := json.Marshal(rows)
	if strings.Contains(string(publicJSON), ticket.Ticket) || strings.Contains(string(publicJSON), "signature") || strings.Contains(string(publicJSON), "token") {
		t.Fatal("public invitation metadata exposed credentials")
	}
	for range 2 {
		if err = store.Revoke(t.Context(), c, record.ID); err != nil {
			t.Fatal(err)
		}
	}
	rows, err = store.List()
	if err != nil || !rows[0].Revoked {
		t.Fatal("confirmed revocation not reflected in private receipt", err)
	}
	if err = store.Revoke(t.Context(), c, "../escape"); err == nil || calls != 3 {
		t.Fatal("admission path escaped private store")
	}
	c.Device = strings.Repeat("f", 64)
	if _, err = store.IssueTask(t.Context(), owner, c, "claude-hosted", "native-session-123", 10*time.Minute, ""); err == nil || calls != 3 {
		t.Fatal("another enrolled identity issued a ticket")
	}
}
