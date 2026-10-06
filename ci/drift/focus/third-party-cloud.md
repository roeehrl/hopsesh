Use the generated manifest to distinguish implemented cloud-only modules from planned
targets. Inspect each declared module and its current capabilities before judging impact.
What matters is how sessions or tasks can be listed, read and created from outside the
vendor's own interface:

- Copilot: `gh agent-task` and the REST agent-tasks API, including any change to the API's
  `X-GitHub-Api-Version` date.
- Jules: the v1alpha REST API (a new discovery document revision) and `jules remote`.
- Devin: the v3 REST API (a new OpenAPI version) and the CLI's cloud and handoff commands.
- Cursor and the Agent Client Protocol are low priority and docs only: report them
  only as `info`, and only for a new way to list, export or hand off sessions.

For Amp, inspect its declared module and cloud Watch rather than treating it as docs only.
Changed response formats, auth requirements and adopted transcript shapes can break an
implemented module even when help flags remain. Trace adoption through shared move and
lineage code; do not invent account-profile support where the module declares none.
