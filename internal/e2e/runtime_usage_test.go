package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

type runtimeProcessUsage struct {
	CPUSeconds float64 `json:"cpu"`
	RSSBytes   int64   `json:"rss"`
}

// Native OS counters, not observer-duration proxies. These finite samples do
// not claim platform energy, hardware wakeups or a GUI baseline comparison.
func readRuntimeUsage(ctx context.Context, pid int) (runtimeProcessUsage, error) {
	if runtime.GOOS == "windows" {
		script := fmt.Sprintf("$ErrorActionPreference='Stop'; $p=Get-Process -Id %d; @{cpu=$p.TotalProcessorTime.TotalSeconds;rss=$p.WorkingSet64}|ConvertTo-Json -Compress", pid)
		body, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
		if err != nil {
			return runtimeProcessUsage{}, err
		}
		var out runtimeProcessUsage
		err = json.Unmarshal(body, &out)
		return out, err
	}
	body, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "rss=,time=").Output()
	if err != nil {
		return runtimeProcessUsage{}, err
	}
	fields := strings.Fields(string(body))
	if len(fields) != 2 {
		return runtimeProcessUsage{}, fmt.Errorf("invalid native process counters")
	}
	rss, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return runtimeProcessUsage{}, err
	}
	cpu := fields[1]
	var daysSeconds float64
	if day, clock, ok := strings.Cut(cpu, "-"); ok {
		days, e := strconv.ParseFloat(day, 64)
		if e != nil {
			return runtimeProcessUsage{}, e
		}
		daysSeconds = days * 86400
		cpu = clock
	}
	parts := strings.Split(cpu, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return runtimeProcessUsage{}, fmt.Errorf("invalid native CPU duration")
	}
	var seconds float64
	for _, part := range parts {
		v, e := strconv.ParseFloat(part, 64)
		if e != nil {
			return runtimeProcessUsage{}, e
		}
		seconds = seconds*60 + v
	}
	return runtimeProcessUsage{CPUSeconds: daysSeconds + seconds, RSSBytes: rss * 1024}, nil
}
