package appinstall

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectionNeverRunsCandidateAndRefusesIncompleteOrForeignBundle(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "hopsesh.app")
	contents := filepath.Join(app, "Contents")
	bin := filepath.Join(contents, "MacOS", "hopsesh-app")
	cli := filepath.Join(contents, "Resources", "bin", "hopsesh")
	for _, p := range []string{bin, cli} {
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("must never execute"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	metadata := `<plist><dict><key>CFBundleIdentifier</key><string>io.github.roeehrl.hopsesh</string><key>CFBundleShortVersionString</key><string>0.5.0-test</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(metadata), 0600); err != nil {
		t.Fatal(err)
	}
	s := Inspect(app, "darwin")
	if !s.Installed || s.Version != "0.5.0-test" || s.CLI != cli {
		t.Fatalf("valid: %+v", s)
	}
	if err := os.Remove(cli); err != nil {
		t.Fatal(err)
	}
	if Inspect(app, "darwin").Installed {
		t.Fatal("incomplete app reported installed")
	}
	if err := os.WriteFile(cli, []byte("still never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(`<plist><dict><key>CFBundleIdentifier</key><string>foreign.app</string></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	if Inspect(app, "darwin").Installed {
		t.Fatal("foreign bundle reported installed")
	}
}
