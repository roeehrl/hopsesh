package registry

import (
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// SpecData is a module's Spec as data, for `hopsesh agents --json` and the test bundle's
// agents.json: what the module declares, with the field names the JSON outputs use. The
// mark (Icon.SVG) is left out; Icon.Apps is desktopApps.
type SpecData struct {
	ID                 agent.ID            `json:"id"`
	Name               string              `json:"name"`
	Vendor             string              `json:"vendor"`
	Stability          agent.Stability     `json:"stability"`
	Tested             []string            `json:"tested"`
	Binaries           []BinaryData        `json:"binaries"`
	Roots              []RootData          `json:"roots"`
	LoginEnv           []string            `json:"loginEnv"`
	Secrets            []string            `json:"secrets"`
	Worktrees          []string            `json:"worktrees"`
	Instructions       []string            `json:"instructions"`
	GlobalInstructions []string            `json:"globalInstructions"`
	Tools              string              `json:"tools,omitempty"`
	Features           []agent.Capability  `json:"features"`
	Experimental       []agent.Capability  `json:"experimental"`
	DesktopApps        map[string][]string `json:"desktopApps"`
	Clouds             []CloudData         `json:"clouds"`
	TerminalEnv        []string            `json:"terminalEnv"`
}

// BinaryData is a program the module may run.
type BinaryData struct {
	Name        string              `json:"name"`
	Candidates  map[string][]string `json:"candidates"`
	VersionArgs []string            `json:"versionArgs"`
}

// RootData is one of the agent's data folders.
type RootData struct {
	Name    string            `json:"name"`
	Env     []string          `json:"env"`
	Default map[string]string `json:"default"`
}

// CloudData is a vendor cloud the module reaches (agent.Cloud).
type CloudData struct {
	Name         string            `json:"name"`
	Title        string            `json:"title"`
	Driver       string            `json:"driver"`
	Tested       []string          `json:"tested"`
	Hosts        []string          `json:"hosts"`
	Up           agent.Fidelity    `json:"up"`
	Down         agent.Fidelity    `json:"down"`
	CodeUp       []agent.CodeWay   `json:"codeUp"`
	CodeDown     []agent.CodeWay   `json:"codeDown"`
	Needs        []agent.Need      `json:"needs"`
	VendorPrefix string            `json:"vendorPrefix,omitempty"`
	Problems     map[string]string `json:"problems"`
	Unset        []string          `json:"unset"`
	Noun         string            `json:"noun,omitempty"`
	Limits       []string          `json:"limits"`
	Summary      bool              `json:"summary"`
	EnvHint      string            `json:"envHint,omitempty"`
	BriefBranch  bool              `json:"briefBranch"`
	NoFollowUp   string            `json:"noFollowUp,omitempty"`
	SignIn       []string          `json:"signIn"`
	Watch        WatchData         `json:"watch"`
}

// WatchData is what the weekly upstream-drift check watches for a cloud (agent.Watch).
type WatchData struct {
	Surface  string     `json:"surface"`
	Docs     []string   `json:"docs"`
	Feeds    []FeedData `json:"feeds"`
	Grep     string     `json:"grep"`
	Help     [][]string `json:"help"`
	Relies   []string   `json:"relies"`
	Issues   []string   `json:"issues"`
	Searches []string   `json:"searches"`
	Code     *CodeData  `json:"code,omitempty"`
}

// FeedData is one changelog or release feed.
type FeedData struct {
	Kind agent.FeedKind `json:"kind"`
	URL  string         `json:"url,omitempty"`
	Repo string         `json:"repo,omitempty"`
	Tag  string         `json:"tag,omitempty"`
}

// CodeData is part of an upstream repository whose commits and canaries are watched.
type CodeData struct {
	Repo     string   `json:"repo"`
	Paths    []string `json:"paths"`
	Canaries []string `json:"canaries"`
}

// Data returns s as data. Lists and maps are never null, so a reader can iterate them.
func Data(s agent.Spec) SpecData {
	d := SpecData{
		ID: s.ID, Name: s.Name, Vendor: s.Vendor, Stability: s.Stability, Tested: list(s.Tested),
		LoginEnv: list(s.LoginEnv), Secrets: list(s.Secrets), Worktrees: list(s.Worktrees),
		Instructions: list(s.Instructions), GlobalInstructions: list(s.GlobalInstructions), Tools: s.Tools,
		Features: list(s.Features), Experimental: list(s.Experimental), DesktopApps: lists(s.Icon.Apps),
		Binaries: []BinaryData{}, Roots: []RootData{}, Clouds: []CloudData{}, TerminalEnv: list(s.TerminalEnv),
	}
	for _, b := range s.Binaries {
		d.Binaries = append(d.Binaries, BinaryData{Name: b.Name, Candidates: lists(b.Candidates), VersionArgs: list(b.VersionArgs)})
	}
	for _, r := range s.Roots {
		def := map[string]string{}
		for k, v := range r.Default {
			def[k] = v
		}
		d.Roots = append(d.Roots, RootData{Name: r.Name, Env: list(r.Env), Default: def})
	}
	for _, c := range s.Clouds {
		problems := map[string]string{}
		for k, v := range c.Problems {
			problems[k] = v
		}
		w := c.Watch
		wd := WatchData{Surface: w.Surface, Docs: list(w.Docs), Feeds: []FeedData{}, Grep: w.Grep, Help: [][]string{},
			Relies: list(w.Relies), Issues: list(w.Issues), Searches: list(w.Searches)}
		for _, f := range w.Feeds {
			wd.Feeds = append(wd.Feeds, FeedData{Kind: f.Kind, URL: f.URL, Repo: f.Repo, Tag: f.Tag})
		}
		for _, h := range w.Help {
			wd.Help = append(wd.Help, list(h))
		}
		if w.Code != nil {
			wd.Code = &CodeData{Repo: w.Code.Repo, Paths: list(w.Code.Paths), Canaries: list(w.Code.Canaries)}
		}
		d.Clouds = append(d.Clouds, CloudData{
			Name: c.Name, Title: c.Title, Driver: c.Driver, Tested: list(c.Tested), Hosts: list(c.Hosts), Up: c.Up, Down: c.Down,
			CodeUp: list(c.CodeUp), CodeDown: list(c.CodeDown), Needs: list(c.Needs), VendorPrefix: c.VendorPrefix,
			Problems: problems, Unset: list(c.Unset), Noun: c.Noun, Limits: list(c.Limits), Summary: c.Summary,
			EnvHint: c.EnvHint, BriefBranch: c.BriefBranch, NoFollowUp: c.NoFollowUp, SignIn: list(c.SignIn), Watch: wd,
		})
	}
	return d
}

func list[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return append([]T{}, s...)
}

func lists(m map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k, v := range m {
		out[k] = list(v)
	}
	return out
}
