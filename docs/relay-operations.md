# Experimental relay operations

The relay is isolated from other codonic.dev applications. Operator credentials
stay out of desktop settings, command arguments and logs. To request metadata,
feed a private JSON document containing `origin`, `space` and `token` to
`node infrastructure/relay/operator.mjs` through standard input. Its fixed HTTPS
route refuses redirects and emits only allowlisted counters. Device credentials
cannot access operator status. No session content, endpoint identity or account
label appears in that response.

Run the local operator and download publisher with Node 24.2 or newer (CI uses
Node 24). Both use the native ES-module entry-point flag, so paths containing
directory symlinks work and importing their functions performs no operation.

Every mailbox transaction enforces a daily UTC admission ceiling: at most 4,096
new envelopes and 256 MiB of serialized encrypted envelopes per account space.
`DAILY_FRAMES_PER_SPACE` and `DAILY_BYTES_PER_SPACE` may tighten these values;
values above the hard ceilings are clamped. Duplicate committed retries do not
consume another reservation. Acknowledgment and expiry do not refund daily
admission. Midnight rollover is computed in the next transaction, with no idle
reset timer. Existing storage/device/frame/retention quotas still apply.

Set `RELAY_PAUSED=1` through the isolated Worker deployment to stop new envelopes,
new routing registrations and new login/cloud claim requests before allocating
paid objects. Existing reads, acknowledgment, revocation and operator inspection
remain available so admitted work can drain. This does not stop native sessions.
No unrelated service or shared domain is affected.

Authenticated mailbox traffic has separate per-device (240/minute) and aggregate
(2,400/minute) edge burst controls. These precede mailbox allocation. Cloudflare's
rate-limit counters are local to a network location and eventually consistent;
they are not exact accounting or a global monetary cap. The transactional daily
meter bounds admitted encrypted traffic per account space, rather than the total
Worker requests, R2 reads, failed publications or account invoice. Configure
billing alerts, an alpha access policy and a manual pause threshold in hosted
staging before launch. Staging now has a verified existing USD 10 account-wide
alert; reaching it requires operator review and a manual Hopsesh-only pause.
The alert is informational and does not automatically enforce that pause. Do not
advertise this infrastructure as free.

The initial hosted account uses Workers Free, whose built-in HTTP CPU ceiling is
10 ms. Cloudflare rejected a custom `cpu_ms` limit on that plan, so the checked-in
configuration omits that paid-only setting. A future plan change must explicitly
review the CPU ceiling (the proposed paid setting is 1,000 ms) and billing alerts.
This is not a promise that all relay workloads fit the free plan.

Before a hosted load run, inspect current UTC-day account usage and reserve capacity
for the entire workload **and cleanup**. Include SQLite writes, not just HTTP
requests: key/value puts, deletes and `setAlarm()` are billed as row writes. The
Free allowance is 100,000 rows written per day across Durable Objects; it resets
at 00:00 UTC. Repeated qualification runs on October 10 exhausted it, causing
HTTP 503 for submissions and revocation and failures in expiry alarms. Pausing
new work cannot restore exhausted provider capacity. Keep the failure and pending
cleanup visible; do not claim successful revocation or retry load while exhausted.
Once capacity is available, verify outstanding cleanup before starting another
complete run. Never delete the natural-retention canary to make a test pass.

The redacted tail category `daily-write-quota` matches the exact provider error;
unknown exception text stays hashed. Usage analytics may lag, and Hopsesh's
per-space traffic limits do not reserve Cloudflare's account-wide allowance.
A Workers Paid upgrade changes account-wide billing and requires explicit
approval; the $10 alert remains informational rather than a spending cap.
See [Durable Objects pricing](https://developers.cloudflare.com/durable-objects/platform/pricing/).

The private ciphertext bucket has a deployed two-day expiration rule and one-day
multipart-abort rule. Its cleanup buffer exceeds the maximum one-day message
lease. Logical mailbox expiry and durable deletion-intent alarms remain separate;
R2 lifecycle removal is asynchronous and does not establish an exact deletion
instant. The download bucket retains immutable release objects. Access enrollment
and budget-alert configuration are verified; observed orphan
expiry and long-duration cost remain gates. See [hosted qualification](relay-hosted-qualification.md).

Node contracts cover concurrent quota admission, duplicate retry, nonrefunding
acknowledgment, byte refusal before R2 publication, rollover, pause/drain and
credential-free metrics. Actual SQLite/R2 tests qualify concurrent admission
and full acknowledgment drain. Native repeated-route/fork scenarios still pass
with the meter present.

Native recovery storage has a separate 256 MiB aggregate limit across operation
records, outgoing request bindings, large-upload descriptors and cached encrypted
replies. The receiver
reserves a bounded result slot before starting native work. Under admission
pressure it removes expired reply ciphertext and expired owner-retained outcomes;
it preserves operation digests, authorization scope, completed/uncertain phases,
outgoing bindings and native journals. A retired completed outcome fails closed
on retry and cannot execute again. Live results are never evicted for new work.
This cleanup runs on write pressure and adds no idle timer. The existing 10,000
operation and outgoing-record ceilings remain in force; receipt tombstones are
not automatically deleted to bypass those ceilings.

Observation and preview are fixed passive read methods. Their wire/result lease
is at most 90 seconds and they do not retain a 24-hour owner outcome. After expiry,
completed passive records and their outgoing read intents can retire on count or
byte pressure; a renewed read can request fresh evidence. Live reads and incomplete
records stay. Native transfer/export/ack/undo bindings remain permanent, even if a
record falsely claims to be passive. Periodic observation therefore does not
consume the lifetime native-action quota. Cleanup adds no idle timer.

The native regression suite covers the production aggregate quota, refusal
before intent/action, bounded linked-file refusal, wire expiry followed by valid
reply renewal, expired-owner cleanup and retry after owner restart.

See [Cloudflare rate limiting](https://developers.cloudflare.com/workers/runtime-apis/bindings/rate-limit/)
for the edge counter consistency boundary and
[Worker CPU limits](https://developers.cloudflare.com/workers/platform/limits/#cpu-time)
for invocation limits. These are traffic controls, not a billing quote.
