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
connection quota, credential renewal and revocation. Hosted hibernation behavior,
provider proxies and measured OS energy usage remain separate release gates.

Design follows [Cloudflare's WebSocket hibernation guidance](https://developers.cloudflare.com/durable-objects/best-practices/websockets/)
and [Apple's guidance on replacing timers with notifications](https://developer.apple.com/library/archive/documentation/Performance/Conceptual/power_efficiency_guidelines_osx/Timers.html).
