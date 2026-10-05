package gui

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"strings"
	"testing"
)

// The terminal window's xterm.js is the pinned packages' own build, as
// internal/devtools/xtermfetch wrote it: every file in VERSIONS is there with its hash,
// nothing else is, the licence comes along, and the page loads nothing from elsewhere.
func TestVendoredXterm(t *testing.T) {
	const dir = "assets/terminal/vendor"
	b, err := fs.ReadFile(Assets, dir+"/VERSIONS")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{"VERSIONS": true, "LICENSE-xterm.txt": true}
	for _, line := range strings.Split(string(b), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "package ") {
			continue
		}
		f, err := fs.ReadFile(Assets, dir+"/"+name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := sha256.Sum256(f)
		if hex.EncodeToString(got[:]) != sum {
			t.Errorf("%s was changed since xtermfetch wrote it", name)
		}
		listed[name] = true
	}
	if !strings.Contains(string(b), "package @xterm/xterm@6.0.0") {
		t.Error("not xterm.js 6.0.0")
	}
	entries, _ := fs.ReadDir(Assets, dir)
	for _, e := range entries {
		if !listed[e.Name()] {
			t.Errorf("%s is not in VERSIONS", e.Name())
		}
	}
	lic, _ := fs.ReadFile(Assets, dir+"/LICENSE-xterm.txt")
	if !strings.Contains(string(lic), "Permission is hereby granted, free of charge") {
		t.Error("no MIT licence")
	}
	page, _ := fs.ReadFile(Assets, "assets/terminal/index.html")
	for _, bad := range []string{"http://", "https://", "<script>", "style=", "onclick"} {
		if strings.Contains(string(page), bad) {
			t.Errorf("the terminal page has %q", bad)
		}
	}
	js, _ := fs.ReadFile(Assets, "assets/terminal/terminal.js")
	for _, bad := range []string{"readText", "clipboard.read", "innerHTML", "eval("} {
		if strings.Contains(string(js), bad) {
			t.Errorf("the terminal page's script has %q", bad)
		}
	}
}
