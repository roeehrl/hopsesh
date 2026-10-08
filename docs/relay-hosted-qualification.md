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
with `paused`. Browser enrollment also remains unavailable without Access config.

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

## Outstanding hosted gates

A fresh install-only task in the selected `hopsesh-cloud-smoke` Codex environment
retried the now-live signed staging download on 2026-10-08. Its configured proxy
was unreachable on port 8080: the bootstrap exited 1, the absent binary exited
127, and the separate 20-second capabilities probe exited 7 with HTTP 000.
The task reported Linux x86_64 and no current task ID in its provided context.
No network/approval settings, repository files or secrets were changed, and no
connector was enrolled. This is a provider connectivity failure before signature
verification, not a successful installation or lifecycle test.

The user completed Zero Trust Free activation in Chrome; the dashboard confirms
the subscription is active. A staging Access application is prepared for the
five enrollment browser paths, restricted to `roeehrl@gmail.com` with a 30-minute
session. Applying that access grant awaits action-time confirmation. The Worker
still lacks the application's audience, and browser enrollment remains closed.
Real PKCE/headless browser approvals, billing alerts and measured long-duration
hibernation/cost behavior remain unqualified. Provider default startup,
pause/resume/rebuild and transcript visibility remain separate gates. The PR stays
draft and the release stays unpublished.

Sources: [Worker limits](https://developers.cloudflare.com/workers/platform/limits/),
[SQLite Durable Objects on Free](https://developers.cloudflare.com/durable-objects/platform/pricing/),
[R2 lifecycle behavior](https://developers.cloudflare.com/r2/buckets/object-lifecycles/),
and [automatic Custom Domain certificates](https://developers.cloudflare.com/workers/configuration/routing/custom-domains/).
