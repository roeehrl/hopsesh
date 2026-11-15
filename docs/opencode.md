# OpenCode (experimental)

hopsesh lists, moves and continues [OpenCode](https://opencode.ai) sessions as it does Claude
Code and Codex sessions: between your machines, into Claude Code or Codex and back, into
account folders, and to the agents' clouds. OpenCode support is **experimental**: it is
tested against stand-ins of OpenCode in every pull request and against a real OpenCode
(without model calls) every night, and it becomes stable after two weeks of green nightly
runs and a manual check on real machines. Please report what you find.

hopsesh is not affiliated with Anomaly, the makers of OpenCode.

## What you need

- **OpenCode 2.1.0 or newer**, the program and its background service. hopsesh is tested with
  2.2 and 2.3. OpenCode 1.x keeps sessions in other tables: hopsesh says it does not recognise
  such a database ("written by OpenCode 1.x") and lists nothing from it.
- The protection plugin (below) needs OpenCode 2.2.7 or newer.
- On another machine, only an SSH server, as for the other agents. hopsesh reads OpenCode's
  database there with `sqlite3` or `python3` when one is installed, and otherwise through
  OpenCode's own commands, which is slower. Windows machines are read the slow way, and have
  not been tried yet.

hopsesh finds `opencode` on your PATH and in the usual install folders (Homebrew, npm,
`~/.opencode/bin`). It counts OpenCode as installed once OpenCode has made its data folder,
`~/.local/share/opencode` (or `$XDG_DATA_HOME/opencode`), on every system. It follows
`XDG_DATA_HOME`, `XDG_STATE_HOME`, `XDG_CONFIG_HOME`, `OPENCODE_CONFIG_DIR` and `OPENCODE_DB`
as OpenCode does. `hopsesh agents` shows what it found.

## Listing, moving and continuing

```sh
hopsesh ls --agent opencode                     # OpenCode's sessions here and on your machines
hopsesh plan studio:opencode/ses_3f1a2b --json   # what moving one here would do
hopsesh pull studio:opencode/ses_3f1a2b          # move it here, in OpenCode
hopsesh pull studio:claude/7f3c2a1e --in opencode   # continue a Claude Code session in OpenCode
hopsesh pull opencode/ses_3f1a2b --in codex      # continue an OpenCode session in Codex
```

- **Listing.** A session's subagent sessions are counted with it, not listed apart. Sessions
  you archived in OpenCode are left out unless you show them (Settings › Agents). A
  session's size is its number of messages, not bytes.
- **Live state.** hopsesh reads whether a turn is running from OpenCode's database, and asks
  the running service only about a session with a turn in flight. It cannot see an OpenCode
  window (a terminal or the desktop app) that shows a session while it is idle, so a move's
  plan says so: close that window, so it does not continue the copy left behind.
- **Stop** in the app, and `--stop-local`, interrupt the running turn through OpenCode.
  hopsesh never ends OpenCode's background service or a window attached to it.
- **Moves** go through OpenCode's own export and import: the copy is made by
  `opencode session import`, with its paths rewritten for the new machine, and its subagent
  sessions come with it. A session is not moved while a turn runs, nor one whose turn was cut
  off by a service shutdown (OpenCode resumes such a turn at its next start).
- **Continuing** in another agent reads the conversation into hopsesh's neutral form and
  writes it in the other agent's format, with the plan's loss report and briefing, as for
  Claude Code and Codex. Into OpenCode, hopsesh writes OpenCode's own import file and imports
  it. The new session names no model: OpenCode continues it with the model and agent it would
  pick for a new session.
- **Undo** deletes a session hopsesh imported, through OpenCode, and puts back one it set
  aside, from the export it took first. It refuses when you have used the session since,
  unless you force it.
- **Rename** goes through OpenCode's API, so OpenCode shows the new title too.

### Resuming, and OpenCode's background service

OpenCode runs one background service that the command line, the terminal UI and the desktop
app share. hopsesh never starts, stops or restarts it:

- Before an OpenCode command that would start the service, hopsesh asks whether it runs
  (`opencode service status`). When it is stopped, hopsesh runs the command with
  `--standalone`, so the command ends without leaving a service behind.
- OpenCode's terminal UI restarts a service of another version, which stops any turn it runs
  and reconnects the desktop app. So the command hopsesh gives you to continue a session uses
  an `opencode` of the running service's version: the one on your PATH, the desktop app's
  bundled CLI, or a CLI the desktop app staged. When none matches, hopsesh asks before going
  on (`hopsesh open --restart-service` agrees on the command line; the app asks in a dialog).
- A resume command is given only once hopsesh has checked the session is there, because
  `opencode -s <id>` makes an empty session for an id it does not know.

### Another folder, same session

`hopsesh cd <session> <dir>` asks OpenCode to move the session to the folder, then waits until
OpenCode has done it. OpenCode moves a session at the end of a step, so this needs its
background service running; with the service stopped, hopsesh tells you to type `/cd <dir>`
in the session instead, or to start OpenCode.

## Protecting the copy left behind

For Claude Code and Codex, hopsesh's hooks refuse a prompt in the copy a move left behind.
OpenCode has no such hook, so hopsesh installs an OpenCode plugin instead:

```sh
hopsesh notices install --agent opencode   # or Settings › Machines › Set up
hopsesh notices status                     # installed, and whether OpenCode loaded it
hopsesh notices remove --agent opencode
```

- The plugin is a folder, `plugins/hopsesh` in OpenCode's config folder
  (`~/.config/opencode/plugins/hopsesh`), and one per account folder. Nothing is installed
  from npm.
- In a session whose copy hopsesh moved or continued elsewhere, your prompt goes through,
  but **every tool call fails** with hopsesh's reason ("Blocked by hopsesh. …"), and an
  instruction tells the model why. So the old copy cannot edit files or run commands. A model
  sees each refused call as an error and could try another way: protection in OpenCode is
  weaker than in Claude Code and Codex.
- The plugin asks hopsesh at each prompt and before a tool call with no recent answer. When
  hopsesh does not answer within 5 seconds, the call goes through, and the failure is counted;
  Settings and `hopsesh notices status` show the count.
- `hopsesh notices status` and Settings › Machines say whether OpenCode loaded the plugin (asked
  of the running service). When it did not, they say why: a `-hopsesh` or `-*` entry in your
  OpenCode config's `plugins`, a policy that denies plugins, or OpenCode not having reloaded
  its plugins yet. hopsesh never edits your OpenCode config.
- An older hopsesh's plugin still runs; Settings offers to update it.
- `hopsesh unblock <session>` lifts the block from one original, as for the other agents.

`hopsesh skill install --add-rules` also writes the approval rules OpenCode's plugin reads
(`.hopsesh/rules.json` in its config folder): hopsesh's read-only commands run without asking,
and moves always ask. They do nothing without the plugin. The hopsesh skill itself needs no
install of its own: OpenCode reads the copy hopsesh puts in `~/.agents/skills` for Codex.

## Accounts

```sh
hopsesh accounts add opencode Work      # ~/.hopsesh/accounts/opencode/Work
hopsesh accounts login <profile-id> --run
hopsesh pull opencode/ses_3f1a2b --target-profile <profile-id>
```

- An OpenCode account folder holds its own `data`, `state` and `config` folders, and hopsesh
  runs OpenCode there with `XDG_DATA_HOME`, `XDG_STATE_HOME`, `OPENCODE_CONFIG_DIR` and
  `OPENCODE_DB` pointing into it.
- Each folder's service gets its own port (the first free one after OpenCode's 49374, or 28894
  on Windows), because two services cannot share one. `hopsesh accounts list` shows it.
