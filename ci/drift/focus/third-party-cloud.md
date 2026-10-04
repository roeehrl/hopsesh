No module drives these clouds. They are candidates for later cloud-only modules, which
would first read sessions and later hand them off, so what matters is how sessions or tasks
can be listed, read and created from outside the vendor's own interface:

- Copilot: `gh agent-task` and the REST agent-tasks API, including any change to the API's
  `X-GitHub-Api-Version` date.
- Jules: the v1alpha REST API (a new discovery document revision) and `jules remote`.
- Devin: the v3 REST API (a new OpenAPI version) and the CLI's cloud and handoff commands.
- Cursor, Amp and the Agent Client Protocol are low priority and docs only: report them
  only as `info`, and only for a new way to list, export or hand off sessions.

Nothing in this group is a break unless a flag or subcommand in a target's `watch.relies`
is gone.
