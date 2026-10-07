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
a new invitation; an independent fork uses its own session ID and invitation.

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
invitations, pruning only once every possible claimed lease has ended.

The three-OS CI relay job exercises real Go and CLI clients against the actual
authorization and mailbox handlers, and separately against local workerd with
SQLite Durable Objects and R2. It includes retry, original/fork independence,
rebuild, revocation and status. Deterministic server contracts inject a lost
cross-object commit and verify native renewal, authority rotation and role
boundaries. The local Wrangler fixture explicitly uses its local HTTPS origin
so signed proofs match the tested service rather than the production route.

Hosted relay deployment, Access configuration and actual default provider
startup/pause/rebuild remain separate release gates. Current Codex Cloud native
transcript export remains unsupported until qualified. Installed helpers and
successful local admission tests do not establish provider lifecycle support.
