package relay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func identities(t *testing.T) (Identity, Identity) {
	t.Helper()
	a, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}
func TestEncryptedEnvelopeAuthenticatesRoutingAndPinnedKeys(t *testing.T) {
	a, b := identities(t)
	now := time.Now()
	space := "test-space-123456"
	op := "operation-1234567"
	e, err := Seal(a, b.Public, space, op, []byte("private conversation"), now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(e.Ciphertext, []byte("private conversation")) {
		t.Fatal("plaintext exposed")
	}
	raw, err := Open(b, a.Public, e, space, now)
	if err != nil || string(raw) != "private conversation" {
		t.Fatal(err)
	}
	for _, change := range []string{"ciphertext", "recipient", "operation", "space", "expiry", "signature"} {
		bad := e
		bad.Ciphertext = bytes.Clone(e.Ciphertext)
		bad.Signature = bytes.Clone(e.Signature)
		switch change {
		case "ciphertext":
			bad.Ciphertext[len(bad.Ciphertext)-1] ^= 1
		case "recipient":
			bad.To = a.Public.ID
		case "operation":
			bad.Operation = "different-1234567"
		case "space":
			bad.Space = "different-1234567"
		case "expiry":
			bad.Expires++
		case "signature":
			bad.Signature[0] ^= 1
		}
		if _, err = Open(b, a.Public, bad, space, now); err == nil {
			t.Fatal("accepted tampering", change)
		}
	}
	attacker, _ := identities(t)
	if _, err = Open(b, attacker.Public, e, space, now); err == nil {
		t.Fatal("relay key substitution accepted")
	}
	if _, err = Open(b, a.Public, e, space, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired ciphertext decrypted")
	}
	if _, err = Open(a, a.Public, e, space, now); err == nil {
		t.Fatal("foreign recipient accepted")
	}
}
func TestRelayDurabilityReplayRevocationAndChangedInput(t *testing.T) {
	ctx := context.Background()
	a, b := identities(t)
	now := time.Now()
	s := Store{Directory: privateTemp(t)}
	g := Grant{Peer: a.Public, Kind: "device", Methods: []string{"apply"}}
	if err := s.Approve(ctx, g); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	p := Processor{Identity: b, Space: "test-space-123456", Store: s, Handle: func(context.Context, Grant, string, string, json.RawMessage) (any, error) {
		calls.Add(1)
		return map[string]string{"journal": "existing-native-journal"}, nil
	}}
	body := []byte(`{"method":"apply","params":{"branch":"fork-A"}}`)
	e, err := Seal(a, b.Public, p.Space, "operation-1234567", body, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	response, err := p.Process(ctx, e, now)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		// Both network replay and a fresh encrypted retransmission must dedupe.
		e, _ = Seal(a, b.Public, p.Space, e.Operation, body, now, time.Hour)
		r, err := p.Process(ctx, e, now)
		if err != nil || r.ID != response.ID {
			t.Fatal("replay did not recover reply", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("native action repeated")
	}
	plain, err := Open(a, b.Public, response, p.Space, now)
	if err != nil || !bytes.Contains(plain, []byte("existing-native-journal")) {
		t.Fatal(err)
	}
	changed, _ := Seal(a, b.Public, p.Space, e.Operation, []byte(`{"method":"apply","params":{"branch":"original"}}`), now, time.Hour)
	if _, err = p.Process(ctx, changed, now); err == nil {
		t.Fatal("operation ID could alter branch")
	}
	if err = s.Revoke(ctx, a.Public.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Process(ctx, e, now); !errors.Is(err, ErrRevoked) {
		t.Fatal("revoked sender accepted", err)
	}
	files, _ := filepath.Glob(filepath.Join(s.Directory, "operation-*.json"))
	for _, file := range files {
		raw, _ := os.ReadFile(file)
		if bytes.Contains(raw, []byte("fork-A")) || bytes.Contains(raw, []byte("existing-native-journal")) {
			t.Fatal("plaintext in durable relay record")
		}
	}
}
func TestInterruptedActionRemainsUncertainAfterRestart(t *testing.T) {
	ctx := context.Background()
	a, b := identities(t)
	s := Store{Directory: privateTemp(t)}
	now := time.Now()
	if err := s.Approve(ctx, Grant{Peer: a.Public, Kind: "device", Methods: []string{"apply"}}); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"method":"apply","params":{}}`)
	op := "operation-1234567"
	digest := sha256.Sum256(body)
	key := sha256.Sum256([]byte(a.Public.ID + "\x00" + op + "\x00apply"))
	if err := writeJSON(filepath.Join(s.Directory, "operation-"+hex.EncodeToString(key[:])+".json"), Record{Peer: a.Public.ID, Operation: op, Method: "apply", Request: digest[:], Phase: "started"}); err != nil {
		t.Fatal(err)
	}
	e, _ := Seal(a, b.Public, "test-space-123456", op, body, now, time.Hour)
	p := Processor{Identity: b, Space: e.Space, Store: s, Handle: func(context.Context, Grant, string, string, json.RawMessage) (any, error) {
		t.Fatal("uncertain native action repeated")
		return nil, nil
	}}
	if _, err := p.Process(ctx, e, now); !errors.Is(err, ErrUncertain) {
		t.Fatal("uncertain operation retried", err)
	}
}
func TestCloudGrantCannotBecomeGeneralReceiver(t *testing.T) {
	ctx := context.Background()
	a, _ := identities(t)
	s := Store{Directory: privateTemp(t)}
	g := Grant{Peer: a.Public, Kind: "cloud-session", Methods: []string{"observe", "export"}, Expires: time.Now().Add(time.Hour).Unix()}
	if err := s.Approve(ctx, g); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"plan", "apply", "undo", "settings.set", "exec"} {
		if g.Allows(method, time.Now()) {
			t.Fatal("cloud received administrative power", method)
		}
		g.Methods = []string{method}
		if err := s.Approve(ctx, g); err == nil {
			t.Fatal("invalid grant persisted", method)
		}
	}
	g.Methods = []string{"observe"}
	g.Expires = time.Now().Add(48 * time.Hour).Unix()
	if err := s.Approve(ctx, g); err == nil {
		t.Fatal("unbounded cloud grant")
	}
	if _, err := s.Grant(ctx, strings.Repeat("../", 20)); !errors.Is(err, ErrRevoked) {
		t.Fatal("untrusted path accepted")
	}
}

func privateTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
