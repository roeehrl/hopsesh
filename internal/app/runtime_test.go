package app

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
)

func TestRunningRuntimeAdoptsFirstEnrollmentAndCredentialRenewalWithoutRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(home, "config"))
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(home, "state"))
	cfg := config.Defaults()
	if err := config.Save(&cfg); err != nil {
		t.Fatal(err)
	}
	r, err := registry.New()
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, r, config.StateDir(), nil)
	opts := observe.Defaults()
	opts.Debounce = 10 * time.Millisecond
	opts.MaxDelay = 20 * time.Millisecond
	opts.Reconcile = time.Hour
	owner, err := a.StartRuntime(t.Context(), "headless", nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	client := localruntime.Client{Namespace: owner.Namespace}
	var first, renewed atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer first-token":
			first.Add(1)
		case "Bearer renewed-token":
			renewed.Add(1)
		default:
			w.WriteHeader(403)
			return
		}
		_ = json.NewEncoder(w).Encode(relay.Batch{})
	}))
	defer server.Close()
	ca := filepath.Join(home, "relay-ca.pem")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	store := relay.Store{Directory: filepath.Join(config.StateDir(), "relay")}
	id, err := store.Identity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c := relay.Connection{URL: server.URL, Space: "hot-enroll-space1", Device: id.Public.ID, Token: "first-token", Expires: time.Now().Add(time.Hour).Unix(), CAFile: ca}
	if err = store.SetConnection(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if _, err = config.SetSetting("relay.enabled", json.RawMessage("true"), nil); err != nil {
		t.Fatal(err)
	}
	wait := func(predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !predicate() {
			if time.Now().After(deadline) {
				t.Fatal("runtime did not adopt relay configuration")
			}
			owner.Engine.Notify()
			time.Sleep(20 * time.Millisecond)
		}
	}
	wait(func() bool { return first.Load() > 0 })
	c.Token = "renewed-token"
	if err = store.SetConnection(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return renewed.Load() > 0 })
	if _, err = config.SetSetting("relay.enabled", json.RawMessage("false"), nil); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		var h relay.Health
		return client.Call(ctx, "relay.status", nil, &h) == nil && !h.Connected && h.Error != ""
	})
	before := renewed.Load()
	time.Sleep(100 * time.Millisecond)
	if renewed.Load() != before {
		t.Fatal("disabled runtime continued polling relay")
	}
	var status localruntime.Status
	if err = client.Call(t.Context(), "status", nil, &status); err != nil || status.Epoch != owner.Status().Epoch {
		t.Fatal("enrollment restarted local owner", err)
	}
}
