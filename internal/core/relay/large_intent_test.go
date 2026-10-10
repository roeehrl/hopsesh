package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

func TestLargeUploadBindingsSurviveQuotaAndExpiry(t *testing.T) {
	_, peer := identities(t)
	store := Store{Directory: privateTemp(t)}
	g := Grant{Peer: peer.Public, Kind: "device", Endpoint: "approved-endpoint", Roots: []string{"/approved"}, SendMethods: []string{"apply"}}
	if err := store.Approve(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	d := descriptor("large-admission-operation-1234", "apply", "request", []byte(`{"session":"approved"}`), time.Now().Add(time.Hour).Unix())
	frozen, err := store.freezeUpload(t.Context(), g, d)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Directory, "large-intent-"+replyKey(g.Peer.ID, d.Operation, d.Method)+".json")
	body, err := localstate.ReadPrivateFile(path, 8192)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the production count ceiling, including descriptors whose uploads
	// never reached the first network request.
	for n := 1; n < MaxOperationRecords; n++ {
		if err := os.WriteFile(filepath.Join(store.Directory, fmt.Sprintf("large-intent-%064x.json", n)), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	later := d
	later.Expires = time.Now().Add(2 * time.Hour).Unix()
	if retry, err := store.freezeUpload(t.Context(), g, later); err != nil || retry != frozen {
		t.Fatal("full quota changed a retry's immutable wire lease", retry, err)
	}
	newRequest := descriptor("different-large-operation-1234", "apply", "request", []byte(`{"session":"different"}`), d.Expires)
	if _, err := store.freezeUpload(t.Context(), g, newRequest); err == nil {
		t.Fatal("new descriptor escaped the production count ceiling")
	}
	if err := store.withLock(t.Context(), func() error { return store.recoverySpaceLimit("", 1, time.Now().Add(2*time.Hour), 1) }); !errors.Is(err, ErrRecoveryQuota) {
		t.Fatal("large descriptors escaped byte accounting", err)
	}
	if after, err := localstate.ReadPrivateFile(path, 8192); err != nil || !bytes.Equal(body, after) {
		t.Fatal("expiry cleanup discarded a durable upload binding", err)
	}
}

func TestLargeUploadRefusesChangedOrExpiredAuthorization(t *testing.T) {
	for _, name := range []string{"scope", "shortened lease", "input", "expired descriptor"} {
		t.Run(name, func(t *testing.T) {
			_, peer := identities(t)
			store := Store{Directory: privateTemp(t)}
			g := Grant{Peer: peer.Public, Kind: "device", Endpoint: "approved-endpoint", Roots: []string{"/approved"}, SendMethods: []string{"apply"}}
			if err := store.Approve(t.Context(), g); err != nil {
				t.Fatal(err)
			}
			d := descriptor("large-scope-operation-1234", "apply", "request", []byte(`{"session":"approved"}`), time.Now().Add(time.Hour).Unix())
			if _, err := store.freezeUpload(t.Context(), g, d); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.Directory, "large-intent-"+replyKey(g.Peer.ID, d.Operation, d.Method)+".json")
			before, _ := localstate.ReadPrivateFile(path, 8192)
			switch name {
			case "scope":
				g.Roots = []string{"/different"}
			case "shortened lease":
				g.Expires = time.Now().Add(time.Minute).Unix()
				d.Expires = g.Expires
			case "input":
				d = descriptor(d.Operation, d.Method, d.Direction, []byte(`{"session":"different"}`), d.Expires)
			case "expired descriptor":
				var prior largeIntent
				if err := json.Unmarshal(before, &prior); err != nil {
					t.Fatal(err)
				}
				prior.Descriptor.Expires = time.Now().Add(-time.Minute).Unix()
				if err := writeJSON(path, prior); err != nil {
					t.Fatal(err)
				}
				before, _ = localstate.ReadPrivateFile(path, 8192)
			}
			if err := store.Approve(t.Context(), g); err != nil {
				t.Fatal(err)
			}
			if _, err := store.freezeUpload(t.Context(), g, d); err == nil {
				t.Fatal("changed upload accepted")
			}
			if after, err := localstate.ReadPrivateFile(path, 8192); err != nil || !bytes.Equal(before, after) {
				t.Fatal("rejection replaced the durable binding", err)
			}
		})
	}
}
