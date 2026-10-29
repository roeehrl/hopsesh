# hopsesh plugin

The hopsesh skill for Claude Code and Codex. Ask your agent things like "bring my laptop
session here", "continue this in Codex" or "which sessions are open on the studio?", and it
uses the hopsesh command to list your coding-agent sessions on all your machines, move one
here with the repository, worktree and paths fixed up, continue it in the other agent, hand
it to a cloud, or undo a move. It always shows the plan first and moves only after you say yes.

The plugin needs the hopsesh command on this machine:

```sh
brew install roeehrl/tap/hopsesh
```

On Windows: `scoop install hopsesh` from the `roeehrl` bucket. Other ways to install, Linux
included, are in the [README](https://github.com/roeehrl/hopsesh#install). Other machines need
only an SSH server.

Source and documentation: <https://github.com/roeehrl/hopsesh>. Issues:
<https://github.com/roeehrl/hopsesh/issues>. Apache-2.0. hopsesh is
unofficial and not affiliated with Anthropic or OpenAI.
