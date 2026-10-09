# Repository startup integration

Environment preparation installs the signed helper binary. Actual task startup
creates fresh session keys. These steps remain separate: reusable setup files
contain neither invitations nor identity, account or routing credentials.

Settings → Internet delivery → **Prepare cloud startup** asks for the provider,
published immutable 0.5 helper version, verified downloads origin and repository.
The preview shows the files to create or update. Changing any input invalidates
the preview. Installation rechecks all input files, refuses concurrent settings
edits, and consumes the review after success. Existing repository settings never
cross the browser bridge. Public install scripts and Start skill instructions
can be copied into the provider's environment configuration.

The CLI provides the same preview and installation:

```sh
hopsesh cloud-integration install-startup /absolute/repository --provider claude-hosted --version 0.5.0 --dry-run
hopsesh cloud-integration install-startup /absolute/repository --provider claude-hosted --version 0.5.0
```

Claude writes `.hopsesh/cloud-install-claude-hosted.sh` and
`.hopsesh/cloud-session-start.sh`, adds its own command hook to
`.claude/settings.json`, and records ownership in a provider-specific public
record. Unrelated settings and hooks are preserved. A modified Hopsesh script
or hook is refused; installation does not overwrite it. Repeated installation
is idempotent. File access is pinned to the chosen repository with `os.Root`;
links, oversized settings and nonregular targets are refused.

The SessionStart script is a no-op unless `CLAUDE_CODE_REMOTE=true`. On startup,
resume, clear, compact or fork, it reads the documented input and prepares a fresh
scoped incarnation. It uses `--quiet` so successful startup does not inject its
metadata into agent context. To read the current public incarnation afterwards:

```sh
hopsesh cloud-integration current --provider claude-hosted --session ACTUAL_SESSION_ID --workspace /actual/repository
```

`current` is passive and requires the exact provider, native session and
workspace. It creates no keys and does not infer connector liveness from the
presence of a routing credential. A fork has its own slot; preparing it does
not supersede its original. See [cloud admission](cloud-admission.md) for claim,
independent fingerprint approval, scoped serving and revocation.

Incarnation records and scoped observations retain the actual hook `source`
(`startup`, `resume`, `clear`, `compact`, or `fork`). Explicit CLI preparation is
recorded as `manual`, including instruction-driven Codex preparation. This is
diagnostic evidence, never proof of VM replacement or permission to export.
Unknown or missing reasons are refused; old staging incarnations must be prepared
again. The startup reason never changes task identity, peer approval or lineage.

Codex writes `.hopsesh/cloud-install-codex-current.sh` and
`.hopsesh/codex-start.md`. Copy their contents into the environment's Install
script and Start skill fields. In the editable setup conversation, ask Codex to
**execute the verified Install script and check the installed helper version**
before publishing. Saving those fields alone does not prepare the filesystem.
Do not run `prepare` in this setup: reusable images must contain no session keys.
Publish the prepared environment, then start a new task to verify the inherited
binary and actual Start skill delivery. Existing tasks keep their own state;
repository refresh does not rerun installation or startup. This follows
[Codex's environment publication model](https://learn.chatgpt.com/docs/environments/cloud-environments).

The Start skill is a documented instruction mechanism,
whose actual per-task startup, pause/resume and rebuild behavior still needs
qualification. If the real task ID is unavailable, session binding must be
reported unsupported. Current Codex Cloud native transcript export is also
unqualified. Legacy Codex and Work Cloud do not receive invented repository
callbacks. Claude and current Codex startup files can coexist independently in
the same repository, including different pinned helper versions.

Cloud installs require the signed immutable archive to be published at the
chosen downloads origin. Preparing repository files does not establish that the
download exists, a cloud session is running, or the relay is connected. Hosted
defaults are qualified separately from these local installer and CLI tests.

The current hosted qualification has verified Claude's actual `/compact`
SessionStart callback and Codex's manually prepared observation-only connector,
including reconnect and fresh-key renewal under one logical task. Automatic
Codex startup, fresh environment publication, provider pause/rebuild and Claude
transcript export remain unqualified. Claude's automatic reviewer refused the
explicitly approved export command and exact temporary allow rules; no permission
mode was weakened to work around it. See the [hosted qualification record](relay-hosted-qualification.md)
for the tested versions, actual evidence and cleanup.

The [2026-10-09 research reassessment](runtime-cloud-relay-design.md#13-qualification-failures-research-reassessment-2026-10-09)
distinguishes provider permission recovery from bootstrap installation and adds
event-source evidence to the lifecycle qualification plan. Do not use setup or
hook execution as an alternate path for an export the provider refused.

A scoped cloud connector exits when its routing credential is refused with HTTP
401/403. Restore it through explicit preparation/claim and independent approval
of the fresh identity; an old cached credential must not silently renew access.

## Codex network qualification

Allow the exact downloads and relay hostnames in the cloud environment's
additional allowed domains, then save and publish the environment. Keep the
restricted preset and configured proxy; public downloads do not require network
secrets. Check the running task's current environment status separately from the
saved configuration, because an existing task can retain earlier settings.

A curl exit 7 with HTTP/CONNECT `000` does not establish that the proxy is down.
First distinguish proxy DNS resolution from TCP socket creation. A per-command
sandbox can reject socket creation with `Operation not permitted` before the
request reaches the proxy. Use the executor's supported command-approval workflow
when available and permitted; do not disable the sandbox or bypass the proxy.
Test an already-allowed HTTPS destination through the same proxy as a control.

CONNECT `403` is a separate destination-policy refusal. Resolve it through the
environment configuration workflow, then verify CONNECT success and an origin
response. Only after connectivity succeeds, run the pinned signature-verifying
installer and verify its binary version. Installation alone does not qualify
task startup, native transcript export, or connector enrollment.

Mechanisms follow [Claude's hook reference](https://code.claude.com/docs/en/hooks)
and [Codex cloud environment configuration](https://learn.chatgpt.com/docs/environments/cloud-environments).
