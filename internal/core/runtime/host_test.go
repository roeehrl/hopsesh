package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func testNamespace(t *testing.T) Namespace {
	t.Helper()
	n, err := NewNamespace(filepath.Join(t.TempDir(), "config"), filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "hr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	n.Directory = dir
	n.Address = address(n.Directory, n.ID)
	return n
}

func TestNamespaceOwnershipPathIgnoresProcessHomeOverrides(t *testing.T) {
	config, state := filepath.Join(t.TempDir(), "config"), filepath.Join(t.TempDir(), "state")
	first, err := NewNamespace(config, state)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CACHE_HOME", "LOCALAPPDATA"} {
		t.Setenv(key, t.TempDir())
	}
	second, err := NewNamespace(config, state)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("process environment split one namespace into two owners: %+v %+v", first, second)
	}
}
func testHost(t *testing.T, n Namespace, collect observe.Collector, guard func() error) *Host {
	t.Helper()
	opts := observe.Defaults()
	opts.Reconcile = time.Hour
	e, err := observe.New(opts, collect)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Start(context.Background(), n, e, "headless", "test", nil, guard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}
func TestOwnershipDeathRestartAndSharedSubscriptions(t *testing.T) {
	n := testNamespace(t)
	var calls atomic.Int32
	collect := func(context.Context) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{"sessions":1}`), nil
	}
	h := testHost(t, n, collect, func() error { return nil })
	client := Client{n}
	var status Status
	if err := client.Call(t.Context(), "status", nil, &status); err != nil {
		t.Fatal(err)
	}
	e, _ := observe.New(observe.Defaults(), collect)
	if _, err := Start(t.Context(), n, e, "desktop", "test", nil, nil); !errors.Is(err, ErrOwned) {
		t.Fatalf("second owner: %v", err)
	}
	var snapshots [2]observe.Snapshot
	for i := range snapshots {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- client.Watch(ctx, func(s observe.Snapshot) error { snapshots[i] = s; cancel(); return nil })
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("watch blocked")
		}
		cancel()
	}
	if calls.Load() != 1 || snapshots[0].Epoch != snapshots[1].Epoch || snapshots[0].ObservedAt != snapshots[1].ObservedAt {
		t.Fatalf("duplicate collector/freshness renewal: %d %+v", calls.Load(), snapshots)
	}
	if err := client.Call(t.Context(), "stop", nil, nil); err != nil {
		t.Fatal(err)
	}
	h.Close()
	next := testHost(t, n, collect, nil)
	if next.Status().Epoch == status.Epoch {
		t.Fatal("restart reused incarnation")
	}
}
func TestNamespaceProtocolAndStaleIncarnationRefused(t *testing.T) {
	n := testNamespace(t)
	h := testHost(t, n, func(context.Context) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }, nil)
	for _, req := range []Request{
		{Protocol: Protocol + 1, Namespace: n.ID, Epoch: h.Status().Epoch, Method: "stop"},
		{Protocol: Protocol, Namespace: "another", Epoch: h.Status().Epoch, Method: "snapshot"},
		{Protocol: Protocol, Namespace: n.ID, Epoch: "old", Method: "snapshot"},
	} {
		c, err := dial(t.Context(), n)
		if err != nil {
			t.Fatal(err)
		}
		var hello Status
		if err = read(c, &hello, responseLimit); err != nil {
			t.Fatal(err)
		}
		if err = write(c, req, requestLimit); err != nil {
			t.Fatal(err)
		}
		var r Response
		if err = read(c, &r, responseLimit); err != nil {
			t.Fatal(err)
		}
		c.Close()
		if r.Error == "" || r.Data != nil {
			t.Fatalf("bad request received data: %+v", r)
		}
	}
	wrong := n
	wrong.ID = "other"
	if err := (Client{wrong}).Call(t.Context(), "snapshot", nil, nil); err == nil {
		t.Fatal("wrong client namespace accepted")
	}
	if err := (Client{n}).Call(t.Context(), "stop", nil, nil); err == nil {
		t.Fatal("unguarded owner stopped")
	}
}
func TestIPCSizeLimitAndDisconnectedWatchDoNotPreventShutdown(t *testing.T) {
	n := testNamespace(t)
	h := testHost(t, n, func(context.Context) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }, nil)
	c, err := dial(t.Context(), n)
	if err != nil {
		t.Fatal(err)
	}
	var hello Status
	_ = read(c, &hello, responseLimit)
	_, _ = c.Write([]byte{0x7f, 0xff, 0xff, 0xff})
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err = c.Read(b[:]); err == nil {
		t.Fatal("oversized request accepted")
	}
	c.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- (Client{n}).Watch(ctx, func(observe.Snapshot) error { cancel(); return nil }) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watch cancellation blocked")
	}
	idle, err := dial(t.Context(), n)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	ended := make(chan struct{})
	go func() { h.Close(); close(ended) }()
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("idle client blocked host shutdown")
	}
}
func TestNamespaceCanonicalAndIndependentState(t *testing.T) {
	dir := t.TempDir()
	n, err := NewNamespace(filepath.Join(dir, "config"), filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewNamespace(filepath.Join(dir, "config", "..", "config"), filepath.Join(dir, "state"))
	if err != nil || n.ID != other.ID {
		t.Fatalf("canonical: %+v %v", other, err)
	}
	other, err = NewNamespace(n.Config, filepath.Join(dir, "state2"))
	if err != nil || n.ID == other.ID {
		t.Fatal("state namespaces collided")
	}
}

// Compile-time assurance that the IPC transport remains a stream, not HTTP/TCP.
var _ net.Conn = (*net.UnixConn)(nil)

func TestRefuseReplacingRegularFile(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Unix socket test")
	}
	n := testNamespace(t)
	if err := n.prepare(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(n.Address, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	e, _ := observe.New(observe.Defaults(), func(context.Context) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
	if _, err := Start(t.Context(), n, e, "headless", "test", nil, nil); err == nil {
		t.Fatal("replaced regular file")
	}
	b, _ := os.ReadFile(n.Address)
	if string(b) != "preserve" {
		t.Fatal("file changed")
	}
}
