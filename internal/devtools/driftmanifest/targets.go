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
	// Baseline is what the probe compares the latest version with: the highest of Tested
	// and the Floors. Help below a floor the code gates on says nothing about what hopsesh
	// runs. Empty means Tested.
	Baseline string  `json:"baseline,omitempty"`
	Floors   []floor `json:"floors,omitempty"`
	Latest   latest  `json:"latest"`
	// Package is the npm package with the CLI that Watch.Help runs. The probe installs it
	// at Tested and at its latest version and diffs the two; without a package the help
	// comes from the runner's own CLI (gh) and is compared with last week's.
	Package     string   `json:"package,omitempty"`
	VersionArgv []string `json:"versionArgv,omitempty"` // prints the version hopsesh parses
	Watch       watch    `json:"watch"`
}

// floor is a minimum version the code gates a feature on (agent.VersionAtLeast or
// HookVersionAtLeast; TestFloorsDeclared finds every one). With a package, the probe runs
// the help, and the schema, at the floor too: what the gated feature runs (Relies,
// Methods) must be there at the floor and at the latest.
type floor struct {
	Version string   `json:"version"`
	Gates   string   `json:"gates"`             // what the code gates on it, in plain words
	Source  string   `json:"source"`            // the file with the check
	Relies  []string `json:"relies,omitempty"`  // flags and subcommands of the help it runs
	Methods []string `json:"methods,omitempty"` // strings of the generated schema it calls ("hooks/list")
}

// format is a record or identity format hopsesh reads (or writes) in an agent's or app's
// files, with the names it relies on. The probe counts each field in the target's docs,
// in the week's changes (feeds, docs, help and schema diffs) and in the upstream code it
// checks out: a field that leaves the code or shows up in a change is for the review.
type format struct {
	ID      string   `json:"id"`      // "codex-session-meta"
	What    string   `json:"what"`    // where it lives and what hopsesh takes from it
	Fields  []string `json:"fields"`  // literal names: keys, record types, values, file names
	Readers []string `json:"readers"` // the project's files that read it; each field is in one
}

