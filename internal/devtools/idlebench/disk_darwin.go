package main

/*
#include <libproc.h>
#include <unistd.h>
static long long written(void) {
	struct rusage_info_v4 ru;
	if (proc_pid_rusage(getpid(), RUSAGE_INFO_V4, (rusage_info_t *)&ru) != 0) return -1;
	return (long long)ru.ri_diskio_byteswritten;
}
*/
import "C"

// diskWritten is the bytes this process caused to be written to storage (-1: unknown).
func diskWritten() int64 { return int64(C.written()) }
