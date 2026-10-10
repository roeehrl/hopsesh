# Accounts and desktop opening

Accounts are named **runtime profiles** for Claude Code and Codex. Each pins an independent
vendor state directory to a persistent machine identity. Names and tags are labels: several
profiles can share the Personal tag, have different emails, or have identical display names.
Selection uses the profile ID, never an email or label.

## Set up and discover

In the GUI, open **Settings → Accounts**. Scan accounts discovers known default/configured
roots and registered roots on allowed machines. Add account creates an empty local profile
or adopts an existing absolute root. It never copies credentials or changes another login.
Use **Sign in** to run the vendor's own login in that profile, then **Scan accounts** to check
it. Rename, edit tags, search, group and collapse account groups from this screen. Sessions
also support Account and Account tag filters and grouping, and show the profile in Details.

In the TUI, `a` opens Accounts; `1`/`2` adds Claude/Codex, `n` renames, `t` edits tags, `/`
filters, `u` selects untagged profiles, `g` cycles grouping, and the arrow keys collapse/expand
groups. `d` forgets a registration after confirming its name; `l` signs in and `r` scans. The setup asks for a name, tags, an optional existing
root and its machine. `A` on a selected session chooses a destination account and opens a plan.

```sh
hopsesh accounts scan
hopsesh accounts list --json
hopsesh accounts add claude "First personal" --tag Personal,Research
hopsesh accounts add claude "Second personal" --tag Personal
hopsesh accounts add codex "Client A" --tag Work
hopsesh accounts login <profile-id> --run
hopsesh accounts edit <profile-id> --name "Research" --tag Personal,Research
hopsesh accounts forget <profile-id>
```

Remote discovery requires an initialized Hopsesh machine identity. SSH alone can read
sessions, but does not initialize remote account tracking. On the remote machine, install
Hopsesh if needed and run `hopsesh accounts scan --machine local`, then click **Scan
accounts** on the controlling machine. The GUI shows an account setup notice for reachable
machines without that identity; scanning never silently installs or initializes remote software.

`forget` removes only Hopsesh's registration. Vendor files, login and credentials remain.
A known default root is rediscovered on the next scan. A remote custom root must first be
registered by Hopsesh on its own machine. Scanning imports its stable registration ID and
public observations; it does not write remote files or start a remote login. Local names
and tags stay local. Offline machines retain their last observation, explicitly marked stale.
There is no background account daemon. Normal session scans reuse login metadata for up to
five minutes; Scan accounts forces a public status check. Apply and launch recheck the login.

## Move and return

Choose **Move → Move to another account…**, select the destination profile, review what
carries over and apply. Other-agent transfers also have a destination account selector.
The plan identifies both profiles. An unqualified target uses the known default profile;
it never silently chooses among custom accounts.

The GUI shows the observed email or account label, with the runtime profile beneath it.
A single destination is a static identity card; multiple destinations use a profile
selector without an extra duplicate Default option. The arrow beside **Continue** chooses
the agent desktop app, Hopsesh Terminal or the configured external terminal. The choice is
remembered per agent and used when applying the transfer. Unsupported desktop destinations
explain their restriction and fall back to the terminal preference.

### Instruction review

Expand **Source instructions** to inspect paths, scope and file text, then select individual
files. Hopsesh discovers the module's declared global instruction files in the selected
source profile and declared instruction files directly in the session's project directory.
It does not resolve imports, parent-directory rules, skills, settings or automatic memory.
Remote transfers package these same declared files for review. Arbitrary submitted paths
are rejected, and a selected file that disappears or becomes unreadable blocks the plan.

Selected text is quoted in the handoff briefing; destination instruction files are never
created or overwritten. Each file is bounded to 8,000 bytes, and the final briefing can
shorten it further to fit the context budget. Review **What the agent is told** for the
final text. On a round trip, earlier quoted text may remain conversation history, but the
original instruction files stay in place and edits are not synchronized back. Review the
current source files on every transfer. The CLI's `--carry-rules` retains its global-only
meaning; individual file selection is available in the GUI.

