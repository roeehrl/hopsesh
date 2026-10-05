package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestTargets(t *testing.T) {
	t.Chdir(filepath.Join("..", "..", ".."))
	mods := modules()
	ts, err := buildTargets(mods)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range checkTargets(ts, groups, false) {
		t.Error(e)
	}
	cloudOnly := map[string]bool{}
	for _, m := range mods {
		cloudOnly[string(m.ID)] = m.cloudOnly()
	}
	seen := map[string]bool{}
	used := map[string]bool{}
	for _, x := range ts {
		seen[x.ID] = true
		used[x.Group] = true
		if x.Module != "" && x.Tested == "" && !cloudOnly[x.Module] {
			t.Errorf("target %s: driven by %s, which has no fixture folder to give the tested version", x.ID, x.Module)
		}
	}
	declared := map[string]bool{}
	for _, m := range mods {
		if !seen[string(m.ID)] && !m.cloudOnly() {
			t.Errorf("module %s is not watched", m.ID)
		}
		for _, c := range m.clouds {
			declared[c.Name] = true
			if !seen[c.Name] {
				t.Errorf("cloud %s of module %s is not watched", c.Name, m.ID)
			}
		}
	}
	for name := range cloudReviews {
		if !declared[name] {
			t.Errorf("cloudReviews names %s, which no module declares", name)
		}
	}
	for _, x := range clouds {
		if declared[x.ID] {
			t.Errorf("cloud %s is declared by a module and listed in clouds too", x.ID)
		}
	}
	// Every group has a target, a review with a budget in ci/drift/groups.json (the
	// matrix of hopsesh's own runs) and a focus file.
	b, err := os.ReadFile(filepath.Join("ci", "drift", "groups.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reviews []struct{ Group, Budget string }
	if err := json.Unmarshal(b, &reviews); err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, r := range reviews {
		listed = append(listed, r.Group)
		if !budget.MatchString(r.Budget) {
			t.Errorf("groups.json: budget %q of %s", r.Budget, r.Group)
		}
	}
	if !slices.Equal(listed, groups) {
		t.Errorf("ci/drift/groups.json lists %v, targets.go %v", listed, groups)
	}
	for _, g := range groups {
		if !used[g] {
			t.Errorf("group %s has no targets", g)
		}
		if _, err := os.Stat(filepath.Join("ci", "drift", "focus", g+".md")); err != nil {
			t.Errorf("group %s: %v", g, err)
		}
	}
}

// The budget format drift.yml accepts for a review.
var budget = regexp.MustCompile(`^[0-9]{1,2}(\.[0-9]{1,2})?$`)

// The manifest hopsesh builds, and the caller fixture the workflow's own test run uses,
// both satisfy ci/drift/manifest.schema.json.
func TestManifestSchema(t *testing.T) {
	t.Chdir(filepath.Join("..", "..", ".."))
	mods := modules()
	ts, err := buildTargets(mods)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"project": hopsesh(mods), "modules": mods, "targets": ts})
	if err != nil {
		t.Fatal(err)
	}
	built := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(built, b, 0o600); err != nil {
		t.Fatal(err)
	}
	schema := filepath.Join("ci", "drift", "manifest.schema.json")
	for _, e := range checkFile(schema, built, false) {
		t.Errorf("hopsesh's manifest: %s", e)
	}
	for _, e := range checkFile(schema, filepath.Join("ci", "drift", "testdata", "manifest.json"), true) {
		t.Errorf("the caller fixture: %s", e)
	}
	// hopsesh's own manifest uses what a caller may not.
	if errs := checkFile(schema, built, true); len(errs) == 0 {
		t.Error("an external manifest with module and watch.tests was accepted")
	}
}

