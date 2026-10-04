The planned Claude Code cloud capability would start a cloud session with `claude --cloud`
or `--remote`, or with `claude -p … --cloud --output-format json` and the session id read
from that JSON; pick a cloud environment with `--environment`; and bring a session back
with `claude --teleport <id>`. Teleport needs a claude.ai login (not an API key), a clean
working tree and the account that owns the session, and writes the transcript under the
Claude config folder's `projects/<slug>/` with a `teleported-from` record. Cloud sessions
are recognised by their `session_…` and `cse_` ids and by the `bridge-session` records
Remote Control leaves in local transcripts.

Look for changes to those flags and their JSON output, to teleport's requirements and to
where its transcript lands; for a new command to list, attach to or archive cloud sessions
(the watched issues ask for these); and for changes to what the docs say about cloud
environments, data use, and the terms that apply to cloud sessions.
