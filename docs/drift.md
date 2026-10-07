# The upstream-drift check

hopsesh depends on details that agent vendors change in any release: where sessions are
stored, the records in them, CLI flags, the clouds' commands and docs. Once a week
`.github/workflows/drift.yml` asks whether the latest upstream changes break any of it, and
keeps a single issue labelled `upstream-drift` up to date.

The same workflow is reusable: another repository can run the engine on its own list of
upstream targets, and its findings go into an issue in its own repository.

## How it works

Three jobs:

1. **probe** (no secrets) reads the manifest, the list of targets and what to watch for each,
   and gathers the facts into an `intel/` folder: the tested and latest versions, help output
   at both (and which relied-on flags disappeared), docs pages hashed and diffed against last
   week's run, changelog and release entries, watched issues, and upstream code canaries.
   The CLIs only print their help, with an empty home folder and no token: nothing signs in
   to a vendor or calls a cloud. Last week's facts come from the previous successful run's
   `drift-intel` artifact.
2. **review** runs once per review group, in parallel, each with its own budget: Claude Code
   on an API key (`--safe-mode`, Read/Grep/Glob only, `dontAsk`, no session persistence)
   reads `intel/` and the repository and returns findings in the shape of
   `ci/drift/schema.json`. This is the only job that sees the key, behind an environment.
3. **report** merges the groups' findings, renders them (third-party text is defused: no
   @mentions), uploads them, and opens, updates or closes the issue. A group without a
   report fails the run and keeps the issue open.

The engine is `ci/drift/probe.sh`, `ci/drift/prompt.sh` with `prompt.md`, `ci/drift/schema.json`,
`internal/devtools/driftmanifest` (manifest checks and feed parsing) and the report step.
hopsesh's own manifest is built from its modules' specs (`Spec.Clouds[].Watch`) and
`internal/devtools/driftmanifest/targets.go`; its review groups and budgets are in
`ci/drift/groups.json`, and each group's focus in `ci/drift/focus/<group>.md`.

Run the probe locally from any folder (needs go, node, gh, git, jq and curl):

```sh
DRIFT_OFFLINE=1 ci/drift/probe.sh /tmp/intel                          # hopsesh's manifest, no network
DRIFT_MANIFEST=path/to/manifest.json ci/drift/probe.sh /tmp/intel      # another project's
ci/drift/prompt.sh local /tmp/intel                                   # the prompt a review gets
```

`DRIFT_NO_INSTALL=1` installs no CLIs (help comes from the ones on `PATH`), and
`DRIFT_BASELINE=dir` uses a folder as last week's `intel/`. Without it, the probe looks up
last week's `drift-intel` with `gh run list` in the repository of the folder it runs in: probe
another project's manifest from that project's own checkout, or set `DRIFT_BASELINE` (an
empty folder means "baseline only"), so you never compare against hopsesh's intel.

## Calling it from another repository

You need three files in your repository and one workflow.

**The manifest** (`ci/drift/manifest.json`, say): the project and its targets, in the shape of
[`ci/drift/manifest.schema.json`](../ci/drift/manifest.schema.json). `project` tells the
reviewer what your project is (`about`) and where a finding should point in your code
(`cite`). Each target names what to watch: docs pages, changelog and release feeds, help
commands (with the npm `package` that provides the CLI, if any, installed at your `tested`
version and at the latest one), the flags you rely on, upstream issues and code canaries, and
its review `group`. Check it before you commit:

```sh
sha=<the hopsesh commit you pin>
curl -fsSLO "https://raw.githubusercontent.com/roeehrl/hopsesh/$sha/ci/drift/manifest.schema.json"
go run "github.com/roeehrl/hopsesh/internal/devtools/driftmanifest@$sha" check manifest.schema.json ci/drift/manifest.json
```

(or from a hopsesh checkout: `go run ./internal/devtools/driftmanifest check
ci/drift/manifest.schema.json <your manifest>`). The probe runs the same check first and
stops on an invalid manifest. `module` and `watch.tests` are hopsesh's
own and are refused in another repository's manifest.

**The focus files** (`ci/drift/focus/<group>.md`): one per review group, a paragraph or two
on what that review should pay attention to (the formats and flags your code depends on,
known gaps to report).

