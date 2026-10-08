package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// What the weekly drift check watches. A cloud a module reaches is declared in that
// module's Spec.Clouds (its Watch says what to watch); this file adds how each is reviewed,
// the agents' own watch lists, and the clouds and standards no module reaches yet. A
// cloud-only module (no data folders: Copilot, Jules, Devin, Amp) is watched through its
// clouds alone.

// A target is one upstream surface the drift check watches: an agent a module drives
// today, a vendor cloud hopsesh plans to reach, or a standard it may adopt.
type target struct {
	ID       string `json:"id"`       // "claude", "codex-cloud"; a finding names it
	Name     string `json:"name"`     // "Codex cloud"
	Kind     string `json:"kind"`     // agent, cloud or standard
	Group    string `json:"group"`    // the review that reads it (see groups)
	Vendor   string `json:"vendor"`   // "OpenAI"
	Surface  string `json:"surface"`  // what hopsesh uses, or plans to use, in plain words
	Priority string `json:"priority"` // high, or low for docs-only targets
	// Module is the agent module whose CLI drives the target ("codex" for Codex cloud).
	Module string `json:"module,omitempty"`
	// Tested is the exact version hopsesh was tested with: the module's newest fixture
	// folder. Empty when no module drives the target.
	Tested string `json:"tested"`
	Latest latest `json:"latest"`
	// Package is the npm package with the CLI that Watch.Help runs. The probe installs it
	// at Tested and at its latest version and diffs the two; without a package the help
	// comes from the runner's own CLI (gh) and is compared with last week's.
	Package     string   `json:"package,omitempty"`
	VersionArgv []string `json:"versionArgv,omitempty"` // prints the version hopsesh parses
	Watch       watch    `json:"watch"`
}

// latest says where the probe reads a target's newest version.
type latest struct {
	From  string `json:"from"`            // npm, github-release, json or none
	Ref   string `json:"ref,omitempty"`   // the npm package, owner/repo or document URL
	Field string `json:"field,omitempty"` // for json: the jq path to the version (".revision")
}

type watch struct {
	// Docs are Markdown pages and llms.txt indexes. The probe hashes them and diffs any
	// that changed since last week's run.
	Docs  []string `json:"docs"`
	Feeds []feed   `json:"feeds"`
	// Grep is an extended regular expression for the feeds' new entries and the docs
	// diffs; the probe keeps the matching lines, and the review searches for the same words.
	Grep string `json:"grep"`
	// Help are commands that need no login, run at the tested and the latest version.
	Help [][]string `json:"help"`
	// Relies are the flags and subcommands in that help which hopsesh uses, or which the
	// cloud design depends on. One that was in the tested help and is gone is a break.
	Relies []string `json:"relies"`
	// Issues are upstream issues and pull requests ("anthropics/claude-code#66373"); the
	// probe records their state, last update and comment count.
	Issues []string `json:"issues"`
	// Searches are GitHub issue searches the probe limits to issues opened since the last run.
	Searches []string `json:"searches"`
	Code     *code    `json:"code,omitempty"`
	Schema   *schema  `json:"schema,omitempty"`
	// Tests is a `go test -run` pattern for hopsesh's real-agent tests, run against the
	// latest CLI (no model calls).
	Tests string `json:"tests,omitempty"`
}

// feed is a changelog or release feed. markdown: a CHANGELOG, cut at the tested version
// when there is one, otherwise compared with last week's copy. feed: RSS or Atom, items
// since the last run. releases: a GitHub repository's releases since Tag+Tested.
type feed struct {
	Kind string `json:"kind"`
	URL  string `json:"url,omitempty"`
	Repo string `json:"repo,omitempty"` // releases
	Tag  string `json:"tag,omitempty"`  // releases: the tag's prefix before the version ("rust-v")
}

// code is part of an upstream repository the probe checks out (shallow and sparse): it
// lists the commits on Paths since the last run and counts the files that contain each
// canary, so a canary that disappears or appears shows up week to week.
type code struct {
	Repo     string   `json:"repo"`
	Paths    []string `json:"paths"`
	Canaries []string `json:"canaries"`
}

// schema is a protocol schema the CLI generates. Keep is a POSIX expression matched
// against basenames at every depth; relative paths are preserved in full diffs.
type schema struct {
	Argv []string `json:"argv"` // the output folder is appended
	Keep string   `json:"keep"`
}

