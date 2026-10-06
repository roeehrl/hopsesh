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
