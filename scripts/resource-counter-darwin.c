// Native qualification only. XNU's fill_task_rusage/task_power_info_locked use
// Mach time for CPU totals and separate task-attributed interrupt/idle counters:
// https://github.com/apple-oss-distributions/xnu/blob/main/osfmk/kern/bsd_kern.c
// https://github.com/apple-oss-distributions/xnu/blob/main/osfmk/kern/task.c
#include <errno.h>
#include <inttypes.h>
#include <limits.h>
#include <libproc.h>
#include <mach/mach_time.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/proc_info.h>
#include <sys/resource.h>
#include <unistd.h>

// Qualification helper only: this private XNU ABI is deliberately absent from
// shipped app code. Refuse unsupported layouts instead of guessing attribution.
// https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/proc_info_private.h
// https://github.com/apple-oss-distributions/xnu/blob/main/osfmk/mach/coalition.h
struct qualification_coalition { uint64_t ids[2]; uint64_t reserved[3]; };
static int coalition(int pid, struct qualification_coalition *value) {
    return proc_pidinfo(pid, 20, 0, value, sizeof(*value)) == sizeof(*value)
        && value->ids[0] && value->ids[1];
}

static int sample(int pid, const char *kind) {
    struct proc_bsdinfo owner = {0};
    if (proc_pidinfo((int)pid, PROC_PIDTBSDINFO, 0, &owner, sizeof(owner)) != sizeof(owner) || owner.pbi_uid != geteuid()) return 3;
    struct rusage_info_v4 info = {0};
    mach_timebase_info_data_t clock = {0};
    if (proc_pid_rusage((int)pid, RUSAGE_INFO_V4, (rusage_info_t *)&info) || mach_timebase_info(&clock) || clock.denom == 0) return 4;
    printf("{\"pid\":%d,\"kind\":\"%s\",\"start\":%" PRIu64 ",\"userMach\":%" PRIu64 ",\"systemMach\":%" PRIu64
           ",\"timebaseNumer\":%u,\"timebaseDenom\":%u,\"interruptWakeups\":%" PRIu64 ",\"packageIdleWakeups\":%" PRIu64
           ",\"residentBytes\":%" PRIu64 ",\"footprintBytes\":%" PRIu64 "}",
           pid, kind, info.ri_proc_start_abstime, info.ri_user_time, info.ri_system_time, clock.numer, clock.denom,
           info.ri_interrupt_wkups, info.ri_pkg_idle_wkups, info.ri_resident_size, info.ri_phys_footprint);
    return 0;
}

int main(int argc, char **argv) {
    int group = argc == 3 && strcmp(argv[1], "--coalition") == 0;
    if (argc != 2 && !group) return 2;
    char *end = NULL;
    errno = 0;
    long pid = strtol(argv[group ? 2 : 1], &end, 10);
    if (errno || !end || *end || pid <= 1 || pid > INT_MAX) return 2;
    if (!group) { int result = sample((int)pid, "app"); puts(""); return result; }
    struct qualification_coalition owner = {0};
    if (!coalition((int)pid, &owner)) return 5;
    int pids[8192];
    int bytes = proc_listpids(PROC_UID_ONLY, geteuid(), pids, sizeof(pids));
    if (bytes <= 0 || bytes >= (int)sizeof(pids)) return 6;
    printf("{\"coalition\":[%" PRIu64 ",%" PRIu64 "],\"members\":[", owner.ids[0], owner.ids[1]);
    if (sample((int)pid, "app")) return 7;
    for (int i = 0; i < bytes / (int)sizeof(int); i++) {
        if (pids[i] <= 1 || pids[i] == pid || pids[i] == getpid()) continue;
        struct qualification_coalition peer = {0};
        if (!coalition(pids[i], &peer) || peer.ids[0] != owner.ids[0] || peer.ids[1] != owner.ids[1]) continue;
        char path[PROC_PIDPATHINFO_MAXSIZE] = {0};
        if (proc_pidpath(pids[i], path, sizeof(path)) <= 0) return 8;
        const char *name = strrchr(path, '/');
        if (!name || strncmp(path, "/System/", 8) != 0 || !strstr(path, "/XPCServices/")) {
            fprintf(stderr, "ambiguous coalition member pid=%d executable=%s\n", pids[i], name ? name + 1 : "unknown");
            return 9;
        }
        name++;
        const char *kind = "system-xpc";
        if (!strcmp(name, "com.apple.WebKit.WebContent") || !strcmp(name, "com.apple.WebKit.Networking") || !strcmp(name, "com.apple.WebKit.GPU")) kind = name;
        printf(",");
        if (sample(pids[i], kind)) return 10;
    }
    struct qualification_coalition final = {0};
    if (!coalition((int)pid, &final) || memcmp(owner.ids, final.ids, sizeof(owner.ids))) return 11;
    puts("]}");
    return 0;
}
