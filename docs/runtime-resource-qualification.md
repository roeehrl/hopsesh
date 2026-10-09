# Native desktop resource qualification

`TestGUIRelayResourcesSQLiteR2` is an opt-in macOS test of real native windows,
three registered local profiles, three approved relay peers, and zero/one/five
additional IPC subscribers. The peers use real CLI owners, verified local HTTPS,
the actual Worker, SQLite Durable Objects and private local R2. It does not
contact a cloud provider or use the installed app's settings or credentials.

The comparison starts with a three-profile local 0.4 desktop. The current
desktop adds three relay peers; those peers and the local Worker are outside
the measured owner's process group. Both windows render a disposable native
session before measurement and enable receiving through the GUI bridge. The
current window must own its shared runtime and observe three fresh peers before
the resource samples begin.

Build the baseline app with `go build -tags=e2e ./cmd/hopsesh-app` and its matching
CLI with `go build ./cmd/hopsesh` in the baseline checkout; build the current app
with the same e2e tag. Supply absolute paths to those disposable executables:

```sh
HOPSESH_RELAY_PLATFORM=1 \
HOPSESH_GUI_RESOURCE_APP=/absolute/current-app \
HOPSESH_GUI_BASELINE_APP=/absolute/baseline-app \
HOPSESH_GUI_BASELINE_CLI=/absolute/baseline-cli \
HOPSESH_GUI_BASELINE_REF=BASELINE_COMMIT \
HOPSESH_GUI_CURRENT_REF=CURRENT_COMMIT \
HOPSESH_GUI_RESOURCE_REPORT=/absolute/new-report.json \
go test ./internal/e2e -run '^TestGUIRelayResourcesSQLiteR2$' -count=1 -v -timeout=6m
```

Run without concurrent builds or other qualification workloads. Each sample is
30 seconds after a ten-second startup settling period. Temporary app bundles
receive unique identifiers and ad-hoc local signatures, and launch through
LaunchServices with isolated home/config/state directories. The test does not
replace an installed app. It closes only the disposable process whose start
identity still matches, and cleans its fixture state.

## Attribution and limits

The qualification-only C helper uses `proc_pid_rusage` v4 for CPU Mach ticks,
process start identity, resident/physical-footprint counters, and attributed
interrupt/platform-idle wakeups. Its optional `--coalition PID` mode reads both
resource and jetsam coalition IDs using the private XNU `PROC_PIDCOALITIONINFO`
ABI. This ABI is confined to the test tool; it is not linked into shipped apps.
Unsupported layouts fail rather than guessing ownership.

Only the app and same-user system XPC processes in both matching coalitions are
included. WebKit content, networking and GPU processes are identified separately.
Unexpected executables, missing WebKit content, process-start changes, counter
rollback, or changed membership invalidate a sample. Directly executing the GUI
from the test runner is deliberately avoided: that placed the app and workerd
in the same coalition during the initial probe.

Reports retain each process's before/after counters and source/executed binary
digests. Summed physical footprint is a process-accounting comparison, not a
measurement of unique machine-wide RAM; GPU and shared allocations can vary.
CPU percentages are fractions of one core. Summed attributed interrupt wakeups
are not a count of unique hardware interrupts or whole-machine energy in watts.

The controlled-workload acceptance targets are under 1% of one core, under
50 MiB of additional summed process footprint relative to the desktop baseline,
and no more than 50 attributed interrupt wakeups per second. The initial wakeup
budget is based on the measured reference Mac: baseline approximately 119–125/s,
current below 24/s across two valid runs. Keep it tied to this workload and
record new evidence before generalizing to other hardware. One scheduled local
reconciliation may occur in a 30-second sample; additional subscribers must
share the same owner and return to the prior subscription count after cancellation.

These tests qualify finite idle behavior. They do not establish active-transfer
energy, long-term leak freedom, hosted relay costs, or provider VM resource use.

Two local runs compared baseline `a894a9e` with current `b15391d`. In the repeat,
0.4 used 1.040% of one core and 3,557 attributed interrupt wakeups in 30 seconds.
Current zero/one/five-extra-client samples used 0.127%, 0.340% and 0.083%, with
211, 694 and 149 wakeups. All samples retained eight attributed processes.
The summed current footprints were 172.6–177.1 MiB versus a 178.0 MiB baseline.
Scheduled collection deltas were zero/one/zero; cancellation returned subscribers
to baseline. GPU allocation differed substantially between the two baseline runs,
so these results support the bounded idle targets rather than a general memory
reduction claim. The private numerical reports are
`/tmp/hopsesh-0.5-gui-coalition-report-1.json` and
`/tmp/hopsesh-0.5-gui-coalition-report-2.json`; the second also hashes the executed
ad-hoc-signed copies independently of the source executables.

The clean `fc154b4` follow-up passes the same workload in 147.44 seconds. Its
baseline executable SHA256 matches the earlier recorded 0.4 binary; the baseline
CLI was rebuilt from exported `a894a9e`. The current app was built from the clean
commit with the qualification-only hooks. No other local qualification workload
ran during measurement. Results retain eight attributed processes per sample:

| Sample | CPU, one core | Summed footprint, bytes | Interrupt wakeups / 30s |
| --- | ---: | ---: | ---: |
| 0.4 baseline | 1.262% | 174,299,328 | 3,436 |
| Current, zero extra clients | 0.185% | 178,149,568 | 348 |
| Current, one extra client | 0.809% | 184,260,824 | 921 |
| Current, five extra clients | 0.127% | 178,116,800 | 306 |

All current samples satisfy the existing CPU, additional-memory and wakeup
budgets. Shared ownership, collection counts and subscriber cleanup assertions
also pass. This remains finite idle evidence, not whole-machine energy or a
long-term leak test. Raw counters and binary digests are in
`/tmp/hopsesh-fc154b4-gui-resource-report.json`; the execution log is
`/tmp/hopsesh-fc154b4-gui-resource.log`.

Counter and attribution contracts follow Apple's
[task rusage implementation](https://github.com/apple-oss-distributions/xnu/blob/main/osfmk/kern/bsd_kern.c),
[private process-info definitions](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/proc_info_private.h),
and [coalition definitions](https://github.com/apple-oss-distributions/xnu/blob/main/osfmk/mach/coalition.h).
The scheduling goal follows [Apple's timer guidance](https://developer.apple.com/library/archive/documentation/Performance/Conceptual/power_efficiency_guidelines_osx/Timers.html).

The clean `3dfa2f0` follow-up includes the consolidated Accounts notification and
freshness scheduler. It passes the same complete workload in 168.66 seconds,
without overlapping local builds or other qualification workloads. The baseline
source executable SHA256 still matches `6ef69bffdc58e3e0ac70cb1b81f82f41562985e2573842b5ae32b91cc71981dd`.

| Sample | CPU, one core | Summed footprint, bytes | Interrupt wakeups / 30s |
| --- | ---: | ---: | ---: |
| 0.4 baseline | 1.118% | 173,119,488 | 3,012 |
| Current, zero extra clients | 0.126% | 179,968,096 | 274 |
| Current, one extra client | 0.316% | 184,408,184 | 619 |
| Current, five extra clients | 0.103% | 181,295,224 | 173 |

All samples retain eight attributed processes; CPU, incremental footprint and
wakeup budgets, runtime ownership and subscriber cleanup pass. This is finite
idle evidence for `3dfa2f0`, not a whole-machine energy or final-release claim.
Evidence: `/tmp/hopsesh-3dfa2f0-gui-resource-report.json` and
`/tmp/hopsesh-3dfa2f0-gui-resource.log`.
