package journal

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestCloudCheckpointBranchTransitionRecoversAndUndoesWithoutNativeFiles(t *testing.T) {
	for _, mode := range []string{"recover and undo", "ordinary receipt", "wrong task", "other endpoint", "other profile", "other agent", "concurrent sibling", "native file"} {
		t.Run(mode, func(t *testing.T) {
			state, data := t.TempDir(), t.TempDir()
			native := filepath.Join(data, "source.jsonl")
			path := lineage.PathFor(native)
			base := lineage.New("private-task-family")
			root := lineage.Replica{Endpoint: "cloud:claude-hosted:task", Binding: "task", Location: "cloud", Key: agent.SessionKey{Agent: "claude", Profile: "task", Session: "native"}}
			base.Upsert(root)
			before := base.Encode()
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
			next := base.Clone()
			next.Branch = next.Fork("reviewed-rewrite", nil)
			root.Line = next.Branch
			if mode == "other endpoint" {
				root.Endpoint = "cloud:claude-hosted:someone-else"
			}
			if mode == "other profile" {
				root.Key.Profile = "other-profile"
			}
			if mode == "other agent" {
				root.Key.Agent = "codex"
			}
			next.Upsert(root)
			if mode == "concurrent sibling" {
				current := base.Clone()
				current.Branch = current.Fork("another-reviewed-rewrite", nil)
				root.Line = current.Branch
				current.Upsert(root)
				before = current.Encode()
				if err := os.WriteFile(path, before, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "native file" {
				if err := os.WriteFile(native, []byte("untouched native history"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			j, err := New(state, KindContinue, "reviewed private task fork")
			if err != nil {
				t.Fatal(err)
			}
			if mode != "recover and undo" {
				if mode == "ordinary receipt" {
					err = j.WriteReceipt(host.LocalFS(), "local", path, next.Encode(), false)
				} else {
					task := "task"
					if mode == "wrong task" {
						task = "other-task"
					}
					err = j.WriteCloudCheckpointReceipt(host.LocalFS(), "local", path, next.Encode(), task)
				}
				if err == nil {
					t.Fatal("unapproved branch replacement accepted")
				}
				body, e := os.ReadFile(path)
				if e != nil || !bytes.Equal(body, before) {
					t.Fatal("refusal changed ledger", e)
				}
				if mode == "native file" {
					body, e := os.ReadFile(native)
					if e != nil || string(body) != "untouched native history" {
						t.Fatal("refusal changed native history", e)
					}
				}
				return
			}
			offline := &failingReceiptFS{FS: host.LocalFS(), fail: true}
			if err = j.WriteCloudCheckpointReceipt(offline, "local", path, next.Encode(), "task"); err == nil {
				t.Fatal("write fault not exercised")
			}
			j, err = Load(state, j.ID)
			if err != nil || !j.PendingReceipts() {
				t.Fatal("transition intention was not durable", err)
			}
			reach := func(string) (host.FS, error) { return host.LocalFS(), nil }
			for range 2 {
				if err = j.RecoverReceipts(reach); err != nil {
					t.Fatal(err)
				}
			}
			actual, err := lineage.Read(host.LocalFS(), native)
			if err != nil || actual.Branch != next.Branch || j.PendingReceipts() {
				t.Fatal("recovery lost branch selection", err)
			}
			if _, err = os.Stat(native); !os.IsNotExist(err) {
				t.Fatal("private ledger created a vendor transcript")
			}
			if err = j.Seal(reach); err != nil {
				t.Fatal(err)
			}
			if err = j.Undo(t.Context(), Files(reach), false); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(body, before) {
				t.Fatal("undo did not restore original private branch", err)
			}
		})
	}
}

type failingReceiptFS struct {
	host.FS
	fail bool
}

func (f *failingReceiptFS) WriteFile(path string, b []byte, perm os.FileMode) error {
	if f.fail {
		f.fail = false
		return errors.New("source offline")
	}
	return f.FS.WriteFile(path, b, perm)
}
func TestReceiptAcknowledgementRecoversWithoutNativeWrite(t *testing.T) {
	state, data := t.TempDir(), t.TempDir()
	native := filepath.Join(data, "conversation.jsonl")
	os.WriteFile(native, []byte("native bytes"), 0600)
	m := lineage.New("family")
	m.Upsert(lineage.Replica{Key: agent.SessionKey{Agent: "claude", Session: "session"}, Endpoint: "source", Location: "source"})
	j, _ := New(state, KindMove, "receipt")
	offline := &failingReceiptFS{FS: host.LocalFS(), fail: true}
	if err := j.WriteReceipt(offline, "source", lineage.PathFor(native), m.Encode(), false); err == nil {
		t.Fatal("fault not triggered")
	}
	disk, err := Load(state, j.ID)
	if err != nil || !disk.PendingReceipts() {
		t.Fatal("pending ack not durable", err)
	}
	if err = disk.RecoverReceipts(func(string) (host.FS, error) { return host.LocalFS(), nil }); err != nil {
		t.Fatal(err)
	}
	if disk.PendingReceipts() {
		t.Fatal("ack still pending")
	}
	raw, _ := os.ReadFile(native)
	if string(raw) != "native bytes" {
		t.Fatal("ack recovery rewrote transcript")
	}
	if err = disk.RecoverReceipts(func(string) (host.FS, error) { return host.LocalFS(), nil }); err != nil {
		t.Fatal("duplicate ack", err)
	}
}

func TestReceiptUnionPreservesOtherDestinationAcknowledgement(t *testing.T) {
	state, data := t.TempDir(), t.TempDir()
	native := filepath.Join(data, "native.jsonl")
	os.WriteFile(native, []byte("native"), 0600)
	base := lineage.New("receipt-union")
	key := agent.SessionKey{Agent: "claude", Session: "original"}
	base.Upsert(lineage.Replica{Endpoint: "A", Location: "A", Key: key})
	first, second := base.Clone(), base.Clone()
	first.Upsert(lineage.Replica{Endpoint: "B", Location: "B", Key: key})
	second.Upsert(lineage.Replica{Endpoint: "C", Location: "C", Key: key})
	j1, _ := New(state, KindMove, "first")
	j2, _ := New(state, KindMove, "second")
	if err := j1.WriteReceipt(host.LocalFS(), "A", lineage.PathFor(native), first.Encode(), false); err != nil {
		t.Fatal(err)
	}
	// Both bodies were created from the same old source metadata.
	if err := j2.WriteReceipt(host.LocalFS(), "A", lineage.PathFor(native), second.Encode(), false); err != nil {
		t.Fatal(err)
	}
	actual, err := lineage.Read(host.LocalFS(), native)
	if err != nil || len(actual.Replicas) != 3 {
		t.Fatalf("another acknowledgement was lost: %v %+v", err, actual)
	}
	if _, err = os.Stat(lineage.PathFor(native) + ".receipt-lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("receipt lock was left behind")
	}
}

func TestReceiptLockKeepsBusyAckDurableAndRecoversOwnCrash(t *testing.T) {
	state, data := t.TempDir(), t.TempDir()
	native := filepath.Join(data, "native.jsonl")
	os.WriteFile(native, []byte("native"), 0600)
	path := lineage.PathFor(native)
	m := lineage.New("lock-test")
	m.Upsert(lineage.Replica{Endpoint: "A", Location: "A", Key: agent.SessionKey{Agent: "claude", Session: "original"}})
	j, _ := New(state, KindMove, "locked receipt")
	os.WriteFile(path+".receipt-lock", []byte("another-operation"), 0600)
	if err := j.WriteReceipt(host.LocalFS(), "A", path, m.Encode(), false); err == nil || !j.PendingReceipts() {
		t.Fatal("busy receipt was neither blocked nor made recoverable")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("locked metadata was overwritten")
	}
	// Simulate this operation's crash after acquiring ownership, before writing.
	os.WriteFile(path+".receipt-lock", []byte(j.ID), 0600)
	disk, err := Load(state, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = disk.RecoverReceipts(func(string) (host.FS, error) { return host.LocalFS(), nil }); err != nil {
		t.Fatal(err)
	}
	if disk.PendingReceipts() {
		t.Fatal("owned stale lock could not recover")
	}
}

func TestReceiptRetryDoesNotBlessLaterNativeWorkForUndo(t *testing.T) {
	state, data := t.TempDir(), t.TempDir()
	native := filepath.Join(data, "native.jsonl")
	j, _ := New(state, KindMove, "ack pending")
	if err := j.WriteFile(host.LocalFS(), "A", native, []byte("native original"), 0600); err != nil {
		t.Fatal(err)
	}
	m := lineage.New("later-native")
	m.Upsert(lineage.Replica{Endpoint: "A", Location: "A", Key: agent.SessionKey{Agent: "claude", Session: "original"}})
	offline := &failingReceiptFS{FS: host.LocalFS(), fail: true}
	if err := j.WriteReceipt(offline, "A", lineage.PathFor(native), m.Encode(), false); err == nil {
		t.Fatal("fault not triggered")
	}
	if err := j.Seal(func(string) (host.FS, error) { return host.LocalFS(), nil }); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(native, []byte("native original plus later authored work"), 0600)
	if err := j.RecoverReceipts(func(string) (host.FS, error) { return host.LocalFS(), nil }); err != nil {
		t.Fatal(err)
	}
	if err := j.Changed(context.Background(), Files(func(string) (host.FS, error) { return host.LocalFS(), nil })); !errors.Is(err, ErrChanged) {
		t.Fatal("metadata recovery blessed later native work for destructive undo", err)
	}
}

func TestUnreachableSourceReceiptIsDurableBeforeConnection(t *testing.T) {
	state, data := t.TempDir(), t.TempDir()
	native := filepath.Join(data, "conversation.jsonl")
	if err := os.WriteFile(native, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	m := lineage.New("offline")
	j, err := New(state, KindMove, "offline source")
	if err != nil {
		t.Fatal(err)
	}
	if err = j.WriteReceipt(nil, "source", lineage.PathFor(native), m.Encode(), false); err == nil {
		t.Fatal("unreachable receipt claimed applied")
	}
	j, err = Load(state, j.ID)
	if err != nil || !j.PendingReceipts() {
		t.Fatal("lost notice receipt", err)
	}
	if err = j.RecoverReceipts(func(string) (host.FS, error) { return host.LocalFS(), nil }); err != nil {
		t.Fatal(err)
	}
	if j.PendingReceipts() {
		t.Fatal("receipt not delivered")
	}
	b, _ := os.ReadFile(native)
	if string(b) != "untouched" {
		t.Fatal("recovery changed native history")
	}
}