This explicit selection follows the vendors' distinction between conversation context and
persistent instructions: [Claude memory](https://code.claude.com/docs/en/memory) and
[Codex AGENTS.md](https://developers.openai.com/codex/guides/agents-md). The launch control
uses a default action with adjacent alternatives, following
[split-button guidance](https://www.nngroup.com/articles/split-buttons/).

### Review notices

Cross-agent moves create portable conversation history and leave private vendor state out.
Moving between runtime profiles does not prove that different people own the two logins;
matching account labels also cannot establish permission to reuse private native state.
An active source is transferred as a snapshot: later messages are not synchronized, and
hopsesh does not stop that process or change its title.

The weekly drift workflow checks current published agent versions against the tested
versions, commands and schemas. It does not certify live resume behavior on every release.
Codex native writing therefore remains experimental, with untested versions identified
separately. See [drift detection](drift.md).

### CLI examples

```sh
hopsesh plan claude@<source-profile-id>/<session-id> --in codex --target-profile <target-profile-id>
hopsesh pull claude@<source-profile-id>/<session-id> --in codex --target-profile <target-profile-id> --yes
hopsesh push codex@<source-profile-id>/<session-id> laptop --in claude --target-profile <remote-profile-id> --yes
```

The source key also accepts a machine alias, for example
`laptop:claude@<source-profile-id>/<session-id>`. IDs come from `accounts list` and `ls --json`.

Current public vendor status cannot reliably distinguish every user/workspace combination.
Matching emails, organizations or plans therefore **do not permit native append across profiles**.
Account transfers write visible conversation history to a fresh native session ID, retain causal
lineage and preserve the original protected state. Signed reasoning, encrypted compaction and
other private runtime state are not replayed. The plan reports conversion losses. A return uses
a new portable copy when identity continuity is unverified, including repeated A → B → A trips.
Independent work in both copies requires a separate branch (`--keep-both`); it is never merged
silently. Operation receipts make retries idempotent; forks keep separate branch counters.

Journey counts stops by machine, agent, profile and observed login binding. It also reports direct
machine transfers and machine round trips separately: changing accounts twice on one computer
is not machine travel. Login changes create new replica segments; previous work retains its
authorship when native anchors verify the inherited history.

Cloud-provider account selection is separate. Bring a cloud session to its default local profile
first, then use an account transfer. Cloud returns preserve that pinned profile through adoption.

## Desktop app

For a local Codex session in the default `~/.codex` store, use the Resume menu's **Codex app**
option, TUI `D`, or:

```sh
hopsesh open codex@<profile-id>/<session-id> --app
```

Hopsesh checks the installed app/protocol handler and opens `codex://threads/<thread-id>`.
The URI selects the exact existing thread. It does not launch a terminal or send a message.
`codex app <path>` is a workspace launcher and is not used as a session-resume command.
[OpenAI deep-link documentation](https://learn.chatgpt.com/docs/reference/commands#deep-links).

The documented link cannot select `CODEX_HOME`, another login, a fork or a first prompt.
Custom profile roots use an integrated or external terminal; the desktop option explains why
it is unavailable. Hopsesh does not change the desktop app's current account. A successful
OS launcher exit confirms dispatch, not that the vendor has loaded or indexed the conversation.

## Vendor limits and verification

- Claude subscriptions support `CLAUDE_CONFIG_DIR` isolation. Multiple Claude Console logins
  without API keys cannot be isolated this way; Hopsesh reports this limitation. See
  [Claude Code authentication](https://code.claude.com/docs/en/authentication).
- Codex profiles pin `CODEX_HOME` and `CODEX_SQLITE_HOME`. New profiles request the OS keyring;
  no silent plaintext fallback is configured. If the vendor cannot use a keyring, its login
  must report the failure. Existing roots retain their own configuration.
- Hopsesh clears inherited credential/provider/root overrides for scoped launches; credentials
  belong in the vendor's configured storage. It uses public login status (`account/read` with
  `refreshToken:false` for Codex), and never reads or transports credential files.
- Account/binding observations have limited confidence. A vendor-hidden change cannot be
  detected from an unchanged public response. Portable copies avoid relying on that distinction.
- Automated fixtures test root isolation, changed plans, repeated routes, cloud adoption,
  forks/lineage, CLI/GUI/TUI and desktop launch dispatch. Paid real two-account sign-ins and
  vendor desktop indexing remain separate live acceptance checks; synthetic tests do not prove them.

Storage is `accounts.json` in Hopsesh's state directory, guarded by a cross-process lock and
atomic replacement. Lineage uses `lineage/5` and peers require protocol 5. Old receipt formats
are refused rather than silently assigned to a default account. The optional presence daemon
is a separate future-release proposal, not required or installed by Accounts.

### Opening a local session in Claude Desktop

Hopsesh uses the [documented Claude Code launcher](https://code.claude.com/docs/en/desktop)
`claude --desktop --resume <session-id>` for a saved local session. This requires
Claude Code 2.1.285 or newer, macOS or Windows x64, Claude Desktop, and a subscription
login. Claude checks installation, login and competing live sessions. API-key and
third-party-provider logins are not supported by this launcher. Hopsesh waits for
its result in a temporary terminal, reports errors, and leaves no terminal tab.
The TUI preserves terminal input/output for the same command.

An already-running Desktop session needs a different focus path: the CLI rejects it
as in use. For a live session identified as belonging to Desktop on macOS, Hopsesh
uses Desktop's own resume URL with that session's current CLI ID, after rechecking
live ownership and verifying the public app bundle and version (2.19675.1 or newer).
This is an observed implementation contract, not a documented public focus API;
the upstream drift check covers it. Older/unverified builds and other platforms
report that exact focus is unavailable. The
[upstream duplicate-session issue](https://github.com/anthropics/claude-code/issues/80773)
is why Hopsesh does not send historical IDs or blindly import to focus a chat.

Both actions require the default account profile and native `.claude` root. They do
not switch Desktop accounts, copy credentials, or send a message. Desktop can still
ask for folder trust or sign-in. A successful launcher handoff alone does not prove
that Desktop completed an import; any Desktop-side prompt must be completed there.
