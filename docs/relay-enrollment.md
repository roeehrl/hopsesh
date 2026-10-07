# Relay enrollment

Status: implemented clients and handler contracts; hosted qualification pending.
The experimental relay has its own Worker, SQLite authorization/mailbox objects,
private ciphertext R2 bucket and domains. It shares no product-site or Souvenir
authorization state. Enrollment authorizes delivery only. Each endpoint still
independently approves peer fingerprints and send/bring/receive/share permissions.

## Operator configuration

Provision a Cloudflare Access application with an explicit allowed-user policy.
Protect `/device`, `/device.js`, `/device.css`, `/v1/device/review` and
`/v1/device/approve`. The public machine endpoints `/v1/device/code`,
`/v1/device/token`, `/v1/authorization/request` and `/v1/authorization/token`
must reach the Worker without an Access browser redirect. Do not put a broad
`/v1/device/*` policy over them. Existing authenticated mailbox routes likewise
use their own scoped routing credentials rather than Access browser cookies.

Set `ACCESS_TEAM_DOMAIN` to the team's exact `https://<team>.cloudflareaccess.com`
issuer, and `ACCESS_AUDIENCE` to this application's audience. Supply
`ENROLLMENT_ADMIN` with `wrangler secret put`; never place it in the checked-in
configuration, a cloud environment or an app bundle. It must contain at least
32 bytes of high-entropy material. Review the two rate-limit namespace IDs for
account-wide uniqueness before provisioning. The checked-in configuration
requires both rate limit bindings and the SQLite Authorization migration.

Unset Access/rate-limit bindings fail closed with HTTP 503. Access signature,
issuer, audience, expiry, subject and app type are verified in the Worker even
when the origin is behind Access. Raw email, client-supplied principal headers
and namespace claims never establish an account. An opaque issuer/subject hash
selects the user's mailbox space. Different users have separate spaces.

The authorization object is a single fixed namespace. It holds at most 512
requests for ten minutes. Per-IP issuance and gateway limits precede paid-object
allocation. These bounds limit admission storage; hosted cost/abuse qualification
and operator spend controls remain required release gates.

## Desktop

Create the endpoint identity explicitly in Settings → Internet delivery, then
choose Connect in browser. Hopsesh opens the validated relay approval page in
the external browser. Review the machine and compare its full fingerprint with
Hopsesh before allowing delivery. Only approve a request you started yourself.

The desktop proves possession of its signing key and binds the origin, native
client, scope, callback, S256 challenge and state to that proof. Only literal
loopback HTTP callbacks on `/callback` are accepted. The listener exists for this
login only; state and duplicate callback parameters are checked. The verifier,
authorization code and token remain in the backend. Canceling or quitting closes
the listener. No polling runs while the browser is open.

## Headless CLI

Run `hopsesh relay init`, then `hopsesh relay login`. Open the displayed URL on a
machine with a browser, enter the code and compare the full displayed fingerprint.
The code expires in ten minutes. CLI polling starts at five seconds, follows
`slow_down` and backs off on transient network/service failures. Denial, expiry
and cancellation end that login; Hopsesh does not start another login silently.

`--origin` selects a private verified HTTPS relay. `--ca-file` supplies an absolute
bounded PEM trust root when needed; certificate hostname validation remains on.
The operator-issued scoped JSON path, `relay connect`, remains available for
controlled provisioning. Neither path grants peer access by itself.

## Credentials and recovery

Routing credentials last at most 24 hours and are saved only in private local
state. The current endpoint ID must still match before publication. Expired
credentials require explicit renewal; no personal refresh credential is issued
to a cloud session. Re-enrollment rotates the existing routing credential, while
local peer approvals remain independently pinned.

Within the short authorization lease, retrying a lost token response retrieves
the same credential. PKCE exchange additionally requires the original verifier
and exact callback URI; its authorization code expires after one minute. No
second mailbox enrollment occurs on a completed retry. Native transfer operations
have their separate encrypted replay and durable receipt protocol.

Qualification covers the deployed handler's signature/Access/quota/expiry/CSRF/
PKCE/retry contracts, Go client failures and cancellation, real CLI and native
clients over verified HTTPS to the handlers, and Chromium/WebKit settings. The
local browser fixture substitutes for Access only in the cross-language test;
actual Access/provider deployment and hosted login remain external gates.

Sources: [device authorization](https://www.rfc-editor.org/rfc/rfc8628),
[native OAuth](https://www.rfc-editor.org/rfc/rfc8252.html),
[PKCE](https://www.rfc-editor.org/rfc/rfc7636.html),
[Access JWT verification](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/validating-json/),
and [Workers rate limits](https://developers.cloudflare.com/workers/runtime-apis/bindings/rate-limit/).
