package main

/*
#include <libproc.h>
#include <unistd.h>
static int usage(struct rusage_info_v4 *ru) {
	return proc_pid_rusage(getpid(), RUSAGE_INFO_V4, (rusage_info_t *)ru);
}
*/
import "C"

// diskWritten is the bytes this process caused to be written to storage (-1: unknown).
func diskWritten() int64 {
	var ru C.struct_rusage_info_v4
	if C.usage(&ru) != 0 {
		return -1
	}
	return int64(ru.ri_diskio_byteswritten)
}

// wakeups counts the times this process woke the CPU (-1: unknown).
func wakeups() int64 {
	var ru C.struct_rusage_info_v4
	if C.usage(&ru) != 0 {
		return -1
	}
	return int64(ru.ri_pkg_idle_wkups + ru.ri_interrupt_wkups)
}
