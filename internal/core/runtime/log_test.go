package runtime

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeLogRotationBoundsArchivesAndUsesOneWriter(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-state")
	w, err := OpenLog(root)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err = OpenLog(root); err == nil {
		t.Fatal("second logger acquired ownership")
	}
	block := bytes.Repeat([]byte("x"), LogBytes/2)
	for range 20 {
		if _, err = w.Write(block); err != nil {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob(filepath.Join(root, "runtime.log*"))
	if err != nil || len(files) != 4 {
		t.Fatal("unbounded archives", files, err)
	}
	for _, p := range files {
		st, err := os.Stat(p)
		if err != nil || st.Size() > LogBytes {
			t.Fatal("log exceeds bound", err)
		}
	}
	if _, err = w.Write(bytes.Repeat([]byte("x"), LogBytes+1)); err == nil {
		t.Fatal("oversized log entry accepted")
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write(block); err == nil {
		t.Fatal("write after shutdown")
	}
	next, err := OpenLog(root)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
}
func TestRuntimeLogRejectsSymlinkWithoutChangingItsTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "runtime.log")); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	if _, err := OpenLog(root); err == nil {
		t.Fatal("log followed a symbolic link")
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "keep" {
		t.Fatal("log changed symlink target", err)
	}
}
