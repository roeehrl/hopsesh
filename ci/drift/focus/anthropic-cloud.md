The Claude Code cloud capability starts a cloud session with `claude --cloud "<briefing>"`
in the user's terminal (Claude Code refuses `--cloud` with `--print` and without a TTY):
Claude Code may first ask whether the folder is trusted ("Quick safety check"), then prints
`Created cloud session`, `View: https://claude.ai/code/session_…` and `Resume with: claude
--teleport session_…`, which hopsesh reads for the session's id. It picks a cloud
environment with `--environment`, and brings a session back with `claude --teleport <id>`.
Teleport needs a claude.ai login (not an API key), a clean working tree and the account that
owns the session. Claude Code 2.1.289 writes nothing until the user sends a message in the
teleported session; then it writes a new session (a new id, every record carrying the local
folder) under the Claude config folder's `projects/<slug>/`, with an `isMeta` user record
"This session is being continued from another machine" after the cloud's conversation, and
no `teleported-from` record (which hopsesh still reads where one appears). Cloud sessions are recognised by their
`session_…` and `cse_` ids and by the `bridge-session` records Remote Control leaves in local
transcripts. hopsesh sends no follow-ups: Claude Code has no non-interactive one.

Look for changes to those flags, to the lines `--cloud` prints and its refusals ("cannot be
combined with --print", "requires an interactive terminal"), to the trust question's
wording, to teleport's requirements, to where its transcript lands, when it is written and
the "continued from another machine" record that marks it; for a new command to
list, attach to, message or archive cloud sessions, or a non-interactive way to start one
(the watched issues ask for these); and for changes to what the docs say about cloud
environments, data use, and the terms that apply to cloud sessions.

Account-profile integration: cloud operations pin their driver profile and binding
(`internal/app/accounts.go`, `internal/core/move`, `internal/app/pending.go`). Watch whether
CLAUDE_CONFIG_DIR/auth changes alter the owner used for handoff or where teleport writes
its local copy. Cloud account selection is separate from a local destination profile;
bring to the default first, then use a reviewed account transfer. Do not assume that a
local label or email proves cloud ownership. Check delayed transcript adoption, plan
invalidation after login changes, and causal receipts when returning to another account
or agent. Use `docs/account-lineage-contract.md` and the module's manifest integrationFiles
for the current implementation and tests; never read credentials or initiate a login.
