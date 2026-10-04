package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The pattern schema.json puts on findings[].target.
var targetID = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

func TestTargets(t *testing.T) {
	t.Chdir(filepath.Join("..", "..", ".."))
	ts, err := buildTargets(modules())
	if err != nil {
		t.Fatal(err)
	}
	issue := regexp.MustCompile(`^[\w.-]+/[\w.-]+#\d+$`)
	seen := map[string]bool{}
	used := map[string]bool{}
	for _, x := range ts {
		where := "target " + x.ID
		if !targetID.MatchString(x.ID) || seen[x.ID] {
			t.Errorf("%s: id is not a unique lower-case name", where)
		}
		seen[x.ID] = true
		used[x.Group] = true
		if x.Name == "" || x.Vendor == "" || x.Surface == "" {
			t.Errorf("%s: name, vendor and surface are required", where)
		}
		if !slices.Contains(groups, x.Group) {
			t.Errorf("%s: group %q is not one of %v", where, x.Group, groups)
		}
		if !slices.Contains([]string{"agent", "cloud", "standard"}, x.Kind) || !slices.Contains([]string{"high", "low"}, x.Priority) {
			t.Errorf("%s: kind %q or priority %q", where, x.Kind, x.Priority)
		}
		if x.Module != "" && x.Tested == "" {
			t.Errorf("%s: driven by %s, which has no fixture folder to give the tested version", where, x.Module)
		}
		switch x.Latest.From {
		case "none":
		case "npm", "github-release":
			if x.Latest.Ref == "" {
				t.Errorf("%s: latest from %s needs a ref", where, x.Latest.From)
			}
		case "json":
			if !strings.HasPrefix(x.Latest.Ref, "https://") || !strings.HasPrefix(x.Latest.Field, ".") {
				t.Errorf("%s: latest from json needs an https ref and a jq field", where)
			}
		default:
			t.Errorf("%s: latest from %q", where, x.Latest.From)
		}
		w := x.Watch
		if len(w.Docs) == 0 || w.Grep == "" {
			t.Errorf("%s: every target hashes docs and greps its feeds", where)
		}
		if _, err := regexp.Compile(w.Grep); err != nil {
			t.Errorf("%s: grep: %v", where, err)
		}
		for _, u := range w.Docs {
			if !strings.HasPrefix(u, "https://") {
				t.Errorf("%s: doc %s is not https", where, u)
			}
		}
		for _, f := range w.Feeds {
			ok := f.Kind == "releases" && f.Repo != "" && f.URL == "" ||
				(f.Kind == "markdown" || f.Kind == "feed") && strings.HasPrefix(f.URL, "https://") && f.Repo == ""
			if !ok {
				t.Errorf("%s: feed %+v", where, f)
			}
		}
		if len(w.Relies) > 0 && len(w.Help) == 0 {
			t.Errorf("%s: relies on help it does not run", where)
		}
		if x.Priority == "low" && (len(w.Help) > 0 || len(w.Issues) > 0 || w.Code != nil) {
			t.Errorf("%s: a low-priority target is docs only", where)
		}
		for _, argv := range w.Help {
			if len(argv) == 0 {
				t.Errorf("%s: empty help argv", where)
			}
		}
		for _, i := range w.Issues {
			if !issue.MatchString(i) {
				t.Errorf("%s: issue %q is not owner/repo#number", where, i)
			}
		}
		for _, s := range w.Searches {
			if !strings.HasPrefix(s, "repo:") {
				t.Errorf("%s: search %q is not limited to a repository", where, s)
			}
		}
		if c := w.Code; c != nil && (c.Repo == "" || len(c.Paths) == 0 || len(c.Canaries) == 0) {
			t.Errorf("%s: code needs a repo, paths and canaries", where)
		}
		if s := w.Schema; s != nil {
			if _, err := regexp.Compile(s.Keep); err != nil || len(s.Argv) == 0 {
				t.Errorf("%s: schema %+v", where, s)
			}
		}
	}
	declared := map[string]bool{}
	for _, m := range modules() {
		if !seen[string(m.ID)] {
			t.Errorf("module %s is not watched", m.ID)
		}
		for _, c := range m.clouds {
			declared[c.Name] = true
			if !seen[c.Name] {
				t.Errorf("cloud %s of module %s is not watched", c.Name, m.ID)
			}
		}
	}
	for name := range cloudGroups {
		if !declared[name] {
			t.Errorf("cloudGroups names %s, which no module declares", name)
		}
	}
	for _, x := range clouds {
		if declared[x.ID] {
			t.Errorf("cloud %s is declared by a module and listed in clouds too", x.ID)
		}
	}
	// Every group has a target, and a review in drift.yml's matrix.
	wf, err := os.ReadFile(filepath.Join(".github", "workflows", "drift.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if !used[g] {
			t.Errorf("group %s has no targets", g)
		}
		if !strings.Contains(strings.ReplaceAll(string(wf), "\r\n", "\n"), "- group: "+g+"\n") { // Windows checks out CRLF
			t.Errorf("group %s has no review in drift.yml", g)
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
