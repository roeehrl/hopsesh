package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// codeFloors finds every version floor in the code: the minimum given to
// agent.VersionAtLeast or HookVersionAtLeast as a string literal or a constant, by file.
func codeFloors(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, root := range []string{"agents", "internal", "sdk"} {
		dirs := map[string][]*ast.File{}
		fset := token.NewFileSet()
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "testdata" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			dirs[filepath.Dir(path)] = append(dirs[filepath.Dir(path)], f)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, files := range dirs {
			consts := map[string]string{}
			for _, f := range files {
				for _, d := range f.Decls {
					g, ok := d.(*ast.GenDecl)
					if !ok || g.Tok != token.CONST {
						continue
					}
					for _, s := range g.Specs {
						vs := s.(*ast.ValueSpec)
						for i, n := range vs.Names {
							if i < len(vs.Values) {
								if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
									consts[n.Name], _ = strconv.Unquote(lit.Value)
								}
							}
						}
					}
				}
			}
			for _, f := range files {
				ast.Inspect(f, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok || len(call.Args) != 2 {
						return true
					}
					name := ""
					switch fn := call.Fun.(type) {
					case *ast.SelectorExpr:
						name = fn.Sel.Name
					case *ast.Ident:
						name = fn.Name
					}
					if name != "VersionAtLeast" && name != "HookVersionAtLeast" {
						return true
					}
					v := ""
					switch a := call.Args[1].(type) {
					case *ast.BasicLit:
						v, _ = strconv.Unquote(a.Value)
					case *ast.Ident:
						v = consts[a.Name]
					}
					if v == "" {
						return true // a variable minimum (the SDK's own helpers)
					}
					file := filepath.ToSlash(fset.Position(call.Pos()).Filename)
					if !slices.Contains(out[file], v) {
						out[file] = append(out[file], v)
					}
					return true
				})
			}
		}
	}
	return out
}

// Every floor the code gates a feature on is declared on a target with its source, and
// the target's baseline is at or above it: the probe then compares the latest with help
// at least as new as what hopsesh runs.
func TestFloorsDeclared(t *testing.T) {
	t.Chdir(filepath.Join("..", "..", ".."))
	ts, err := buildTargets(modules())
	if err != nil {
		t.Fatal(err)
	}
	found := codeFloors(t)
	if len(found) < 4 {
		t.Fatalf("found only %v: the scan misses floors", found)
	}
	declared := map[string]string{}
	for _, x := range ts {
		for _, f := range x.Floors {
			declared[f.Source+"@"+f.Version] = x.ID
			if _, err := os.Stat(f.Source); err != nil {
				t.Errorf("target %s: floor %s: %v", x.ID, f.Version, err)
			}
			if !slices.Contains(found[f.Source], f.Version) {
				t.Errorf("target %s declares floor %s in %s, which the code does not check there", x.ID, f.Version, f.Source)
			}
			if versionLess(x.Baseline, f.Version) {
				t.Errorf("target %s: baseline %q is below floor %s", x.ID, x.Baseline, f.Version)
			}
		}
	}
	for file, vs := range found {
		for _, v := range vs {
			if declared[file+"@"+v] == "" {
				t.Errorf("%s gates on version %s, which no drift target declares as a floor (targets.go)", file, v)
			}
		}
	}
	for _, id := range []string{"claude", "codex"} {
		for _, x := range ts {
			if x.ID == id && x.Baseline == x.Tested {
				t.Errorf("%s: baseline %s did not rise to the code's floors", id, x.Baseline)
			}
		}
	}
}

// Every field a format names is in one of the files the manifest says read it, so the
// list stays what hopsesh actually relies on.
func TestFormatsRead(t *testing.T) {
	t.Chdir(filepath.Join("..", "..", ".."))
	ts, err := buildTargets(modules())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, x := range ts {
		for _, f := range x.Watch.Formats {
			var src strings.Builder
			for _, r := range f.Readers {
				b, err := os.ReadFile(r)
				if err != nil {
					t.Errorf("%s/%s: %v", x.ID, f.ID, err)
				}
				src.Write(b)
			}
			for _, field := range f.Fields {
				n++
				if !strings.Contains(src.String(), field) {
					t.Errorf("%s/%s: %q is in none of %v", x.ID, f.ID, field, f.Readers)
				}
			}
		}
	}
	// The identity and record formats this release reads.
	all := map[string]bool{}
	for _, x := range ts {
		for _, f := range x.Watch.Formats {
			all[f.ID] = true
		}
	}
	for _, id := range []string{"claude-oauth-account", "claude-transcript-identity", "claude-live-registry", "claude-hooks", "claude-desktop-identity",
		"codex-auth", "codex-session-meta", "codex-writer-lock", "codex-hooks", "codex-app-bundle", "codex-app-link", "claude-desktop-link"} {
		if !all[id] {
			t.Errorf("no format %s", id)
		}
	}
}

func TestFeedLatest(t *testing.T) {
	p := filepath.Join(t.TempDir(), "appcast.xml")
	appcast := `<?xml version="1.0" encoding="utf-8"?><rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><title>Codex</title>
<item><title>26.930.1</title><pubDate>Tue, 30 Sep 2026 10:00:00 +0000</pubDate></item>
<item><title>26.1007.71326</title><pubDate>Wed, 14 Oct 2026 00:50:34 +0000</pubDate><sparkle:shortVersionString>26.1007.71326</sparkle:shortVersionString></item>
<item><title>undated</title></item>
</channel></rss>`
	if err := os.WriteFile(p, []byte(appcast), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, err := feedLatest(p); err != nil || v != "26.1007.71326" {
		t.Fatalf("feedLatest = %q, %v", v, err)
	}
	if err := os.WriteFile(p, []byte(`<rss><channel><item><title>x</title></item></channel></rss>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := feedLatest(p); err == nil {
		t.Fatal("an appcast without dates gave a version")
	}
}
