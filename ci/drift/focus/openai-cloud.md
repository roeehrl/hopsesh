The planned Codex cloud capability would submit a task with
`codex cloud exec --env <environment> --branch <branch>`, passing uncommitted work in
`CODEX_STARTING_DIFF`; list tasks with `codex cloud list --json` (a `tasks` array with
`id`, `url`, `title`, `status`, `updated_at`, `environment_id`, `environment_label`,
`summary`, `is_review` and `attempt_total`, and a `cursor`) and `codex cloud status`; and
bring the result back with `codex cloud diff`, `codex cloud apply` or `codex apply`. It
needs a ChatGPT sign-in and a GitHub repository, and it reaches only the Legacy cloud
environments: the new VM-based Codex cloud has no CLI yet. The remote-connections page says
that a handoff to a Codex cloud environment isn't supported; a change to that sentence may
mean an official handoff.

Look for changes to those subcommands, flags and JSON fields; a new `resume`, `attach` or
`pull` subcommand under `codex cloud`, or `cloud/*` app-server methods; the `/wham/tasks`,
`CODEX_STARTING_DIFF` and `ThreadService` canaries disappearing or moving; and dates for
retiring the Legacy environments.
