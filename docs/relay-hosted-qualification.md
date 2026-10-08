# Hosted 0.5 staging qualification

2026-10-08, isolated codonic.dev account resources. This is a qualification record,
not a published 0.5 release or a claim that provider lifecycle gates passed.

## Deployed resources

- `hopsesh-relay-experimental` on `relay.hopsesh.codonic.dev`: SQLite Mailbox and
  Authorization objects, four independent rate-limit namespaces, and private
  `hopsesh-relay-experimental-ciphertext` R2 storage.
- `hopsesh-downloads-experimental` on `downloads.hopsesh.codonic.dev`: separate
  private `hopsesh-downloads-experimental-releases` R2 storage and publication
  credential. Only versioned public downloads pass through the Worker.
- Existing Workers and their rate-limit bindings were read before provisioning;
  the Hopsesh namespace IDs do not overlap. No existing Worker was modified.
- Operator and publication secrets are distinct, generated locally and supplied
  to Wrangler through standard input. They are absent from configuration and CI.
- The account remains on Workers Free. The custom paid-only CPU setting was
  rejected and removed; the plan's 10 ms HTTP CPU limit applies. Both deployed
  entrypoints use publicly trusted TLS. Initial certificate propagation took
  several minutes; no certificate checks were disabled.

The ciphertext lifecycle expires objects after two days and aborts incomplete
multipart uploads after one day. It preserves a buffer beyond the maximum
one-day mailbox lease. Reading back the deployed rule passes; this is policy
verification, not an observed two-day expiry. R2 cleanup is asynchronous.

## Billing and orphan-retention follow-up

The live account Billing → Billable usage page was inspected on 2026-10-08.
Its existing default budget alert is configured at **USD 10**, with one recipient
(the account billing address). The page showed USD 0.00 usage cost for the
September 12–October 11 cycle through October 8. This is a current account-level
snapshot, not Hopsesh-only attribution or a forecast of production cost. No
subscription, payment method, recipient or threshold was changed.

The Mailbox namespace's last-24-hour metrics showed 5.61 GB-seconds billable
duration, CPU P99 5.12 ms and memory P99 3.19 MB. All 16 invocation errors were
classified as client disconnects; CPU-limit, memory-limit, internal and thrown
exception counts were zero. These are cumulative staging samples, not an isolated
idle-cost measurement or a production load test.