**An API key** for the review: a secret named `ANTHROPIC_API_KEY`, either in an environment
(recommended: an environment named `drift-review` that only `main` may use; the review job
runs in it) or as a repository secret passed with `secrets:`. A dry run needs none.

**The workflow** (`.github/workflows/drift.yml` in your repository), pinned to a hopsesh
commit, with the same SHA as `engine-ref`:

```yaml
name: upstream drift

on:
  schedule:
    - cron: "17 7 * * 1"
  workflow_dispatch:

permissions: {}

jobs:
  drift:
    permissions:
      contents: read  # check out this repository (the reviewer reads it)
      actions: read   # last week's drift-intel artifact, the baseline
      issues: write   # the single upstream-drift issue in this repository
    # Replace both SHAs with the hopsesh commit you use (the full SHA of a release tag:
    # git ls-remote https://github.com/roeehrl/hopsesh refs/tags/v0.4.0).
    uses: roeehrl/hopsesh/.github/workflows/drift.yml@0123456789abcdef0123456789abcdef01234567 # v0.4.0
    with:
      engine-ref: 0123456789abcdef0123456789abcdef01234567
      manifest: ci/drift/manifest.json
      focus: ci/drift/focus
      groups: '[{"group": "agents", "budget": "1.00"}, {"group": "clouds", "budget": "0.75"}]'
      issue-label: upstream-drift
      issue-title: Upstream drift
      environment: drift-review
    secrets:
      ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }} # leave out when the key is in the environment
```

### Inputs

| Input | Required | Default | What |
|---|---|---|---|
| `engine-ref` | yes | | The full commit SHA of hopsesh whose engine runs: probe, prompts, findings schema, report. Use the SHA you pinned in `uses:`; anything but 40 hex digits is refused. |
| `engine-repository` | no | `roeehrl/hopsesh` | Where the engine is checked out from (a fork, to test a change). |
| `manifest` | yes, for another repository | `""` | Path of your manifest in your repository. Empty builds hopsesh's own, which only makes sense in hopsesh. |
| `focus` | with a manifest | `""` | Folder in your repository with one `<group>.md` per group. |
| `groups` | with a manifest | `""` | JSON list of `{"group", "budget"}`; the budget is US dollars as a string, at most `99.99`. Every group in the manifest needs exactly one entry and every entry needs targets. |
| `issue-label` | no | `upstream-drift` | The label of the one issue the check keeps (letters, digits, space, `._:-`; at most 50). |
| `issue-title` | no | `Upstream drift` | The issue's title; the targets with breaks or risks are appended (at most three named). |
| `environment` | no | `drift-review` | Your environment the review job runs in, so its protection rules and its `ANTHROPIC_API_KEY` apply. `""` for none. |
| `dry-run` | no | `false` | Run the probe, the prompts and the report, but pay for no review and touch no issue. |

Paths must stay inside your repository (no leading `/`, no `..`).

### Secrets

| Secret | Required | What |
|---|---|---|
| `ANTHROPIC_API_KEY` | unless `dry-run`, or the key is a secret of `environment` | The key the review runs on. Only the review step sees it, as an environment variable; it is never printed. When the environment has a secret of the same name, GitHub uses that one. |

### Outputs

| Output | What |
|---|---|
| `findings-artifact` | The name of the run's artifact that holds the findings and the report (`drift-findings`). |
| `findings-file` | The findings JSON in that artifact (`findings.json`): a list with one object per group, `{group, summary, breaking, findings[]}`, each finding as in `ci/drift/schema.json` (`target`, `surface`, `severity` break/risk/info, `title`, `evidence`, `source`, `affected`, `test`). |
| `report-file` | The rendered report in that artifact (`report.md`), the issue's body. |
| `breaking` | `"true"` when a review found a break. |
| `count` | The number of findings that are breaks or risks. |
| `issue-url` | The issue that was opened, updated or closed; empty in a dry run or when there was none. |

A later job reads them as `needs.drift.outputs.<name>` and the files with
`actions/download-artifact` (`name: ${{ needs.drift.outputs.findings-artifact }}`).

### Permissions

The calling job must grant `contents: read`, `actions: read` and `issues: write` (GitHub
refuses to start a called job that asks for more than its caller has). Inside, each job takes
only what it needs: probe `contents: read` and `actions: read`, review `contents: read`,
report `issues: write`. Your repository's issue gets the label and the comments; nothing is
written to hopsesh.

