package localstate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBoundedStateReadRefusesLinksAndOversizedData(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(p, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadPrivateFile(p, 7); err != nil || string(b) != "private" {
		t.Fatal(string(b), err)
	}
	if _, err := ReadPrivateFile(p, 6); err == nil {
		t.Fatal("oversized state read")
	}
	link := p + ".link"
	if err := os.Symlink(p, link); err == nil {
		if _, err := ReadPrivateFile(link, 7); err == nil {
			t.Fatal("state link followed")
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(p, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPrivateFile(p, 7); err == nil {
			t.Fatal("public state read")
		}
		if _, err := ReadOwnedFile(p, 7); err != nil {
			t.Fatal("authorized native read", err)
		}
	}
}
