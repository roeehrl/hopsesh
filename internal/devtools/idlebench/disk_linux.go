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
