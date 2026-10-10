# Relay change notifications

One shared runtime owner keeps one authenticated notification socket for all its
local clients. A mailbox sends only `{"type":"mailbox-changed"}` after durable
message publication. That hint wakes the existing encrypted HTTP receiver; it
cannot acknowledge a message, grant permissions, carry a conversation or change
source freshness. Initial subscription requests reconciliation so messages queued
before connection are found. Committed batches drain immediately.

Healthy streams reconcile every five minutes. Protocol ping/pong detects broken
idle proxies every five minutes without waking the hibernating Durable Object.
Cancellation closes the socket and joins the reader before owner shutdown.
WebSocket refusal falls back to adaptive HTTP reconciliation (up to one minute
while idle), with bounded notification reconnect attempts. Settings and private
runtime status distinguish notifications from HTTP fallback.

An outgoing request wakes that same listener after submission. While HTTP
fallback has pending replies, healthy reconciliation runs at most once a second;
server Retry-After and failure backoff still take precedence. Completing,
canceling or reaching the request's signed lease removes that demand, returning
to adaptive idle reconciliation. A healthy notification socket does not add
polling for pending requests. An idle remote receiver may still take up to one
minute to collect the request; senders no longer add another idle minute to
retrieve a committed reply. Deterministic clock tests cover wakeup, return to
idle, request expiry and Retry-After, alongside actual socket idle tests.

The `/v1/notifications` upgrade requires the same verified HTTPS origin, JWT and
current mailbox grant as encrypted delivery. Credentials stay in headers, never
URLs. Native clients refuse redirects and oversized, binary or unexpected frames.
The gateway rate-limits upgrades before allocating a paid mailbox object; each
device can hold four sockets and a space can hold 128. Socket attachments contain
only the device and credential hash. Renewal, revocation, operator-key changes
and lease expiry close stale sockets. Durable Object alarms run at actual lease
or message expiry; no idle object timer prevents hibernation.

Tests cover native idle suppression, hint latency, cancellation, proxy fallback,
redirect refusal, unsafe frames, post-commit publication and attachment recovery.
The actual local SQLite/R2 qualification exercises the real upgrade, wake hints,
connection quota, credential renewal and revocation. Hosted qualification also
passes ten minutes without polling or pings, then a committed wake hint on the
same connection, alarm cleanup, revocation and credential expiry. This finite
idle-wake result does not establish billed hibernation duration; sustained cost,
provider proxies and broader OS energy behavior remain separate qualifications.
See [hosted evidence](relay-hosted-qualification.md).

## Source inventory updates

Native devices also publish signed, encrypted `observation` envelopes to paired
devices that have incoming `observe` approval. Recipients require current outgoing
`observe` approval for that pinned native device. These frames cannot dispatch a
transfer, masquerade as a reply, or grant receive permission. Cloud credentials
remain response-only at the gateway and cannot publish native inventory.

The source projects each inventory through the exact current peer/root approval,
using the same privacy filter as an explicit scan. It excludes other machines,
process tables, watcher paths, account labels/tags, last-message previews and
transport diagnostics. Inventory never includes `LastPrompt`, even if the peer
has export permission; conversation previews require a separate authorized call.
Comparing projected content ignores collection timestamps, so remote updates and
mailbox health cannot echo across the mesh. One shared subscriber and an
event-driven ten-second coalescing window replace repeated remote scans. A failed
publication retries from the latest evidence at most once a minute, driven by
shared collection events; no additional idle polling loop is installed.

Only a still-fresh local collection can issue a six-minute remote inventory lease,
measured from its original `observedAt`. Local presence keeps its shorter lease.
Unchanged remote inventory renews after five minutes of new source observations.
Changed, failed or paused state publishes through the same bounded path. Receipt,
socket pings, duplicate/reordered frames and errors never renew source evidence.
A missed renewal expires visibly and triggers the existing shared scan scheduler;
the fallback runs after expiry, rather than racing each healthy publication.
Approval expiry can shorten the lease. Current local approval is checked again
before a queued update is installed and before cached inventory is displayed.

An update is capped at 1 MiB before encryption and uses no native-operation or
reply recovery record. The in-memory replay index retains source metadata only,
is bounded to 4096 peers, and retires expired evidence. If an inventory exceeds
the update bound, diagnostics report the failure and the regular scan path remains
available. Runtime relay health exposes sent, received and failed update counts
plus the outstanding delivery error, without paths or credentials.
Rejected replaceable observations are counted and acknowledged without occupying
the durable quarantine reserved for native operations.

For four mutually paired idle devices, five-minute one-way renewal is 3456 frames
per day, before startup scans, changes, transfers, retries and byte limits. The
experimental 4096-frame/256-MiB daily ceiling is a small staging allowance, **not**
a full-day guarantee for an active fleet. Quota exhaustion is reported and old
evidence expires normally; this change does not raise paid service limits.

Native race tests cover current approval/direction/role, altered envelopes,
replays, source incarnation changes, overlong/future evidence, revocation and an
older in-flight reply arriving after a newer pushed snapshot. The real SQLite/R2
scenario runs separate CLI owners and verifies filesystem-driven publication,
pause/resume propagation and bounded mesh traffic without explicit remote scans.

Design follows [Cloudflare's WebSocket hibernation guidance](https://developers.cloudflare.com/durable-objects/best-practices/websockets/)
and [Apple's guidance on replacing timers with notifications](https://developer.apple.com/library/archive/documentation/Performance/Conceptual/power_efficiency_guidelines_osx/Timers.html).
