# Experimental relay operations

The relay is isolated from other codonic.dev applications. Operator credentials
stay out of desktop settings, command arguments and logs. To request metadata,
feed a private JSON document containing `origin`, `space` and `token` to
`node infrastructure/relay/operator.mjs` through standard input. Its fixed HTTPS
route refuses redirects and emits only allowlisted counters. Device credentials
cannot access operator status. No session content, endpoint identity or account
label appears in that response.

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
staging before launch. Do not advertise this infrastructure as free.

The Worker also has a 1,000 ms CPU ceiling per invocation. Unreferenced R2 objects
from a failed cross-service transaction require the bucket lifecycle policy;
logical mailbox expiry and the durable deletion-intent alarm remain separate.
Hosted lifecycle/billing controls require operator authentication and remain a
release gate. Local tests do not establish deployed policy.

Node contracts cover concurrent quota admission, duplicate retry, nonrefunding
acknowledgment, byte refusal before R2 publication, rollover, pause/drain and
credential-free metrics. Actual SQLite/R2 tests qualify concurrent admission
and full acknowledgment drain. Native repeated-route/fork scenarios still pass
with the meter present.

See [Cloudflare rate limiting](https://developers.cloudflare.com/workers/runtime-apis/bindings/rate-limit/)
for the edge counter consistency boundary and
[Worker CPU limits](https://developers.cloudflare.com/workers/platform/limits/#cpu-time)
for invocation limits. These are traffic controls, not a billing quote.
