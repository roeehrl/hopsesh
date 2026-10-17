package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishNewMacAppNeverReplacesAnAppearingDestination(t *testing.T) {
	for _, kind := range []string{"missing", "empty directory", "app directory", "file", "dangling link"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			next, target := filepath.Join(root, "staged.app"), filepath.Join(root, "hopsesh.app")
			if err := os.Mkdir(next, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(next, "new"), []byte("candidate"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "empty directory", "app directory":
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
				if kind == "app directory" {
					if err := os.WriteFile(filepath.Join(target, "old"), []byte("keep"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "file":
				if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "dangling link":
				if err := os.Symlink(filepath.Join(root, "unrelated-missing"), target); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.Lstat(target)
			err := publishMacApp(next, target, false)
			if kind == "missing" {
				if err != nil {
					t.Fatal(err)
				}
				if b, e := os.ReadFile(filepath.Join(target, "new")); e != nil || string(b) != "candidate" {
					t.Fatal("new app not published", e)
				}
				return
			}
			if err == nil {
				t.Fatal("appearing destination was replaced")
			}
			after, statErr := os.Lstat(target)
			if statErr != nil || !os.SameFile(before, after) {
				t.Fatal("refusal changed the destination", statErr)
			}
			if b, e := os.ReadFile(filepath.Join(next, "new")); e != nil || string(b) != "candidate" {
				t.Fatal("refusal lost staged app", e)
			}
		})
	}
}