// groups are the reviews, each with its own budget; ci/drift/groups.json, the review
// matrix of hopsesh's own runs, lists the same.
var groups = []string{"local", "anthropic-cloud", "openai-cloud", "third-party-cloud"}

// agentWatch is what the probe watches for each agent module. A module without an entry
// fails the tests.
var agentWatch = map[agent.ID]target{
	"claude": {
		Group:    "local",
		Surface:  "transcripts and their records under the Claude config folder, `claude --resume` with its fork and Remote Control flags, `claude auth status --json`, isolated account profiles via CLAUDE_CONFIG_DIR and auth login, public identity and credential-store isolation, `--version` output, CLAUDE.md, skills, settings and the desktop app",
		Priority: "high",
		Latest:   latest{From: "npm", Ref: "@anthropic-ai/claude-code"},
		Package:  "@anthropic-ai/claude-code",
		Watch: watch{
			Docs:   claudeDocs("sessions", "settings", "skills", "hooks", "headless", "cli-reference", "claude-directory", "permissions", "authentication", "env-vars", "desktop", "memory"),
			Feeds:  []feed{{Kind: "markdown", URL: claudeChangelog}},
			Grep:   `session|transcript|jsonl|resume|fork|CLAUDE_CONFIG_DIR|hook|SessionStart|UserPromptSubmit|skills|CLAUDE\.md|AGENTS\.md|settings|auth|account|login|keychain|credential|Console|desktop|deprecat|remov|rename|breaking`,
			Help:   [][]string{{"claude", "--help"}, {"claude", "auth", "status", "--help"}, {"claude", "auth", "login", "--help"}},
			Relies: []string{"--resume", "--fork-session", "--remote-control", "--desktop", "--version", "--json", "auth", "login", "status"},
		},
	},
	"codex": {
		Group:    "local",
		Surface:  "rollout files under isolated CODEX_HOME and CODEX_SQLITE_HOME profiles and their session_meta, session_index.jsonl, `codex resume` and `codex fork`, the `codex app-server` JSON-RPC methods for threads and accounts (account/read with refreshToken:false), login and keyring credential storage, exact desktop navigation through codex://threads/<uuid> for the default profile, `--version` output, AGENTS.md, skills and config.toml",
		Priority: "high",
		Latest:   latest{From: "npm", Ref: "@openai/codex"},
		Package:  "@openai/codex",
		Watch: watch{
			Docs:  codexDocs("app-server", "config-file/config-reference", "build-skills", "agent-configuration/agents-md", "non-interactive-mode", "auth", "config-file/environment-variables", "config-file/config-advanced", "reference/commands", "developer-commands", "hooks"),
			Feeds: []feed{{Kind: "releases", Repo: "openai/codex", Tag: "rust-v"}},
			// Track storage evolution without assuming an old limitation is still present;
			// the reviewer must inspect the current reader and writer.
			Grep:   `session|rollout|thread|resume|fork|CODEX_HOME|CODEX_SQLITE_HOME|cli_auth_credentials_store|keyring|refreshToken|workspace|identity|desktop|deep.link|codex://|hook|SessionStart|UserPromptSubmit|skills|AGENTS\.md|config\.toml|account|auth|app-server|paginated|history_mode|jsonl\.zst|zstd|compress|revert|migrate-rollouts|deprecat|remov|rename|breaking`,
			Help:   [][]string{{"codex", "--help"}, {"codex", "resume", "--help"}, {"codex", "fork", "--help"}, {"codex", "app-server", "--help"}, {"codex", "login", "--help"}, {"codex", "login", "status", "--help"}},
			Relies: []string{"resume", "fork", "app-server", "--version", "login", "status"},
			Code: &code{
				Repo:     "openai/codex",
				Paths:    []string{"codex-rs/protocol/src/protocol.rs", "codex-rs/rollout", "codex-rs/thread-store", "codex-rs/history/src/rollout_payload.rs", "codex-rs/cli/src/main.rs", "codex-rs/app-server-protocol/src/protocol/v2/account.rs", "codex-rs/app-server/src/request_processors/account_processor", "codex-rs/login", "codex-rs/keyring-store", "codex-rs/core/src/config/auth_keyring.rs", "codex-rs/config/src/lib.rs", "codex-rs/config/src/types.rs", "codex-rs/config/src/config_toml.rs"},
				Canaries: []string{"history_mode", "ThreadHistoryMode", ".jsonl.zst", "rollout_id", "migrate-rollouts", "refresh_token", "GetAccountResponse", "AccountLogin", "Chatgpt", "cli_auth_credentials_store", "CODEX_SQLITE_HOME"},
			},
			Schema: &schema{
				Argv: []string{"codex", "app-server", "generate-json-schema", "--out"},
				Keep: `^(Initialize|Thread|Account|GetAccount|LoginAccount|LogoutAccount|CancelLoginAccount|ExternalAgentConfig)|^(ClientRequest|ServerNotification)\.json$`,
			},
			Tests: "TestCodex",
		},
	},
}

