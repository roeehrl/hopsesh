package gui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
)

// Other agents write their sessions constantly. While nobody looks at hopsesh, a burst
// of writes must not turn into a rescan per write (it once rescanned 300 ms after every
// write, keeping a busy machine's app at a full core).
func TestUnattendedWritesDoNotRescanEachTime(t *testing.T) {
	a, f := desktopApp(t)
	f.attended.Store(false)
	if _, err := a.InitialScan(); err != nil {
		t.Fatal(err)
	}
	var session string
	_ = filepath.Walk(filepath.Join(os.Getenv("HOME"), ".claude", "projects"), func(p string, fi os.FileInfo, err error) error {
		if err == nil && session == "" && filepath.Ext(p) == ".jsonl" {
			session = p
		}
		return nil
	})
	if session == "" {
		t.Fatal("no demo session to write to")
	}
	time.Sleep(time.Second) // the watcher is set up and the first refresh is done
	before := app.ResourceCounters().Scans
	for i := range 20 {
		fh, err := os.OpenFile(session, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fh.WriteString(`{"type":"system","uuid":"idle-` + string(rune('a'+i)) + `","sessionId":"x"}` + "\n")
		fh.Close()
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)
	if n := app.ResourceCounters().Scans - before; n > 1 {
		t.Fatalf("20 writes while unattended caused %d rescans, want at most 1", n)
	}
}
