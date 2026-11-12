package main

import (
	"os"
	"strconv"
	"strings"
)

// diskWritten is the bytes this process caused to be written to storage (-1: unknown).
func diskWritten() int64 {
	b, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "write_bytes: "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err == nil {
				return n
			}
		}
	}
	return -1
}

// wakeups counts this process's voluntary context switches, the closest Linux has to
// wakeups (-1: unknown).
func wakeups() int64 {
	entries, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return -1
	}
	var total int64
	for _, e := range entries {
		b, err := os.ReadFile("/proc/self/task/" + e.Name() + "/status")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "voluntary_ctxt_switches:"); ok {
				n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
				total += n
			}
		}
	}
	return total
}
