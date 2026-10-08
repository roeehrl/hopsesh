# Contributing to hopsesh

Thanks for helping. A few ground rules keep the project safe and maintainable.

## Start here (about 60 seconds)

```sh
git clone https://github.com/roeehrl/hopsesh && cd hopsesh
make test                 # vet + tests
go run ./cmd/hopsesh      # the TUI against your own machines
demo/record.sh shell      # or: two made-up machines in Docker, no setup needed
```

- Not sure where to begin? Look at issues labelled
  [good first issue](https://github.com/roeehrl/hopsesh/contribute) or
  [help wanted](https://github.com/roeehrl/hopsesh/labels/help%20wanted), or ask in
  [Discussions](https://github.com/roeehrl/hopsesh/discussions).
- For anything bigger than a small fix, open an issue or discussion first so we can agree on
  the approach before you spend time on it.
- We try to reply to new issues and pull requests within a couple of days.

## Development

```sh
make test     # go vet + go test -race
make lint     # golangci-lint v2 (install separately; config in .golangci.yml)
make build    # builds ./bin/hopsesh
make app      # macOS only: builds dist/macos/hopsesh.app (unsigned unless SIGN_IDENTITY is set)
```

- Go 1.26+. On Linux the desktop-app packages need GTK 4 and WebKitGTK 6
  (`libgtk-4-dev libwebkitgtk-6.0-dev` on Debian/Ubuntu) for `go vet ./...` and the tests.
- `scripts/integration-test.sh` runs a real SSH round trip (Linux, needs sudo; CI runs it).
- The Windows app: `scripts/build-windows-app.sh` builds `hopsesh-app.exe` and `hopsesh.exe`
  for amd64 and arm64, the app `.zip` (what the app updates itself from) and the per-user
  installer. It needs `makensis` (`apt install nsis`); Homebrew's makensis 3.13 crashes on
  current macOS, so build it on Linux or let CI do it. It downloads Microsoft's ConPTY
  package from NuGet (`internal/devtools/conptyfetch`, which pins and checks its hash).
- The app's terminal tabs (`internal/core/pty`): `scripts/app-terminal-check.sh` runs a tab
  in the real terminal window of a `-tags e2e` build on macOS or Linux. On Windows, run
  `go run ./internal/devtools/conptyfetch -out <dir>` and set `HOPSESH_CONPTY_DIR=<dir>` so
  the tests also cover the bundled ConPTY. The terminal window's xterm.js comes from
  `go run ./internal/devtools/xtermfetch` (pinned npm versions and hashes; a test checks the
  vendored files). `internal/devtools/winres` makes the
  Windows resource file (icon, manifest, version information).
- Window tests: `internal/devtools/webtest` drives the app's real window code in a browser
  (Playwright), the hopsesh Terminal window included (`tests/terminal.spec.ts`, with tabs
  running `internal/testkit/termfake`). `playwright.real.config.ts` drives the real Windows window instead and needs
  `HOPSESH_APP_EXE` pointing at a `-tags e2e` build. That build tag opens a debugging port:
  never ship it.
- `demo/record.sh shell` gives you two made-up machines (see [demo/README.md](demo/README.md)),
  so you can test listing and moving sessions without a second computer. If you change what
  the TUI or app shows, re-record the demo with `demo/record.sh`.
- Keep dependencies few and permissively licensed (MIT, BSD, Apache-2.0).
- Anything that depends on an agent's file formats goes in that agent's module under
  `agents/<id>/` (see [Add an agent module](#add-an-agent-module)), with tests and fixtures.
  Those formats are not public APIs and change between agent versions.
- Test fixtures use made-up names, paths and addresses (alice, bob, `100.64.0.x`), never
  real ones from your machines.
- Never add code that copies credentials, calls an agent vendor's API (Anthropic, OpenAI, …)
  directly, or writes into a running session's socket. See the principles in [docs/design.md](docs/design.md).
- Remote operations must be read-only unless they are part of a confirmed plan.

## Add an agent module

Each coding agent hopsesh supports is a module: a Go package under `agents/<id>/` behind a
small SDK. Claude Code and Codex keep sessions on machines; the GitHub Copilot cloud agent,
Jules, Devin and Amp are cloud-only modules that reach their vendors' clouds. Adding one, such as OpenCode
([#10](https://github.com/roeehrl/hopsesh/issues/10)), doesn't touch the core. Please open an
issue or discussion first so we can agree on the approach.

**Start from a template:**
- `agents/claude`: full-featured and stable.
- `agents/codex`: experimental; talks to the agent through its `app-server` (JSON-RPC) and
  uses its own importer.

**The rules:**
- A module imports only `sdk/agent` (and `sdk/ir` if it converts conversations).
  `internal/archtest` enforces this.
- Register it with one line in `Modules()` in `internal/agents/all/all.go`.
- A module reaches machines only through `agent.Host` (facts, files, exec, paths, locks,
  processes). It writes only under its own data folders, and every write is journaled so undo
  can reverse it. It runs only its own binaries, and never opens the files it lists as secrets.

**Required: `agent.Module`**

| Method | What it does |
|---|---|
| `Spec` | id, name, vendor, stability (`experimental` until proven), tested version prefixes, binaries (with search paths and version arguments), data folders (env var plus default), login variables, secrets (never opened; globs allowed), instruction files, global instruction files (carried by `--carry-rules`), features, icon (below), clouds (each with its driver's own sign-in command, `SignIn`, if it has one), variables for the app's terminal tabs (`TerminalEnv`) |
| `Detect` | turns a machine's facts into an install (most modules start from `DefaultInstall`) |
| `List` | the sessions on a machine; one unreadable session is reported, not fatal |
| `Bundle` | the files that make up one session |
| `PlanMove` | where each file goes on the target and how it's rewritten (pure) |
| `Verify` | checks the staged result before install |
| `Resume` | the command that continues the session |

**Optional capabilities.** Each one switches on matching features in the CLI and app:

| Capability | Enables |
|---|---|
| `LiveDetector` | open / working / idle state |
| `Stopper` | `--stop-local` (quit an open session) |
| `Marker` | the "↪ moved to …" mark in the agent's own list |
| `AccountProber`, `Sanitizer` | detecting the account, and moves across accounts |
| `PostInstaller` | registering an installed session with the agent |
| `Reader` | being the source of "continue in" |
| `Writer` | being the target of "continue in" (a profile with context window and native replay) |
| `ContextSizer` | destination capacity and active-context accounting for safe writes |
| `Importer` | "use the agent's own importer" (`--via import`) |
| `Integrator` | where the skill and approval rules go |
| `MovementHookIntegrator` | passive local movement notices on resume or prompt submission |

**Cloud capabilities.** A module whose agent has a cloud declares it in `Spec.Clouds` (the
program that drives it, fidelity each way, how code travels, what it needs, its sign-in
command and what the drift check watches) and implements what its vendor's command line
allows. A cloud-only module embeds `agent.NoLocal` and has no data folders. A cloud is
reached only by running the vendor's own CLI through `agent.Host`: never a token, a login
file or a vendor API.

| Capability | Enables |
|---|---|
| `CloudLister` | its sessions in `hopsesh ls --cloud` and the app's Clouds group |
| `CloudSender` (+ `CloudStepReader` for a step that needs the user's terminal) | hand-offs (`hopsesh handoff --to <cloud>`) |
| `CloudFetcher` (+ `CloudAdopter` when the vendor writes the local copy itself) | bringing sessions home (`hopsesh pull <cloud>:<id>`) |
| `CloudLinker` | pasted session links |
| `CloudTester` | `hopsesh clouds test` |
| `CloudFollower`, `CloudArchiver` | follow-ups and archiving (no shipped module has them yet) |

**Features declared in `Spec.Features`** rather than as interfaces: `fork`, `remote-control` and
`app`. When a module declares one, `Resume` must honour the matching `ResumeOptions` field
(`Fork`, `RemoteControl`, `App`). These turn on "Keep the old session running too", "Turn on
Remote Control" and "Open it in the <agent> app" in the plan. Native replay is part of the
`Writer`'s profile (`Profile.NativeReplay`).

**The agent's icon (`Spec.Icon`)**, shown in session rows, the sidebar, the plan's from → to
chips, the palette and Settings → Agents. The app picks the first of these that's available:
1. The icon of the agent's own desktop app, read from the user's machine at run time and never
   shipped. `Icon.Apps` lists where it may be installed, by `GOOS` (a macOS `.app` bundle or a
   Windows `.exe`). Users can turn this off in Settings (`app_icons`).
2. `Icon.SVG`, a mark drawn for hopsesh. It must be a complete `<svg>` with no scripts, event
   handlers, `foreignObject` or external `href`s (the registry rejects them), and it must not
   reproduce a vendor's logo or trademark.
3. The agent's two-letter initials.

**Required tests**
- The conformance kit: `agenttest.Run(t, module, newHost)` from `sdk/agent/agenttest`. It
  covers listing, bundle, move plan, verify, resume and a round trip; writers get the writer
  checks too. A module with a cloud also runs `agenttest.RunCloud` (or `RunCloudWith`)
  against a stand-in of its vendor's CLI (see `internal/testkit/fakecloud`), and marks in the
  code every output shape it could not check against the real tool.
- Fixtures copied from a real install into `agents/<id>/testdata/<version>/`, with all personal
  data replaced (alice, `/home/alice/…`), and no credentials or account files.
- Unit tests for the module, and a complete `Spec` (the registry tests check it).
- An end-to-end scenario in `internal/e2e` for moves, plus a continuation in both directions if
  the module is a `Reader` and `Writer`.
- Optional: tests against the real installed agent, behind `HOPSESH_REAL_AGENTS=1` (never run
  by default).

**Then document it:** add the agent to the README, add an entry to `CHANGELOG.md`, and a line to
`docs/design.md`.

## Contribute to the test bundle

Projects that build on hopsesh can add payloads they captured from a real agent (Claude Code
hook inputs, running-session registry entries such as one with `waitingFor`, Codex `notify`
payloads) under `testbundle/<agent>/<agent version>/`, so every release's test bundle carries
them. The payloads must be scrubbed to made-up data (alice, bob, `example.com`,
`192.0.2.x`/`100.64.0.x`, no tokens) and satisfy their slot's schema;
`go run ./internal/devtools/testbundle check` runs the same checks as CI. The layout, the
slots and the rules are in [testbundle/README.md](testbundle/README.md).

The weekly upstream-drift check, and how another repository reuses it, is described in
[docs/drift.md](docs/drift.md).

## Pull requests

- One logical change per PR, with tests. CI runs on Linux, macOS and Windows.
- Describe user-visible changes in `CHANGELOG.md` under "Unreleased".
- By contributing you agree that your contribution is licensed under Apache-2.0, and you
  certify the [Developer Certificate of Origin](https://developercertificate.org/) by signing
  off your commits (`git commit -s`).

## Conduct

This project follows the [Contributor Covenant 2.1](CODE_OF_CONDUCT.md).
