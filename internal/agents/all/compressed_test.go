package all

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// copyTree copies a fixture folder; compress true writes each rollout as Codex compresses
// it (rollout-….jsonl.zst, mtime kept, the plain file gone).
func copyTree(t *testing.T, from, to string, compress bool) {
	t.Helper()
	enc, _ := zstd.NewWriter(nil)
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if compress && strings.HasPrefix(d.Name(), "rollout-") {
			b, dst = enc.EncodeAll(b, nil), dst+".zst"
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			return err
		}
		fi, _ := d.Info()
		return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	})
	if err != nil {
		t.Fatal(err)
	}
}

type codexView struct {
	Sessions []agent.Summary
	Reads    map[agent.SessionID]ir.Segment
}

func viewCodex(t *testing.T, root string) codexView {
	t.Helper()
	ctx := context.Background()
	m := codex.New()
	machine := &host.Machine{Local: true, Name: "local", Facts: host.Facts{OS: "darwin", Home: root, Endpoint: "local"}}
	in := agent.Install{Agent: "codex", Roots: map[string]string{"home": filepath.Join(root, ".codex")}}
	h, err := machine.For(ctx, m.Spec(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.List(ctx, h, in)
	if err != nil || len(l.Errors) > 0 {
		t.Fatalf("list: %v %+v", err, l.Errors)
	}
	v := codexView{Reads: map[agent.SessionID]ir.Segment{}}
	for _, s := range l.Sessions {
		seg, err := m.Read(ctx, h, in, s, ir.Cursor{})
		if err != nil {
			t.Fatalf("read %s: %v", s.Key.Session, err)
		}
		v.Reads[s.Key.Session] = seg
		s.Path = strings.TrimPrefix(s.Path, root)
		v.Sessions = append(v.Sessions, s)
	}
	return v
}

// Codex compresses rollouts it has not written for a week. hopsesh lists and reads them
// exactly as it does the plain files, by the same paths.
func TestCodexCompressedRolloutsAreTheSameSessions(t *testing.T) {
	plain, packed := t.TempDir(), t.TempDir()
	fixture := filepath.Join("..", "..", "..", "agents", "codex", "testdata", "0.153.2")
	copyTree(t, fixture, filepath.Join(plain, ".codex"), false)
	copyTree(t, fixture, filepath.Join(packed, ".codex"), true)

	want, got := viewCodex(t, plain), viewCodex(t, packed)
	if len(want.Sessions) == 0 {
		t.Fatal("the fixture lists no sessions")
	}
	wb, _ := json.Marshal(want)
	gb, _ := json.Marshal(got)
	if string(wb) != string(gb) {
		t.Fatalf("compressed rollouts differ from plain ones:\nplain:  %s\npacked: %s", wb, gb)
	}
	for _, s := range got.Sessions {
		if !strings.HasSuffix(s.Path, ".jsonl") {
			t.Fatalf("a session keeps its plain path: %s", s.Path)
		}
	}
}
