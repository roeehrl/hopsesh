# Cloud session admission in 0.5

Install the verified CLI during environment setup. Prepare a fresh connector
only inside the actual task. An environment ID is not a session ID, and setup
must never capture keys, invitations or routing credentials in cached images.

Internet delivery has two separate approvals: admission gives a fresh cloud
identity a bounded routing credential, while local fingerprint approval allows
the desktop to request that session's observation or conversation checkpoint.
Creating or claiming an invitation never approves a peer on the desktop.

In Settings → Internet delivery, enroll the native machine, then choose
**Invite cloud session**. Select Claude Code or Codex, supply its actual native
session ID and choose a routing lifetime. Hopsesh saves a private invitation
file, displays its path and the issuing machine's fingerprint, and keeps the
one-use secret out of the browser bridge and clipboard.

The equivalent CLI operation is:

```sh
hopsesh cloud-integration ticket --provider claude-hosted --session ACTUAL_SESSION_ID --lease 1h
```

In the actual cloud task, prepare a fresh incarnation. Supply the invitation
through stdin to the claim command and compare the owner's full fingerprint
through an independent trusted channel:

```sh
hopsesh cloud-integration claim INSTANCE_DIRECTORY --fingerprint OWNER_FINGERPRINT < PRIVATE_INVITATION_FILE
```

The result contains only public pairing metadata. Credentials stay in that
incarnation's private directory. Claiming pins the checked native owner on the
cloud side for observation and, only if explicitly enabled at preparation,
transcript export. It grants no inventory, receiver, shell or device settings
methods. Routing from the cloud endpoint is restricted to its issuing native
machine. Its lease cannot exceed the native machine's remaining routing lease
or the fresh cloud incarnation's expiry.

Use **Check claim** on the invitation row to retrieve the current server status
without adding a polling timer. A claimed identity can be opened in the separate
cloud approval dialog. Its fingerprint must still be compared against the
actual task's output; server status is not peer approval. Export starts unchecked.
The CLI status command also grants no peer permissions:

```sh
hopsesh cloud-integration status-ticket PRIVATE_INVITATION_FILE
hopsesh cloud-integration serve INSTANCE_DIRECTORY
```

An invitation's claim window is ten minutes. The requested routing lifetime is
one minute to 24 hours. The invitation binds the provider and native session;
the first successful claim also binds its fresh incarnation and public keys.
An identical retry returns the same credential, including after a failure
between mailbox enrollment and authorization-object commit. A changed identity,
scope or incarnation cannot reuse that invitation. A resume or rebuild requires
a new invitation. Choose **New task or independent fork** for unrelated work;
matching native IDs, titles and environments never join their lineage.

For a resume or rebuild, explicitly select the saved logical task in the GUI's
**Task continuity** field. The CLI equivalent is:

```sh
hopsesh cloud-integration tasks
hopsesh cloud-integration ticket --provider claude-hosted --session CURRENT_NATIVE_SESSION_ID --resume-task LOGICAL_TASK_ID --lease 1h
```

The logical task ID survives invitation expiry and a changed provider-native
session ID. Each invitation reserves a higher owner-signed generation before
requesting routing authority. Invalid local input is rejected before reservation.
An uncertain network failure leaves its generation reserved; prepare a fresh
invitation to continue. Previous generations cannot authorize new checkpoint
imports. Old routing leases remain independently revocable until expiry.

Claiming the new generation supersedes the previous connector in the same task
state directory, including when the native session ID changed. Private filesystem
notifications stop its listener and join it without another polling timer. A
partially published association can recover its exact claim, but cannot roll back
a newer generation. A fork has an independent logical task and generation.

Native checkpoint review verifies the exact invitation's current claim against
the independently approved cloud key, the owner's saved task and its latest
generation. A historical task signature or connector-provided label alone cannot
join lineage. The generation is rechecked before native installation. Each
incarnation still requires its own fingerprint approval and export permission.

