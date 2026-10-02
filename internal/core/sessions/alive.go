package sessions

import "os"

// LocalAlive reports which pids are running on this machine.
func LocalAlive(pids []int) map[int]bool {
	out := make(map[int]bool, len(pids))
	for _, pid := range pids {
		out[pid] = processExists(pid)
	}
	return out
}

func processExists(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return signalZero(p)
}
