# hopsesh plugin

The hopsesh skill for Claude Code and Codex. Ask your agent things like "bring my laptop
session here", "continue this in Codex" or "which sessions are open on the studio?", and it
uses the hopsesh command to list your coding-agent sessions on all your machines, move one
here with the repository, worktree and paths fixed up, continue it in the other agent, hand
it to a cloud, or undo a move. It always shows the plan first and moves only after you say yes.

The plugin needs the hopsesh command on this machine:

```sh
brew install roeehrl/tap/hopsesh   # or: curl -fsSL https://raw.githubusercontent.com/roeehrl/hopsesh/main/scripts/install.sh | sh
```

On Windows: `scoop install hopsesh` from the `roeehrl` bucket, or the installer from the
[releases](https://github.com/roeehrl/hopsesh/releases/latest). Other machines need only an SSH
server.

Source, documentation and issues: https://github.com/roeehrl/hopsesh. Apache-2.0. hopsesh is
unofficial and not affiliated with Anthropic or OpenAI.
