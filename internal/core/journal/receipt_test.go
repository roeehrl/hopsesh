package journal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

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
