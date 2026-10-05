package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A package is used only when its SHA-512 is the pinned one, and an app's arch gets
// conpty.dll for it and the OpenConsole.exe of each machine it runs on.
func TestExtract(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"runtimes/win-x64/native/conpty.dll", "runtimes/win-arm64/native/conpty.dll",
		"build/native/runtimes/x64/OpenConsole.exe", "build/native/runtimes/arm64/OpenConsole.exe", "build/native/runtimes/x86/OpenConsole.exe"} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte("bytes of " + name))
	}
	_ = zw.Close()
	pkg := buf.Bytes()
	sum := sha512.Sum512(pkg)
	if err := check(pkg, base64.StdEncoding.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if err := check(pkg, SHA512); err == nil {
		t.Fatal("a package with another hash passed")
	}
	for arch, want := range map[string]map[string]string{
		"amd64": {"conpty.dll": "win-x64", "x64/OpenConsole.exe": "x64/", "arm64/OpenConsole.exe": "arm64/"},
		"arm64": {"conpty.dll": "win-arm64", "arm64/OpenConsole.exe": "arm64/"},
	} {
		out := t.TempDir()
		if err := extract(pkg, arch, out); err != nil {
			t.Fatal(err)
		}
		for p, from := range want {
			b, err := os.ReadFile(filepath.Join(out, p))
			if err != nil || !strings.Contains(string(b), from) {
				t.Errorf("%s %s: %q %v", arch, p, b, err)
			}
		}
		if _, err := os.Stat(filepath.Join(out, "x86")); err == nil {
			t.Error("x86 host written")
		}
		if b, _ := os.ReadFile(filepath.Join(out, "LICENSE-conpty.txt")); !strings.Contains(string(b), "MIT License") || !strings.Contains(string(b), Version) {
			t.Error("no licence notice")
		}
	}
	if err := extract(pkg, "386", t.TempDir()); err == nil {
		t.Error("an unknown arch")
	}
}