func TestCheckRefuses(t *testing.T) {
	t.Chdir(filepath.Join("..", "..", ".."))
	schema := filepath.Join("ci", "drift", "manifest.schema.json")
	good := `{"id": "demo", "name": "Demo", "kind": "agent", "group": "sample", "vendor": "Example", "surface": "its help",
	  "priority": "high", "tested": "", "latest": {"from": "none"}, "watch": {"docs": ["https://example.com/a.md"], "grep": "x"}}`
	for name, c := range map[string]struct{ doc, want string }{
		"no project":  {`{"targets": [` + good + `]}`, "project is required"},
		"no targets":  {`{"project": {"name": "p", "about": "a", "cite": "c"}, "targets": []}`, "fewer than 1"},
		"duplicate":   {`{"project": {"name": "p", "about": "a", "cite": "c"}, "targets": [` + good + `,` + good + `]}`, "not a unique"},
		"http doc":    {`{"project": {"name": "p", "about": "a", "cite": "c"}, "targets": [` + strings.Replace(good, "https://", "http://", 1) + `]}`, "does not match"},
		"bad grep":    {`{"project": {"name": "p", "about": "a", "cite": "c"}, "targets": [` + strings.Replace(good, `"grep": "x"`, `"grep": "("`, 1) + `]}`, "grep:"},
		"relies only": {`{"project": {"name": "p", "about": "a", "cite": "c"}, "targets": [` + strings.Replace(good, `"grep": "x"`, `"grep": "x", "relies": ["--x"]`, 1) + `]}`, "relies on help"},
		"unknown key": {`{"project": {"name": "p", "about": "a", "cite": "c"}, "targets": [` + strings.Replace(good, `"tested"`, `"tset": "", "tested"`, 1) + `]}`, "tset is not allowed"},
	} {
		p := filepath.Join(t.TempDir(), "m.json")
		if err := os.WriteFile(p, []byte(c.doc), 0o600); err != nil {
			t.Fatal(err)
		}
		errs := checkFile(schema, p, true)
		if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, c.want) }) {
			t.Errorf("%s: %q, want an error with %q", name, errs, c.want)
		}
	}
}

func TestNewest(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"0.99.0", "0.153.2", "0.153.10"}, "0.153.10"},
		{[]string{"2.1.284", "2.1.3"}, "2.1.284"},
		{[]string{"1.0", "1.0.1"}, "1.0.1"},
	} {
		if got := newest(c.in); got != c.want {
			t.Errorf("newest(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFeed(t *testing.T) {
	dir := t.TempDir()
	rss := `<?xml version="1.0"?><rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel>
<title>Changelog</title><link>https://example.com/</link>
<item><title>New cloud &amp; handoff</title><link>https://example.com/new</link><pubDate>Thu, 01 Oct 2026 10:00:00 +0000</pubDate>
<description>short</description><content:encoded><![CDATA[<p>Run <code>codex cloud</code> now.</p><p>Second&nbsp;paragraph.</p>]]></content:encoded></item>
<item><title>Old</title><link>https://example.com/old</link><pubDate>Mon, 01 Sep 2026 10:00:00 +0000</pubDate></item>
<item><title>Undated</title></item>
</channel></rss>`
	atom := `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">
<entry><title>rust-v0.160.0</title><link rel="alternate" href="https://example.com/r160"/><updated>2026-10-02T00:00:00Z</updated><content type="html">&lt;p&gt;paginated&lt;/p&gt;</content></entry>
</feed>`
	for name, c := range map[string]struct{ doc, want, not string }{
		"rss":  {rss, "### New cloud & handoff\n\n2026-10-01T10:00:00Z · https://example.com/new\n\nRun codex cloud now.\n\nSecond paragraph.\n\n", "Old"},
		"atom": {atom, "### rust-v0.160.0\n\n2026-10-02T00:00:00Z · https://example.com/r160\n\npaginated\n\n", "<p>"},
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(c.doc), 0o644); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		if err := printFeed(&b, p, "2026-09-26T00:00:00Z"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasPrefix(b.String(), c.want) || strings.Contains(b.String(), c.not) {
			t.Errorf("%s:\n%s", name, b.String())
		}
	}
}