// latest says where the probe reads a target's newest version.
type latest struct {
	From  string `json:"from"`            // npm, github-release, json, feed (its newest item's title) or none
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
	// Formats are the records and identities hopsesh reads in the target's files.
	Formats []format `json:"formats,omitempty"`
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
			Grep:   `session|transcript|jsonl|resume|fork|CLAUDE_CONFIG_DIR|hook|SessionStart|UserPromptSubmit|/hooks|skills|CLAUDE\.md|AGENTS\.md|settings|auth|account|login|keychain|credential|Console|oauthAccount|organization|desktop|entrypoint|claude://|deep.link|deprecat|remov|rename|breaking`,
			Help:   [][]string{{"claude", "--help"}, {"claude", "auth", "--help"}, {"claude", "auth", "status", "--help"}, {"claude", "auth", "login", "--help"}},
			Relies: []string{"--resume", "--fork-session", "--remote-control", "--desktop", "--version", "-v", "--json", "auth", "auth login", "auth status"},
			Formats: []format{
				{ID: "claude-oauth-account", What: "~/.claude.json (or <CLAUDE_CONFIG_DIR>/.claude.json) oauthAccount: the signed-in account and organization, ids only", Fields: []string{"oauthAccount", "accountUuid", "organizationUuid", "emailAddress"}, Readers: []string{"agents/claude/account.go"}},
				{ID: "claude-auth-status", What: "`claude auth status --json`: the public sign-in state and its config directory", Fields: []string{"loggedIn", "authMethod", "apiProvider", "orgId", "configDirectory", "subscriptionType"}, Readers: []string{"agents/claude/account.go"}},
				{ID: "claude-transcript", What: "transcript records: the conversation tree, titles, compaction and relocation", Fields: []string{"parentUuid", "logicalParentUuid", "isSidechain", "isCompactSummary", "customTitle", "aiTitle", "leafUuid", "relocatedCwd", "sessionId"}, Readers: []string{"agents/claude/reader.go"}},
				{ID: "claude-transcript-identity", What: "the organization each launch ran under (a credential_org attachment) and Remote Control bridge-session records with their owner", Fields: []string{"credential_org", "attachment", "bridge-session", "bridgeSessionId", "ownerOrganizationUuid", "ownerAccountUuid"}, Readers: []string{"agents/claude/summary.go", "agents/claude/claude.go"}},
				{ID: "claude-live-registry", What: "sessions/<pid>.json: the live registry of running sessions (what started each, and what it waits for)", Fields: []string{"pid", "sessionId", "status", "waitingFor", "entrypoint", "claude-desktop"}, Readers: []string{"agents/claude/live.go"}},
				{ID: "claude-hooks", What: "settings.json hooks (SessionStart, UserPromptSubmit), the payload a hook reads and the output it prints", Fields: []string{"hooks", "SessionStart", "UserPromptSubmit", "disableAllHooks", "session_id", "transcript_path", "hook_event_name", "hookSpecificOutput", "hookEventName", "additionalContext", "decision", "systemMessage"}, Readers: []string{"sdk/agent/hooks_json.go", "agents/claude/hooks.go", "internal/ui/cli/movement_hooks.go"}},
			},
		},
		Floors: []floor{
			{Version: "2.1.284", Gates: "the SessionStart/UserPromptSubmit protection hooks", Source: "agents/claude/hooks.go"},
			{Version: "2.1.285", Gates: "opening a session in Claude desktop (`claude --desktop --resume <uuid>`)", Source: "agents/claude/desktop.go", Relies: []string{"--desktop", "--resume"}},
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
			Grep:   `session|rollout|thread|resume|fork|CODEX_HOME|CODEX_SQLITE_HOME|cli_auth_credentials_store|keyring|refreshToken|workspace|identity|desktop|deep.link|codex://|hook|SessionStart|UserPromptSubmit|hooks/list|/hooks|trust|skills|AGENTS\.md|config\.toml|account|auth|id_token|originator|creator_|thread_source|writer.lock|daemon|app-server|paginated|history_mode|jsonl\.zst|zstd|compress|revert|migrate-rollouts|deprecat|remov|rename|breaking`,
			Help:   [][]string{{"codex", "--help"}, {"codex", "resume", "--help"}, {"codex", "fork", "--help"}, {"codex", "app-server", "--help"}, {"codex", "login", "--help"}, {"codex", "login", "status", "--help"}},
			Relies: []string{"resume", "fork", "app-server", "--version", "login", "login status", "--no-daemon"},
			Code: &code{
				Repo: "openai/codex",
				Paths: []string{"codex-rs/protocol/src/protocol.rs", "codex-rs/rollout", "codex-rs/thread-store", "codex-rs/history/src/rollout_payload.rs", "codex-rs/cli/src/main.rs", "codex-rs/app-server-protocol/src/protocol/v2/account.rs", "codex-rs/app-server/src/request_processors/account_processor", "codex-rs/login", "codex-rs/keyring-store", "codex-rs/core/src/config/auth_keyring.rs", "codex-rs/config/src/lib.rs", "codex-rs/config/src/types.rs", "codex-rs/config/src/config_toml.rs",
					"codex-rs/core/src/thread_manager.rs", "codex-rs/core/src/session/session.rs", "codex-rs/hooks/schema/generated", "codex-rs/config/src/hook_config.rs", "codex-rs/app-server-protocol/src/protocol/common.rs", "codex-rs/install-context/src/lib.rs", "codex-rs/tui/src/daemon_startup.rs",
					"codex-rs/otel/src/auth_storage/originator.rs", "codex-rs/app-server-protocol/schema/json/v2/HooksListResponse.json"},
				Canaries: []string{"history_mode", "ThreadHistoryMode", ".jsonl.zst", "rollout_id", "migrate-rollouts", "refresh_token", "GetAccountResponse", "AccountLogin", "Chatgpt", "cli_auth_credentials_store", "CODEX_SQLITE_HOME", "hooks/list", "no-daemon"},
			},
			Schema: &schema{
				Argv: []string{"codex", "app-server", "generate-json-schema", "--out"},
				Keep: `^(Initialize|Thread|Account|GetAccount|LoginAccount|LogoutAccount|CancelLoginAccount|ExternalAgentConfig|Hook)|^(ClientRequest|ServerNotification)\.json$`,
			},
			Tests: "TestCodex",
			Formats: []format{
				{ID: "codex-auth", What: "auth.json: tokens.account_id and the ID token's claims (ids only; the tokens are never used)", Fields: []string{"tokens", "account_id", "id_token", "chatgpt_account_id", "chatgpt_user_id", "user_id"}, Readers: []string{"agents/codex/identity.go"}},
				{ID: "codex-session-meta", What: "a rollout's session_meta: who ran it (creator ids), what wrote it (originator: codex_cli_rs, Codex Desktop, codex_work_desktop), voice threads, forks and history mode", Fields: []string{"session_meta", "creator_account_id", "creator_user_id", "originator", "codex_cli_rs", "Codex Desktop", "codex_work_desktop", "thread_source", "realtime_voice", "cli_version", "forked_from_id", "history_mode"}, Readers: []string{"agents/codex/codex.go"}},
				{ID: "codex-index", What: "session_index.jsonl: thread names the resume picker shows", Fields: []string{"session_index.jsonl", "thread_name"}, Readers: []string{"agents/codex/codex.go"}},
				{ID: "codex-writer-lock", What: "thread-writer-locks/<id>.lock: the lock a running thread's writer holds", Fields: []string{"thread-writer-locks", ".lock"}, Readers: []string{"agents/codex/codex.go"}},
				{ID: "codex-hooks", What: "hooks.json (SessionStart, UserPromptSubmit), its payload and output, and hook trust through app-server hooks/list (/hooks in the TUI)", Fields: []string{"hooks.json", "SessionStart", "UserPromptSubmit", "hooks/list", "trustStatus", "eventName", "enabled", "session_id", "transcript_path", "hook_event_name", "hookSpecificOutput", "additionalContext"}, Readers: []string{"agents/codex/hooks.go", "agents/codex/hooktrust.go", "sdk/agent/hooks_json.go", "internal/ui/cli/movement_hooks.go"}},
			},
		},
		Floors: []floor{
			{Version: "0.129.0", Gates: "the protection hooks: user hooks and hook trust read through app-server hooks/list", Source: "agents/codex/hooks.go", Methods: []string{"hooks/list"}},
			{Version: "0.156.0", Gates: "`--no-daemon` on resume and fork in hopsesh's own terminal tabs (native writer-lock evidence)", Source: "internal/ui/gui/terminal_binding.go", Relies: []string{"--no-daemon"}},
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

// apps are the agents' desktop apps and the runtimes they bundle: hopsesh finds them,
// reads their sign-ins and opens sessions in them by URL. No package prints their help,
// so they are watched through their release feeds, docs and URL schemes.
var apps = []target{
	{
		ID: "claude-desktop", Name: "Claude desktop", Kind: "app", Group: "local", Vendor: "Anthropic",
		Surface: "the Claude desktop app (bundle com.anthropic.claudefordesktop, its public version), its own Claude Code under Application Support/Claude/claude-code/<version>/ " +
			"(CLAUDE_CODE_ENTRYPOINT=claude-desktop), its signed-in account (config.json lastKnownAccountUuid, claude-code-sessions/<account>/<org>/), " +
			"and claude://resume?session=<id>, the route that focuses a session Desktop already runs",
		Priority: "high",
		Latest:   latest{From: "json", Ref: "https://downloads.claude.ai/releases/darwin/universal/RELEASES.json", Field: ".currentRelease"},
		Floors: []floor{
			{Version: "2.19675.1", Gates: "focusing a session Desktop already runs through claude://resume?session=<id> (older builds can duplicate the chat)", Source: "agents/claude/desktop.go"},
		},
		Watch: watch{
			Docs:   claudeDocs("desktop", "desktop-quickstart", "desktop-linux", "deep-links"),
			Feeds:  []feed{{Kind: "markdown", URL: claudeChangelog}},
			Grep:   `desktop|Desktop|claude://|claude-cli://|deep.link|url scheme|protocol|resume|entrypoint|claude-code-sessions|lastKnownAccount|bundle|Code tab|deprecat|remov|rename|breaking`,
			Issues: []string{"anthropics/claude-code#80773"},
			Formats: []format{
				{ID: "claude-desktop-identity", What: "Desktop's config.json and its Code sessions folder: the account it is signed in to, ids only", Fields: []string{"lastKnownAccountUuid", "claude-code-sessions"}, Readers: []string{"agents/claude/desktop.go", "agents/claude/account.go"}},
				{ID: "claude-desktop-runtime", What: "the bundle, its version and the Claude Code it runs (Application Support/Claude/claude-code/<version>/)", Fields: []string{"claudefordesktop", "CFBundleShortVersionString", "Application Support", "claude-code", "CLAUDE_CODE_ENTRYPOINT"}, Readers: []string{"agents/claude/desktop.go", "agents/claude/account.go"}},
				{ID: "claude-desktop-link", What: "the URL that focuses a running Desktop session", Fields: []string{"claude://resume", "session="}, Readers: []string{"agents/claude/claude.go"}},
			},
		},
	},
	{
		ID: "codex-desktop", Name: "Codex app (ChatGPT desktop)", Kind: "app", Group: "local", Vendor: "OpenAI",
		Surface: "the ChatGPT desktop app's Codex (bundle com.openai.codex, ChatGPT.app or Codex.app), the runtime it bundles at Contents/Resources/codex-cli/bin/codex " +
			"with codex-package.json's version (used when no Codex CLI is installed; HOPSESH_APP_RUNTIMES=off in tests), and codex://threads/<uuid> for opening a thread",
		Priority: "high",
		Latest:   latest{From: "feed", Ref: codexAppcast},
		Watch: watch{
			Docs:  codexDocs("app", "reference/commands", "reference/settings", "windows/windows-app", "enterprise/manage-app-updates", "remote-connections"),
			Feeds: []feed{{Kind: "feed", URL: codexAppcast}},
			Grep:  `desktop|codex://|deep.link|url scheme|threads/|bundle|codex-cli|codex-package|runtime|com\.openai\.codex|ChatGPT\.app|deprecat|remov|rename|breaking`,
			Formats: []format{
				{ID: "codex-app-bundle", What: "the app bundle and the Codex it bundles: its identifier, codex-package.json's version and entrypoint", Fields: []string{"com.openai.codex", "codex-cli", "codex-package.json", "version", "entrypoint"}, Readers: []string{"agents/codex/desktop.go"}},
				{ID: "codex-app-link", What: "the URL that opens a thread in the app", Fields: []string{"codex://threads/"}, Readers: []string{"agents/codex/desktop.go"}},
			},
		},
	},
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
	codexAppcast    = "https://persistent.oaistatic.com/codex-app-prod/appcast.xml"
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
	out = append(out, apps...)
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
	for i := range out {
		out[i].Baseline = baseline(out[i])
	}
	return out, nil
}

// baseline is the highest of the tested version and the floors, or "" with neither.
func baseline(t target) string {
	var vs []string
	for _, v := range append([]string{t.Tested}, floorVersions(t)...) {
		if v != "" {
			vs = append(vs, v)
		}
	}
	return newest(vs)
}

func floorVersions(t target) []string {
	out := make([]string, len(t.Floors))
	for i, f := range t.Floors {
		out[i] = f.Version
	}
	return out
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
	if c, ok := agent.CompareVersions(a, b); ok {
		return c < 0
	}
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