// review is how the drift check reviews a cloud a module declares (Spec.Clouds, which
// says what to watch). Latest and Package are for a cloud-only module's driver; a cloud
// an agent module drives takes them from the agent's watch list.
type review struct {
	Group   string
	Latest  latest
	Package string
}

// cloudReviews are the reviews of the clouds the modules declare. A declared cloud
// without one fails the tests.
var cloudReviews = map[string]review{
	"claude-cloud":  {Group: "anthropic-cloud"},
	"codex-cloud":   {Group: "openai-cloud"},
	"copilot-cloud": {Group: "third-party-cloud", Latest: latest{From: "github-release", Ref: "cli/cli"}},
	"jules":         {Group: "third-party-cloud", Latest: latest{From: "json", Ref: julesDiscovery, Field: ".revision"}, Package: "@google/jules"},
	// The Devin CLI installs from a download script, not a package.
	"devin": {Group: "third-party-cloud", Latest: latest{From: "json", Ref: devinOpenAPI, Field: ".info.version"}},
	"amp":   {Group: "third-party-cloud", Latest: latest{From: "npm", Ref: "@sourcegraph/amp"}, Package: "@sourcegraph/amp"},
}

// clouds are the vendor clouds and standards no module reaches yet, in review order;
// they are watched so the cloud design learns of changes before it is built.
var clouds = []target{
	{
		ID: "wails-desktop", Name: "Wails desktop shell", Kind: "standard", Group: "local", Vendor: "Wails",
		Surface: "native tray, popup, activation, login startup and window lifecycle", Priority: "high",
		Tested: "v3.0.0-beta.27", Latest: latest{From: "none"},
		Watch: watch{
			Docs:  []string{"https://raw.githubusercontent.com/wailsapp/wails/master/v3/README.md"},
			Feeds: []feed{{Kind: "releases", Repo: "wailsapp/wails"}},
			Grep:  `tray|StatusNotifier|activation|autostart|focus|taskbar|window|shutdown`,
			Code:  &code{Repo: "wailsapp/wails", Paths: []string{"v3/pkg/application"}, Canaries: []string{"RegisterStatusNotifierItem", "TaskbarCreated", "HideOnFocusLost", "EnableWithOptions"}},
		},
	},
	{
		ID: "cursor", Name: "Cursor cloud agents", Kind: "cloud", Group: "third-party-cloud", Vendor: "Anysphere",
		Surface:  "the cloud-agent API (docs only; no plan to drive it yet)",
		Priority: "low",
		Latest:   latest{From: "none"},
		Watch: watch{
			Docs:  []string{"https://cursor.com/docs/cloud-agent.md", "https://cursor.com/docs/cloud-agent/api/endpoints.md", "https://cursor.com/docs/cloud-agent/api/v0.md"},
			Feeds: []feed{{Kind: "feed", URL: "https://cursor.com/changelog/rss.xml"}},
			Grep:  `cloud agent|background agent|api|conversation|handoff|deprecat`,
		},
	},
	{
		ID: "acp", Name: "Agent Client Protocol and trajectories", Kind: "standard", Group: "third-party-cloud", Vendor: "community",
		Surface:  "session interchange standards hopsesh may adopt (docs only)",
		Priority: "low",
		Latest:   latest{From: "none"},
		Watch: watch{
			Docs: []string{"https://agentclientprotocol.com/llms.txt", "https://agentclientprotocol.com/get-started/agents.md",
				"https://raw.githubusercontent.com/letta-ai/trajectory/main/README.md"},
			Grep: `session|load|resume|fork|trajectory|RFD`,
		},
	},
}

const (
	claudeChangelog = "https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md"
	julesDiscovery  = "https://jules.googleapis.com/$discovery/rest?version=v1alpha"
	devinOpenAPI    = "https://docs.devin.ai/v3-openapi.json"
)

func claudeDocs(pages ...string) []string {
	return prefixed("https://code.claude.com/docs/en/", ".md", pages)
}

