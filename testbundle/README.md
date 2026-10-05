# hopsesh test bundle

A project that bundles or drives hopsesh can test against exactly the hopsesh it ships with
this bundle: hopsesh's stand-in agents (programs that answer like Claude Code, Codex and the
vendors' cloud CLIs, without a model, a login or a network), the agents' fixtures, the
specs of the compiled-in modules, and payloads other projects contributed.

Every release publishes it as `hopsesh-testbundle-<version>.tar.gz`, with
`hopsesh-testbundle-<version>.tar.gz.sha256`. Like the other release files, it is listed in
the release's signed `checksums.txt` and carries build provenance:

```sh
v=0.4.0
gh release download "v$v" -R roeehrl/hopsesh -p "hopsesh-testbundle-$v.tar.gz*" -p 'checksums.txt*'
openssl dgst -sha256 -verify release-key.pub -signature checksums.txt.sig checksums.txt  # release-key.pub: packaging/ in hopsesh
shasum -a 256 -c checksums.txt --ignore-missing
gh attestation verify "hopsesh-testbundle-$v.tar.gz" --repo roeehrl/hopsesh
tar -xzf "hopsesh-testbundle-$v.tar.gz"
```

## Layout

The archive holds one folder, named for the hopsesh version:

```
hopsesh-testbundle-<version>/
  manifest.json          versions, the stand-ins, and every file with its SHA-256
                         (schemas/bundle-manifest.schema.json)
  README.md              this file
  agents.json            each module's Spec as data, as `hopsesh agents --json` prints it
                         under "spec": folders, binaries, secrets, clouds and their watch lists
  schemas/               the JSON Schemas below
  bin/<os>-<arch>/fakeagent[.exe]
                         the stand-in program, for darwin, linux and windows on amd64 and arm64
  <agent>/<agent version>/
    home/                hopsesh's own fixtures for that agent version: a Claude config
                         folder (CLAUDE_CONFIG_DIR) or a CODEX_HOME, with made-up sessions
    hooks/<Event>/*.json contributed payloads (see below)
    registry/*.json
    notify/*.json
```

`<agent>` is a module id (`claude`, `codex`, …) and `<agent version>` the agent's own version
(`2.1.284`): payloads depend on the agent's version, the bundle as a whole on hopsesh's.

**The stand-in program** plays whichever program it is named as. Copy or link it as `claude`,
`codex`, `gh`, `jules`, `devin`, `amp` or `fakecloud` on a `PATH` of its own:

```sh
b=hopsesh-testbundle-$v
mkdir -p stand-ins && for n in claude codex; do cp "$b/bin/darwin-arm64/fakeagent" "stand-ins/$n"; done
PATH="$PWD/stand-ins:/usr/bin:/bin" claude --version    # 2.1.284 (Claude Code)
```

It answers `--version` (the versions in `manifest.json`), Codex's `app-server` (JSON-RPC
over stdio), and both agents' cloud verbs against a local stand-in cloud. Every call is
appended to `$FAKE_AGENT_LOG`; `$FAKE_CLOUD_DIR` holds the stand-in clouds' state;
`$FAKE_CLOUD_FAIL` plays one failure (`manifest.json` lists them). Anything else it is asked
succeeds and prints nothing. The `home/` fixtures use `/home/u/git/demo` as the project
folder; rewrite it to a real folder when you copy them (hopsesh's tests do the same).
`home/` includes decoy credential files (`.credentials.json`, `auth.json`, a `*.key`) that
hold no secret: hopsesh must never open them, and a test can check that a program doesn't.

## Contributing payloads

Another project (MyCNC, say) adds what it captured from a real agent so that both projects
test against the same shapes. Open a pull request against hopsesh that adds files here, in
the source tree:

```
testbundle/<agent>/<agent version>/<slot>/[<Event>/]<name>.json
```

| Agent | Slot | What | Schema |
|---|---|---|---|
| `claude` | `hooks/<Event>/` | the JSON a hook command reads on its standard input; the folder is its `hook_event_name` (`Notification`, `Stop`, …) | `claude-hook.schema.json` |
| `claude` | `registry/` | a running-session registry entry (`<config>/sessions/<pid>.json`), such as one with `waitingFor` | `claude-registry.schema.json` |
| `codex` | `notify/` | the JSON Codex passes to its `notify` program | `codex-notify.schema.json` |

The rules:

- **Scrubbed, made-up data only.** Replace every real value before you commit: people are
  `alice`, `bob` or `carol` (`/Users/alice/…`, `/home/bob/…`), e-mail addresses are at
  `example.com`, addresses are `192.0.2.x` or `100.64.0.x`, machine names are made up, and
  there is no token, key, cookie or account id. Session ids may stay random UUIDs. Keep the
  shape (every field, its type, an empty list where there was one) and shorten long text.
- **Schema-validated.** Each file must satisfy its slot's schema, and a file under
  `hooks/<Event>/` must have that `hook_event_name`. A registry entry must also read in
  hopsesh as you meant: open, with its `pid`, and `waiting for <waitingFor>` as its status.
- **One payload per file**, named in lower case with dashes (`permission-prompt.json`), under
  the version of the agent that wrote it. A payload that changed in a later agent version is
  a new file under that version; leave the older one.
- **A new slot** (another agent's hooks, another file the agent writes) adds its schema to
  `schemas/` and a row to `slots` in `internal/devtools/testbundle/check.go`, in the same
  pull request.

Check before you push:

```sh
go run ./internal/devtools/testbundle check
```

CI runs the same check, builds the bundle, and runs the stand-in from it. A release then
publishes your files in that version's bundle; earlier bundles never change.
