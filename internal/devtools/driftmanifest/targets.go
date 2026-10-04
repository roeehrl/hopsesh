package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// What the weekly drift check watches lives here as data until the cloud capability lands.
// Then each agent's watch list moves into its module's Spec, and each cloud's into the
// Spec.Clouds entry of the module whose CLI drives it, and this file keeps only the clouds
// that no module drives yet.

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

// schema is a protocol schema the CLI generates; files matching Keep are diffed in full.
type schema struct {
	Argv []string `json:"argv"` // the output folder is appended
	Keep string   `json:"keep"`
}

// groups are the reviews, each with its own budget; drift.yml's review matrix lists the same.
var groups = []string{"local", "anthropic-cloud", "openai-cloud", "third-party-cloud"}

// agentWatch is what the probe watches for each agent module. A module without an entry
// fails the tests.
var agentWatch = map[agent.ID]target{
	"claude": {
		Group:    "local",
		Surface:  "transcripts and their records under the Claude config folder, `claude --resume` with its fork and Remote Control flags, `claude auth status --json`, `--version` output, CLAUDE.md, skills, settings and the desktop app",
		Priority: "high",
		Latest:   latest{From: "npm", Ref: "@anthropic-ai/claude-code"},
		Package:  "@anthropic-ai/claude-code",
		Watch: watch{
			Docs:   claudeDocs("sessions", "settings", "skills", "hooks", "headless", "cli-reference", "claude-directory", "permissions"),
			Feeds:  []feed{{Kind: "markdown", URL: claudeChangelog}},
			Grep:   `session|transcript|jsonl|resume|fork|CLAUDE_CONFIG_DIR|skills|CLAUDE\.md|AGENTS\.md|settings|auth|desktop|deprecat|remov|rename|breaking`,
			Help:   [][]string{{"claude", "--help"}, {"claude", "auth", "status", "--help"}},
			Relies: []string{"--resume", "--fork-session", "--remote-control", "--desktop", "--version", "--json"},
		},
	},
	"codex": {
		Group:    "local",
		Surface:  "rollout files under CODEX_HOME and their session_meta, session_index.jsonl, `codex resume` and `codex fork`, the `codex app-server` JSON-RPC methods for threads and accounts, `--version` output, AGENTS.md, skills and config.toml",
		Priority: "high",
		Latest:   latest{From: "npm", Ref: "@openai/codex"},
		Package:  "@openai/codex",
		Watch: watch{
			Docs:  codexDocs("app-server", "config-file/config-reference", "build-skills", "agent-configuration/agents-md", "non-interactive-mode"),
			Feeds: []feed{{Kind: "releases", Repo: "openai/codex", Tag: "rust-v"}},
			// The storage words track three gaps in the module: paginated history is the
			// default for new threads since 0.158, rollouts may be zstd-compressed
			// (.jsonl.zst), and a reverted thread has several rollout files.
			Grep:   `session|rollout|thread|resume|fork|CODEX_HOME|skills|AGENTS\.md|config\.toml|account|auth|app-server|paginated|history_mode|jsonl\.zst|zstd|compress|revert|migrate-rollouts|deprecat|remov|rename|breaking`,
			Help:   [][]string{{"codex", "--help"}, {"codex", "resume", "--help"}, {"codex", "fork", "--help"}, {"codex", "app-server", "--help"}},
			Relies: []string{"resume", "fork", "app-server", "--version"},
			Code: &code{
				Repo:     "openai/codex",
				Paths:    []string{"codex-rs/protocol/src/protocol.rs", "codex-rs/rollout", "codex-rs/thread-store", "codex-rs/history/src/rollout_payload.rs", "codex-rs/cli/src/main.rs"},
				Canaries: []string{"history_mode", "ThreadHistoryMode", ".jsonl.zst", "rollout_id", "migrate-rollouts"},
			},
			Schema: &schema{
				Argv: []string{"codex", "app-server", "generate-json-schema", "--out"},
				Keep: `^(Initialize|Thread|Account|ExternalAgentConfig)|^(ClientRequest|ServerNotification)\.json$`,
			},
			Tests: "TestCodex",
		},
	},
}

