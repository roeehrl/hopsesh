package opencode

import (
	_ "embed"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

const id agent.ID = "opencode"

// The roots OpenCode keeps its files in (the same on every OS: it follows the XDG base
// directories everywhere). "db" is not declared: Detect adds it when OPENCODE_DB names
// the database, and only SQL reads it.
const (
	rootData   = "data"
	rootConfig = "config"
	rootState  = "state"
	rootDB     = "db"
)

// kindArchived marks sessions archived in OpenCode, which its own lists leave out.
const kindArchived = "archived"

// binName is OpenCode's program.
const binName = "opencode"

// minVersion is the oldest OpenCode whose database and commands the module reads
// (session_v2 and the replay field "native" came in 2.1.0; session_part and time_busy,
// from 2.2.1 and 2.2.2, are read when there).
const minVersion = "2.1.0"

// iconSVG is a mark drawn for hopsesh (not OpenCode's logo); the installed desktop app's
// icon is preferred.
//
//go:embed icon.svg
var iconSVG string

// sqlTables are the tables the module reads, and no more. kv is read only for the
// models.dev catalog key and the hopsesh plugin's failure count, migration only for its
// latest id.
var sqlTables = []string{"session_v2", "session_message", "session_part", "kv", "migration"}

// sqlDenied hold credentials, accounts and permission grants: never read.
var sqlDenied = []string{"credential", "account", "control_account", "account_state", "permission", "workspace"}

// accounts are OpenCode's account folders (profiles): each keeps its own data (the
// database, so its sessions and sign-ins), state (the background service's registration)
// and config folders, as data/, state/ and config/ inside it. OPENCODE_CONFIG_DIR is set
// instead of XDG_CONFIG_HOME, so the tools OpenCode runs keep their own config; the
// database is pinned too, since an OPENCODE_DB of the shell would name the default
// folder's. The default folder keeps the machine's variables. Each folder hopsesh makes
// gets its own service port (PrepareAccount). Sign-ins are OpenCode's own (`opencode auth
// login --standalone`), per provider; nothing is inherited from the default folder but a few display
// settings of cli.json (opencode.json may hold provider keys, so it is never read).
// Provider keys in the shell (OPENAI_API_KEY, …) reach every folder, as they reach
// OpenCode itself; the server passwords do not.
var accounts = &agent.ProfileSpec{
	RootVars: []agent.ProfileVar{
		{Env: "XDG_DATA_HOME", Sub: "data"},
		{Env: "XDG_STATE_HOME", Sub: "state"},
		{Env: "OPENCODE_CONFIG_DIR", Sub: "config"},
		{Env: "OPENCODE_DB", Sub: "data/opencode/opencode.db"},
	},
	Unset: []string{"OPENCODE_PASSWORD", "OPENCODE_SERVER_PASSWORD"},
	// A private server, so signing in never starts the folder's background service (the
	// default folder's is the desktop app's). OpenCode keeps sign-ins in the folder's
	// database, where a running service reads them.
	Login: []string{"auth", "login", "--standalone"},
	Inherit: map[string][]string{"{config}/cli.json": {"theme.name", "theme.mode", "mouse", "animations", "leader.timeout",
		"scroll.speed", "scroll.acceleration", "cursor.style", "cursor.blinking"}},
}

// Spec declares OpenCode 2.x.
func (*Module) Spec() agent.Spec {
	return agent.Spec{
		ID:            id,
		Name:          "OpenCode",
		Vendor:        "Anomaly",
		Stability:     agent.Experimental,
		Tested:        []string{"2.2", "2.3"},
		DesktopScheme: "opencode",
		// A provider is not a person: sessions resume under whatever OpenCode is signed in to.
		ResumeAcrossAccounts: true,
		Accounts:             accounts,
		Binaries: []agent.Binary{{
			Name: binName,
			Candidates: map[string][]string{
				"*":       {"/opt/homebrew/bin/opencode", "/usr/local/bin/opencode", "~/.local/bin/opencode", "~/.npm-global/bin/opencode", "~/.opencode/bin/opencode"},
				"windows": {"~/AppData/Roaming/npm/opencode.cmd"},
			},
			VersionArgs: []string{"--version"},
		}},
		Roots: []agent.Root{
			{Name: rootData, EnvSub: []agent.EnvSub{{Env: "XDG_DATA_HOME", Sub: "opencode"}}, Default: map[string]string{"*": "~/.local/share/opencode"}},
			{Name: rootConfig, Env: []string{"OPENCODE_CONFIG_DIR"}, EnvSub: []agent.EnvSub{{Env: "XDG_CONFIG_HOME", Sub: "opencode"}}, Default: map[string]string{"*": "~/.config/opencode"}},
			{Name: rootState, EnvSub: []agent.EnvSub{{Env: "XDG_STATE_HOME", Sub: "opencode"}}, Default: map[string]string{"*": "~/.local/state/opencode"}},
		},
		LoginEnv: []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "OPENCODE_CONFIG_DIR", "OPENCODE_DB"},
		// The service registration holds its password; the config may hold provider keys.
		// config.json is the legacy global config, which may hold 1.x provider keys.
		Secrets: []string{"{state}/service*.json", "{config}/service.json", "{data}/auth.json", "{config}/opencode.json", "{config}/opencode.jsonc", "{config}/config.json"},
		// The database is read only through SQL, and is a Secret for every other use.
		SQLFiles:           []string{"{data}/opencode*.db", "{db}"},
		SQLTables:          sqlTables,
		SQLDenied:          sqlDenied,
		Instructions:       []string{"AGENTS.md"},
		GlobalInstructions: []string{"{config}/AGENTS.md"},
		Tools:              "shell, read, edit, write, patch, glob, grep, subagent, todo, webfetch",
		Icon: agent.Icon{SVG: iconSVG, Apps: map[string][]string{
			"darwin": {"/Applications/OpenCode.app", "~/Applications/OpenCode.app"},
		}},
		// One desktop process serves every session, through the same background service.
		Desktop: &agent.Desktop{Tool: "OpenCode desktop", Product: "OpenCode", Shared: true},
		Kinds: []agent.SessionKind{
			{ID: kindArchived, Name: "Archived sessions", Note: "Sessions archived in OpenCode, which its own lists leave out."},
		},
	}
}
