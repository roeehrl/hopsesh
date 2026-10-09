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

That is an earliest observation threshold, not an exact deletion deadline.
Record the actual `x-amz-expiration` header and disappearance time. R2 documents
typical removal within 24 hours after that expiration value, with possible delays;
see [lifecycle behavior](https://developers.cloudflare.com/r2/buckets/object-lifecycles/).

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

## Thirty-minute idle billing attribution

The longer opt-in `TestRelayHostedIdleWake` run passed under the race detector in
**1861.78 seconds**, from 2026-10-08 22:48:30 UTC through 23:19:32 UTC. Its existing
notification stream remained silent from 22:48:31 through 23:18:31: no mailbox
polls, WebSocket pings or operator reads. The stream received the next committed
wake hint; deployed alarms cleaned expired ciphertext, replay tombstones and
delete intents. Revocation and lease expiry each closed an existing stream and
refused subsequent HTTP access. All fixture credentials were revoked or expired.

After cleanup, GraphQL telemetry read at 23:20 UTC isolated Mailbox namespace
`ea98605207744990bfec79669b4d94ae`, name
`734f997145687066d6bdd4644304894a`, object
`be09f1c055d4c10902b719a5bc8b3399fd4248c15637279a9383f899105bbea6`.
The `durableObjectsPeriodicGroups` sums covered only setup, wake and cleanup
minutes (22:48, 23:18, 23:19):

| Metric | Reported total |
| --- | ---: |
| Billable duration | 0.066295808 GB-seconds |
| Active time | 517,936 microseconds |
| CPU time | 29,141 microseconds |
| SQLite rows read / written | 152 / 36 |
| Inbound / outbound WebSocket messages | 0 / 5 |
| CPU-limit / memory-limit / fatal internal errors | 0 / 0 / 0 |

The invocation dataset reports 21 invocations, including two alarms and two
hibernation events. Two HTTP events are classified `clientDisconnected`, matching
the test's intentionally terminated streams; the other invocations report
success. Long-lived WebSocket wall time must not be mistaken for billed duration.
No periodic samples occurred in the silent interval. Together with the successful
wakeup, these measurements support hibernation for this finite fixture. A single
continuously billed 128 MB object over just 1,800 seconds would represent
230.4 GB-seconds, compared with the measured 0.0663 including setup and cleanup.

This is Mailbox-object attribution, not total relay cost: Worker requests,
Authorization objects, R2 operations/storage, production clients' five-minute
reconciliation and sustained multi-user load require their own accounting.
It is not a claim of free operation or a production cost forecast. Metrics follow
[Cloudflare's dataset semantics](https://developers.cloudflare.com/durable-objects/observability/metrics-and-analytics/)
and [duration accounting](https://developers.cloudflare.com/durable-objects/platform/pricing/).

Staging was restored to paused deployment
`ef02d890-1161-4dcd-b119-c08c605d3e9b`. An immediate check still hit the prior
deployment (HTTP 400); after propagation a fresh admission request returned
HTTP 503 with `{"error":"paused"}`. Deployment success alone is not the pause
verification. Repeat with `HOPSESH_HOSTED_IDLE_DURATION=30m` and a timeout of at
least 34 minutes; the duration override is bounded between ten minutes and one hour.

## Immutable downloads

The newer immutable helper `0.5.0-staging.20261009.f134df2` was independently
read back over HTTPS: its manifest and signature match local bytes, OpenSSL
verifies the signature, and both archive sizes and hashes match the manifest.
Linux amd64 is 7,095,378 bytes (SHA256
`27159fe8d1a56ceea55b755fd42c6332e43d7412528d27a37e25ec6056d39f4b`);
Linux arm64 is 6,367,587 bytes (SHA256
`df6c64245b2d41241b599a76acfd684e91b4b67cb11c49b713bbec1cc01e10e1`).
This is staging distribution evidence, not a final release.

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

The corrected source `2583f29` was exported and built again as
`0.5.0-staging.20261009.2583f29`: both Linux architectures received a separately
signed immutable manifest and were published without replacing the earlier
version. The universal macOS candidate was locally signed, notarized and stapled
outside Applications. Its signed-DMG production-installer test passed under the
race detector in 2.36 seconds, including preserving a destination that appears
during download. The installed 0.4 application remains untouched. These are
staging artifacts, not a published GitHub release or final release attestation.

Source `3650f60` was separately exported and built as universal candidate
`0.5.0-staging.20261009.3650f60`. Local Developer ID signing, Apple notarization and
both app/DMG stapling succeeded. The signed-manifest production installer passed
new installation, embedded-CLI version verification, existing-app refusal and
concurrently appearing destination preservation (2.76 seconds). The entire
macOS updater package passed under the race detector (4.34 seconds). Artifacts
remain outside Applications under `/tmp/hopsesh-candidate-3650f60.2iG1j7/macos`;
build and installer logs are `/tmp/hopsesh-candidate-3650f60-build.log` and
`/tmp/hopsesh-candidate-3650f60-install.log`. No GitHub release or cloud download
publication was performed for this candidate.

## Provider qualification and hosted enrollment

The October 8 installation attempts returned curl exit 7 with HTTP/CONNECT
`000`. Follow-up diagnosis on **2026-10-09 (Asia/Jerusalem)** established that the
per-command sandbox rejected TCP socket creation with `Operation not permitted`.
Proxy DNS resolution succeeded. The supported command-approval path reached
already-allowed GitHub with CONNECT/HTTP 200, proving the proxy was available.
Both Hopsesh hosts then returned CONNECT 403 because the restricted policy did
not allow them. The earlier description of an unreachable provider proxy was
incomplete; these were two separate permission layers.

The user explicitly approved adding exactly `downloads.hopsesh.codonic.dev` and
`relay.hopsesh.codonic.dev`. The environment editor saved and published those two
domains while preserving the package-manager preset, private sharing, and empty
network-secret/environment-variable configuration. The older install task still
retained its original policy and correctly refused both destinations. The
published configuration environment reached GitHub and both Hopsesh endpoints
with curl exit 0, CONNECT 200 and origin HTTP 200 using normal TLS/proxy settings.
Its runtime snapshot lists both approved hosts; `environment_status` reports
current observations but policy state `unknown`, so the result establishes actual
connectivity without claiming a formal policy-readiness attestation.

After the user raised Claude's usage limit, a fresh disposable task in **Default**
with `roeehrl/hopsesh-cloud-smoke` ran successfully using usage credits. Both
Hopsesh endpoints returned CONNECT/HTTP 200; the GitHub homepage returned CONNECT
200 and origin HTTP 400. The actual workspace is `/home/user/hopsesh-cloud-smoke`
on Linux x86_64. Claude's session-info tool exposes cloud task
`session_01NFYqYKdDjaLh2yLWavKqHD`. The later startup test below established the
native CLI UUID through the actual environment and hook input, not an incidental
scratchpad path. Connectivity alone does not establish lifecycle or export.

The unchanged generated installer then passed in both providers. Release
signature and archive SHA-256 checks completed before installing
`0.5.0-staging.20261008.d93f24e` (commit `d93f24e`). Installer, `version`, and
`cloud-integration --help` each exited 0. Codex installed under
`/home/agent/.local/share/hopsesh/cloud/v0.5.0-staging.20261008.d93f24e/`;
Claude installed under `/root/.local/share/hopsesh/cloud/v0.5.0-staging.20261008.d93f24e/`.
Normal TLS/proxy settings were retained. These were disposable Linux amd64
installations; this installation phase made no repository edits, connector
enrollment or release pointer changes. Codex used its supported per-command approval
mechanism; Claude required no command approval. Network and install gates for
these tested environments are satisfied. Published startup scripts, pause/resume,
rebuild, export and connector lifecycle remain separate qualification work.

### Actual startup and scoped connector tests

On October 8 UTC / October 9 Israel time, the generated Claude startup hook was
installed in the disposable repository after explicit user approval. A real
`/compact` invocation then ran `SessionStart` and created disconnected incarnation
`e8d92b4feb307e1895dde977a9d80959` at 22:48:35 UTC. The hook's native session was
`ffd9cdad-061f-5ec4-9f36-abc28072e779`, matching `CLAUDE_CODE_SESSION_ID`; its exact
native JSONL existed under the actual Claude projects root. Export remained off.
This qualifies the real compact callback, not a fresh clone, pause or rebuild.

The user approved a 20-minute, session-only Claude export connector, but Claude's
automatic reviewer refused the explicit prepare command as Data Exfiltration.
It then refused the explicitly approved repository-local exact command rules as
Self-Modification. No allow rule was installed, no invitation was claimed and no
transcript was exported. The owner revoked that unused invitation. Permission
modes and unrelated settings were not changed; these refusals remain an external
qualification blocker, not a successful export test.

For current Codex Cloud, `CODEX_SESSION_ID` and `CODEX_THREAD_ID` matched the actual
provider UI chat ID `01a11d8e-638b-738d-92e9-58933275d04e`. This is an observed
binding in this environment, not a documented future lifecycle guarantee. Manual
prepare created incarnation `43cc9036d349490e686e41840cc17417`; claim used a one-use
invitation pinned to the isolated native owner's independently checked identity.
The native owner approved only observation. Real encrypted inspection succeeded
at 22:57:16 UTC, reporting logical task `0c31aaef63c14832bc154062800b6c79`,
generation 1, no native transcript and export disabled. An explicit preview was
correctly refused because export was not approved. Stopping and restarting only
the connector with the same credential then passed another native inspection at
23:02:38 UTC. This is a connector reconnect test, not provider pause/resume.

Manual prepare/claim renewal then produced incarnation
`8a7c6b062e2d30ac486ab07f847900ab`, generation 2 under the same logical task.
Supersession stopped the prior listener without a manual kill. The native owner
independently approved the fresh key for observation only and inspected it
successfully at 23:05:11 UTC. Both generations were revoked. From Codex, one
authenticated mailbox request using the second credential returned HTTP 403
with CONNECT 200 and normal TLS verification. The old staging helper nevertheless
kept retrying silently until explicitly stopped during cleanup. This exposed a
product defect: cloud connectors now terminate with a typed authorization cause
after HTTP 401/403, while native owners retain their supervised recovery behavior.
Regression tests cover successful initial polling followed by each refusal and
require termination without retrying the refused credential.

Both providers removed their test-created incarnation keys, credentials, pointers
and generated repository files after the test. Claude had generated a second
disconnected incarnation on a later callback; its event source was not captured,
so this is not counted as verified provider resume. Both disconnected directories
were removed after checking their exact pointers. No temporary allow rule ever
existed. Both repositories remained clean at their original commit, and native
conversation records and installed signed helpers were preserved. The isolated
native owner's process was stopped, its peer grants revoked, and its routing
credential self-revoked with HTTP 200; subsequent mailbox access returned 403.

The revocation defect was then retested in the same real Codex environment using
the signed `0.5.0-staging.20261009.2583f29` helper. A fresh observation-only
incarnation `978a5e42cf16fd731ae6447890bf5ad1` passed native encrypted inspection at
23:13:15 UTC. After owner revocation, the helper exited automatically with status
1 and `relay authorization refused or revoked`, without a kill signal and well
before its 23:30:31 UTC lease expiry. The provider confirmed that both the serve
process and supervisor had stopped. Test-created keys, credentials, pointers and
temporary files were removed; the native owner's renewed credential was also
revoked (HTTP 200 followed by mailbox HTTP 403) and its runtime stopped.

Both providers used the pinned signed helper above. Their real environment UIs
did not expose pause/rebuild controls during this test. Neither a manually
restarted helper nor repository refresh is recorded as provider VM lifecycle.
Fresh-start publication, actual provider pause/rebuild and Claude transcript
export remain separate gates.

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

Observed two-day orphan expiry and sustained production load/cost behavior
remain unqualified. Finite thirty-minute Mailbox hibernation billing attribution
and the existing billing alert are verified above. Provider default startup,
pause/resume/rebuild and transcript visibility remain separate gates. The PR stays
draft and the release stays unpublished.

Sources: [Worker limits](https://developers.cloudflare.com/workers/platform/limits/),
[SQLite Durable Objects on Free](https://developers.cloudflare.com/durable-objects/platform/pricing/),
[R2 lifecycle behavior](https://developers.cloudflare.com/r2/buckets/object-lifecycles/),
and [automatic Custom Domain certificates](https://developers.cloudflare.com/workers/configuration/routing/custom-domains/).

## October 9 sustained-load failure and correction

The first 100-logical-client workload ran from 09:00:18 UTC to 09:16:37 UTC
including cleanup. All clients received three authenticated observation rounds,
including reconnection after forced connection loss. The complete test **failed**
while claiming a cloud invitation. Its four spaces held 32/32/32/4 native devices;
cloud admission needed another slot in the first, already-full space. The Mailbox
correctly refused at its production 32-device ceiling, but Authorization converted
that refusal into HTTP 503, so the client retried until its nine-minute lease ended.

A regression using the actual Authorization/Mailbox handlers reproduced the 503.
Capacity now returns `409 capacity_exceeded`, with an actionable client error and
no automatic retry; ordinary gateway 429 throttling retains bounded retries.
The invitation remains pending and can be claimed after expiry cleanup releases
capacity. All 43 Worker/download contracts and the relay race suite pass.
The sustained workload now distributes 100 native clients as 25 per space, leaving
room for cloud claims without increasing production quotas. Each renewal/claim
phase logs its start/completion; cleanup registration precedes ticket parsing.
A full corrected hosted rerun is required before this gate closes.

The failed run revoked its test credentials, restored the relay to paused, and
verified HTTP 503. Its partial delivery results do not qualify sustained load,
cloud renewal, or whole-service cost.

### Absolute lease expiry and successful rerun

The capacity-corrected run passed its first three delivery rounds but failed
claim validation with `relay returned a widened cloud admission credential`.
Authorization calculated a relative TTL from the signed absolute lease, then
Mailbox added that TTL to its later clock, extending expiry by cross-object
latency. The Go client correctly refused that credential. Authorization now
forwards the absolute expiry, and Mailbox clamps issuance and replay to that
bound. A real-handler test advances the Mailbox clock for claim and retry: it
fails before the fix and passes afterward. All 44 Worker/download contracts pass.

The corrected hosted race test passed in **753.65 seconds**, starting
2026-10-09 09:43:50 UTC. All 100 clients in four spaces received rounds at
09:44:27, 09:49:59, 09:50:38 and 09:56:13. This includes two five-minute steady
intervals, forced disconnect/reconnect, native routing renewal, cloud claim and
revocation in every space, and ciphertext drain. Client counters report 3,117
HTTP requests, 301 WebSocket handshakes and zero rate-limited responses.
Fixture credential cleanup completed; paused deployment
`ff82b644-9a45-40ce-8a57-e4ed167ca4b3` returned HTTP 503 at 09:56:54 UTC.
The tested unpaused deployment was `dc7e7202-f31a-4b75-a893-b68d2e5c7a79`.

GraphQL metrics covering 09:43:00 through 09:58:00 UTC include setup and cleanup:

| Component | Observed metric |
| --- | --- |
| Worker | 3,229 estimated requests, 0 execution errors; 4,092,066 microseconds aggregate CPU |
| Mailbox | 13.691949056 GB-seconds; 47,287 SQLite reads / 7,122 writes |
| Authorization | 0.20246336 GB-seconds; 18 SQLite reads / 13 writes |
| Both object classes | 0 CPU-limit, memory-limit or fatal internal errors |
| R2 operations | 433 successful puts, 349 gets, 422 deletes, 4 bucket heads; 24 delete responses were HTTP 404 |

Adaptive dataset counts are estimates and can settle later; they are not exact
client counters or an invoice. Invocation datasets classify 303 Mailbox stream
closures as disconnect errors during deliberate reconnect/drain. R2 delete 404s
are recorded separately from successful cleanup; the functional drain assertions
passed. No R2 storage samples were returned for this short window, so it does not
prove zero storage usage. Full storage attribution, account-level billing/rounding
and the elapsed orphan-retention canary remain required before declaring the
whole-service cost/retention gate complete. Worker elapsed request duration and
long-lived stream wall time are not Durable Object billed GB-seconds.

Metrics are retained locally in `/tmp/hopsesh-load-success-window-final.json`;
the query is `/tmp/hopsesh-load-success-window.graphql`. The test transcript is
`/tmp/hopsesh-hosted-load-expiry-fixed.log`. These files contain no credentials.

The later SQL-storage query uses `durableObjectsSqlStorageGroups` (the SQL
dataset, separate from the older storage dataset). Its 09:15 UTC samples report
131,072 bytes for Mailbox and 16,384 for Authorization; at 10:15 UTC they report
417,792 and 16,384 bytes respectively. These bracket the load but do not measure
its peak or isolate new storage from existing staging databases. A later R2
readback includes a 09:40 UTC sample with two objects, 1,352 payload bytes and
68 metadata bytes, followed by 09:50 UTC with one 256-byte object, 43 metadata
bytes and zero uploads. These are sampled storage evidence, not the peak or a
post-completion sample (the workload ended at 09:56:13). The query and results
are retained in `/tmp/hopsesh-load-storage-final.graphql`, `.json` and
`/tmp/hopsesh-load-storage-refreshed.json`. Missing or delayed samples
remain unknown, not zero. Billing also applies account-level included usage and
rounding, so multiplying this short test's counts by unit prices is not an
invoice; see [R2 pricing](https://developers.cloudflare.com/r2/pricing/).

A further October 9 readback adds a **post-cleanup R2 sample at 10:40 UTC**:
one object, 256 payload bytes, 43 metadata bytes and zero uploads. The SQL samples
remain 417,792 Mailbox bytes and 16,384 Authorization bytes at 10:15 UTC.
The result is `/tmp/hopsesh-load-storage-post-cleanup.json`. The residual R2 size
is consistent with the retained orphan canary, but aggregate analytics do not
identify its key or prove its expiration. No manual deletion was used to obtain
this result. The short-window post-cleanup storage observation is now available;
peak/billing-period storage and actual orphan expiration remain separate.

Cost interpretation was rechecked against current primary pricing pages. The
Worker's request duration is not billed CPU; use its measured CPU milliseconds.
For Durable Objects, the periodic dataset reports 13.894412416 total GB-seconds
across both classes; do not derive duration charges from overlapping HTTP/stream
wall times. Its invocation dataset has 3,251 HTTP, 99 alarm and 301 hibernation
events. Do not classify all 301 hibernation events as incoming billable messages:
the periodic dataset reports zero inbound messages and 701 outbound messages,
and outgoing messages are free. Billing applies its own event classification,
included allocation and rounding. See
[Workers pricing](https://developers.cloudflare.com/workers/platform/pricing/)
and [Durable Objects pricing](https://developers.cloudflare.com/durable-objects/platform/pricing/).

R2 successful puts are Class A; gets and bucket heads are Class B; deletes are
free operations. SQL reads, writes and stored bytes use the SQLite dimensions,
not the older key-value storage rates. Neither sparse storage samples nor the
100-client burst should be linearly extrapolated into a monthly invoice. Account
usage, included allocations, billing-unit rounding, daily storage peaks and
download-service storage must be included in the final operating-cost estimate.

## Codex prepared-filesystem publication correction

A fresh task from the earlier published configuration had neither the versioned
helper nor supplied Start skill evidence. Reading an editable draft inherited
from that publication confirmed both fields were present (2,520 and 707
characters). The missing helper was not an unsaved script: setup had not executed
it before publication. Current OpenAI documentation states that publishing
captures the prepared filesystem, and repository refresh does not rerun setup.
The generated guidance now requires executing the verified installer in the
editable setup, checking its version, publishing, then qualifying a new task.
No incarnation is created in reusable setup. A new setup successfully installed
and verified `0.5.0-staging.20261009.f134df2`; fresh-task inheritance and Start skill
execution were tested separately: the new task inherited the verified helper,
reported no supplied Start skill, and created no public incarnation. No manual
preparation or connector was used to turn that negative startup result into a
pass. Prepared installation inheritance is qualified; Start skill delivery and
real pause/rebuild remain open. See
[Codex cloud environments](https://learn.chatgpt.com/docs/environments/cloud-environments).

The October 9 read-only setup follow-up still reads a nonempty 707-character
`start_skill` from its saved draft. The supported field accepts agent instructions
as a string, with no documented filename/front matter or explicit invocation
requirement. However, that setup's read interface exposes a draft whose base
predates the current published configuration; it cannot prove the published
field contents. The fresh-task absence remains a failed delivery qualification,
with cause unverified. No settings, permissions or publication were changed by
this diagnostic; no preparation keys or connector were created.

A subsequent fresh Settings → Edit flow created a draft from the current
publication. Its draft base and attached runtime source configuration versions
match; it retains the 2,520-character installer and 707-character Start skill,
and the executable helper is present (18,219,168 bytes, mode 755). This removes
the stale-draft ambiguity about configured content. The actual task separately
checked its supported executor/cloud skill catalogs and environment status:
none expose the configured Start instructions or a historical delivery audit.
The task's earlier categorical absence claim is therefore narrowed to no
accessible startup instructions or observed invocation. Delivery versus lookup
remains unresolved; automatic startup still does not pass. Both diagnostics were
read-only, with no preparation, keys, connections or permission changes.

A separate minimal delivery experiment prepended a nonsecret local-file marker
instruction to the Start skill, reviewed it through the setup UI and published
the prepared environment. A new task received an ordinary README-summary request
with no prohibition on startup. Its source version was
`7633e54b-2d4d-4006-9163-ec513f170c7b~cecfgver_6ac8c49aeca08197966b0a04df2cd3eb`.
The helper was inherited, but the marker was absent; the task reported no Start
instructions in initial context and no execution. This removes task-ID binding
as the sole explanation of that experiment's failure. It does not locate the
provider's internal delivery fault or qualify automatic startup. Screenshot:
`/tmp/hopsesh-start-skill-proof-result.png`. A fresh supported Edit workflow
restored the original 707-character Start skill and published it; the diagnostic
prefix is no longer in the published configuration.

Official [repository guidance](https://learn.chatgpt.com/docs/agent-configuration/agents-md)
is a separate documented instruction mechanism. A nonsecret `AGENTS.md` marker
probe was committed as `b16380d` in the disposable `hopsesh-cloud-smoke` repository
to test that route independently. It authorizes only a local diagnostic file,
skips setup/local/Claude execution, and grants no connector or transcript access.
The first task inherited the old `b80bcbd` checkout, making its negative result
invalid for this route. Setup was explicitly fast-forwarded to `b16380d` and
republished. Fresh task `01a1204d-203c-7500-9543-602d1938f07c` then read the root
instructions from disk and created the diagnostic before answering its ordinary
README request. Read-only verification found HEAD `b16380d`, only untracked
`.hopsesh/`, and the expected marker/reason with timestamp
`2026-10-09T10:55:28.455991Z`. No proof was repaired during verification.
The instructions were read after initial runtime checks, not supplied before
the first action. The task found no documented current task ID: only environment
and configuration IDs. This qualifies repository-instruction discovery in that
task, not automatic identity binding or connector startup. The generated setup
now includes reviewed, ownership-checked repository guidance while retaining an
explicit unsupported state when the actual task identity is unavailable.

The generated setup was then installed through the actual CLI into the disposable
repository and committed as `85876c9`, replacing the diagnostic marker probe.
Fresh editable setup fast-forwarded to that exact commit, retained the verified
`0.5.0-staging.20261009.f134df2` helper, saved the generated 1,130-byte Start skill
and published without creating keys. Task `01a1205b-2d3c-7339-aa04-a5b26f3e4050`
read the checked-in instructions during an ordinary README request and reported
unsupported identity binding without preparing an incarnation. After its exact
current URL was supplied explicitly, passive `current` found no helper state;
preparation created incarnation `a46d6209717bc7ad37e4200e8f5d3cee`, bound to that
task, with source `manual`, expiry `2026-10-09T12:12:08.880072089Z` and no routing
credential, approved peer or transcript-export grant. The next ordinary message
retained the same ID and expiry and reported disconnected/awaiting authorization.
Screenshots: `/tmp/hopsesh-generated-startup-published.png`,
`/tmp/hopsesh-generated-startup-missing-identity.png` and
`/tmp/hopsesh-generated-startup-continuity.png`. Cleanup verified that no connector
was running, removed only that incarnation and its exact current-slot pointer,
and confirmed passive `current` could no longer find it. The repository remained
clean at `85876c9`; no network credentials were issued or needed revocation.
Cleanup proof: `/tmp/hopsesh-generated-startup-cleanup.png`.
This qualifies instruction discovery, missing-ID
refusal, explicitly bound preparation and same-task continuity; it does not claim
automatic ID discovery, VM pause/rebuild or a running connector.

A read-only lifecycle follow-up in that task found only
`cloud_environment.environment_status({})` (current readiness/configuration) and
`wait_for_environment({environment_id})` (waiting for an already-starting instance)
in its supported runtime interfaces. The reported snapshot had desired/observed
phase `running`, current observations and connected transport; its two spec
revisions were 6. No pause/resume/restart/rebuild mutation or historical lifecycle
event was exposed in that task's catalog. Ordinary follow-up therefore remains
continuation evidence only. This does not establish that no such provider
capability exists anywhere, nor waive the real pause/rebuild qualification gate.