### Notes

- **One call per run.** The artifacts have fixed names (`drift-intel`, `drift-report-<group>`,
  `drift-findings`), and last week's baseline is looked up by your workflow file's name on
  your default branch.
- **Concurrency.** Runs queue on the group `<your workflow's name>-drift-engine`; don't give
  your calling workflow the same group.
- **Issue searches** match words anywhere in an issue's body, so a broad query (say
  `repo:openai/codex app-server approval`) returns a hundred issues a week. Search for a
  method, hook or flag name, or limit the query with `in:title`, to keep it to the few that
  matter.
- **Updating the engine** means changing the SHA in both places. Read the hopsesh changelog
  first; `ci/drift/manifest.schema.json` says what a manifest may contain at that commit.
- **What never happens:** no vendor sign-in, no subscription login, no cloud call. Every
  action is pinned by SHA and every job starts with harden-runner.

## Testing a change to the workflow

`.github/workflows/drift-caller-test.yml` calls `drift.yml` exactly as another repository
would, with the made-up manifest in `ci/drift/testdata/` and `dry-run: true`, on every pull
request that touches the drift files. It then checks the outputs and the findings artifact.
To try the paid review on a branch, run `upstream drift` by hand (`workflow_dispatch`); the
`drift-review` environment allows `main` only.

### Account, lineage and desktop coverage

The generated module records include the actual `Spec.Accounts` root, login, environment
cleanup and initial credential-store policy, plus `DesktopScheme` and shared integration
paths. The local review follows these through profile discovery (local and remote), binding
changes and stale-plan rejection, portable cross-account continuation, repeated/multi-party
round trips and forks, and GUI/TUI/CLI consumers. Claude and Codex cloud reviews also check
profile-pinned drivers and adoption. Desktop coverage includes Codex's exact thread URL,
default-profile restriction, application/protocol detection and surfaced launch failures.
Names/tags are labels, not identities; the optional presence daemon remains a proposal.

The probe watches authentication, environment/configuration and desktop documentation and
login help. Codex upstream account protocol/login code is included in the canaries. Schema
diffs now recurse into versioned folders, include GetAccount/LoginAccount records and keep
added/removed contents under `intel/schema/<target>/<relative-path>.diff`. Generation or
comparison failures are reported as incomplete coverage, with logs, rather than an empty
diff. Reviewers must inspect current code before repeating an old known limitation.

The drift workflow test now checks Hopsesh's own manifest and all four prompts in addition
to the external caller fixture. Relevant adapter, SDK, core, app, UI and scenario changes
trigger it. Its `hopsesh-drift-review-inputs` artifact contains the generated manifest,
probe report and prompts. To run those checks locally:

```sh
go test -race ./internal/devtools/driftmanifest ./internal/devtools/schemalite
ci/drift/test-hopsesh.sh /tmp/hopsesh-drift
```

These checks are offline and do not pay for a review or sign in. The existing scenario
matrices test account/lineage routes; weekly upstream probes are narrower and do not prove
live authenticated round trips or that a desktop displayed the requested chat. The four
review groups, their model budgets and the main-only protected review environment are
unchanged. Scheduled runs acquire the new coverage when this change lands on main.

### Movement and return coverage

The local review includes the [movement-return contract](movement-return.md), persisted
`lineage/5` notice preferences, peer protocol 5, source title marks, scan-derived movement
and return statuses, and Claude/Codex hook adapters. It checks vendor hook payload and
output changes, trust/version gates, preservation of unrelated hooks, and whether native
reader changes could misreport preparation as continued agent work. Neither notice
output nor imported transcript records may become newly authored conversation nodes.

The generated module integration paths include the real-module movement/return tests,
interrupted recovery, the SSH matrix runner, config defaults and scenario CI definitions.
PR and nightly scenario jobs explicitly include `TestMovement`; matrix rows cover ordinary
and quiet returns and forks for all Claude/Codex pairings. Offline drift tests verify the
review prompt retains these concerns. Notify-off still preserves return metadata; undo
compensates movement history. These checks make no paid calls and modify no live vendor
configuration. Agent-files/hook implementation and live hook delivery remain separate
verification surfaces; static fixtures are not evidence of a live vendor session.