func codexDocs(pages ...string) []string {
	return prefixed("https://learn.chatgpt.com/docs/", ".md", pages)
}

func prefixed(pre, suf string, pages []string) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = pre + p + suf
	}
	return out
}

// buildTargets returns the agents, from their modules, then the clouds the modules
// declare, then the other clouds. Tested comes from the driving module's newest fixture
// folder.
func buildTargets(mods []module) ([]target, error) {
	tested := map[string]string{}
	var out []target
	for _, m := range mods {
		if m.cloudOnly() {
			continue // no agent here: its clouds are its targets
		}
		t, ok := agentWatch[m.ID]
		if !ok {
			return nil, fmt.Errorf("module %s has no drift watch list in internal/devtools/driftmanifest/targets.go", m.ID)
		}
		t.ID, t.Name, t.Kind, t.Vendor, t.Module = string(m.ID), m.Name, "agent", m.Vendor, string(m.ID)
		t.Tested = newest(m.Fixtures)
		if len(m.Binaries) > 0 {
			t.VersionArgv = append([]string{m.Binaries[0].Name}, m.Binaries[0].VersionArgs...)
		}
		tested[t.ID] = t.Tested
		out = append(out, t)
	}
	for _, m := range mods {
		for _, c := range m.clouds {
			t, err := declaredCloud(m, c)
			if err != nil {
				return nil, err
			}
			out = append(out, t)
		}
	}
	for _, c := range clouds {
		if c.Module != "" {
			v, ok := tested[c.Module]
			if !ok {
				return nil, fmt.Errorf("cloud %s is driven by module %s, which is not compiled in", c.ID, c.Module)
			}
			c.Tested = v
		}
		out = append(out, c)
	}
	return out, nil
}

// declaredCloud is the target for a cloud a module declares. When the module's agent
// drives it, the latest version and the package come from the agent's own watch list; a
// cloud-only module's driver has them in its review.
func declaredCloud(m module, c agent.Cloud) (target, error) {
	rv, ok := cloudReviews[c.Name]
	if !ok {
		return target{}, fmt.Errorf("cloud %s (module %s) has no review in internal/devtools/driftmanifest/targets.go", c.Name, m.ID)
	}
	w := c.Watch
	t := target{
		ID: c.Name, Name: c.Title, Kind: "cloud", Group: rv.Group, Vendor: m.Vendor, Surface: w.Surface, Priority: "high",
		Module: string(m.ID), Tested: newest(m.Fixtures),
		Watch: watch{Docs: w.Docs, Grep: w.Grep, Help: w.Help, Relies: w.Relies, Issues: w.Issues, Searches: w.Searches},
	}
	switch {
	case m.cloudOnly():
		if rv.Latest.From == "" {
			return target{}, fmt.Errorf("cloud %s is driven by %s: give its review a latest-version source", c.Name, c.Driver)
		}
		t.Latest, t.Package = rv.Latest, rv.Package
		for _, b := range m.Binaries {
			if b.Name == c.Driver {
				t.VersionArgv = append([]string{b.Name}, b.VersionArgs...)
			}
		}
	case len(m.Binaries) == 0 || c.Driver != m.Binaries[0].Name:
		return target{}, fmt.Errorf("cloud %s is driven by %s, not by %s's agent: give it a latest-version source", c.Name, c.Driver, m.ID)
	default:
		a := agentWatch[m.ID]
		t.Latest, t.Package = a.Latest, a.Package
	}
	for _, f := range w.Feeds {
		t.Watch.Feeds = append(t.Watch.Feeds, feed{Kind: string(f.Kind), URL: f.URL, Repo: f.Repo, Tag: f.Tag})
	}
	if w.Code != nil {
		t.Watch.Code = &code{Repo: w.Code.Repo, Paths: w.Code.Paths, Canaries: w.Code.Canaries}
	}
	return t, nil
}

// newest is the highest of dotted numeric versions ("0.153.2" beats "0.99.0").
func newest(versions []string) string {
	best := ""
	for _, v := range versions {
		if best == "" || versionLess(best, v) {
			best = v
		}
	}
	return best
}

func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, errx := strconv.Atoi(as[i])
		y, erry := strconv.Atoi(bs[i])
		if errx != nil || erry != nil {
			if as[i] != bs[i] {
				return as[i] < bs[i]
			}
			continue
		}
		if x != y {
			return x < y
		}
	}
	return len(as) < len(bs)
}
