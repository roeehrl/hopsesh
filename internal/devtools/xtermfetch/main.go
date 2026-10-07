// Command xtermfetch vendors xterm.js and the addons the app's terminal window uses into
// internal/ui/gui/assets/terminal/vendor: the npm packages pinned below (MIT, from
// https://github.com/xtermjs/xterm.js), each checked against the registry's published
// SHA-512 before anything is taken out of it. It writes each package's ES module build
// (lib/*.mjs, minified), xterm.js's stylesheet, the licence (LICENSE-xterm.txt: every
// package's notice) and VERSIONS, which lists each file with its SHA-256 (a test checks the
// folder against it). The app loads them from its own assets: no CDN.
//
//	go run ./internal/devtools/xtermfetch
//
// Bumping a version: change it and its integrity (npm view <pkg>@<version>
// dist.integrity) here, run this, and commit the folder.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// pkg is one pinned npm package and the files the app takes from it (path in the package →
// name in vendor/).
type pkg struct {
	Name, Version, Integrity string
	Files                    map[string]string
}

// The pinned packages: xterm.js 6.0.0 and the addons released with it (2025-12-22).
var pkgs = []pkg{
 {"@xterm/addon-serialize", "0.14.0", "sha512-uteyTU1EkrQa2Ux6P/uFl2fzmXI46jy5uoQMKEOM0fKTyiW7cSn0WrFenHm5vO5uEXX/GpwW/FgILvv3r0WbkA==", map[string]string{"lib/addon-serialize.mjs": "addon-serialize.mjs"}},
	{"@xterm/xterm", "6.0.0", "sha512-TQwDdQGtwwDt+2cgKDLn0IRaSxYu1tSUjgKarSDkUM0ZNiSRXFpjxEsvc/Zgc5kq5omJ+V0a8/kIM2WD3sMOYg==",
		map[string]string{"lib/xterm.mjs": "xterm.mjs", "css/xterm.css": "xterm.css"}},
	{"@xterm/addon-fit", "0.11.0", "sha512-jYcgT6xtVYhnhgxh3QgYDnnNMYTcf8ElbxxFzX0IZo+vabQqSPAjC3c1wJrKB5E19VwQei89QCiZZP86DCPF7g==",
		map[string]string{"lib/addon-fit.mjs": "addon-fit.mjs"}},
	{"@xterm/addon-webgl", "0.19.0", "sha512-b3fMOsyLVuCeNJWxolACEUED0vm7qC0cy4wRvf3oURSzDTYVQiGPhTnhWZwIHdvC48Y+oLhvYXnY4XDXPoJo6A==",
		map[string]string{"lib/addon-webgl.mjs": "addon-webgl.mjs"}},
	{"@xterm/addon-unicode11", "0.9.0", "sha512-FxDnYcyuXhNl+XSqGZL/t0U9eiNb/q3EWT5rYkQT/zuig8Gz/VagnQANKHdDWFM2lTMk9ly0EFQxxxtZUoRetw==",
		map[string]string{"lib/addon-unicode11.mjs": "addon-unicode11.mjs"}},
	{"@xterm/addon-web-links", "0.12.0", "sha512-4Smom3RPyVp7ZMYOYDoC/9eGJJJqYhnPLGGqJ6wOBfB8VxPViJNSKdgRYb8NpaM6YSelEKbA2SStD7lGyqaobw==",
		map[string]string{"lib/addon-web-links.mjs": "addon-web-links.mjs"}},
	{"@xterm/addon-search", "0.16.0", "sha512-9OeuBFu0/uZJPu+9AHKY6g/w0Czyb/Ut0A5t79I4ULoU4IfU5BEpPFVGQxP4zTTMdfZEYkVIRYbHBX1xWwjeSA==",
		map[string]string{"lib/addon-search.mjs": "addon-search.mjs"}},
}

const maxSize = 8 << 20

func main() {
	out := flag.String("out", "internal/ui/gui/assets/terminal/vendor", "the folder to write")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	var license strings.Builder
	license.WriteString("The app's terminal window uses xterm.js and its addons (https://github.com/xtermjs/xterm.js),\nunder the MIT License. These files are the packages' own builds, unchanged.\n")
	sums := map[string]string{}
	var versions []string
	for _, p := range pkgs {
		files, lic, err := fetch(p)
		if err != nil {
			log.Fatalf("%s@%s: %v", p.Name, p.Version, err)
		}
		for name, b := range files {
			if err := os.WriteFile(filepath.Join(*out, name), b, 0o644); err != nil { //nolint:gosec // vendored web assets, readable by all
				log.Fatal(err)
			}
			s := sha256.Sum256(b)
			sums[name] = hex.EncodeToString(s[:])
		}
		versions = append(versions, p.Name+"@"+p.Version)
		fmt.Fprintf(&license, "\n---------------------------------------------------------------------\n%s %s\n\n%s", p.Name, p.Version, lic)
	}
	if err := os.WriteFile(filepath.Join(*out, "LICENSE-xterm.txt"), []byte(license.String()), 0o644); err != nil { //nolint:gosec // a licence notice
		log.Fatal(err)
	}
	var v strings.Builder
	v.WriteString("# xterm.js and addons vendored by internal/devtools/xtermfetch; the files' SHA-256 below.\n")
	for _, s := range versions {
		v.WriteString("package " + s + "\n")
	}
	names := make([]string, 0, len(sums))
	for n := range sums {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&v, "%s  %s\n", sums[n], n)
	}
	if err := os.WriteFile(filepath.Join(*out, "VERSIONS"), []byte(v.String()), 0o644); err != nil { //nolint:gosec // a manifest
		log.Fatal(err)
	}
	log.Printf("wrote %d files to %s", len(sums)+2, *out)
}

// fetch downloads a package, checks its integrity, and returns the files the app takes
// and its licence text.
func fetch(p pkg) (map[string][]byte, string, error) {
	base := path.Base(p.Name)
	url := "https://registry.npmjs.org/" + p.Name + "/-/" + base + "-" + p.Version + ".tgz"
	c := &http.Client{Timeout: 2 * time.Minute}
	resp, err := c.Get(url) //nolint:noctx // a developer tool
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	tgz, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, "", err
	}
	if len(tgz) > maxSize {
		return nil, "", errors.New("the package is larger than expected")
	}
	sum := sha512.Sum512(tgz)
	if got := "sha512-" + base64.StdEncoding.EncodeToString(sum[:]); got != p.Integrity {
		return nil, "", fmt.Errorf("integrity %s, want %s", got, p.Integrity)
	}
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, "", err
	}
	tr := tar.NewReader(zr)
	files := map[string][]byte{}
	lic := ""
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", err
		}
		rel := strings.TrimPrefix(h.Name, "package/")
		want, take := p.Files[rel]
		if !take && rel != "LICENSE" {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxSize))
		if err != nil {
			return nil, "", err
		}
		if rel == "LICENSE" {
			lic = string(b)
			continue
		}
		files[want] = b
	}
	// serialize 0.14.0 omits LICENSE in its npm tarball. It is part of the
 // same MIT-licensed xterm release; obtain that notice from the pinned,
 // integrity-verified core tarball, never an unversioned URL.
 if lic == "" && p.Name == "@xterm/addon-serialize" {
  for _, core := range pkgs { if core.Name == "@xterm/xterm" { _, lic, err = fetch(core); if err != nil { return nil, "", err }; break } }
 }
 if len(files) != len(p.Files) || lic == "" {
		return nil, "", errors.New("the package lacks a file the app takes, or its licence")
	}
	return files, lic, nil
}
