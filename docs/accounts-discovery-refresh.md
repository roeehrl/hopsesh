# Account identity and stable discovery

This work extends the progressive session catalog. Account detection is a separate
read-only enrichment step: a slow identity probe cannot hide sessions already found.

## Research and observed failures

- [Claude authentication](https://code.claude.com/docs/en/authentication) documents
  isolated configuration roots and macOS Keychain access differences over SSH.
  [CLI reference](https://code.claude.com/docs/en/cli-reference) defines JSON auth
  status, exit status, and the reported configuration directory. Always check the
  selected root; never read credentials or automatically unlock a keychain.
- [Codex app-server](https://learn.chatgpt.com/docs/app-server) defines initialization,
  `account/read`, login completion and account-change notifications. Its public
  ChatGPT response supplies email and plan, not a stable verified account ID.
  Parse JSON-RPC IDs regardless of JSON spacing/property order and wait for the
  initialize reply before sending requests.
- [Tailscale CLI](https://tailscale.com/docs/reference/tailscale-cli?tab=macos)
  requires `TAILSCALE_BE_CLI=1` in scripts using the macOS app executable. Failed
  discovery must be visible, independently of SSH aliases. MagicDNS is optional;
  peers with an IP remain discoverable without a DNS name.
- [WCAG status messages](https://www.w3.org/WAI/WCAG22/Understanding/status-messages.html)
  require progress feedback without changing focus. [NN/g filter guidance](https://www.nngroup.com/articles/applying-filters/)
  describes how repeated result flicker distracts users. Preserve the selected
  inspector, its conversation and disclosure states during unrelated updates.

Read-only implementation checks found both local logins and a running Tailscale
peer inventory. A configured remote SSH endpoint timed out; remote behavior was
verified with fixtures instead, and no remote credentials were inspected.
Code inspection found public email deliberately removed from remote observations,
no account-login completion refresh, unchecked discovery errors, and full inspector
replacement for every list/presence update.

## Behavior

Account means a pinned runtime profile. Show the current vendor-reported email,
profile name, agent, machine, last check and observation source. Distinguish not
checked, signed out, failed check and signed in. Explain identity limitations in
profile details: email or organization is not proof of historical session ownership.
Group by profile ID, with readable email/agent/machine labels, never merge roots
because their emails match. Session chips say which profile stores the session.
Account grouping includes each retained native copy after a move; the ordinary
repository list continues to select a logical conversation representative.

Import public owner-machine observations over the already-authorized SSH transport.
Keep local labels/tags local. Prefer recent owner observations over SSH login probes
(which may lack Keychain access); stale identities must be labeled as last known.
A failed probe preserves the last known account with an explicit failure message.
Login completion automatically checks that profile. While Accounts is visible,
stale profiles are checked one at a time, with a five-minute retry floor. Missing
vendor CLIs and failed connections show specific errors while retaining last known
metadata; identity failures do not prevent successful session enumeration. Refresh controls target one
machine; they do not trigger scans of every configured host on returning to Sessions.

Keep session previews mounted when source updates do not alter the selected
conversation. Keep known repository/family metadata provisional during enrichment
so a refresh does not regroup saved rows into a pending bucket and back again.
Update header/actions without replacing conversation DOM when metadata
changes. Preserve list focus, selection, scroll and disclosure states. Coalesce
notifications; do not interrupt a pointer interaction, menu or review.

Local file watching plus reconciliation covers native changes outside Hopsesh.
The released peer CLI has no change subscription; the 0.5 companion observation
engine already owns that next-release work. Use the existing authenticated transport
and versioned observation capability, with periodic scans as a visible fallback;
no unauthenticated listener or credential copying is introduced by this change.

## Verification

Adapter fixtures cover signed out, missing identity fields, wrong reported root,
JSON-RPC ordering/spacing and missing/error replies. Profile tests cover public
remote identity, binding rotation, stale observations and failed probes. A native
terminal fixture verifies automatic account refresh after the vendor login exits. Discovery
fixtures cover GUI CLI mode, DNS-less peers and failed/not-running Tailscale.
Browser checks cover account grouping/chips, independent
source scans and preserving an expanded selected conversation across updates.
The shared scenario matrix must run on macOS, Linux and Windows.

Remote/cloud reconciliation runs every ten minutes while Sessions is active and on
returning after five minutes, independently of the local watcher update clock.
Machines refreshes healthy rows every five minutes while visible. Local summaries
appear before git enrichment; early actions open a freshly validated review, and
checkout pickers resolve known local session directories on demand.