The alert-configuration gate is satisfied. For staging, an alert triggers operator
review and a manual pause of the Hopsesh relay while its usage is investigated.
Alerts themselves do not cap spend or stop traffic, and unrelated account services
must remain untouched. See [Cloudflare budget alerts](https://developers.cloudflare.com/billing/manage/budget-alerts/).

The live ciphertext bucket's enabled two-day object-expiry and one-day
multipart-abort rule was read back again. A 256-byte random disposable canary was
uploaded directly to the private bucket, outside any mailbox, and its downloaded
SHA256 matched on 2026-10-08 at 20:32:02 UTC. This isolates R2 orphan cleanup from
mailbox alarms:

- Bucket: `hopsesh-relay-experimental-ciphertext`
- Key: `qualification/orphan-20261008T203148Z-d0020f34cd19557e`
- SHA256: `ad941a5b48bb242361d74b488a5128c4b1064efec7238e93ab190301d403cfe0`
- Observe after **2026-10-10 20:33 UTC**; expiration is asynchronous. Do not delete
  the canary manually and then claim lifecycle success. An authenticated missing
  object response is required; authentication/network failures do not pass.

The private local readback/record lives under the operator's
`cloud-0.5/orphan-lifecycle` qualification directory. The object contains no
session data or credentials. No shorter lifecycle rule replaced the deployed
retention policy. Actual orphan expiration remains pending elapsed time.

## Native hosted smoke

`TestRelayHostedStaging` is explicitly opt-in and restricted to the relay hostname
above. It reads a bounded private operator-secret file, refuses redirects, creates
a random disposable namespace and three native CLI homes, and grants only their
fixture repository roots. Hosted credentials last at most ten minutes and are
revoked on normal test cleanup. No real conversation is included.

```sh
HOPSESH_HOSTED_RELAY=1 \
HOPSESH_HOSTED_RELAY_ADMIN_FILE=/absolute/private/operator-secret \
go test -race -count=1 -run '^TestRelayHostedStaging$' -v -timeout 4m ./internal/e2e
```

The hosted run passed in 39 seconds including cleanup. It covers authenticated
notification streams, native Claude/Codex push and pull on A-B-C-A, one origin
round trip, destination owner restart, immutable retry, separately travelling
fork/original content and peer-owned undo. All three owners ran on this Mac;
this is real internet transport, not three physical hosts or mixed OS evidence.
An initial harness assertion returned to a different agent and incorrectly
expected an origin round trip; the corrected route returns to the original
machine and agent. No product code change was needed for that assertion.

The repository configuration defaults to paused. Local SQLite/R2 fixtures
explicitly unpause their disposable service. After the hosted run, the deployed
relay was restored to paused and a real admission request returned HTTP 503
with `paused`. Access enrollment configuration was subsequently qualified below.

## Hosted expiry and authorization termination

`TestRelayHostedRetention` passed under the race detector in 63 seconds. It uses
an isolated namespace, three fresh identities and one disposable encrypted frame.
An unacknowledged ten-second frame was present before expiry; the deployed
Durable Object alarm then removed the mailbox row, replay tombstone and R2 delete
intent. Operator counters reached zero retained bytes/messages/deletions while
the daily admitted-frame count stayed one. Stats reads do not run maintenance,
and the test did not acknowledge the frame. This exercises the actual alarm and
R2 deletion path; it does not establish the separate two-day orphan lifecycle.

The same run verified initial and post-commit WebSocket wake hints. Explicit
self-revocation closed an existing stream with policy status 1008 and refused
subsequent HTTP access with 403. A separate 60-second credential expired without
revocation, also closing its existing stream with 1008 and refusing HTTP access.
This verifies finite hosted lease termination, not long-duration hibernation cost.
Cleanup revokes remaining fixture credentials; deployment is restored to paused.

Use the same opt-in environment as above with
`-run '^TestRelayHostedRetention$' -timeout 3m` to repeat this separate gate.

## Ten-minute idle notification qualification

`TestRelayHostedIdleWake` passed under the race detector in **661.19 seconds**
on 2026-10-08. An already subscribed client remained silent for ten minutes:
no HTTP polling, WebSocket pings or operator reads. The existing stream then
received the wake hint for a newly committed encrypted frame. The same run
verified alarm-driven ciphertext/tombstone expiry, drained R2 deletion intents,
and HTTP 403/WebSocket 1008 after both explicit revocation and lease expiry.
All disposable credentials were revoked or expired during cleanup. Staging was
restored to paused deployment `4dc1280e-71b4-451d-8e01-234fe98bbcf6`; a live
POST to `/v1/device/code` returned HTTP 503 with `{"error":"paused"}`.

This qualifies finite idle reconnection-free wakeup and authorization termination.
It does not attribute billed duration to the idle interval or establish sustained
load cost. The production client normally reconciles and pings every five minutes;
this deliberately quieter transport fixture tests a longer silent connection.

```sh
HOPSESH_HOSTED_RELAY=1 HOPSESH_HOSTED_IDLE=1 \
HOPSESH_HOSTED_RELAY_ADMIN_FILE=/absolute/private/operator-secret \
go test -race -count=1 -v -timeout 13m -run '^TestRelayHostedIdleWake$' ./internal/e2e
```

## Immutable downloads

Version `0.5.0-staging.20261008.d93f24e` contains Linux amd64 and arm64 CLI archives
built from exact Git-exported source `d93f24e`. The existing local release key
signed the manifest; the bundled public key matches it. No Git tag, GitHub release,
latest-release pointer or installed application was changed by this publication.

The hosted publisher uploaded the two archives plus manifest/signature. Repeating
the same publication succeeded without replacement. Downloads independently pass
signature and archive digest verification. A missing publication credential is
refused with HTTP 403; an authenticated attempt to change an existing object's
bytes is refused with HTTP 409. Downloading the original again confirms unchanged
bytes. The current immutable download list is visible in the
[signed manifest](https://downloads.hopsesh.codonic.dev/releases/v0.5.0-staging.20261008.d93f24e/checksums.txt).

Curl and Node fetched successfully. One Python urllib request received HTTP 403;
its cause is not established. This does not qualify every user-agent or provider
proxy path.

## Local macOS candidate

The same exported source built a universal GUI bundle and embedded CLI, signed
with the local Developer ID identity and notarized with the existing Keychain
profile. Apple accepted the DMG; both DMG and app tickets were stapled. The bundle
stays outside Applications. A separate locally signed DMG manifest drives the
opt-in `TestInstallSignedMacApp` fixture, which uses the production installer and
Gatekeeper assessment against a temporary destination. It installs successfully,
runs the embedded CLI's version command, and refuses existing/concurrently
appearing destinations. The full updater race suite passes. This candidate is
qualification input; final release artifacts still require the final commit and
release workflow's provenance/signing checks.

## Provider qualification and hosted enrollment

A fresh install-only task in the selected `hopsesh-cloud-smoke` Codex environment
retried the now-live signed staging download on 2026-10-08. Its configured proxy
was unreachable on port 8080: the bootstrap exited 1, the absent binary exited
127, and the separate 20-second capabilities probe exited 7 with HTTP 000.
The task reported Linux x86_64 and no current task ID in its provided context.
No network/approval settings, repository files or secrets were changed, and no
connector was enrolled. This is a provider connectivity failure before signature
verification, not a successful installation or lifecycle test.

A second bounded attempt in the same default Codex task at 20:33 UTC on
2026-10-08 returned curl exit 7, HTTP 000 and CONNECT 000 for both the signed
checksums and capabilities endpoints: the configured proxy on port 8080 remains
unreachable. No installer or connector was run after those failures. The supplied
context exposes a source thread ID, not a verified current native task identity.
The Claude cloud UI was also rechecked: weekly usage remains 100%, active sessions
report the monthly spend limit, and the displayed weekly reset is October 9 at
20:00 Asia/Jerusalem. No spending limit or provider network policy was changed.

The user completed Zero Trust Free activation in Chrome and approved creating
the staging Access application. The saved application
`63bd3ec9-6a55-4fe7-8d54-f04565769b8a` covers exactly `/device`, `/device.js`,
`/device.css`, `/v1/device/review` and `/v1/device/approve`. Its sole Allow policy
includes `roeehrl@gmail.com`; the application session is 30 minutes. The Worker
has the matching issuer and audience in nonsecret configuration.

Live unauthenticated requests to all five browser paths return an Access login
redirect. The four machine authorization endpoints reach the Worker and return
JSON validation errors for empty requests, without browser redirects. Chrome
completed the real Cloudflare identity login and loaded the enrollment page.
A disposable native CLI produced a device code; the browser review displayed
its exact independently printed fingerprint. No peer permissions were granted.
With explicit user approval, both disposable delivery enrollments completed on
2026-10-08 against source `51cb388`. The actual CLI exited successfully and saved
its isolated connection. `TestRelayHostedAccessBrowser` passed under the race
detector in 24.88 seconds: CLI mailbox access and zero peer grants, the native
S256 PKCE loopback callback and token exchange, and distinct device credentials
in the same Access-authenticated user's routing space. Cleanup self-revoked both
credentials and verified that each subsequent mailbox request returned HTTP 403.

Staging was restored to paused afterward (deployment
`d458a955-bfb8-4b12-b9b9-7e1d9cb69ef6`); a fresh POST to `/v1/device/code`
returned HTTP 503 with `{"error":"paused"}`. The saved disposable CLI connection
contains a revoked credential. The installed app and its settings were untouched.

`TestRelayHostedAccessBrowser` is reusable interactive qualification: complete CLI login
in a disposable namespace, then run with `HOPSESH_HOSTED_ACCESS=1` and
`HOPSESH_HOSTED_ACCESS_CLI_STATE=/absolute/disposable/state/relay`. The test checks
the CLI credential and zero peer grants, prints a second URL/fingerprint for the
desktop PKCE login, verifies both devices have the same authenticated user's
space, then self-revokes both credentials and verifies HTTP 403. Browser cookies
and operator credentials are never read by the test. Run only during a supervised
temporary unpause; restore the paused deployment after qualification:

```sh
HOPSESH_HOSTED_ACCESS=1 \
HOPSESH_HOSTED_ACCESS_CLI_STATE=/absolute/disposable/state/relay \
go test -race -count=1 -v -timeout 15m -run '^TestRelayHostedAccessBrowser$' ./internal/e2e
```

Observed two-day orphan expiry and measured long-duration hibernation/cost
behavior remain unqualified. The existing billing alert is verified above. Provider default startup,
pause/resume/rebuild and transcript visibility remain separate gates. The PR stays
draft and the release stays unpublished.

Sources: [Worker limits](https://developers.cloudflare.com/workers/platform/limits/),
[SQLite Durable Objects on Free](https://developers.cloudflare.com/durable-objects/platform/pricing/),
[R2 lifecycle behavior](https://developers.cloudflare.com/r2/buckets/object-lifecycles/),
and [automatic Custom Domain certificates](https://developers.cloudflare.com/workers/configuration/routing/custom-domains/).
