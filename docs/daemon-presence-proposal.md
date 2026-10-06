# Optional daemon and cross-machine presence proposal

**Status:** research/design only; explicitly after 0.4.0; no implementation approval.
**Research date:** 2026-10-06.
**Repository inspected:** `/Users/tnt/git/hopsesh-redesign`, HEAD `be924e9075193521358bce8d372cca18b77e1235`, with concurrent, uncommitted lineage work. References below describe the inspected working files, not a released binary or an immutable snapshot of that work.
**Write scope:** this new document only. No implementation, lineage plan, service registration, configuration, credentials, peer connections, commits, or thread messages were changed or used to exercise the design. Local Codex inspection was limited to version/help commands; no vendor daemon was contacted or started.

Evidence labels used throughout:

- **Repo:** directly inspected source; behavior was not exercised in this research.
- **Official:** primary documentation fetched during this research, with a supporting URL adjacent to the claim.
- **Proposal:** a Hopsesh design decision, including every new command, field, threshold, and API below.
- **Validate:** unresolved behavior that requires a later, separately authorized experiment or implementation test.

## 1. Recommendation and release boundary

**Proposal:** retain a fully functional no-daemon mode and offer an explicitly enabled, unprivileged, per-user **presence broker**. It collects observations once, shares them among GUI/CLI/TUI processes, and optionally exchanges a minimal presence view with approved SSH peers. Reuse vendor status where a passive, profile-scoped interface is available. Do not build another agent execution server or another vendor remote-control service.

The user benefit is: “This related session is open on Studio under Work Claude; checked 8 seconds ago,” even when the Hopsesh GUI there is closed. It is not proof of exclusive ownership, permission to stop that session, or a guarantee that every machine has been checked.

Ship in stages after 0.4.0: first the observation/identity contract and safe in-process collector, then an optional local broker, then explicitly enabled SSH observation. Persistent background monitoring must earn its cost through measurements. If vendor integration and the in-process collector meet demand, defer the broker rather than making it a prerequisite for Hopsesh.

The broker must never own PTYs or agent processes. Existing launch, focus, resume, transfer, remote-control, approval, and termination flows remain separate, user-invoked control paths. Disabling or upgrading the broker must not stop a Claude/Codex session or a vendor daemon.

## 2. Verified repository evidence and consequences

| Repo evidence | Consequence for this proposal |
| --- | --- |
| [Local classifier](/Users/tnt/git/hopsesh-redesign/internal/core/presence/presence.go) walks process parents to a known terminal/editor/tmux/SSH/vendor app. It bounds traversal and handles cycles and truncated names. `Proc` contains PID, PPID, name, but no process creation identity. Platform snapshots use macOS sysctl, Linux `/proc`, and Windows ToolHelp. | Reuse location classification as best-effort evidence. A process name or ancestor is neither a session identity nor evidence of work. Add creation identity before relying on durable process observations. |
| [GUI Presence](/Users/tnt/git/hopsesh-redesign/internal/ui/gui/inspect.go:280) calls [LiveHere](/Users/tnt/git/hopsesh-redesign/internal/app/preview.go:104) only for sessions already in the local inventory, with a 10-second deadline. Other machines arrive through inventory scans. Its DTO has a live boolean and status, but no observation age/coverage. | Separate discovery from refresh; refreshing known sessions alone misses newly created sessions. Add explicit freshness and failure coverage without treating a missing DTO entry as ended. |
| [Sessions polling](/Users/tnt/git/hopsesh-redesign/internal/ui/gui/assets/sessions.js:70) refreshes local presence every 5 seconds on focused Sessions, 30 seconds while visible otherwise, and pauses while hidden. Local inventory refreshes about every minute; remote refreshes are much less frequent. [TUI](/Users/tnt/git/hopsesh-redesign/internal/ui/tui/tui.go:135) scans on entry and exposes refresh. [CLI list](/Users/tnt/git/hopsesh-redesign/internal/ui/cli/ls.go:17) uses an inventory snapshot. | GUI/CLI/TUI do not yet share a continuous observation source. Preserve one-shot CLI behavior; add an explicit watch mode rather than starting background work whenever a CLI runs. |
| [Claude registry](/Users/tnt/git/hopsesh-redesign/agents/claude/live.go:32) reads `<config>/sessions/<pid>.json`, deliberately skips non-JSON key files, verifies numeric PIDs exist, and can report multiple processes for one session. Missing registry entries result in `Ended`; individual read failures can be skipped. The source describes this as an internal vendor format. | Preserve multiple runtimes. Registry absence or partial reads cannot establish global termination. Numeric PID existence is weaker than matching the process instance that wrote a registry entry. Do not read sibling secret files. |
| [Codex Live](/Users/tnt/git/hopsesh-redesign/agents/codex/codex.go:535) tests writer locks. A held lock starts with status `idle`, changed to `working` only if the [bounded transcript tail](/Users/tnt/git/hopsesh-redesign/agents/codex/codex.go:634) indicates an open turn. Errors can leave that initial idle status. Free locks become `Ended`. [Windows local lock probing](/Users/tnt/git/hopsesh-redesign/internal/core/host/local_windows.go) and [remote Windows probing](/Users/tnt/git/hopsesh-redesign/internal/core/host/remote.go:362) return unknown. | Never promote legacy `idle` into an authoritative claim. An absent start event outside the tail, a read error, a format change, or an unsupported lock probe means activity unknown. Prefer verified vendor status for supported Codex versions. |
| [Scan](/Users/tnt/git/hopsesh-redesign/internal/app/scan.go:149) calls [adoptWaiting](/Users/tnt/git/hopsesh-redesign/internal/app/fetch.go:265). [scanMachine](/Users/tnt/git/hopsesh-redesign/internal/app/scan.go:267) calls [applyPending](/Users/tnt/git/hopsesh-redesign/internal/app/pending.go), which can write marks and journals. [listedEntries](/Users/tnt/git/hopsesh-redesign/internal/app/scan.go:466) discards a detector error and substitutes unknown for missing states. | **Do not run `App.Scan` as an observe-only background loop.** Extract/audit a side-effect-free collector in a future change. Preserve per-profile/per-detector errors, and keep handoff adoption and deferred marks out of observation. |
| [Terminal launches](/Users/tnt/git/hopsesh-redesign/internal/app/terminal.go:339) record PID/TTY/session tickets, wait for the child, and record exit. [Store.Running](/Users/tnt/git/hopsesh-redesign/internal/core/termapp/ticket.go:188) also deletes invalid/dead records. [GUI Shutdown](/Users/tnt/git/hopsesh-redesign/internal/ui/gui/service.go:481) closes its PTYs. | Read ticket observations without invoking cleanup. An agent exiting is different from its shell/tab closing. Keep current GUI quit semantics explicit; the optional observer does not make GUI-owned terminals survive GUI quit. |
| [SessionKey](/Users/tnt/git/hopsesh-redesign/sdk/agent/agent.go:26) is agent + native ID. [GUI map](/Users/tnt/git/hopsesh-redesign/internal/ui/gui/inspect.go:272) prefixes a machine name. [Install](/Users/tnt/git/hopsesh-redesign/sdk/agent/agent.go:131) has roots, and [Account](/Users/tnt/git/hopsesh-redesign/sdk/agent/session.go:222) has an opaque key/label. | None of these alone supplies the forthcoming persistent profile and auth-binding identity. A new presence map must not reuse machine + native session, or even machine + agent + native session, as its complete key. |
| [Peer protocol](/Users/tnt/git/hopsesh-redesign/internal/core/peer/peer.go:25) is JSON over SSH stdio with hello/plan/apply/undo and exact protocol matching. At initial inspection its constant was **2**; concurrent work changed it to **3** by the final check, while `FirstVersion` remained **0.4.0** and the adjacent comment still described 2. [Receiver](/Users/tnt/git/hopsesh-redesign/internal/app/peer.go:79) gates all non-hello methods on `Peer.Receive`. | Preserve the transfer contract that actually ships; neither observed constant proves a released version. Observation needs a separate permission and protocol boundary; enabling presence must not enable receipt of sessions. Do not insert new watch notifications into the sequential transfer client. |
| [SSH base options](/Users/tnt/git/hopsesh-redesign/internal/core/transport/ssh.go:100) use strict host-key checking, disable agent/X11/port forwarding, and normally use batch authentication. Password-enabled connections can prompt. [StartPipe](/Users/tnt/git/hopsesh-redesign/internal/core/transport/ssh.go:300) owns an explicitly closed pipe whose lifetime outlives the initial context; it uses a dedicated connection rather than multiplexing the command pipe. | Reuse transport safety policy, but create a prompt-free observation path with owned cancellation and bounded cleanup. Audit connection lifetime and password callbacks; do not assume an expired request context closes an existing pipe. |

