package observe

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFilesNotifiesForWritesAndMissingRootCreation(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "new", "sessions")
	events := make(chan struct{}, 100)
	f, err := NewFiles(100, func() {
		select {
		case events <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.SetRoots([]string{root}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	select {
	case <-events:
	case <-time.After(5 * time.Second):
		t.Fatal("missing root creation was not observed")
	}
	if err := f.SetRoots([]string{root}); err != nil {
		t.Fatal(err)
	}
	for len(events) > 0 {
		<-events
	}
	if err := os.WriteFile(filepath.Join(root, "session.jsonl"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-events:
	case <-time.After(5 * time.Second):
		t.Fatal("transcript creation was not observed")
	}
	if err := f.SetRoots(nil); err != nil {
		t.Fatal(err)
	}
	if len(f.w.WatchList()) != 0 {
		t.Fatal("obsolete watches retained")
	}
}

func TestFilesBoundedAndSymlinksNotFollowed(t *testing.T) {
	base := t.TempDir()
	for _, d := range []string{"a", "b", "c"} {
		if err := os.Mkdir(filepath.Join(base, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	f, err := NewFiles(2, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.SetRoots([]string{base}); err == nil {
		t.Fatal("expected explicit watch coverage failure")
	}
	if len(f.w.WatchList()) > 2 {
		t.Fatal("unbounded directory watches")
	}
	if err := f.SetRoots([]string{"relative"}); err == nil {
		t.Fatal("relative watch root accepted")
	}
	target := t.TempDir()
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := f.SetRoots([]string{link}); err != nil {
		t.Fatal(err)
	}
	if len(f.w.WatchList()) != 0 {
		t.Fatal("followed a directory symlink")
	}
}
