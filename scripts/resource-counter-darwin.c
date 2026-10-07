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
#include <sys/proc_info.h>
#include <sys/resource.h>
#include <unistd.h>

int main(int argc, char **argv) {
    if (argc != 2) return 2;
    char *end = NULL;
    errno = 0;
    long pid = strtol(argv[1], &end, 10);
    if (errno || !end || *end || pid <= 1 || pid > INT_MAX) return 2;
    struct proc_bsdinfo owner = {0};
    if (proc_pidinfo((int)pid, PROC_PIDTBSDINFO, 0, &owner, sizeof(owner)) != sizeof(owner) || owner.pbi_uid != geteuid()) return 3;
    struct rusage_info_v4 info = {0};
    mach_timebase_info_data_t clock = {0};
    if (proc_pid_rusage((int)pid, RUSAGE_INFO_V4, (rusage_info_t *)&info) || mach_timebase_info(&clock) || clock.denom == 0) return 4;
    printf("{\"pid\":%ld,\"start\":%" PRIu64 ",\"userMach\":%" PRIu64 ",\"systemMach\":%" PRIu64
           ",\"timebaseNumer\":%u,\"timebaseDenom\":%u,\"interruptWakeups\":%" PRIu64 ",\"packageIdleWakeups\":%" PRIu64
           ",\"residentBytes\":%" PRIu64 ",\"footprintBytes\":%" PRIu64 "}\n",
           pid, info.ri_proc_start_abstime, info.ri_user_time, info.ri_system_time, clock.numer, clock.denom,
           info.ri_interrupt_wkups, info.ri_pkg_idle_wkups, info.ri_resident_size, info.ri_phys_footprint);
    return 0;
}
