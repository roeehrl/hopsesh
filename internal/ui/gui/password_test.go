package gui

import (
	"context"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
)

// The password dialog round trip, while another call holds App.mu (as a settings change
// might): the answer must arrive without a deadlock, be cached, and a refusal must ask
// again with retry set.
func TestPasswordBroker(t *testing.T) {
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	a := &App{core: app.New(config.Defaults(), nil, t.TempDir(), nil)}
	reqs := make(chan PasswordRequest, 4)
	a.broker().emit = func(r PasswordRequest) { reqs <- r }
	h := config.Host{Name: "nas", Destination: "me@nas", Auth: "password"}
	a.core.Cfg.Hosts = []config.Host{h}
	pf := a.passwordFor(h)

	a.mu.Lock() // a connecting call holds the lock
	got := make(chan string, 1)
	go func() {
		pw, err := pf(context.Background(), "me@nas", false)
		if err != nil {
			t.Error(err)
		}
		got <- pw
	}()
	r := <-reqs
	if r.Machine != "nas" || r.Retry {
		t.Fatalf("request = %+v", r)
	}
	if err := a.ProvidePassword(r.ID, "s3cret", false); err != nil {
		t.Fatal(err)
	}
	select {
	case pw := <-got:
		if pw != "s3cret" {
			t.Fatalf("password = %q", pw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no answer while App.mu was held")
	}
	a.mu.Unlock()

	// Cached: no second question.
	if pw, _ := pf(context.Background(), "me@nas", false); pw != "s3cret" {
		t.Fatalf("cached password = %q", pw)
	}
	// Refused: asked again, with retry; Skip cancels.
	go func() {
		r := <-reqs
		got <- map[bool]string{true: "retry", false: "first"}[r.Retry]
		a.CancelPassword(r.ID)
	}()
	if _, err := pf(context.Background(), "me@nas", true); err == nil {
		t.Fatal("a skipped question should fail")
	}
	if v := <-got; v != "retry" {
		t.Fatalf("second question was %s", v)
	}
	if len(a.PendingPasswords()) != 0 {
		t.Fatal("questions left pending")
	}
}