Successive checkpoints preserve the same source ledger. A changed native ID
inherits only the complete previously verified native prefix, checked by native
anchors and content hashes. Rewritten or compacted history is refused unless the
user explicitly chooses a separate fork; its fidelity warning remains attached.
Checkpoint forks retain separate branches, and retries retain their native IDs.
When a reviewed history rewrite creates a new source branch, the private task
ledger selects that branch for subsequent unchanged checkpoints. This transition
requires the same cloud task and a direct child branch; ordinary native receipts,
other tasks and concurrent sibling branches cannot replace it. Interrupted receipt
writes recover durably, and undo restores the prior private ledger. Cloud vendor
files remain read-only. A saved handoff capsule continues to prove historical
ancestry without resetting the task's current branch.
This establishes checkpoint ancestry across approved incarnations. To connect a
task to its prior local-to-cloud handoff, use **Settings → Internet delivery →
Link task to saved handoff…** before reviewing the first checkpoint. Select the
logical task and its saved handoff, review the original cloud session and family,
then save. The list offers recent local handoffs with applied native receipts,
filtered by provider; it never chooses an association from names or native IDs.

The CLI exposes the same review and validation:

```sh
hopsesh cloud-integration handoffs
hopsesh cloud-integration link-handoff TASK_ID SAVED_HANDOFF_ID --dry-run
hopsesh cloud-integration link-handoff TASK_ID SAVED_HANDOFF_ID
```

For an independent cloud fork, issue a new logical task and select **Independent
cloud fork** (CLI `--fork`). It inherits the saved family but has a separate
branch and trip counts. A handoff can have only one continuation task; additional
associations require an explicit fork. Each still needs fresh keys and approval.

The native owner signs a private capsule containing the exact saved journal
receipt, task identity, briefing and fork choice. Import must independently
verify the saved briefing at the start of the conversation or the complete
original native prefix with matching anchors and content hashes. Similar text
or a claimed cloud ID cannot supply ancestry. Missing or edited evidence fails
closed; generic import fork options cannot bypass the saved-proof check.

The association is a causal identity event, not another transfer or round trip.
Settings shows saved task ancestry; GUI/TUI journey history labels the verified
identity event. Later transfers retain the briefing as inherited context without
counting it as new authored work. Original handoff and conversion losses remain
recorded; provider-private reasoning, code and process state are not reconstructed.

Reviewing a checkpoint freezes its association. Remove an unstarted cached
review before adding a link; removed review IDs remain retired. Once checkpoint
lineage exists, keep it and use a new task for different ancestry. Linking is
idempotent, and an existing different link cannot be overwritten. Undoing the
saved handoff invalidates future use while its journal exists. A signed capsule
can survive deliberate journal retirement. Private capsule storage is bounded to
1,024 entries, 16 MiB each and 64 MiB total; signatures and ownership are rechecked
before use. This local association does not enable unsupported provider export.

**Revoke invitation** revokes the claim opportunity and any claimed routing
lease. It remains available after the ten-minute claim window, through the
claimed lease. The CLI equivalent records the same local revocation receipt:

```sh
hopsesh cloud-integration revoke-ticket PRIVATE_INVITATION_FILE
```

Native routing renewal invalidates unclaimed tickets bound to the previous
credential. Operator signing-key rotation also invalidates that authority.
Revoking an original leaves independent forks usable. Previously delivered
checkpoints and local peer permissions are independent of routing revocation.

Server limits are 512 retained admission records and 16 per issuing native
device. Claimed records remain available for revocation until their lease ends;
alarms reclaim expired records. Native state retains at most 128 private
invitations, pruning only once every possible claimed lease has ended. Logical task
history retains at most 10,000 bounded metadata records independently of routing
secrets; a full task quota still permits explicitly resuming an existing task.

The three-OS CI relay job exercises real Go and CLI clients against the actual
authorization and mailbox handlers, and separately against local workerd with
SQLite Durable Objects and R2. It includes retry, original/fork independence,
rebuild, revocation and status. The native checkpoint scenario additionally exercises
six fresh incarnations, changed native IDs, original/fork branch independence,
Claude/Codex local imports, stable retries and automatic old-process shutdown.
Saved-handoff and independent-cloud-fork rows additionally preserve briefing
context through A→B→C→B→C→A→B with alternating agents and unchanged vendor files.
These use disposable provider transcripts; they do not qualify real provider
startup or lifecycle behavior. Deterministic server contracts inject a lost
cross-object commit and verify native renewal, authority rotation and role
boundaries. The local Wrangler fixture explicitly uses its local HTTPS origin
so signed proofs match the tested service rather than the production route.

Hosted relay deployment, Access configuration and actual default provider
startup/pause/rebuild remain separate release gates. Current Codex Cloud native
transcript export remains unsupported until qualified. Installed helpers and
successful local admission tests do not establish provider lifecycle support.