- Sign in with `hopsesh accounts login <profile-id> --run`, which runs
  `opencode auth login --standalone` in the folder. Only `cli.json`'s display settings are
  copied from your default folder: `opencode.json`, which may hold provider keys, is never read.
- OpenCode signs in per provider, not per person. hopsesh shows which providers a folder is
  signed in to (`opencode auth list`), never a key or a label. Providers set up only in
  `opencode.json` are not listed there.
- A session taken into another folder is continued there as a new session, with its
  conversation; the original stays as it was.
- The desktop app uses only the default folder.

## What does not carry

A **move** brings the conversation and its subagent sessions. OpenCode's import does not take:
the session's `/undo` snapshots, its archived state, its short name (slug), its fork link, its
start and update times, its instruction state and scheduled jobs. The plan says so.

**Into OpenCode** from Claude Code or Codex, as for any continuation: tool calls other than
shell commands arrive as text, reasoning is left out, and so are signatures, encrypted
content, images (unless they are text), cost and token history, and which model wrote each
reply. With `--native`, shell calls are replayed as OpenCode's own.

**Out of OpenCode**: OpenCode's replay state, snapshots and reverts, instruction state, the
detail of subagent sessions (each is summarised as its call's result) and attachments (their
names stay).

## Limits

- **A return into OpenCode makes a new session.** OpenCode cannot add messages to a session it
  has, so where hopsesh would add only the new work to an earlier copy (as it does for Claude
  Code and Codex), it writes a new OpenCode session that continues it, and the earlier one
  stays as it is. The plan says so.
- A subagent's session cannot be moved or continued on its own: move its parent.
- "Keep the old session running too" (a fork on resume) is not offered for OpenCode yet.
- There is no link that opens an OpenCode session in the desktop app, so hopsesh opens it in a
  terminal. Sessions hopsesh imports show in the desktop app, which shares the database.
- OpenCode has no cloud agent of its own (and its sharing ended in 1.19.1), so there is no
  OpenCode cloud to hand off to. An OpenCode session can be handed off to Claude Code's or
  Codex's cloud; brought back into OpenCode, the cloud's work comes as a new OpenCode session.
- A session on OpenCode's Claude Agent provider moves as an OpenCode session, not a Claude Code
  one.

## What hopsesh never does with OpenCode

- It never writes OpenCode's database, and never copies the database file: it reads a fixed
  list of tables, read only, and every change goes through OpenCode's own commands
  (`session import`, `session delete`, `session export`, `opencode api`).
- It never reads OpenCode's credentials: not the `credential` and account tables, not
  `auth.json`, `service.json` or `opencode.json`.
- It never starts, stops or restarts OpenCode's background service, and never passes
  `--server` (that needs the service's password).
- It never calls a model.
