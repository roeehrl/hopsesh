The Codex cloud capability (agents/codex/cloudtasks.go) submits a task with
`codex cloud exec --env <environment> --branch <branch> [--attempts N] <briefing>`, passing a
few changes on a pushed branch in `CODEX_STARTING_DIFF`, and reads the task's link from its
output; lists tasks with `codex cloud list --json` (a pretty-printed object: a `tasks` array
with `id`, `url`, `title`, `status` (pending, ready, applied, error), `updated_at`,
`environment_id`, `environment_label`, `summary` (an object of `files_changed`,
`lines_added` and `lines_removed`), `is_review` and `attempt_total`, and a `cursor` for
`--cursor`); reads `codex cloud status <id>` (the lines `[READY] title`, `environment  •  3m
ago` and `+12/-3 • 2 files`; it exits 1 unless the task is READY); brings the result back
with `codex cloud diff <id>`; and checks the login with `codex login status` ("Logged in
using ChatGPT"). These shapes were read from openai/codex's source, not from a real task. It
needs a ChatGPT sign-in and a GitHub repository, and it reaches only the Legacy cloud
environments: the new VM-based Codex cloud has no CLI yet. The remote-connections page says
that a handoff to a Codex cloud environment isn't supported; a change to that sentence may
mean an official handoff.

Look for changes to those subcommands, flags, JSON fields, status lines and error wordings
("Not signed in. Please run 'codex login'", "environment '…' not found", "No diff available
for task"); a new `resume`, `attach` or `pull` subcommand under `codex cloud`, or `cloud/*`
app-server methods; the `/wham/tasks`, `CODEX_STARTING_DIFF` and `ThreadService` canaries
disappearing or moving; and dates for retiring the Legacy environments.

Account-profile integration: CODEX_HOME, CODEX_SQLITE_HOME and credential-store/login
changes can redirect a cloud command as well as a local launch. Inspect the pinned driver
profile/binding in `internal/app/accounts.go`, `internal/core/move` and
`internal/app/pending.go`; a local profile is not a selector for cloud environments or
proof of cloud ownership. Watch new public account/workspace identity and account/read
semantics, retaining the current limited-identity behavior until upstream proves more.
Bring to the default profile first, then review a separate account transfer. Check
receipt/projection integrity and stale plans when login changes during handoff/adoption.
Use the generated module accounts policy, integrationFiles and
`docs/account-lineage-contract.md`; do not inspect credentials or initiate a login.
