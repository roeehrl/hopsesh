# hopsesh reference (version 0.7.0)

## Read-only commands

| Command | Does |
|---|---|
| `hopsesh ls --json [--host M] [--agent A] [--repo R] [--live] [--no-git] [--limit N]` | list sessions |
| `hopsesh show <ref> --json` | one session's details and lineage |
| `hopsesh plan <ref> --json [--in A] [flags]` | what pulling it would do; never writes |
| `hopsesh agents --json` | the agents hopsesh supports and what is installed here |
| `hopsesh hosts --json` | known machines and whether each is allowed |
| `hopsesh hosts status [machine] --json [--refresh]` | what hopsesh can do on each machine: its version against the newest, receiving, command, each agent's hook and skill, with the fix for each (exit 3 when one needs something) |
| `hopsesh installs --json` | every hopsesh on this machine (the app, commands, packages, hopsesh's own copy): version, owner, which one the machine runs |
| `hopsesh clouds --json` | the agents' clouds, whether each is allowed, and their state |
| `hopsesh clouds test [cloud]` | check a cloud's login and flags, read-only |
| `hopsesh plan <ref> --to <cloud> --json [flags]` | what handing it off to a cloud would do; never writes |
| `hopsesh doctor [machine] --json` | SSH, host trust, agents, skill |
| `hopsesh version` | the installed version |

## Needs the user's yes first

`hopsesh pull <ref> --json --yes [flags]` moves the session here or continues it in another
agent. `hopsesh push <ref> <machine> --json --yes [flags]` sends a session on this machine to
another machine whose hopsesh receives sessions (same flags, `--dry-run` to plan; `--to` is a
folder there). `hopsesh handoff <ref> --to <cloud> --json --yes [flags]` starts a cloud session
that continues it (a briefing and a branch); for `claude-cloud` only the user can run it, in
their own terminal (Claude Code starts the session there and may ask them a question).
`hopsesh clouds cleanup --list --json` lists the branches hand-offs left on the remotes and
which are merged, and `hopsesh clouds cleanup --yes` deletes the merged ones (undo pushes them
back); the approval rule covers both. `hopsesh clouds continue <hop>` takes a hop whose first
leg waited for a terminal on to the next cloud. `hopsesh cd <ref> <dir> --json --yes [--other-repo] [--stop-local]` continues a session on this
machine in another folder, keeping the session (`--dry-run` plans; an open Claude Code session
changes folder when the user types `/cd <dir>` in it, which hopsesh waits for, up to `--wait`).
`hopsesh installs --align --yes` brings the other copies of hopsesh on this machine to this
version (the app with its own updater). `hopsesh undo <id>` reverses a pull, a push, a folder change or
a hand-off (a cloud session itself stays in the cloud).

## handoff flags

| Flag | Meaning |
|---|---|
| `--to <cloud>` | the cloud (`claude-cloud`, `codex-cloud`, `copilot-cloud`, `jules`, `devin`, `amp`) |
| `--env <id or name>` | `codex-cloud`: the environment the task runs in (only the one the user picks; the repository's remembered one is the default) |
| `--attempts <1-4>` | `codex-cloud`: that many attempts at once (only when the user asks) |
| `--starting-diff` | `codex-cloud`: the changes go with the task instead of on a new branch (the session's branch must be on the remote as it is) |
| `--untracked <path or glob>` | carry this untracked file (repeat it); credential-like files never go |
| `--bundle` | the agent uploads the repository instead of hopsesh pushing a branch (a repository not on GitHub; results can't be pushed back) |
| `--history-file` | also commit the conversation as `.hopsesh/handoff.md` (only when the user asks: anyone who can see the branch can read it) |
| `--note-file <path>` | a handoff note for the briefing |
| `--brief-file <path>` | the briefing the user wrote, instead of hopsesh's |
| `--carry-rules` | add the user's instructions for every project to the briefing (only when the user asks) |
| `--cleanup after-merge\|on-undo\|never` | when hopsesh deletes the handoff branch |

## plan and pull flags

| Flag | Meaning |
|---|---|
| `--in <claude|codex>` | continue in this agent (default: the session's own agent) |
| `--fidelity history\|note` | another agent gets the whole conversation as text (default), or only a briefing |
| `--native` | another agent that can replays exact tool calls as its own (experimental) |
| `--note-file <path>` | a handoff note for the other agent's briefing |
| `--via import` | let the other agent's own importer convert the session (Codex reads Claude Code sessions); hopsesh still adds its briefing |
| `--carry-rules` | add the user's instructions for every project of the session's agent to the briefing (only when the user asks) |
| `--go` | start the continued session with "Continue." |
| `--clone` | clone the repository into the repos folder if it is not here |
| `--to <dir>` | continue in this directory instead of matching the repository |
| `--worktree auto\|create\|main` | recreate the session's worktree (auto), always use one, or the main checkout |
| `--rc` | turn the agent's remote control on, where it has one |
| `--notify` | protect the original until the session moves back: blocked or warned, per the user's setting (default on; `--notify=false` leaves it alone) |
| `--redact` | redact likely secrets in the copy |
| `--stop-local` | quit a copy of this session open here first |
| `--keep-both` / `--replace` | when both copies changed: keep both / replace the copy here |
| `--no-sync` | leave the checkout alone (default: fetch, and fast-forward when clean) |
| `--push` | push the source branch's unpushed commits first |

## JSON

`plan --json` prints the plan: `kind` (`move` or `continue`), `key`, `title`, `agent`,
`source`, `target.cwd`, `repo`, `sync`, `conflict`, `warnings`, `blockers`, and for
another agent `continue` (`relation`, `report`, `briefing`). `pull --json --yes` prints
`{"plan": …, "result": …, "command": "…"}`; the result has `journal` (for undo), `syncNote`,
`pushed`, `pushError`, `warnings`. `handoff --json --yes` prints
`{"plan": …, "result": …, "handoff": …}` (and `error` when a step failed); `handoff` has
`session`, `url`, `branch`, `env`, `noun`, `steps`, `stayed`, `failed`, `message`, `manual`,
`pasted` (the user gave the session's link by hand) and `noFollowUp`. A pull from a cloud prints `{"plan": …, "result": …, "brought": …}`; `brought`
has `outcome`, `message`, `branch`, `worktree`, `command`, `written` (hopsesh wrote the
session from what the cloud gave back) and `loss`. `hopsesh clouds env codex-cloud --json`
lists each repository's Codex cloud environment.

## Permissions and the sandbox

hopsesh reads other machines over SSH and writes into the agents' data folders, which agent
sandboxes usually block. `hopsesh skill install --add-rules` adds approval rules for each agent:
the read-only commands run without asking (and, in Codex, outside its sandbox), while `pull`,
`push`, `handoff`, `undo`, `clouds cleanup` and `clouds continue` always ask.