Existing classifier, registry, lock, terminal, and peer tests are useful fixtures, not validation of this unimplemented design. In particular, source comments asserting PID safety do not substitute for a process creation identity check.

## 3. Alternatives and tradeoffs

| Option | Benefits | Costs / limitations | Recommendation |
| --- | --- | --- | --- |
| On-demand scans, no daemon | Lowest installation burden; works in portable/locked-down environments; easy shutdown. | Repeated work across clients; remote snapshots age; no observation while all clients are closed. | Keep as baseline and fallback, with honest timestamps and a pure collector. |
| GUI-owned background worker | Simple reuse of existing lifetime and permissions. | Stops when GUI exits; does not coordinate independent CLI/TUI processes; risks confusing observer lifetime with PTY ownership. | Useful first integration, insufficient for closed-GUI presence. |
| Shared cache plus file lock, no resident process | Avoids repeated recent scans; no startup service. | Election/expiry complexity; no continuous refresh; cached data is not current; network filesystems introduce ambiguity. | Small optional optimization, not the presence authority. |
| Vendor status only | Best semantics for vendor-owned sessions; avoids duplicating execution/control. | Coverage differs across versions, profiles, separate server instances, agents, and disconnected clients. | Preferred evidence adapter where validated, not a cross-agent identity or coordination substitute. |
| Optional per-user broker | One collector and peer connection per scope; consistent views; survives GUI close; incremental watches. | Lifecycle/install support, memory and battery cost, IPC security, stale-cache and upgrade handling. | Recommended opt-in, gated by measured benefit and platform tests. |
| Hopsesh TCP/WebSocket service, LAN discovery, or central relay | Easier browser access or remote discovery in some deployments. | New authentication, certificates, revocation, firewall/NAT, privacy, and operating burden. | Out of initial scope. Existing SSH peers suffice. |

