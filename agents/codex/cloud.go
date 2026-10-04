package codex

import "github.com/roeehrl/hopsesh/sdk/agent"

// cloud declares Codex cloud (the legacy cloud the open-source CLI talks to): the module
// reaches it through the codex binary only (codex cloud exec, list, status, diff; codex
// login status), never through the ChatGPT backend.
func cloud() agent.Cloud {
	return agent.Cloud{
		Name:   cloudName,
		Title:  "Codex cloud",
		Driver: "codex",
		// The cloud commands were read from 0.153.2's help and their output from openai/codex's
		// source; they run against the stand-in cloud in the tests, and scripts/cloud-smoke.sh
		// checks a real round trip by hand.
		Tested: []string{"0.153"},
		Hosts:  []string{"github.com"},
		// Up is a briefing in the task's prompt; down is the task's diff, its title and a
		// summary (the CLI prints no messages).
		Up:       agent.FidBrief,
		Down:     agent.FidCode,
		CodeUp:   []agent.CodeWay{agent.ViaBranch, agent.ViaStartingDiff},
		CodeDown: []agent.CodeWay{agent.ViaDiff},
		Needs:    []agent.Need{agent.NeedSubscriptionLogin, agent.NeedGitHub, agent.NeedPushedBranch, agent.NeedEnvironment},
		Noun:     "task",
		// The new VM-based Codex Cloud (DevDay, 2026-09-29) is used from the web, the phone and
		// the desktop app only; its native client (ThreadService, openai/codex#50113) is not
		// released.
		Limits: []string{
			"Codex cloud (legacy) tasks only: the new Codex Cloud has no command line yet",
			"A task comes back as its title and its diff; its messages stay in the cloud",
		},
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
				{"codex", "apply", "--help"}, {"codex", "login", "status", "--help"}, {"codex", "features", "list"},
			},
			// A new resume, attach or pull subcommand would show in the help diffs.
			Relies: []string{"cloud", "exec", "list", "status", "diff", "--env", "--branch", "--attempts", "--json", "--limit", "--cursor", "login status"},
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
