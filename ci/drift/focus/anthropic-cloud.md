The Claude Code cloud capability starts a cloud session with `claude --cloud "<briefing>"`
in the user's terminal (Claude Code refuses `--cloud` with `--print` and without a TTY):
Claude Code may first ask whether the folder is trusted ("Quick safety check"), then prints
`Created cloud session`, `View: https://claude.ai/code/session_…` and `Resume with: claude
--teleport session_…`, which hopsesh reads for the session's id. It picks a cloud
environment with `--environment`, and brings a session back with `claude --teleport <id>`.
Teleport needs a claude.ai login (not an API key), a clean working tree and the account that
owns the session, and writes the transcript under the Claude config folder's
`projects/<slug>/` with a `teleported-from` record. Cloud sessions are recognised by their
`session_…` and `cse_` ids and by the `bridge-session` records Remote Control leaves in local
transcripts. hopsesh sends no follow-ups: Claude Code has no non-interactive one.

Look for changes to those flags, to the lines `--cloud` prints and its refusals ("cannot be
combined with --print", "requires an interactive terminal"), to the trust question's
wording, to teleport's requirements and to where its transcript lands; for a new command to
list, attach to, message or archive cloud sessions, or a non-interactive way to start one
(the watched issues ask for these); and for changes to what the docs say about cloud
environments, data use, and the terms that apply to cloud sessions.
