package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/fsys"
)

func TestTurnsAfterMoveMark(t *testing.T) {
	f := filepath.Join(t.TempDir(), "s.jsonl")
	msg := func(ts string) string {
		return `{"type":"user","uuid":"u","sessionId":"s","timestamp":"` + ts + `","message":{"role":"user","content":"x"}}` + "\n"
	}
	mark := `{"type":"custom-title","customTitle":"↪ moved to laptop · x","sessionId":"s"}` + "\n"
	write := func(s string) { os.WriteFile(f, []byte(s), 0o600) }

	write(msg("2026-10-01T10:00:00Z"))
	if n, _ := TurnsAfterMoveMark(fsys.Local{}, f); n != -1 {
		t.Fatalf("no mark: %d", n)
	}
	write(msg("2026-10-01T10:00:00Z") + mark)
	if n, _ := TurnsAfterMoveMark(fsys.Local{}, f); n != 0 {
		t.Fatalf("clean mark: %d", n)
	}
	write(msg("2026-10-01T10:00:00Z") + mark + msg("2026-10-01T12:00:00Z") + msg("2026-10-01T12:01:00Z"))
	if n, _ := TurnsAfterMoveMark(fsys.Local{}, f); n != 2 {
		t.Fatalf("kept working: %d", n)
	}
	if n, _ := TurnsSince(fsys.Local{}, f, time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)); n != 2 {
		t.Fatalf("since: %d", n)
	}
}
