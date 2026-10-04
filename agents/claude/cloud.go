package claude

import "github.com/roeehrl/hopsesh/sdk/agent"

// cloud declares Claude Code's cloud sessions (Claude Code on the web) as data: the
// module reaches them through the claude binary only (--cloud, -p … --cloud <id>,
// --teleport <id>), never through Anthropic's web backend. The capability methods come
// later; the declaration already drives the drift check and the location model.
func cloud() agent.Cloud {
	return agent.Cloud{
		Name:   "claude-cloud",
		Title:  "Claude Code cloud",
		Driver: "claude",
		// The cloud flags were read from 2.1.284's help; they run against the stand-in
		// cloud in the tests until a real round trip is checked by hand.
		Tested: []string{"2.1"},
		Hosts:  []string{"github.com"},
		// Up is always a briefing: Claude Code cannot push a terminal session to the cloud.
		// Down is the native conversation through --teleport.
		Up:       agent.FidBrief,
		Down:     agent.FidNative,
		CodeUp:   []agent.CodeWay{agent.ViaBranch, agent.ViaBundle},
		CodeDown: []agent.CodeWay{agent.ViaBranch},
		Needs: []agent.Need{agent.NeedSubscriptionLogin, agent.NeedGitHub, agent.NeedPushedBranch, agent.NeedCleanTree,
			agent.NeedSameAccount, agent.NeedTerminal},
		Watch: agent.Watch{
			Surface: "cloud sessions started with `claude --cloud` or `--remote` (and `-p … --cloud --output-format json`), brought back with `claude --teleport <id>`, Remote Control, cloud environments, and the transcript records a teleport or bridge leaves",
			Docs: append(docs("claude-code-on-the-web", "web-quickstart", "cloud-environments", "remote-control", "desktop",
				"sessions", "routines", "self-hosted-environments", "env-vars", "feature-availability", "data-usage", "legal-and-compliance"),
				"https://code.claude.com/docs/llms.txt"),
			Feeds: []agent.Feed{{Kind: agent.FeedMarkdown, URL: "https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md"}},
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
	}
}

// docs are Claude Code documentation pages as Markdown.
func docs(pages ...string) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = "https://code.claude.com/docs/en/" + p + ".md"
	}
	return out
}
