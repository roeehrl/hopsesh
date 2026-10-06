package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/roeehrl/hopsesh/internal/devtools/schemalite"
)

// project is what the review is protecting, for the prompt and the issue. A repository
// that calls the reusable workflow writes its own in its manifest.
type project struct {
	Name  string `json:"name"`
	About string `json:"about"`
	Cite  string `json:"cite"`
}

// hopsesh is this repository's project record. The modules sentence is built from the
// compiled-in modules.
func hopsesh(mods []module) project {
	names := make([]string, 0, len(mods))
	for _, m := range mods {
		names = append(names, fmt.Sprintf("`agents/%s` (%s)", m.ID, m.Name))
	}
	list := strings.Join(names, ", ")
	if i := strings.LastIndex(list, ", "); i >= 0 {
		list = list[:i] + " and " + list[i+2:]
	}
	return project{
		Name: "hopsesh",
		About: "hopsesh is a tool that finds coding-agent sessions on several machines, moves them between machines " +
			"and converts them between agents and isolated account profiles, preserving causal lineage, forks and repeated " +
			"multi-machine/account/agent round trips. GUI, TUI and CLI share these operations. Each agent is a compiled-in module written against `sdk/agent`: " + list + ". " +
			"hopsesh also has a cloud capability: handing a session off to a vendor's cloud and bringing cloud sessions " +
			"back, through the vendor's own CLI. `intel/manifest.json` has, under `modules`, what each module declares " +
			"(folders, binaries, instruction files, desktop apps and URL scheme, accounts root/login/environment policy, " +
			"capabilities, tested versions, sourceFiles and shared integrationFiles including regression scenarios).",
		Cite: "Follow module integrationFiles through shared app/core code and GUI/TUI/CLI consumers as well as regression tests. " +
			"An agent's code is its module (`agents/<module>/`). A cloud a module declares is in that module's " +
			"`cloud.go` (`agents/<module>/cloud.go`); a cloud no module reaches yet has no code: cite its entry in " +
			"`internal/devtools/driftmanifest/targets.go` instead.",
	}
}

// manifest is the probe's input, as driftmanifest prints it or as a caller commits it.
type manifest struct {
	Project project  `json:"project"`
	Targets []target `json:"targets"`
}

// checkFile validates a manifest file against the schema and the rules a schema cannot
// say. external is a manifest from another repository: hopsesh's own Go tests are not
// available to it.
func checkFile(schemaPath, path string, external bool) []string {
	s, err := schemalite.Load(schemaPath)
	if err != nil {
		return []string{err.Error()}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return []string{err.Error()}
	}
	if errs := s.Validate(b); len(errs) > 0 {
		return errs
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return []string{err.Error()}
	}
	return checkTargets(m.Targets, nil, external)
}

var (
	// targetID is the pattern schema.json puts on findings[].target.
	targetID = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	issueRef = regexp.MustCompile(`^[\w.-]+/[\w.-]+#\d+$`)
)

// checkTargets returns what is wrong with the targets. groups, when not nil, are the only
// review groups allowed.
func checkTargets(ts []target, groups []string, external bool) []string {
	var errs []string
	bad := func(t target, f string, a ...any) {
		errs = append(errs, "target "+t.ID+": "+fmt.Sprintf(f, a...))
	}
	seen := map[string]bool{}
	for _, x := range ts {
		if !targetID.MatchString(x.ID) || seen[x.ID] {
			bad(x, "id is not a unique lower-case name")
		}
		seen[x.ID] = true
		if x.Name == "" || x.Vendor == "" || x.Surface == "" {
			bad(x, "name, vendor and surface are required")
		}
		if !targetID.MatchString(x.Group) || groups != nil && !slices.Contains(groups, x.Group) {
			bad(x, "group %q is not one of %v", x.Group, groups)
		}
		if !slices.Contains([]string{"agent", "cloud", "standard"}, x.Kind) || !slices.Contains([]string{"high", "low"}, x.Priority) {
			bad(x, "kind %q or priority %q", x.Kind, x.Priority)
		}
		switch x.Latest.From {
		case "none":
		case "npm", "github-release":
			if x.Latest.Ref == "" {
				bad(x, "latest from %s needs a ref", x.Latest.From)
			}
		case "json":
			if !strings.HasPrefix(x.Latest.Ref, "https://") || !strings.HasPrefix(x.Latest.Field, ".") {
				bad(x, "latest from json needs an https ref and a jq field")
			}
		default:
			bad(x, "latest from %q", x.Latest.From)
		}
		w := x.Watch
		if len(w.Docs) == 0 || w.Grep == "" {
			bad(x, "every target hashes docs and greps its feeds")
		}
		if _, err := regexp.Compile(w.Grep); err != nil {
			bad(x, "grep: %v", err)
		}
		for _, u := range w.Docs {
			if !strings.HasPrefix(u, "https://") {
				bad(x, "doc %s is not https", u)
			}
		}
		for _, f := range w.Feeds {
			ok := f.Kind == "releases" && f.Repo != "" && f.URL == "" ||
				(f.Kind == "markdown" || f.Kind == "feed") && strings.HasPrefix(f.URL, "https://") && f.Repo == ""
			if !ok {
				bad(x, "feed %+v", f)
			}
		}
		if len(w.Relies) > 0 && len(w.Help) == 0 {
			bad(x, "relies on help it does not run")
		}
		if x.Priority == "low" && (len(w.Help) > 0 || len(w.Issues) > 0 || w.Code != nil) {
			bad(x, "a low-priority target is docs only")
		}
		for _, argv := range w.Help {
			if len(argv) == 0 {
				bad(x, "empty help argv")
			}
		}
		for _, i := range w.Issues {
			if !issueRef.MatchString(i) {
				bad(x, "issue %q is not owner/repo#number", i)
			}
		}
		for _, s := range w.Searches {
			if !strings.HasPrefix(s, "repo:") {
				bad(x, "search %q is not limited to a repository", s)
			}
		}
		if c := w.Code; c != nil && (c.Repo == "" || len(c.Paths) == 0 || len(c.Canaries) == 0) {
			bad(x, "code needs a repo, paths and canaries")
		}
		if s := w.Schema; s != nil {
			if _, err := regexp.CompilePOSIX(s.Keep); err != nil || len(s.Argv) == 0 {
				bad(x, "schema %+v", s)
			}
		}
		if external && (w.Tests != "" || x.Module != "") {
			bad(x, "watch.tests and module are hopsesh's own (its Go tests and agent modules); leave them out")
		}
	}
	return errs
}
