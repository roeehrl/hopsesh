package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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
	err := publishWindowsAppFiles(dir, files, names, func(from, to string) error {
		if !failed && to == filepath.Join(dir, "hopsesh.exe") {
			failed = true
			return errors.New("simulated rename denial after first publication")
		}
		return os.Rename(from, to)
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
		_ = publishWindowsAppFiles(dir, files, names, func(from, to string) error {
			err := os.Rename(from, to)
			if err == nil && to == filepath.Join(dir, "conpty", "conpty.dll") {
				panic("simulated process exit")
			}
			return err
		})
	}()
	if err := recoverWindowsApp(dir, os.Rename); err != nil {
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
	if err := recoverWindowsApp(dir, os.Rename); err != nil {
		t.Fatal("recovery not idempotent", err)
	}
}
