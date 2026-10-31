# hopsesh client API

Other programs on the same machine, run by the same user, use this API to see the coding-agent
sessions hopsesh knows (on this machine and the user's others) and to move or continue them. A
phone companion's host is the first user. The API has its own version, separate from the peer
protocol that hopsesh speaks to hopsesh on other machines. The peer protocol stays internal.

API 1 is in hopsesh 0.8.0 and later.

## Connecting

There are two ways in. Both carry the same messages.

1. **hopsesh serve's socket**, when it runs (`hopsesh serve install` starts it at login):
   - macOS and Linux: `~/.hopsesh/run/serve.sock`. When that path is longer than about 100
     bytes, it is `/tmp/hopsesh-<uid>/serve.sock` instead.
   - The folder is 0700 and owned by the user. Refuse a socket in a folder that isn't.
   - Send one line, `{"mode":"client"}`. The answer is one line,
     `{"ok":true,"version":"…","pid":…}` (or `{"error":"…"}`), and then the API starts.
2. **`hopsesh client --stdio`**, a process whose standard input and output carry the API. When
   hopsesh serve runs and knows the client API, it passes the connection on to it. Otherwise,
   when serve isn't running or is an older hopsesh, it answers itself. The only difference is
   that `terminal.open` then needs hopsesh serve (error `nologin`); `hello`'s `login` and
   `features` say which you got.

If there is no socket, hopsesh may still be installed but not serving. Run `hopsesh client
--stdio`, or point the user to *Start at login* (`hopsesh serve install`). Don't offer to
install hopsesh in that case.

## Messages

Every message is one JSON object on one line (UTF-8, `\n`).

- **Request:** `{"id": <number or string>, "method": "…", "params": {…}}`.
- **Response:** `{"id": …, "result": …}` or
  `{"id": …, "error": {"code": "…", "message": "…", "fix": "…"}}`. It carries the id of the
  request it answers.
- **Event:** `{"event": "…", "for": <request id>, "data": …}`. hopsesh sends events on its own,
  for a request still in progress (a move's steps) or one that subscribed (a catalog watch).

Requests run concurrently, so responses can arrive in any order: match them by id. A
request's events can arrive before and after its response. Plans and watches belong to their
connection and end with it.

The first request must be `hello`. Until it succeeds, every other request gets the error
`hello`.

Fields are only ever added. Ignore the ones you don't know. Check for a feature in `hello`,
not for a version.

## Methods

### hello

```json
{"id":1,"method":"hello","params":{"api":1,"minApi":1,"client":{"name":"mycnc-host","version":"2.3.0"}}}
```

```json
{"id":1,"result":{"api":1,"minApi":1,"version":"0.8.0","endpoint":"b536…bf49","machine":"studio","os":"darwin","login":true,
  "features":["catalog.watch","plan.push","owners","terminal.open"]}}
```

- `api` is the version both ends agreed. If they share none, the error is `api`, with a fix.
- `endpoint` is this machine's identity (see Identity below). On a machine where hopsesh has
  never run, hello creates it.
- `login` is true when hopsesh serve answers, from the user's login session.
- `features`:

  | Feature | Meaning |
  |---|---|
  | `catalog.watch` | `catalog.watch` sends changes |
  | `plan.push` | `plan` can send a session from this machine to another |
  | `owners` | hopsesh reads owner records (see Owner records below) |
  | `terminal.open` | `terminal.open` works (hopsesh serve runs) |

### catalog

The sessions and machines, read now:

```json
{"id":2,"method":"catalog","params":{"local":false,"machines":[],"cached":false}}
```

**Params**

| Param | Meaning |
|---|---|
| `local` | only this machine (fast, no network) |
| `machines` | only these machines (none means every machine the user allowed, and the clouds) |
| `cached` | answer at once from hopsesh's last catalog, when there is one; its rows say `"cached": true` |

**Result:** `{"sessions":[Session…],"machines":[Machine…],"attention":[Attention…],"complete":true,"at":"…"}`.
Sessions come newest first.

**Session**

| Field | Meaning |
|---|---|
| `ref` | Names it in `plan` and `terminal.open`: `<machine>:<agent>[@profile]/<session>`. Opaque: don't build or parse it. |
| `key` | `{agent, profile, session}`: unique together with `endpoint`. `profile` is the account folder there, as an opaque id (often a hash, also for the agent's default folder). Copy it; don't compare it with `""`. |
| `machine` | hopsesh's name for the machine (or the cloud) |
| `endpoint` | the machine's identity (`""` for a cloud session) |
| `cloud` | true for a cloud session |
| `agentName`, `title`, `dir`, `branch`, `activity` | what the session is and where |
| `live.state` | `live`, `ended` or `unknown` |
| `live.status` | the agent's own words (`busy`, `idle`, `waiting for …`) |
| `live.pids` | the processes that have the session open |
| `live.app` | it is open in the agent's desktop app |
| `live.remoteControl`, `live.remoteUrl` | Claude Code's Remote Control: `on` with its claude.ai page, `off`, or absent when the agent doesn't say |
| `replica` | this copy in the session's lineage (see Identity below) |
| `hop` | how this copy came here: `{operation, kind, machine, agent, at}`; `kind` is `moved`, `continued`, `fetched`, `handoff`, `returned` or `continuation` |
| `movedTo` | set on a copy that a move left behind: the session goes on there. `plan` refuses this copy, and `terminal.open` refuses it unless asked with `anyway`. |
| `owner` | another program running it, from its owner record: `{name, pid, stop}` |
| `cached` | from hopsesh's last catalog, not read just now |

**Machine**

| Field | Meaning |
|---|---|
| `name`, `endpoint`, `local`, `os` | which machine |
| `status` | `ok`, or why it can't be read now |
| `problem`, `fix` | when `status` isn't `ok`: what went wrong, and what the user can do |
| `hopsesh` | the hopsesh version installed there (on this machine, the installed copy, which can differ from the one answering: `hello` has that one's `version`) |
| `presence` | `login` (hopsesh serve answers there), `ssh` (reached over SSH only) or `none` |
| `agents` | the agents found there |
| `checked` | when it was read |

**Attention:** what needs the user, for an inbox. Its `id` stays the same for as long as the
item needs the user.

| Field | Meaning |
|---|---|
| `kind` | `old-copy-open` (a copy a move left behind is open, so the session may be continued twice), `old-copy-used` (that copy was used after the move) or `machine` (a machine can't be read, or hopsesh there needs something) |
| `machine`, `session` | where, and the session's ref when it is about one |
| `title`, `text`, `fix` | what to show; `fix` is what the user can do |

### catalog.watch, catalog.unwatch

`catalog.watch` takes the same params as `catalog`. It answers with the catalog as it is now,
then sends `catalog.changed` events, each carrying the whole catalog, whenever it changes:

```json
{"event":"catalog.changed","for":3,"data":{"sessions":[…],"machines":[…],"complete":true,"at":"…"}}
```

This machine is read again within about 3 seconds of a change. The other machines are read
every minute. The watch stops with `{"method":"catalog.unwatch","params":{"watch":3}}` (the
watch request's id), or when the connection ends.

### machines

Takes the same params as `catalog`. Its result is the catalog's `machines` alone.

### plan

Works out a move or continuation. Nothing changes.

```json
{"id":4,"method":"plan","params":{"session":"<ref>","to":"","agent":"","dir":"","fork":false}}
```

**Params**

| Param | Meaning |
|---|---|
| `session` | a ref from the catalog |
| `to` | `""` (or this machine's name) brings the session here. Another machine's name sends it there; only a session on this machine can be sent (feature `plan.push`). To bring a session to another machine, ask hopsesh there. |
| `agent` | continue it in another agent (`"claude"`, `"codex"`); `""` keeps its own |
| `dir` | the folder it goes to (`""`: hopsesh finds or clones the same repository) |
| `fork` | keep the original usable instead of blocking it |

**Result:** a Plan.

| Field | Meaning |
|---|---|
| `id` | apply or cancel it by this, on this connection |
| `kind`, `title`, `agent` | what kind of plan, for which session, in which agent |
| `from`, `to` | each `{machine, key, dir, account}`; `to.key` is the key it will have there |
| `files`, `bytes` | what is copied |
| `repo` | `{action, path, branch, remote}`: what happens to the code there (use, clone, worktree …) |
| `conflict`, `warnings` | what the user should know |
| `blockers` | when not empty, the plan cannot be applied: show them |
| `noWork` | it is there already |
| `protected` | the original will be blocked from going on, so it isn't continued twice |
| `live` | the original is open now |
| `owner` | another program runs the original (see Owner records below) |

Show the plan and get the user's explicit confirmation before `apply`.

### apply

```json
{"id":5,"method":"apply","params":{"plan":"<plan id>","ownerStopped":false}}
```

- **Steps:** while it runs, hopsesh sends `apply.step` events for each step, numbered from 1:
  `{"plan":"…","n":1,"title":"cloning … into …","state":"running"}`, then the same step with
  `"done"`, or `"failed"` when the apply fails in it. Which steps a move needs is known only as
  it goes.
- **Result:** a Receipt:

  | Field | Meaning |
  |---|---|
  | `journal` | undoes it |
  | `machine`, `key`, `ref` | where the session is now |
  | `dir` | its folder there |
  | `command` | resumes it there, as the user would type it |
  | `files`, `bytes`, `cloned`, `worktree`, `pushed`, `stopped`, `warnings` | what the apply did |

- **A plan is used once.** After an apply, successful or not, plan again: the next plan starts
  from what is there now, so a step that already happened isn't done twice. A failed apply's
  error carries `journal` when it had changed something; `undo` with it reverses that.
- **Errors:**

  | Code | When |
  |---|---|
  | `blocked` | the plan has blockers |
  | `owned` | another program runs the session, and `ownerStopped` wasn't set |
  | `stale` | the session changed since the plan; plan again |
  | `not-found` | no such plan on this connection |

### cancel

`{"method":"cancel","params":{"plan":"<plan id>"}}` drops a plan without applying it.

### undo

`{"method":"undo","params":{"journal":"<journal id>","force":false}}` reverses an applied move,
on both machines for a push. It refuses when the session was used since, unless `force` is set
(that later work is lost). The result is `{"journal":"…","title":"…"}`.

### terminal.open

`{"method":"terminal.open","params":{"session":"<ref on this machine>","cols":120,"rows":40,"anyway":false}}`
runs the session here, in a terminal hopsesh serve owns (feature `terminal.open`). The result
is `{id, title, dir, state, started}`; `hopsesh terminals attach <machine> <id>` shows it. Use
it only when the companion can't run the session itself.

## Identity

- **A machine** is its `endpoint`: 32 random bytes in hex, in `~/.hopsesh/endpoint-id` (or
  `$HOPSESH_CONFIG_DIR/endpoint-id`). It is written when hopsesh is first set up there. It is
  not derived from names, so it survives renames and reinstalls; deleting `~/.hopsesh` loses
  it. It identifies a user's home on a machine.
- **A running or stored session** is `(endpoint, key)`.
- **A session across moves** is its lineage `replica`, plus the `hop` that brought it. A move
  can give it a new `key` (a continuation in another agent always does), so follow `replica`,
  not `key`.

## Owner records

A program that runs agent processes itself (a companion's managed agents) says so in one file
per process: `~/.hopsesh/owners/<program>/<pid>.json`. On Windows that is
`%USERPROFILE%\.hopsesh\owners\…`.

```json
{"owner":"mycnc","pid":4812,"started":"2026-10-31T12:00:00Z",
 "session":{"agent":"claude","profile":"","session":"6149fe06-…"},
 "endpoint":"b536…bf49","stop":"Stop it in MyCNC, or MyCNC stops it when you confirm the move"}
```

**The program's side**

- `<program>` is lowercase letters, digits and dashes (at most 40 characters), and equals
  `owner`.
- The file is named by `pid`, and the folder is created 0700.
- `session` is the session's `key` as the catalog shows it. A record without `profile` (or with
  `""`) matches that agent's session id in any account folder.
- Write it atomically (to a temporary file, then rename) when the process starts, and remove it
  when the process ends. A file under 16 KiB is read.
- `stop` is shown to the user as written; hopsesh never runs it.

**hopsesh's side**

- hopsesh only reads the records.
- A record counts while its process runs and, when the agent says which processes have the
  session open, is one of them. A stale record or a reused process id is ignored.
- A session with an owner shows it in the catalog (`owner`). Its plan names it (`owner`), and
  `apply` refuses with `owned` until the program stopped or detached the process and the client
  says so with `"ownerStopped": true`.

## Errors

| Code | Meaning |
|---|---|
| `api` | no API version both ends speak |
| `hello` | a request before `hello` |
| `unknown` | a method this hopsesh doesn't know: check `features` |
| `bad-request` | the params couldn't be read |
| `not-found` | no such session, machine, plan or journal |
| `ambiguous` | several sessions match |
| `blocked` | the plan has blockers, or it is a copy a move left behind |
| `owned` | another program runs the session |
| `stale` | what the plan was made from changed: plan again |
| `nologin` | needs hopsesh serve in the login session (`fix` says how) |
| `unsupported` | this hopsesh can't do that here |
| `failed` | anything else (`message` says what) |

When `fix` is present, it is something the user can do. Show it.
