# Account profiles: lineage integration contract

Status: approved and implemented with `lineage/5` and peer protocol 5, 2026-10-06.
Companion: [account design](accounts-design.md); [current interfaces and limitations](accounts.md).

## Identity boundary

An endpoint identifies a persistent Hopsesh installation and OS user, not a hostname alias.
Within it, a runtime account profile identifies an agent-specific, pinned state root. A login
binding identifies the account/provider/workspace association observed for that profile.
Refreshing credentials for a verified unchanged association does not create a new binding.
An observed account change does. Unknown identity never proves continuity.

| Identifier | Lifetime and use |
| --- | --- |
| EndpointID | Persistent installation; labels and SSH aliases can change |
| RuntimeProfileID | Opaque ID for one registered agent state root; renaming its label preserves the ID |
| AuthBindingID | Opaque local-issued ID for an observed login association; includes confidence, not credentials |
| ReplicaID | Immutable ID for a native conversation copy in a particular endpoint/profile/binding scope |
| Family / Line / Revision | Existing lineage concepts, preserved through account changes and projection |
| OperationID | Existing retry identity; now also commits to the selected profile and binding |

The native lookup key must include `(EndpointID, AgentID, RuntimeProfileID, NativeSessionID)`.
Historical replica provenance additionally includes AuthBindingID. A re-login does not rewrite
old replica provenance or retroactively claim ownership of every transcript in that directory.
If subsequent native work crosses an authentication boundary, record a new replica segment
linked to the existing lineage; do not mutate its previous association.

Personal/Work are example labels, not account types. Support an arbitrary profile collection;
renaming, tags or duplicate labels never change replica selection, receipts or trip counters.
Tags are optional many-to-many organization metadata, not account identity or transfer permission.
Discovery matches registered profile/root scope and preserves user labels/tags; email is not a key.

Do not derive identity from email, organization alone, display name, path spelling, session
title, or the current account probe of a different process. Provider IDs are typed by their
namespace. Native `creator_account_id` and a hash of email cannot be compared.
Keep vendor account metadata local by default; portable sidecars carry opaque binding IDs
and scoped provenance. A receiving peer resolves its own configured profile and authorization.

## Required boundaries before the next format is frozen

1. Give replicas explicit runtime-profile and login-binding references. Keep machine identity
   distinct; do not overload a machine label or concatenate an untyped account string into it.
2. Include these references in target selection, locks, receipts, expected-state fingerprints,
   operation journals, movement statuses, scan caches, grouped inventory, terminal tickets and peer
   requests. A sibling profile with the same native session ID must remain independently selectable.
3. Keep revisions' logical identities across sanitization and agent conversion. Receipts describe
   delivered representation and losses. Dropped reasoning does not become a new authored revision.
4. Cross-account return selects the exact original replica, line and binding, then computes missing
   authored revisions from validated provenance. It must not select “the Claude copy on this Mac.”
5. Protected native replay is permitted only with a module-verified identity/compatibility
   contract. Separately, a module may support appending ordinary portable text to an exact
   original in the selected profile, with verified native anchors, missing-revision coverage,
   current root/login revalidation and no active writer. This preserves old protected records
   in place; it never imports source-private state or infers cross-provider account equality.
   A binding observation starts a new segment without reassigning historical authorship.
6. Revalidate profile generation, observed binding, root and native heads at plan, apply and launch.
   Reject observed changes. Where the vendor cannot expose a distinction, explicitly mark it
   unverified and avoid operations requiring that distinction; a timestamp is not proof.
7. Derive trips from committed route events. A stop is endpoint + agent + runtime profile + binding.
   Keep machine-only travel a separate view. Account A → B → A on one Mac is one account round trip
   and zero machine round trips. Forks start independent counters, retaining parent ancestry.
8. Presence is an optional observation referencing these same IDs. It neither advances lineage
   receipts nor proves the absence of another writer. Daemon lease expiry never authorizes overwrite.

## Compatibility and sequencing

The currently observed in-progress lineage implementation calls the conversation branch `Line`
and has explicit `Replica`, `State`, `Hop` and projection records. Use those concepts; do not
introduce a second competing graph. Account scope is included in format 5 and peer protocol 5. Do not silently label unknown older
records as the default account. The project currently does not require backward compatibility.

Acceptance contract: per-profile collision tests; A/B/A/B/A; three-account and cross-agent routes;
same email in two workspaces; two users in one organization; re-login between plan and apply;
source history predating its current login; native fork discovery; divergence; interrupted delivery;
receipt replay; partial history after compaction; no sibling mutation; no protected-state replay
under an unverified binding. These extend the lineage matrix rather than creating a separate runner.
