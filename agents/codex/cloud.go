package codex

import "github.com/roeehrl/hopsesh/sdk/agent"

// cloud declares Codex cloud (the legacy cloud the open-source CLI talks to) as data: the
// module reaches it through the codex binary only (codex cloud exec, list, status, diff,
// apply), never through the ChatGPT backend. The capability methods come later; the
// declaration already drives the drift check and the location model.
func cloud() agent.Cloud {
	return agent.Cloud{
		Name:   "codex-cloud",
		Title:  "Codex cloud",
		Driver: "codex",
		// The cloud commands were read from 0.153.2's help; they run against the stand-in
		// cloud in the tests until a real round trip is checked by hand.
		Tested: []string{"0.153"},
		Hosts:  []string{"github.com"},
		// Up is a briefing in the task's prompt; down is the task's diff, title and summary.
		Up:       agent.FidBrief,
		Down:     agent.FidCode,
		CodeUp:   []agent.CodeWay{agent.ViaBranch, agent.ViaStartingDiff},
		CodeDown: []agent.CodeWay{agent.ViaDiff, agent.ViaPR},
		Needs:    []agent.Need{agent.NeedSubscriptionLogin, agent.NeedGitHub, agent.NeedPushedBranch, agent.NeedEnvironment},
		Watch: agent.Watch{
			Surface: "cloud tasks through `codex cloud exec --env --branch` (with CODEX_STARTING_DIFF), `codex cloud list --json`, `status`, `diff` and `apply`, `codex apply`, the cloud environments, and the docs sentence that a handoff to a Codex cloud environment isn't supported",
			// Never the 2.9 MB codex-manual.md.
			Docs: append(docs("cloud", "environments/cloud-environments", "environments/cloud-environment", "environments/modes",
				"developer-commands", "remote-connections", "import", "auth", "pricing", "third-party/github"),
				"https://developers.openai.com/codex/llms.txt"),
			Feeds: []agent.Feed{
				{Kind: agent.FeedRSS, URL: "https://developers.openai.com/codex/changelog/rss.xml"},
				{Kind: agent.FeedRSS, URL: "https://github.com/openai/codex/releases.atom"},
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
			Code: &agent.WatchCode{
				Repo: "openai/codex",
				Paths: []string{"codex-rs/cloud-tasks", "codex-rs/cloud-tasks-client", "codex-rs/cloud-client", "codex-rs/backend-client",
					"codex-rs/chatgpt", "codex-rs/app-server-protocol/src/protocol/v2/thread.rs", "codex-rs/history/src/rollout_payload.rs",
					"codex-rs/protocol/src/protocol.rs", "codex-rs/thread-store"},
				Canaries: []string{"/wham/tasks", "CODEX_STARTING_DIFF", "pre_apply_patch", "ThreadService/Resume", "ThreadService/Attach",
					"thread/resume.history", ".jsonl.zst"},
			},
		},
	}
}

// docs are Codex documentation pages as Markdown.
func docs(pages ...string) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = "https://learn.chatgpt.com/docs/" + p + ".md"
	}
	return out
}