// clouds are the vendor clouds and standards, in review order. None has hopsesh code yet;
// they are watched so the cloud design learns of changes before it is built.
var clouds = []target{
	{
		ID: "claude-cloud", Name: "Claude Code cloud", Kind: "cloud", Group: "anthropic-cloud", Vendor: "Anthropic",
		Surface:  "cloud sessions started with `claude --cloud` or `--remote` (and `-p … --cloud --output-format json`), brought back with `claude --teleport <id>`, Remote Control, cloud environments, and the transcript records a teleport or bridge leaves",
		Priority: "high",
		Module:   "claude",
		Latest:   latest{From: "npm", Ref: "@anthropic-ai/claude-code"},
		Package:  "@anthropic-ai/claude-code",
		Watch: watch{
			Docs: append(claudeDocs("claude-code-on-the-web", "web-quickstart", "cloud-environments", "remote-control", "desktop",
				"sessions", "routines", "self-hosted-environments", "env-vars", "feature-availability", "data-usage", "legal-and-compliance"),
				"https://code.claude.com/docs/llms.txt"),
			Feeds: []feed{{Kind: "markdown", URL: claudeChangelog}},
			Grep:  `teleport|--cloud|--remote|Remote Control|bridge|cloud session|cse_|self-hosted|Continue in|environment|sessions:|deprecat`,
			// `claude remote-control --help` needs a claude.ai login, so it is not run.
			Help:   [][]string{{"claude", "--help"}},
			Relies: []string{"--cloud", "--remote", "--teleport", "--environment", "--remote-control", "--session-id", "--fork-session", "--resume"},
			Issues: []string{
				"anthropics/claude-code#66373", // local → cloud handoff from the CLI
				"anthropics/claude-code#97813", // attach to a running cloud session
				"anthropics/claude-code#97446", // archive a cloud session from the CLI
				"anthropics/claude-code#93892", // Remote Control teleport
				"anthropics/claude-code#95873", // Remote Control teleport
				"anthropics/claude-code#94836", // partial teleport
				"anthropics/claude-code#92734", // web → desktop, prompt history
			},
			Searches: []string{"repo:anthropics/claude-code is:issue teleport", "repo:anthropics/claude-code is:issue \"cloud session\""},
		},
	},
	{
		ID: "codex-cloud", Name: "Codex cloud", Kind: "cloud", Group: "openai-cloud", Vendor: "OpenAI",
		Surface:  "cloud tasks through `codex cloud exec --env --branch` (with CODEX_STARTING_DIFF), `codex cloud list --json`, `status`, `diff` and `apply`, `codex apply`, the cloud environments, and the docs sentence that a handoff to a Codex cloud environment isn't supported",
		Priority: "high",
		Module:   "codex",
		Latest:   latest{From: "npm", Ref: "@openai/codex"},
		Package:  "@openai/codex",
		Watch: watch{
			// Never the 2.9 MB codex-manual.md.
			Docs: append(codexDocs("cloud", "environments/cloud-environments", "environments/cloud-environment", "environments/modes",
				"developer-commands", "remote-connections", "import", "auth", "pricing", "third-party/github"),
				"https://developers.openai.com/codex/llms.txt"),
			Feeds: []feed{
				{Kind: "feed", URL: "https://developers.openai.com/codex/changelog/rss.xml"},
				{Kind: "feed", URL: "https://github.com/openai/codex/releases.atom"},
			},
			Grep: `cloud|handoff|wham|thread/|ThreadService|paginated|rollout|Legacy|environment|apply|deprecat`,
			Help: [][]string{
				{"codex", "--help"}, {"codex", "cloud", "--help"}, {"codex", "cloud", "exec", "--help"}, {"codex", "cloud", "list", "--help"},
				{"codex", "cloud", "status", "--help"}, {"codex", "cloud", "diff", "--help"}, {"codex", "cloud", "apply", "--help"},
				{"codex", "apply", "--help"}, {"codex", "features", "list"},
			},
			// A new resume, attach or pull subcommand would show in the help diffs.
			Relies: []string{"cloud", "exec", "list", "status", "diff", "apply", "--env", "--branch", "--json", "--limit"},
			Issues: []string{"openai/codex#50113"},
			Code: &code{
				Repo: "openai/codex",
				Paths: []string{"codex-rs/cloud-tasks", "codex-rs/cloud-tasks-client", "codex-rs/cloud-client", "codex-rs/backend-client",
					"codex-rs/chatgpt", "codex-rs/app-server-protocol/src/protocol/v2/thread.rs", "codex-rs/history/src/rollout_payload.rs",
					"codex-rs/protocol/src/protocol.rs", "codex-rs/thread-store"},
				Canaries: []string{"/wham/tasks", "CODEX_STARTING_DIFF", "pre_apply_patch", "ThreadService/Resume", "ThreadService/Attach",
					"thread/resume.history", ".jsonl.zst"},
			},
		},
	},
	{
		ID: "copilot-cloud", Name: "Copilot cloud agent", Kind: "cloud", Group: "third-party-cloud", Vendor: "GitHub",
		Surface:  "agent tasks through `gh agent-task` and the REST agent-tasks API (its X-GitHub-Api-Version date)",
		Priority: "high",
		Latest:   latest{From: "github-release", Ref: "cli/cli"},
		Watch: watch{
			Docs: githubDocs("rest/agent-tasks/agent-tasks", "copilot/how-tos/copilot-cli/use-copilot-cli/delegate-tasks-to-cca",
				"copilot/concepts/agents/coding-agent/about-coding-agent"),
			Feeds: []feed{
				{Kind: "feed", URL: "https://github.blog/changelog/feed/"},
				{Kind: "feed", URL: "https://github.com/cli/cli/releases.atom"},
			},
			Grep:   `copilot|agent.task|agent-task|coding agent|cloud agent|X-GitHub-Api-Version`,
			Help:   [][]string{{"gh", "agent-task", "--help"}, {"gh", "agent-task", "create", "--help"}, {"gh", "agent-task", "list", "--help"}, {"gh", "agent-task", "view", "--help"}},
			Relies: []string{"create", "list", "view"},
		},
	},
	{
		ID: "jules", Name: "Jules", Kind: "cloud", Group: "third-party-cloud", Vendor: "Google",
		Surface:  "Jules sessions through the v1alpha REST API (its discovery document) and `jules remote`",
		Priority: "high",
		Latest:   latest{From: "json", Ref: julesDiscovery, Field: ".revision"},
		Package:  "@google/jules",
		Watch: watch{
			Docs:   []string{julesDiscovery, "https://jules.google/docs/cli/reference.md"},
			Feeds:  []feed{{Kind: "markdown", URL: "https://jules.google/docs/changelog.md"}},
			Grep:   `session|activit|remote|pull|teleport|api|deprecat`,
			Help:   [][]string{{"jules", "--help"}, {"jules", "remote", "--help"}},
			Relies: []string{"remote", "list", "pull"},
		},
	},
	{
		ID: "devin", Name: "Devin", Kind: "cloud", Group: "third-party-cloud", Vendor: "Cognition",
		Surface: "Devin sessions through the v3 REST API (its OpenAPI document) and the Devin CLI's cloud and handoff commands",
		// The Devin CLI installs from a download script, not a package, so its help is not run.
		Priority: "high",
		Latest:   latest{From: "json", Ref: devinOpenAPI, Field: ".info.version"},
		Watch: watch{
			Docs:  []string{devinOpenAPI, "https://docs.devin.ai/llms.txt", "https://docs.devin.ai/cli/handoff.md", "https://docs.devin.ai/cli/cloud.md"},
			Feeds: []feed{{Kind: "markdown", URL: "https://docs.devin.ai/cli/changelog/stable.md"}},
			Grep:  `session|handoff|cloud|--cloud|teleport|pull|api|deprecat`,
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
		ID: "amp", Name: "Amp", Kind: "cloud", Group: "third-party-cloud", Vendor: "Sourcegraph",
		Surface:  "threads, orbs and the SDK (docs only; no plan to drive it yet)",
		Priority: "low",
		Latest:   latest{From: "none"},
		Watch: watch{
			Docs:  []string{"https://ampcode.com/manual.md", "https://ampcode.com/manual/orbs.md", "https://ampcode.com/manual/sdk.md"},
			Feeds: []feed{{Kind: "feed", URL: "https://ampcode.com/news.rss"}},
			Grep:  `thread|orb|sdk|cloud|handoff|deprecat`,
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

// githubDocs are docs.github.com articles as Markdown, through the docs site's own API.
func githubDocs(pages ...string) []string {
	return prefixed("https://docs.github.com/api/article/body?pathname=/en/", "", pages)
}

func prefixed(pre, suf string, pages []string) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = pre + p + suf
	}
	return out
}

// buildTargets returns the agents, from their modules, then the clouds. Tested comes
// from the driving module's newest fixture folder.
func buildTargets(mods []module) ([]target, error) {
	tested := map[string]string{}
	var out []target
	for _, m := range mods {
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
