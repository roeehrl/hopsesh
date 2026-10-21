package host

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// rolloutText is a rollout long enough to span several zstd blocks.
func rolloutText() []byte {
	var b bytes.Buffer
	for i := 0; b.Len() < 600<<10; i++ {
		b.WriteString(`{"timestamp":"2026-10-11T17:02:37.000Z","type":"response_item","payload":{"n":`)
		b.WriteString(strings.Repeat("7", i%13+1))
		b.WriteString("}}\n")
	}
	return b.Bytes()
}

// compress writes text as Codex does: one frame that states its size (stated false: a
// streamed frame that does not), with mtime and mode set.
func compress(t *testing.T, p string, text []byte, stated bool, mod time.Time) {
	t.Helper()
	var out bytes.Buffer
	if stated {
		e, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
		out.Write(e.EncodeAll(text, nil))
	} else {
		e, _ := zstd.NewWriter(&out)
		if _, err := e.Write(text); err != nil {
			t.Fatal(err)
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, out.Bytes(), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestCompressedRolloutReadsAsPlain(t *testing.T) {
	for _, stated := range []bool{true, false} {
		dir := t.TempDir()
		text := rolloutText()
		mod := time.Date(2026, 10, 12, 10, 23, 0, 0, time.UTC)
		compress(t, filepath.Join(dir, "rollout-a.jsonl.zst"), text, stated, mod)
		// Beside a plain file the compressed one is stale: the plain one is in use.
		compress(t, filepath.Join(dir, "rollout-b.jsonl.zst"), []byte("old\n"), true, mod)
		if err := os.WriteFile(filepath.Join(dir, "rollout-b.jsonl"), []byte("new\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		// Other compressed files are not sessions: they stay as they are.
		compress(t, filepath.Join(dir, "notes.txt.zst"), []byte("x"), true, mod)
		fsys := LocalFS()

		ls, err := fsys.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range ls {
			names = append(names, e.Name())
			if e.Name() == "rollout-a.jsonl" && (e.Size() != int64(len(text)) || !e.ModTime().Equal(mod) || e.IsDir()) {
				t.Errorf("stated=%v listed %s: size %d (want %d), mtime %v", stated, e.Name(), e.Size(), len(text), e.ModTime())
			}
		}
		if got := strings.Join(names, " "); got != "notes.txt.zst rollout-a.jsonl rollout-b.jsonl" {
			t.Fatalf("stated=%v listing: %s", stated, got)
		}

		p := filepath.Join(dir, "rollout-a.jsonl")
		fi, err := fsys.Stat(p)
		if err != nil || fi.Name() != "rollout-a.jsonl" || fi.Size() != int64(len(text)) {
			t.Fatalf("stat: %v %v", fi, err)
		}
		if b, err := fsys.ReadFile(p, int64(len(text))); err != nil || !bytes.Equal(b, text) {
			t.Fatalf("read file: %d bytes, %v", len(b), err)
		}
		if _, err := fsys.ReadFile(p, int64(len(text))-1); err == nil {
			t.Fatal("the limit is on the decompressed size")
		}
		if b, _ := fsys.ReadFile(filepath.Join(dir, "rollout-b.jsonl"), 100); string(b) != "new\n" {
			t.Fatalf("the plain file wins: %q", b)
		}

		f, err := fsys.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		// Forward, back, past the end, then a whole sequential read.
		for _, off := range []int64{400 << 10, 10, int64(len(text)) - 5, 0} {
			b := make([]byte, 64)
			n, err := f.ReadAt(b, off)
			want := text[off:min(off+64, int64(len(text)))]
			if !bytes.Equal(b[:n], want) || (n < 64) != errors.Is(err, io.EOF) {
				t.Fatalf("read at %d: %q %v, want %q", off, b[:n], err, want)
			}
		}
		if all, err := io.ReadAll(f); err != nil || !bytes.Equal(all, text) {
			t.Fatalf("sequential read: %d bytes %v", len(all), err)
		}
		if st, _ := f.Stat(); st.Size() != int64(len(text)) {
			t.Fatalf("open file's size %d", st.Size())
		}
		f.Close()
		if real, err := fsys.RealPath(p); err != nil || filepath.Base(real) != "rollout-a.jsonl" {
			t.Fatalf("real path %q %v", real, err)
		}
		if _, err := fsys.Stat(filepath.Join(dir, "rollout-c.jsonl")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("missing session: %v", err)
		}
	}
}

func TestWritingCompressedRolloutUnpacksItFirst(t *testing.T) {
	dir := t.TempDir()
	text := rolloutText()
	mod := time.Date(2026, 10, 12, 10, 23, 0, 0, time.UTC)
	z := filepath.Join(dir, "rollout-a.jsonl.zst")
	p := filepath.Join(dir, "rollout-a.jsonl")
	compress(t, z, text, true, mod)
	fsys := LocalFS()

	before, _ := fsys.Stat(p)
	size := before.Size()
	if err := fsys.Append(p, []byte(`{"mark":1}`+"\n"), agent.AppendOptions{NewLine: true, KeepMtime: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(z); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the compressed file should be gone: %v", err)
	}
	fi, err := os.Stat(p)
	// Windows keeps no Unix permission bits; the mode is checked elsewhere.
	if err != nil || !fi.ModTime().Equal(mod) || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o640 {
		t.Fatalf("unpacked file keeps its mtime and mode: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(p); !bytes.Equal(b, append(append([]byte(nil), text...), `{"mark":1}`+"\n"...)) {
		t.Fatalf("content after append: %d bytes", len(b))
	}
	// Undo of the append (the journal truncates to the size it saw) gives the session back.
	if err := fsys.Truncate(p, size); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); !bytes.Equal(b, text) {
		t.Fatal("truncate to the size seen before the append")
	}
}

func TestCompressedRolloutRenameRemoveTouch(t *testing.T) {
	dir := t.TempDir()
	mod := time.Date(2026, 10, 12, 10, 23, 0, 0, time.UTC)
	compress(t, filepath.Join(dir, "rollout-a.jsonl.zst"), []byte("a\n"), true, mod)
	fsys := LocalFS()
	if err := fsys.Rename(filepath.Join(dir, "rollout-a.jsonl"), filepath.Join(dir, "sub", "rollout-b.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "rollout-b.jsonl.zst")); err != nil {
		t.Fatalf("renamed as it is: %v", err)
	}
	later := mod.Add(time.Hour)
	if err := fsys.Chtimes(filepath.Join(dir, "sub", "rollout-b.jsonl"), later); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "sub", "rollout-b.jsonl.zst")); !fi.ModTime().Equal(later) {
		t.Fatalf("touched the compressed file: %v", fi.ModTime())
	}
	if err := fsys.Remove(filepath.Join(dir, "sub", "rollout-b.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "rollout-b.jsonl.zst")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("removed: %v", err)
	}
}

func TestReplacingCompressedRolloutDropsTheOldForm(t *testing.T) {
	dir := t.TempDir()
	compress(t, filepath.Join(dir, "rollout-a.jsonl.zst"), []byte("a\n"), true, time.Now())
	fsys := LocalFS()
	if err := fsys.WriteFile(filepath.Join(dir, "rollout-a.jsonl"), []byte("b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "rollout-a.jsonl.zst")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the replaced compressed file stays: %v", err)
	}
	if _, ok := fsys.(interface {
		CreateExclusive(string, []byte, fs.FileMode) error
	}); !ok {
		t.Fatal("the wrapped filesystem keeps CreateExclusive")
	}
}