**Official:** SQLite WAL coordinates readers/writers on one host; it does not support multi-host access through a network filesystem. **Proposal:** avoid a shared cross-machine presence database. Initial broker state can be in memory with one small private snapshot; defer SQLite until measured history/query needs justify it. [SQLite WAL](https://www.sqlite.org/wal.html)

## 4. Vendor status reuse, including Codex 0.160.1

### Verified local CLI surface

**Local help evidence:** `/Users/tnt/.local/bin/codex --version` returned `codex-cli 0.160.1`. Help advertises `agents` for the shared local app-server daemon, `--remote` with `unix://`, explicit Unix paths, `ws://`, and `wss://`, and `--no-daemon`. Subcommand help advertises `app-server proxy --sock`, daemon management, and experimental `remote-control`. It also distinguishes a configuration `--profile` layer under `CODEX_HOME`. A vendor configuration profile is not automatically a Hopsesh persistent ProfileID or an account boundary. No command beyond help/version was run.

**Official:** App Server requires initialization. `thread/read` returns runtime status without loading or subscribing to a thread; `thread/loaded/list` lists in-memory threads. Statuses include `notLoaded`, `idle`, `systemError`, and `active` with flags. `thread/status/changed` reports changes. Subscribers affect unloading; after the last subscriber and activity disappear, the documented grace period is 30 minutes. `thread/list` normally scans/repairs metadata; `useStateDbOnly` avoids that repair path. Generated schemas are CLI-version-specific. [Codex App Server](https://learn.chatgpt.com/docs/app-server)

**Official:** Codex has vendor-managed remote-control/SSH workflows. Remote access uses the host's resources and permissions. App-server command documentation distinguishes a Unix socket transport from TCP WebSockets; accepting a Unix connection does not imply raw newline JSON on that socket. [Remote connections](https://learn.chatgpt.com/docs/remote-connections), [Developer commands](https://learn.chatgpt.com/docs/developer-commands)

**Proposal: Codex adapter.** Connect only to an already-running, explicitly associated profile endpoint using a verified passive interface. A CLI proxy is a candidate, not a proven safe implementation. Record endpoint/server incarnation separately from the local CLI version. Apply a narrow method allowlist; do not proxy the vendor control socket to Hopsesh peers.

Candidate initial sequence: initialize → list loaded IDs → read summaries with turns omitted → poll selected summaries. Use metadata-only inventory only with a tested no-repair option and explicit source coverage. Enable status notifications only after proving they are delivered without resuming/loading sessions or holding them open. Do not call `thread/resume` merely to subscribe. Do not open a new app-server to observe an existing server's work. A missing server means unavailable coverage, not no sessions.

A fresh vendor `idle` may be displayed as “Codex reports idle,” scoped to that runtime. `notLoaded` means no loaded instance on that endpoint; it does not prove absence on another endpoint or an exited OS process. A loaded ID alone does not mean a turn is working. These mappings are Hopsesh interpretation, not stronger vendor guarantees. Polling freshness and endpoint coverage remain visible.

**Validate:** the supported passive attachment path for 0.160.1; Unix WebSocket versus proxy framing; actual running-server version; notification audience; absence of metadata repair, auth refresh, or unload-lifetime changes; complete pagination/source filters; Windows endpoint behavior; multiple `CODEX_HOME` roots and servers; standalone `--no-daemon` sessions; and auth-binding changes while old threads remain loaded. Do not scrape the `agents` TUI or use doctor/login/account commands to discover credentials. Do not claim app-server sandbox `read-only` makes protocol control methods read-only.

**Official:** Claude exposes session/turn lifecycle hooks. `SessionEnd` has a bounded execution budget, so hook completion is not a universal delivery guarantee. Its Remote Control uses outbound HTTPS and syncs transcripts through Anthropic; it is a separate vendor feature with its own authentication and retention. [Claude hooks](https://code.claude.com/docs/en/hooks), [Claude Remote Control](https://code.claude.com/docs/en/remote-control)

**Proposal: Claude adapter.** Initially audit and reuse the repository's local registry adapter as lower-confidence evidence with explicit format/version coverage. If a supported passive vendor status interface becomes available, prefer it. Optional hooks can later improve event timing, but installing them changes vendor configuration and is a separate opt-in; hooks must be bounded, nonblocking, and metadata-only. Missing hooks or a killed process must age to unknown. No automatic Remote Control enablement, pairing, credential access, or transcript synchronization.

Vendor control remains vendor-owned. Hopsesh may later offer a user-invoked “Open in Codex/Claude” affordance using a verified profile route. Presence must not invoke vendor start/stop/update/remote-control commands, accept approvals, or send prompts.

## 5. Identity contract with forthcoming multi-account support

**Proposed integration requirement:** distinct persistent `ProfileID`, `AuthBindingID` with epoch, `AgentID`, and `MachineID`. The account proposal owns their definitions/provisioning; this document consumes that contract and does not redefine login or credential storage.

**Companion proposal alignment:** the concurrently authored [account/lineage contract](/Users/tnt/git/hopsesh-redesign/docs/account-lineage-contract.md:8) calls the installation-plus-OS-user scope `EndpointID` and the persistent profile `RuntimeProfileID`; the [account design](/Users/tnt/git/hopsesh-redesign/docs/accounts-design.md:80) pins an immutable registered root. Here `ProfileID` means that `RuntimeProfileID`, and `(MachineID, OSUserScope)` resolves through that `EndpointID`. Reuse those records rather than allocating competing IDs. The wire names below are illustrative aliases to align before implementation. A label rename preserves identity; root replacement/relocation follows the account layer's explicit registration/migration policy and invalidates cached routing. Presence must not silently migrate a pinned root.

**Proposal:** separate durable session identity, current observation binding, and runtime instance:

```text
SessionRef = (MachineID, OSUserScope, AgentID, ProfileID, NativeSessionID)
BindingRef = (AuthBindingID, AuthEpoch) | unresolved
PresenceKey = (SessionRef, BindingRef, RuntimeID)
ObservationOrigin = (ObserverID, ObserverEpoch, SourceInstanceID, ObservationSeq)
```

| Identifier | Required semantics |
| --- | --- |
| MachineID | Persistent opaque machine identity from the account/device contract; independent of hostname, IP, SSH alias, or displayed name. Bind observed identity to a verified SSH destination and remote OS-user scope. A restored/cloned identity conflict requires explicit repair, not an automatic merge. |
| OSUserScope | Separate local OS-account scope. Never combine observations across Unix users or Windows SIDs merely because the machine or vendor account matches. |
| AgentID | Provider/module identity, e.g. `claude` or `codex`, not the executable's mutable name. |
| ProfileID | Persistent identity of a configured profile; a label rename does not change it. Consume the account layer's pinned root and configuration generation. A root path, account email, current token, or the literal label “default” must not be its identity. |
| AuthBindingID + AuthEpoch | Identifies the profile's authentication binding and its generation. Store the binding, confidence, and generation supplied by account management. A verified unchanged credential refresh preserves the binding; a detected association change invalidates it. Logout or uncertain reauthentication withdraws confidence until the account layer resolves it. Exact epoch rules remain an account-contract decision; the observer must not manufacture account continuity from a timestamp. Tokens are never presence identifiers. |
| NativeSessionID | Vendor ID, unique only inside the full session scope. Equal IDs in two profiles must remain distinct. |
| RuntimeID | Distinguishes simultaneous opens, including multiple processes or multiple vendor server instances. A vendor daemon PID can serve many threads; PID is not RuntimeID. |
| ObserverEpoch / SourceInstanceID | Random incarnation IDs on Hopsesh restart and vendor/source replacement. Reject events from previous incarnations even if a PID, sequence number, or socket path repeats. |

`SessionRef` deliberately survives a login change; the observation's binding does not. A previously open runtime under binding epoch 7 must not silently become an epoch-8 runtime because the profile logged into another account. Retain its old binding when provable, otherwise mark binding unresolved. Rebinding invalidates current routing, subscriptions, and cached equality assertions; it does not kill the runtime or erase session history. When supplied, retain the lineage contract's `ReplicaID` as a relation/provenance reference, never equate it with an execution RuntimeID or advance its receipts.

Unknown bindings do not compare equal. Allocate a local provisional source scope when legacy data lacks profile identity; show “Profile unverified,” do not assign it to whichever profile is currently selected, and do not permit account-dependent control from that observation. A later identity resolution replaces/links the provisional observation explicitly, without rewriting unrelated historical observations.

Cross-machine profile labels are not equality evidence. Profile equivalence and account equality must come from the account feature's explicit, non-secret mapping. Do not invent a globally comparable email hash or export existing `Account.Key` wholesale; hashes can remain identifying and may expose account linkage.

Presence storage, caches, filters, watch subscriptions, deduplication, notification keys, and any future action preconditions must use the full scope. Identity must exist in the first wire version, even if the first rollout exposes only one profile per agent.

### Detecting “active elsewhere”

1. Resolve the selected local session's full identity and any **read-only** relationship information exposed by the lineage feature after it stabilizes. Presence neither writes manifests nor selects a lineage winner.
2. Match remote observations through an explicit copy/continuation relationship and approved profile scope. Machine name, title, repository path, account label, and coincidentally equal native IDs cannot create that relationship.
3. For a fresh positive runtime observation on another MachineID, show “Open on Studio · Work Claude · checked 8s ago.” Add “Working” or “Waiting for approval” only when the activity evidence supports it.
4. For a related but different agent/profile, say “Related session open…” rather than implying the same runtime. If identity/lineage is unresolved, list the observation without joining it to this session.
5. Show all simultaneous opens. Local and remote copies can both be active. Preserve partial/unreachable peer coverage alongside positive results; absence from an unobserved peer cannot cancel them.

On a future resume/bring action, use presence for a warning and offer a targeted refresh. Never silently stop a remote copy, migrate ownership, or auto-resume a stale one. Even a fresh absence result is a point-in-time observation; it cannot lock out an independent vendor client that starts immediately afterward.

## 6. Architecture and cross-process coordination

```text
GUI / CLI / TUI
   | authenticated local IPC, snapshot or watch
   v
optional per-user Hopsesh observation broker
   |-- pure local collector --> vendor status / registries / locks / process evidence
   |-- local adapters -------> profile + auth-binding metadata (no secrets)
   |-- selected peer worker -> SSH -> presence --stdio -> remote local collector/broker
   `-- bounded in-memory view + optional private last-known snapshot

Explicit user control --> existing plan/approval/action flow --> vendor or transfer API
                         (not through the observation protocol)
```

**Proposal:** one broker per OS user and Hopsesh state namespace, supervising only its own collectors and transport helpers. Profiles are scopes inside it, not separate services. Custom state directories get separate namespace IDs; clients must not silently attach to the default namespace. Independent namespaces do not claim global mutual exclusion.

Use private Unix-domain sockets on macOS/Linux and a local named pipe on Windows. No Hopsesh TCP listener, including loopback, in v1. A client checks namespace, peer credentials, protocol, and incarnation before using a snapshot. A broker request cannot select arbitrary filesystem paths; it selects configured profile IDs.

**Official:** Linux Unix sockets support peer credentials; filesystem and abstract sockets have different permission behavior, and portable code cannot rely on socket-file mode alone. XDG runtime directories are user-owned, mode 0700, local, and tied to login lifetime. **Proposal:** use a private directory plus OS credential checks; do not use Linux abstract sockets. On macOS validate the corresponding peer-credential mechanism and short, private runtime path. [Linux unix(7)](https://man7.org/linux/man-pages/man7/unix.7.html), [XDG Base Directory Specification](https://specifications.freedesktop.org/basedir/latest/)

**Official:** the default Windows named-pipe security descriptor can grant read access beyond the owner. `CreateNamedPipe` supports rejecting remote clients. **Proposal:** set an explicit restrictive DACL, require the intended user SID, reject network clients, and use first-instance creation/ownership checks. Use a user-wide scope so an SSH process for that user can connect; a logon-SID-only ACL would intentionally exclude other logon sessions and must not be assumed compatible with that goal. Validate this policy under RDP, SSH, and fast user switching. [Named-pipe security](https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipe-security-and-access-rights), [CreateNamedPipe](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-createnamedpipea)

Singleton acquisition needs an OS-held lock/secured mutex, not “PID file exists.” Keep the lock held through endpoint lifetime; namespace lock names and endpoint ownership. Concurrent starts either connect to the validated existing broker or report contention. Never unlink a socket because one connection timed out. Under the ownership lock, verify a stale endpoint is not live before cleanup. Symlinks, wrong owners, and unexpected file types are errors. Do not terminate an unknown or incompatible process to recover the name. **Official basis:** `flock` locks follow open file descriptions and are released when their last referencing descriptor closes. [flock(2)](https://man7.org/linux/man-pages/man2/flock.2.html)

Use one in-flight collection per profile/source; coalesce client requests with a small bounded queue. Separate source workers and deadlines so one hung source does not stall all profiles. No distributed leader election, ownership lock, quorum, or peer-to-peer gossip is needed: each peer reports only its own local observations, and a viewer combines them. Do not recursively scan a peer's peers.

Keep the durable profile registry separate from disposable presence data. Optional persisted last-known snapshots are mode 0600 (Windows equivalent), atomically replaced by the broker only, bounded, and always loaded as stale after restart. Initial proposed retention: 24 hours for minimal last-known metadata, with no activity history and an immediate clear option. Never store IPC sockets/locks on synced or network storage. If a state directory is remote, use a verified local runtime/cache location or disable the broker explicitly.

## 7. SSH transport, permissions, and the no-daemon path

**Official:** SSH transport provides server authentication, confidentiality, and integrity; user authentication is a separate SSH layer. The SSH connection protocol supports command channels. This lets Hopsesh reuse an existing authenticated remote OS account without opening another Hopsesh network port. [RFC 4253](https://www.rfc-editor.org/rfc/rfc4253), [RFC 4252](https://www.rfc-editor.org/rfc/rfc4252), [RFC 4254](https://www.rfc-editor.org/rfc/rfc4254)

**Proposal:** add a separate `hopsesh presence --stdio` command after 0.4.0, with its own observation protocol and method allowlist. This is a design sketch, not a current command. A remote helper checks the remote sharing policy, reads only local scopes, proxies to an already-running matching broker when available, or runs a bounded pure one-shot collector. Remote read access must never install, enable, or start a persistent Hopsesh or vendor service. A one-shot helper is an explicit SSH command, not a background installation.

Three separate opt-ins are required in the eventual product:

| Setting | Default / meaning |
| --- | --- |
| Background local observation | Off. Enable a per-user broker and select profiles. |
| Share local presence through SSH | Off. Respond with selected minimal scopes to authorized SSH users. Independent of transfer `Peer.Receive`. |
| Watch selected peers in background | Off. Requires existing host approval plus explicit selection for recurring observation. No enrollment by discovery alone. |

Background SSH uses batch-only authentication, strict host-key verification, no forwarding, no PTY, bounded connect/read/close deadlines, and no password/keychain/UI prompts. If a key/agent is unavailable in the service environment, report “Authentication needed”; do not hunt through other processes' environments or persist passwords. Do not import an interactive shell wholesale into a service. A later user-invoked refresh can use the established interactive authentication flow, under its own control context.

The local host allowlist controls **outbound** destinations; it does not prove an inbound caller's device identity. `hello.machineId`, `from`, or SSH environment strings are descriptive data, not authentication. Initial inbound authority is the SSH-authenticated remote OS user plus that user's sharing policy. Do not advertise per-device authorization until the receiver has a trusted key-to-device mechanism. A user with general shell access can already inspect that OS account's files; Hopsesh cannot create isolation from that user.

**Official:** OpenSSH supports forced commands and restrictions in authorized keys. **Proposal:** document an advanced, separately configured read-only key/forced-command setup if users need a narrower SSH grant. Never install it automatically; implementation must validate command argument handling, forwarding restrictions, and origin attribution. [OpenSSH authorized-key options](https://man.openbsd.org/sshd.8#AUTHORIZED_KEYS_FILE_FORMAT)

Keep a dedicated observation stream initially. Reusing the transport code does not imply using the same SSH master or JSON decoder as transfer/control. Share a single peer observation connection among local GUI/CLI/TUI subscribers via the broker, not across unrelated OS users. No Hopsesh port forwarding, Bonjour/mDNS advertisements, internet relay, or vendor bearer-token transport is needed.

No-daemon matrix:

| Local / remote | Behavior |
| --- | --- |
| Neither broker running, modern peer | Explicit snapshot uses pure collectors locally and via short-lived SSH helper. |
| Local broker, no remote broker | Broker obtains bounded one-shot snapshots at the selected peer cadence. No persistent remote startup. |
| Both brokers enabled | SSH helper relays local-only remote snapshots/watch data; sharing still requires opt-in. |
| Old/missing remote Hopsesh | Presence is unsupported. Existing manually requested inventory workflows remain available with their own semantics. Do not run legacy inventory automatically as a background fallback. |
| Local broker incompatible/unavailable | Local one-shot observation remains available; show degraded mode. A disabled sharing policy is not bypassed by a fallback. |

## 8. Evidence, heartbeats, leases, and truthful state

**Proposal:** model distinct dimensions rather than a single live boolean:

| Dimension | Values / meaning |
| --- | --- |
| Reachability | `reachable`, `unreachable`, `auth_required`, `host_key_changed`, `disabled`, `unsupported`, `unknown`; about this observation route. |
| Freshness | `fresh`, `stale`, `unknown`; independently for each source/profile and observation. |
| Presence | `open_observed`, `no_open_observed`, `unknown`; bounded by declared coverage. |
| Activity | `working`, `waiting_input`, `waiting_approval`, `idle_reported`, `unknown`; explicit evidence required. |
| Runtime lifecycle | `running_observed`, `exit_observed`, `unknown`; exit refers to one identified runtime, not every copy. |
| Coverage | Profiles/sources checked, completeness, errors, unsupported features, pagination/truncation. |

Do not infer idle/dead from low CPU, no file changes, a quiet terminal, a stale registry, a heartbeat timeout, or a lost socket. An explicit observed exit is historical evidence about that runtime, even after it becomes old; it cannot establish that the session has not reopened. “No open instance observed in these profiles at 14:32” is more precise than “dead.”

For each observation record source kind/version, collection outcome, age, applicable scope, and confidence basis. Positive evidence can survive partial coverage, but claims of absence require a successful complete check of the stated scope. Partial reads retain prior observations as stale/unknown; they do not emit bulk exits. Conflicting positive evidence is retained with source attribution instead of letting the newest failed scan overwrite it. Source preference is per dimension: vendor activity status, identified process existence, and terminal location answer different questions.

**Official:** Kubernetes uses leases for node heartbeat reporting; etcd leases expire without renewal. These are useful patterns for bounded freshness, not a requirement to deploy either system. **Proposal:** use observer-local freshness leases only; expiry withdraws confidence, never grants execution ownership or permission to kill/restart work. [Kubernetes leases](https://kubernetes.io/docs/concepts/architecture/leases/), [etcd lease API](https://etcd.io/docs/v3.6/learning/api/#lease-api)

Initial tunable budgets, all **proposed** and unbenchmarked:

| Work | Budget |
| --- | --- |
| Local selected active scopes with UI subscribers | Collect every 5s; positive/activity freshness ceiling 15s. |
| Local background scopes, no visible subscribers | Collect every 30s; freshness ceiling 90s. |
| Peer observation | At most every 15s when viewed, 60s in opted-in background mode; server source age still limits usefulness. |
| Transport heartbeat | 15s; after 45s without a valid round trip, mark the route unresponsive. |
| Source probe | 5s target deadline; a slower source reports timeout, not an empty successful result. |
| Retry | Exponential backoff with jitter, 5s to 5min; at most two concurrent peer connection attempts. Auth/host-key/policy failures wait for user repair. |
| Inventory discovery | Bounded 60s reconciliation, adaptive when hidden; targeted refresh on focus. Do not tail all history at the presence cadence. |

A network heartbeat means only that the observer transport answered. It carries collector health but does not renew a session's observation lease. A collector hung for five minutes behind a healthy heartbeat produces stale sessions. Advancing `deliverySeq` also does not renew `observationSeq`; only fresh evidence can.

**Official:** OpenSSH server-alive probes use the encrypted channel and can close an unresponsive SSH connection; they are not application-level session evidence. Retain separate application deadlines even when SSH keepalives are configured. [ssh_config server-alive options](https://man.openbsd.org/ssh_config#ServerAliveCountMax)

### Time and delayed-message rules

Use monotonic elapsed time locally. Do not compare peer wall clocks to decide freshness. A challenged response contains source age at emission and a bounded source validity duration. Conservatively compute:

```text
remaining = max(0, min(sourceTTL, viewerTTL) - sourceAgeAtSend - requestRoundTrip)
```

Start the local expiry timer on receipt. Reject unsolicited/replayed challenge responses, wrong epochs, decreasing sequences, and invalid ages. The full round-trip deduction is intentionally conservative. A stream delta between challenges cannot extend the prior validated deadline merely because it just arrived; a prompt challenged renewal establishes a new bound. A slow or buffered connection may yield no current lease even though the displayed historical record is useful.

**Official:** Go monotonic readings are not serialized, and some systems stop their monotonic clock during sleep. **Proposal:** invalidate all current leases on suspend/resume, observer restart, detected clock discontinuity, or unexplained scheduling gap; require a fresh challenged snapshot. Cross-platform wake detection/suspend-inclusive clock behavior is a release gate, not an assumption that `time.Since` alone solves sleep. [Go time, monotonic clocks](https://pkg.go.dev/time#hdr-Monotonic_Clocks)

**Official:** Linux file-watch queues can overflow and lose events. **Proposal:** use notifications only to accelerate reconciliation; overflow/rename/replacement triggers a bounded rescan and marks affected coverage incomplete until it succeeds. Equivalent macOS/Windows watcher recovery needs platform validation. [inotify(7)](https://man7.org/linux/man-pages/man7/inotify.7.html)

### Process evidence

Persist no claim keyed only by PID. Pair a PID with machine boot/source incarnation and creation identity, then revalidate identity when resolving a registry/ticket. Linux exposes process start time in `/proc/<pid>/stat`; `pidfd_open` can support exit observation. Windows exposes creation time through `GetProcessTimes`. These primitives help distinguish reused PIDs; obtaining a handle still requires validating it matches the originally observed instance. macOS process creation identity and access restrictions need a targeted prototype. [proc_pid_stat(5)](https://man7.org/linux/man-pages/man5/proc_pid_stat.5.html), [pidfd_open(2)](https://man7.org/linux/man-pages/man2/pidfd_open.2.html), [GetProcessTimes](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-getprocesstimes)

In a shared vendor daemon, use thread/runtime status for the session; the daemon process being alive proves only that the daemon exists. A detached tmux server proves neither an attached person nor a working agent. Location may remain unknown even when session presence is established.

## 9. Concrete observation wire/API sketch

**Proposal, not an implemented API.** Use a separate envelope inspired by the existing peer framing: UTF-8 newline-delimited JSON, one bounded object per line, no terminal decoration. Local IPC and SSH helper use the same semantic messages; transport authentication is outside the payload. Do not assume these frames are accepted by a vendor app-server.

First client message:

```json
{
  "id": "1",
  "method": "hello",
  "params": {
    "service": "hopsesh.presence",
    "protocols": [{"major": 1, "minMinor": 0, "maxMinor": 0}],
    "clientVersion": "post-0.4.0-design",
    "capabilities": ["snapshot", "watch", "identity.v1", "freshness.v1"],
    "namespaceId": "ns_opaque",
    "challenge": "random-per-request"
  }
}
```

Illustrative response:

```json
{
  "id": "1",
  "result": {
    "service": "hopsesh.presence",
    "protocol": {"major": 1, "minor": 0},
    "serverVersion": "post-0.4.0-design",
    "observerId": "obs_opaque",
    "observerEpoch": "incarnation_opaque",
    "machineId": "machine_opaque",
    "osUserScope": "user_opaque",
    "capabilities": ["snapshot", "watch", "identity.v1", "freshness.v1"],
    "permissions": ["presence.read"],
    "maxFrameBytes": 262144,
    "challenge": "random-per-request"
  }
}
```

Local namespace IDs need not equal remote namespace IDs; the configured peer binding selects the remote namespace. The hello value must match that expected binding. Returned machine IDs are checked against the authenticated destination association, not trusted as proof by themselves.

| Method | Contract |
| --- | --- |
| `presence.snapshot` | Allowed profile filters, optional known session refs, `maxAgeMs`, `deadlineMs`, `limit`, opaque cursor, challenge. Return complete/partial coverage and source errors even if no observations match. |
| `presence.watch` | Subscribe from `(observerEpoch, revision)` with scoped filters. Deliver snapshot boundary, changes, progress/heartbeats, and explicit `resync_required`. Never reuse the transfer decoder for unsolicited events. |
| `presence.cancel` | Cancel one subscription/request; does not affect another client or any agent. |
| `presence.ping` | Echo a new challenge; optionally return a bounded summary renewal from actual fresh source observations. Transport-only pings never renew observation leases. |
| `presence.goodbye` | Close this connection gracefully; not daemon shutdown or agent stop. |
| Local management API, separate endpoint | Status, stop-observer, and lifecycle management; never exposed by SSH observation forwarding. Registration/config changes remain explicit user actions. |

Snapshot observation example:

```json
{
  "id": "2",
  "result": {
    "observerEpoch": "incarnation_opaque",
    "revision": 42,
    "challenge": "snapshot-nonce",
    "snapshotId": "snapshot_42",
    "complete": true,
    "nextCursor": null,
    "coverage": [{"agentId": "codex", "profileId": "profile_work", "result": "ok", "sourceInstanceId": "vendor_instance_A"}],
    "observations": [{
      "session": {
        "machineId": "machine_opaque",
        "osUserScope": "user_opaque",
        "agentId": "codex",
        "profileId": "profile_work",
        "nativeSessionId": "thread_opaque"
      },
      "binding": {"authBindingId": "binding_opaque", "epoch": 7, "confidence": "verified"},
      "runtimeId": "runtime_opaque",
      "source": {"kind": "vendor_status", "instanceId": "vendor_instance_A", "version": "validated-version"},
      "observationSeq": 103,
      "sourceAgeMs": 1200,
      "validForMs": 15000,
      "observedAt": "2026-10-06T12:00:00Z",
      "presence": "open_observed",
      "activity": "working",
      "lifecycle": "running_observed",
      "evidence": ["vendor_runtime_status"],
      "location": {"kind": "unknown"}
    }],
    "errors": []
  }
}
```

`observedAt` is display/audit metadata only. `validForMs` is total source validity from collection, not additional validity starting at emission. `binding: null` means unresolved and cannot satisfy an account equality or control precondition. Binding confidence is inherited from the account contract (`verified`, `limited`, `unknown`); presence freshness never upgrades it. Native IDs and profile IDs are opaque data; no executable command or arbitrary path is accepted here.

Protocol details to make implementation reviewable:

- Use connection-local unique string request IDs, bounded outstanding requests, and typed errors: `unsupported_protocol`, `unsupported_capability`, `sharing_disabled`, `scope_denied`, `source_unavailable`, `identity_unresolved`, `deadline_exceeded`, `resync_required`, `overloaded`. Error text is sanitized; do not leak another profile's existence in a denied response.
- Require compatible major plus mandatory identity/freshness capabilities. Negotiate the highest common minor; ignore unknown optional fields, but never ignore unknown required semantics. Unknown activity enums display unknown. App version strings are diagnostic, not the protocol negotiation mechanism.
- Proposed bounds: 256 KiB per frame, 128 observations per page, 4 MiB buffered output per client, and a 5-second blocked-write deadline. Reject oversized/deep/invalid JSON before allocation grows without bound. These limits require workload testing.
- Paginate against a stable snapshot ID/revision. Do not infer absence from page one, a truncated response, or failed pagination. Filters and snapshot identity bind cursors; expired cursors force a new snapshot. A completed filtered response proves coverage only for that filter.
- Watch events carry epoch/revision and full scoped keys. On a gap, restart, overflow, or evicted revision, require a new snapshot. Initial v1 may always resnapshot after reconnect; a durable event log is unnecessary.
- A removal event must say `exit_observed`, `no_longer_observed`, `scope_removed`, or `expired`; only the first is an explicit runtime exit. A stale entry cannot disappear and thereby imply a successful exit.
- Remote helpers export only local-origin data. Never forward a vendor API, local management API, peer transfer method, raw process table, arbitrary command, or recursively obtained peer record.

## 10. Optional per-user lifecycle on each platform

**Proposal:** the binary's foreground `presence serve` implementation is shared; platform supervisors provide optional startup. Normal launches remain foreground under the supervisor (no double-fork). Installation state, process state, IPC responsiveness, and source freshness are separately visible.

| Platform | Official basis | Proposed integration / validation |
| --- | --- | --- |
| macOS | `SMAppService` manages bundled helpers on macOS 13+, with registration subject to user approval. LaunchAgents are per-user; Apple's archived launchd guide describes logout SIGTERM and demand activation. [SMAppService](https://developer.apple.com/documentation/servicemanagement/smappservice), [launchd guide](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html) | Bundled GUI distribution: opt-in LaunchAgent via ServiceManagement. CLI-only/older supported OS: explicitly installed user LaunchAgent, if support is validated. No root LaunchDaemon. Validate bundle moves, approval revoked in System Settings, login/logout, SSH-only sessions, helper updates, and existing local signing/release integration. Do not promise operation before login or after logout. |
| Linux with systemd | systemd supports socket activation; its service documentation defines restart/stop behavior. Linger starts/retains a user manager outside login sessions. [systemd](https://systemd.io/), [service manual source](https://raw.githubusercontent.com/systemd/systemd/main/man/systemd.service.xml), [loginctl manual source](https://raw.githubusercontent.com/systemd/systemd/main/man/loginctl.xml) | Opt-in user unit, foreground process, bounded restart-on-failure and stop timeout. No automatic linger; expose it only as a separate advanced choice with its lifetime effect. Start with an ordinary user service; socket activation can later reduce idle cost but must not defeat “disabled.” |
| Linux without systemd / restricted containers | No universal service manager is assumed by this design. | Support manual foreground operation and no-daemon mode. Do not install systemd, demand root, or invent a hidden supervisor. Validate `/proc` visibility and restricted-user coverage. |
| Windows | Task Scheduler supports tasks under the current user's interactive token and limited privileges. Microsoft's per-user service documentation describes template-based Windows services; it does not establish a portable third-party installation path for this feature. [Task security contexts](https://learn.microsoft.com/en-us/windows/win32/taskschd/security-contexts-for-running-tasks), [per-user services](https://learn.microsoft.com/en-us/windows/application-management/per-user-services-in-windows) | Prefer an opt-in current-user, at-logon task with interactive token, limited privilege, no stored password, and bounded restart policy. Named-pipe IPC. No LocalSystem service. Validate non-admin task registration, task battery/time-limit settings, console hiding, signout, RDP/multiple logons, and Windows OpenSSH access to the user-wide pipe. Until proven, offer foreground/no-daemon mode. |

The service observes selected profiles regardless of which GUI profile is active. It loads explicit non-secret root/endpoint mappings; it must not inherit a random client's auth environment or use a changing `HOME`/`CODEX_HOME` as its identity. Where an endpoint requires secret access that has not been separately designed, report unsupported rather than expanding observer privileges.

### Shutdown, disable, uninstall, upgrade

- **Close a GUI/CLI/TUI:** close that subscription. GUI PTY quit behavior continues as today and must be explained independently. It must not stop the enabled presence broker or vendor service.
- **Stop observer now:** stop only Hopsesh observation workers/helpers and local IPC; supervisor integration must prevent immediate automatic resurrection. Define this separately from disabling next-login startup.
- **Disable background presence:** remove/disable startup and any activation trigger, cancel retries, close broker connections, mark local data stale, and record the setting through the future lifecycle manager. A later read must not reactivate it.
- **Disable sharing:** deny new SSH observation requests and close existing exported subscriptions immediately. Disabling outbound monitoring alone does not disable inbound sharing. Receivers cannot promise erasure of data already disclosed to another authorized computer.
- **Uninstall:** remove only the exact Hopsesh-owned unit/task/agent and endpoint after checking ownership; offer deletion of disposable presence cache. Preserve profiles, identity records, session files, lineage, and vendor services.
- **Graceful stop:** signal/cancel workers, send best-effort `observer_stopping`, allow a proposed 5-second drain, then close owned transport children and IPC. Never emit “all sessions ended.” Unexpected crash follows the same stale-data semantics through lease expiry.
- **Upgrade/downgrade:** reject incompatible connections clearly, drain the observer, restart with a new epoch, and resnapshot. Keep the optional cache schema disposable. Never upgrade/restart a vendor daemon to make presence work. Its version may differ from the installed CLI.

No agent process may be a broker child, member of its shutdown job object/cgroup, or target of its cleanup signals. This separation is a must-pass test, including upgrade while agents are working. Hopsesh transport-helper termination must be scoped to owned handles, not process-name matching.

## 11. Operational UX

All commands in this section are **proposed**, not available in 0.4.0:

```text
hopsesh presence status --json
hopsesh presence snapshot --local --json
hopsesh presence snapshot --host <approved-host> --json
hopsesh presence watch --json
hopsesh presence enable --user
hopsesh presence disable --user
hopsesh presence stop --user
hopsesh presence uninstall --user
```

`status` is local and never starts services, scans credentials, or contacts peers. It reports configured mode, supervisor registration, broker PID/incarnation/version, IPC state, source coverage/freshness, and redacted diagnostics. `snapshot` is an explicit bounded observation; `watch` is foreground and ends cleanly on Ctrl+C. Enabling local observation does not silently enable peer sharing or monitoring.

GUI settings should show three separate controls for background observation, SSH sharing, and watched machines, with profile selection and a brief data preview. Display “Runs while you are logged in” where applicable and explain the effect of Stop versus Disable. A supervisor registration success must not render “Running” until IPC answers; responsive IPC must not render every source healthy.

Session labels:

| Evidence | Display |
| --- | --- |
| Fresh vendor working state and known peer/profile | “Working on Studio · Work Codex · checked 8s ago” |
| Identified process or held lock, activity unverified | “Open on Studio · activity unknown” |
| Connection lost | “Studio unreachable · last seen open 3m ago” |
| Observer answers but source fails | “Studio connected · Codex status unavailable” |
| Policy disabled / version unsupported | “Presence sharing off” / “Presence unavailable in this version” |
| Full successful scoped check with no open instance | “No open instance observed · checked 8s ago” |
| Profile/binding cannot be resolved | “Open instance detected · profile unverified” |

Prefer “Unreachable” to “Offline”; neither implies physical power state. Every stale badge retains its last observation time and reason. CLI JSON preserves separate dimensions rather than flattening them into a boolean. TUI watches use the same data contract and visibly age entries even without new messages.

Default notifications off. If enabled later, deduplicate by full identity/runtime and transition, suppress floods during reconnect/wake, and notify only on meaningful changes such as fresh waiting-for-user status. Never include prompt text or sensitive titles in lock-screen notifications by default. “Refresh” does not change authentication settings or repair host keys.

## 12. Security, privacy, and separate control

**Proposal threat model:** protect against other local OS users, unauthenticated network clients, unapproved peer scopes, malicious metadata, stale/replayed messages, and resource exhaustion. A malicious same-user process or authorized general SSH shell already has substantial access; do not claim profile-level OS isolation from it. A compromised authorized peer can lie about its own status, so presence cannot authorize destructive action.

Default exported fields: opaque scoped IDs, observer/source epochs, evidence category, coarse status/location, freshness and errors. Titles, prompts, transcript bodies, terminal output, raw argv/env, tokens, account emails, filesystem roots, repository URLs, PID tables, and vendor pairing links are excluded. Existing inventory display information must not become the automatic presence payload. Friendly labels may be added locally or shared with explicit selection.

Use allowlisted structured adapters; no arbitrary `exec`, file-read endpoint, URL fetch, or path parameter. Cap strings and JSON depth. Escape text for GUI/terminal output, including control characters. Redact logs and diagnostics; do not log full wire frames. Error categories should be actionable without exposing private remote paths. Profile filtering applies before serialization, pagination, event queuing, and diagnostic aggregation.

The observation API has no `stop`, `resume`, `focus`, `approve`, `send`, `apply`, `undo`, `pair`, `login`, or credential operations. Its code dependencies and handler allowlist should enforce this separation, beyond hiding UI controls.

A possible later control design would require a separate permission, explicit user action, fresh target-side revalidation of ProfileID/AuthBindingID/epoch/RuntimeID, a short-lived plan, and an idempotent action record. It cannot treat the presence lease as a lock or use a stale PID. This document does not approve that work. Vendor remote control and existing transfer planning remain the routes for authorized actions.

## 13. Failure modes and required behavior

| Failure / ambiguity | Required behavior |
| --- | --- |
| Sleep, reboot, wall-clock adjustment, broker restart | Invalidate current leases; new epoch after restart; show historical evidence until a fresh challenge succeeds. |
| SSH disconnect, packet buffering, NAT change, remote suspend | Age observations; back off; no automatic host-key acceptance or “session dead.” |
| Healthy heartbeat, hung source | Route reachable, source stale/error. Do not renew source observations. |
| PID reuse, orphaned registry/ticket, parent PID reuse | Require process-instance verification; ambiguous entries become unknown. Never signal from observer. |
| Agent schema/version changes; missed transcript start event | Capability/version-specific fallback to unknown; never default activity to idle. |
| Vendor server absent, multiple vendor servers, standalone Codex | Record endpoint-specific coverage; do not start a replacement daemon or claim complete coverage. |
| Vendor read/subscription mutates metadata or lifetime | Disable that adapter path; use proven passive polling/fallback. Report limitation. |
| Profile rename/root replacement, logout/rebind during watch | Rename preserves ProfileID; root changes follow explicit account-layer registration policy. Invalidate affected routing; old binding observations cannot be relabeled as current. |
| Duplicate native IDs across profiles; cloned machine IDs | Keep scoped records distinct; report identity conflict; require explicit mapping/repair. |
| Source permission denied; unsupported Windows locks; hidden processes | Mark incomplete/unsupported coverage, preserve positives, no inferred absence. |
| Watch overflow, source truncation, failed page two | Resnapshot; incomplete coverage; no removal-by-omission. |
| Slow subscriber / peer flood | Bounded queues, disconnect or resync that client, protect collectors and other viewers. |
| Two broker starts; stale socket; hostile/symlink endpoint | OS ownership lock and identity checks; no blind unlink or kill. |
| Config root on network/synced filesystem | Use local verified runtime state or fail safely to no-daemon mode. |
| Sharing revoked / source profile removed | Cancel and purge exported view for that scope; `scope_removed`, not `exit_observed`. |
| Service registered but denied/disabled by OS | Show registration/authorization state separately; manual fallback, no approval loop. |
| GUI quits with an active embedded terminal | Existing explicit GUI quit handling applies; observer does not claim the terminal is preserved. |
| Unknown protocol / downgraded executable | Clear compatibility error and scoped one-shot fallback only where supported; no service replacement. |

## 14. Test matrix and acceptance gates

These are **future tests**, not results from this research. Existing test files were read only; no repository test/build/service suite was run because this change is a document and the checkout has concurrent implementation work.

| Area | Representative cases | Must-pass oracle |
| --- | --- | --- |
| Pure collection | Pending cloud adoption, deferred marks, stale ticket cleanup, source error | Observe leaves agent files, journals, lineage, configuration, git state, and vendor lifecycle unchanged; only its own optional cache/log may change. |
| Identity | Same native ID in two profiles; same account across profiles; renamed label/replaced root; auth epoch changes; null binding | No collisions or cross-profile notifications; no reassignment to current login or silent pinned-root migration; unknown never equals known/equivalent. |
| Multiple runtimes | Claude terminal + desktop; Codex shared server with several threads; two app-server instances; standalone CLI | Count/attribute each runtime correctly; server PID does not collapse threads or imply activity. |
| Evidence | Held/free/unsupported lock, registry read denied, incomplete listing, missing tail start, no activity signal | No false idle/ended result; explicit source and coverage remain inspectable. |
| Vendor passive behavior | Codex 0.160.1 + older/newer fixtures; differing running-server version; read/list repair; subscription grace; disconnected adapter | No start/resume/repair/auth mutation or unload extension; unsupported paths degrade to unknown. |
| Process identity | PID reuse, process exits mid-snapshot, inaccessible creation time, parent cycles, reboot | No stale PID promoted to a current runtime; location failure does not erase stronger session evidence. |
| IPC/security | Another UID/SID, remote named-pipe attempt, same-user multiple logons, forged hello, malicious metadata, symlink socket | Deny unauthorized scope; no arbitrary execution/read; correct escaping; known same-user trust limitation documented. |
| Singleton | Simultaneous GUI/CLI starts, namespace mismatch, crashed owner, live incompatible broker | One collector per namespace; no duplicate cleanup, blind unlink, or unknown-process termination. |
| Time/leases | Fake-clock forward/backward jumps, suspend where monotonic stops, huge RTT, queued old deltas, missed heartbeats | Stale/unknown within declared bounds; heartbeat cannot renew a hung collector; only challenged fresh evidence renews. |
| Wire/watch | Oversize frame, unknown enum, malformed JSON, duplicate/out-of-order revision, dropped delta, expired cursor, failed pagination | Bounded memory; clear errors; resync; no absence inferred from partial data. |
| SSH topology | Wrong/changed host key, expired SSH agent, password-only peer, revoked sharing, one-way reachability | No prompt/trust change in background; no inbound-device identity inferred from payload; clear partial coverage. |
| Compatibility | Final shipped 0.4.0 transfer protocol, earlier 2 and concurrent 3 fixtures as applicable; old/missing helper; future presence major; optional/required capability mismatch | Transfer behavior untouched; no accidental `receive=true`; safe unsupported/fallback behavior. |
| macOS lifecycle | Supported OS versions; app moved; LaunchAgent disabled; login/logout; SSH-only access; sleep | State accurately reflects OS approval and IPC; no surprise activation; agents unaffected. |
| Linux lifecycle | User unit, linger off/on, no systemd, session cleanup, container `/proc` restrictions | Explicit lifetime semantics; no automatic linger/root use; foreground/no-daemon works. |
| Windows lifecycle | Non-admin user, logon task, battery restrictions, RDP/fast switching, SSH SID scope, ConPTY | No console flash/service elevation; correct ACL and shutdown; unsupported observation stays unknown. |
| Stop/upgrade/uninstall | Active agent work and upload; GUI-owned/external terminals; vendor daemon in use | Stop only observer-owned processes; active work and vendor services continue; cache restarts stale. |
| Resource/load | 0/10/1000 sessions, 1/3/10 clients, 0/3/20 peers, battery and network loss | Measure CPU/RSS/wakeups/bytes/FDs, p50/p95 latency, no retry storm; ten clients do not multiply source polling tenfold. |
| UX/privacy | Reconnect notification flood, inaccessible peer, incomplete scope, lock-screen notification, diagnostic export | Correct labels, no false “all clear,” no prompts/secrets/transcripts/paths in default presence or diagnostics. |

Proposed performance gate on reference laptops: under 1% average of one CPU core when idle and under 50 MiB incremental steady-state broker RSS for three profiles/three peers; bounded two connection attempts; no full transcript scans at five-second cadence. These are targets to test and revise, not performance claims. Compare against no-daemon and vendor-only baselines before enabling any default beyond opt-in.

## 15. Rollout and unresolved decisions

| Phase, all after 0.4.0 | Scope / exit condition |
| --- | --- |
| Design alignment | Accept account identity contract and a read-only lineage query boundary. Confirm semantic definitions and privacy defaults. This document alone authorizes no implementation. |
| Pure observation foundation | Side-effect-free collector, scoped identity, source outcomes/freshness, conservative legacy adapters, fake-clock/fixture tests. Works without a daemon. |
| Vendor adapter experiment | Separately authorized disposable fixtures for passive Codex attachment/status; validate profile binding and no-repair/no-lifetime side effects. Disable unsupported combinations. |
| Local broker preview | Manual foreground first, then opt-in per-user lifecycle on individually validated platforms; GUI/CLI/TUI share snapshots. Measure benefit. |
| SSH presence preview | Selected peers/profiles, separate sharing permission, no new ports, explicit version negotiation, bounded snapshot before watch optimization. |
| Broader opt-in release | Pass security/lifecycle/identity/lease tests and publish support matrix, disable/uninstall behavior, and resource measurements. No automatic migration into background monitoring. |

Open validation questions with concrete owners/interfaces rather than guessed answers:

1. **Account design:** finalize the EndpointID/RuntimeProfileID aliases above, binding epoch transitions, legacy-profile migration, same-account equivalence, and what non-secret metadata an observer may consume. Agree the companion identity contract before implementation; do not add a second profile registry.
2. **Lineage design:** a stable read-only relation query sufficient to link known copies across machines/agents/profiles, without presence changing manifests or merge decisions. Defer integration until concurrent lineage work settles.
3. **Vendor integration:** exact passive Codex attach/read path, version/capability discovery, subscriptions without resume, server-versus-CLI version mismatch, account binding of already-loaded threads, and Claude registry support boundaries. The docs establish useful primitives, not end-to-end passive compatibility.
4. **Platform integration:** macOS credential/start-time APIs and SSH-only user-agent behavior; Windows per-user task/pipe behavior across logons; Linux service/session cleanup and suspend handling. No target machines were contacted.
5. **Operational value:** whether users need background cross-agent visibility often enough to justify a broker, and whether adaptive polling achieves the proposed budget. Vendor status reuse can reduce cost, but does not remove cross-profile identity work.

## 16. Research limitations and document validation

Primary sources above were fetched on the research date. Apple's dynamic SMAppService HTML exposed little content through the web reader; its official [Markdown representation](https://developer.apple.com/tutorials/data/documentation/servicemanagement/smappservice.md) was successfully read through an HTTPS text fetch. The freedesktop-hosted systemd man pages returned access errors; the cited upstream systemd manual XML was available. Apple's launchd guide is explicitly archived and is used only for basic lifecycle concepts. The current Codex App Server and Remote connections pages identified during research were accessible. No unavailable documentation was treated as verified behavior.

The proposed architecture, thresholds, wire schema, lifecycle commands, and tests are recommendations, not an implementation or release claim. No credentials, live session contents, process environments, peer machines, or running vendor APIs were inspected. No hooks, daemons, services, tasks, or configuration were installed or started. Repository references are a dated working-tree read and should be rechecked when implementation is authorized, especially around the active lineage changes.

Document-only validation passed: all three illustrative JSON messages parse; all 29 local file references resolve and their line anchors are in bounds; Markdown fences balance; no trailing whitespace or diff whitespace errors. The document cites 31 distinct primary-source URLs. The sole file authored by this research is `docs/daemon-presence-proposal.md`; other working-tree changes belong to concurrent work and were not reverted, staged, or edited. No production/runtime validation is claimed.
