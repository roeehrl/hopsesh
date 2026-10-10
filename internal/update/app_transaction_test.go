package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func recoverWindowsApp(dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return recoverWindowsAppRoot(root)
}

func TestWindowsMultiFilePublicationFailureRestoresWholeInstall(t *testing.T) {
	dir := t.TempDir()
	names := []string{"hopsesh-app.exe", "hopsesh.exe", "conpty/conpty.dll"}
	files := map[string][]byte{}
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(path), 0755)
		if err := os.WriteFile(path, []byte("old "+name), 0755); err != nil {
			t.Fatal(err)
		}
		files[name] = []byte("new " + name)
	}
	failed := false
	err := publishWindowsAppFiles(dir, files, names, func(root *os.Root, from, to string) error {
		if !failed && to == "hopsesh.exe" {
			failed = true
			return errors.New("simulated rename denial after first publication")
		}
		return root.Rename(from, to)
	})
	if err == nil {
		t.Fatal("publication failure ignored")
	}
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil || string(body) != "old "+name {
			t.Fatalf("mixed installation after failure: %s %q %v", name, body, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, appTransactionFile)); !os.IsNotExist(err) {
		t.Fatal("successful rollback left pending transaction", err)
	}
}

func TestWindowsInterruptedPublicationRestoresOldFilesAndRemovesNewLibrary(t *testing.T) {
	dir := t.TempDir()
	names := []string{"hopsesh-app.exe", "conpty/conpty.dll", "hopsesh.exe"}
	files := map[string][]byte{}
	for _, name := range names {
		files[name] = []byte("new " + name)
		if name == "conpty/conpty.dll" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old "+name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("failure injection did not interrupt publication")
			}
		}()
		_ = publishWindowsAppFiles(dir, files, names, func(root *os.Root, from, to string) error {
			err := root.Rename(from, to)
			if err == nil && to == filepath.Join("conpty", "conpty.dll") {
				panic("simulated process exit")
			}
			return err
		})
	}()
	if err := recoverWindowsApp(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hopsesh-app.exe", "hopsesh.exe"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(body) != "old "+name {
			t.Fatal("old program not recovered", name, string(body), err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "conpty", "conpty.dll")); !os.IsNotExist(err) {
		t.Fatal("new library survived rollback", err)
	}
	if err := recoverWindowsApp(dir); err != nil {
		t.Fatal("recovery not idempotent", err)
	}
}

func TestRecoveryRefusesLinkedOrOversizedManifestBeforeMutation(t *testing.T) {
	for _, name := range []string{"linked", "oversized"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			live := filepath.Join(dir, "hopsesh.exe")
			if err := os.WriteFile(live, []byte("current"), 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, appTransactionFile)
			if name == "linked" {
				other := filepath.Join(t.TempDir(), "manifest")
				if err := os.WriteFile(other, []byte(`{"stage":".hopsesh-app-stage-test","files":["hopsesh.exe"],"existed":{}}`), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, path); err != nil {
					t.Skip("OS does not permit a test symlink")
				}
			} else if err := os.WriteFile(path, make([]byte, 16385), 0600); err != nil {
				t.Fatal(err)
			}
			if err := recoverWindowsApp(dir); err == nil {
				t.Fatal("unsafe manifest accepted")
			}
			if data, err := os.ReadFile(live); err != nil || string(data) != "current" {
				t.Fatal("invalid manifest altered the install", string(data), err)
			}
		})
	}
}

func TestPublicationAndRecoveryCannotEscapeChangedLibraryDirectory(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(fmt.Sprintf("during-publication-%t", during), func(t *testing.T) {
			dir, outside := t.TempDir(), t.TempDir()
			libraryDir := filepath.Join(dir, "conpty")
			if err := os.Mkdir(libraryDir, 0755); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{filepath.Join(dir, "hopsesh.exe"), filepath.Join(libraryDir, "conpty.dll"), filepath.Join(outside, "conpty.dll"), filepath.Join(outside, "conpty.dll.old")} {
				if err := os.WriteFile(path, []byte("old"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			linkDirectory := func() {
				if err := os.Rename(libraryDir, libraryDir+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, libraryDir); err != nil {
					t.Skip("OS does not permit a test directory symlink")
				}
			}
			if !during {
				linkDirectory()
			}
			swapped := false
			err := publishWindowsAppFiles(dir, map[string][]byte{"hopsesh.exe": []byte("new"), "conpty/conpty.dll": []byte("new")}, []string{"hopsesh.exe", "conpty/conpty.dll"}, func(root *os.Root, from, to string) error {
				err := root.Rename(from, to)
				if during && !swapped && err == nil {
					swapped = true
					linkDirectory()
				}
				return err
			})
			if err == nil {
				t.Fatal("directory substitution escaped installation containment")
			}
			for _, name := range []string{"conpty.dll", "conpty.dll.old"} {
				if body, err := os.ReadFile(filepath.Join(outside, name)); err != nil || string(body) != "old" {
					t.Fatal("publication or recovery changed an outside file", name, string(body), err)
				}
			}
			if body, err := os.ReadFile(filepath.Join(dir, "hopsesh.exe")); err != nil || string(body) != "old" {
				t.Fatal("rollback did not restore the first program", string(body), err)
			}
			if err := os.Remove(libraryDir); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(libraryDir+".saved", libraryDir); err != nil {
				t.Fatal(err)
			}
			if err := recoverWindowsApp(dir); err != nil {
				t.Fatal("repair could not recover the retained transaction", err)
			}
		})
	}
}
