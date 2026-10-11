//go:build !windows

// Command idlebench holds the desktop app's idle resource use to budgets: it runs the
// real GUI service headless, with session watching on, on a generated busy home (many
// sessions, some large) while a writer appends to one session several times a second,
// as other agents do. It measures what an app nobody is looking at does, then what it
// does while someone is, and fails when a budget is exceeded. CI runs it on Linux and
// macOS; budgets catch regressions of several times, not noise.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/testkit"
	"github.com/roeehrl/hopsesh/internal/ui/desktop"
	"github.com/roeehrl/hopsesh/internal/ui/gui"
)

// shell is a desktop whose attention the bench sets: nobody looking, or someone.
type shell struct {
	attended atomic.Bool
	mu       sync.Mutex
	s        desktop.State
}

func (f *shell) Snapshot() desktop.State              { f.mu.Lock(); defer f.mu.Unlock(); return f.s }
func (f *shell) Apply(p config.Desktop, _ bool) error { return nil }
func (f *shell) Recheck()                             {}
func (f *shell) KeepOnClose() bool                    { return true }
func (f *shell) OpenMain()                            {}
func (f *shell) CloseQuick()                          {}
func (f *shell) SetAttention(int, int)                {}
func (f *shell) Stop()                                {}
func (f *shell) Attended() bool                       { return f.attended.Load() }

type phase struct {
	Name            string  `json:"name"`
	Seconds         float64 `json:"seconds"`
	Scans           int64   `json:"scans"`
	ContentReads    int64   `json:"contentReads"`
	CPUSeconds      float64 `json:"cpuSeconds"`
	CPUPercent      float64 `json:"cpuPercent"`
	DiskWritten     int64   `json:"diskWrittenBytes"` // by the app's process (-1: unknown here)
	CatalogChanged  bool    `json:"catalogChanged"`
	HeapAfterGCMiB  uint64  `json:"heapAfterGCMiB"`
	CPUPerScanMilli float64 `json:"cpuPerScanMs,omitempty"`
}

func main() {
	sessions := flag.Int("sessions", 300, "generated sessions")
	large := flag.Int("large", 3, "of which large (many turns)")
	warmup := flag.Duration("warmup", 45*time.Second, "settling time before measuring")
	idle := flag.Duration("idle", 2*time.Minute, "unattended measurement")
	attended := flag.Duration("attended", 30*time.Second, "attended measurement")
	writeEvery := flag.Duration("write-every", 250*time.Millisecond, "how often the writer appends to a session")
	out := flag.String("json", "", "write results here")
	writeTo := flag.String("writer", "", "internal: be the other agent, appending to this session")
	// Budgets.
	maxIdleCPU := flag.Float64("max-idle-cpu", 1.0, "unattended CPU, percent of one core")
	maxIdleScans := flag.Int64("max-idle-scans", 1, "unattended rescans")
	maxIdleReads := flag.Int64("max-idle-reads", 0, "unattended transcript and sidecar reads")
	maxIdleDisk := flag.Int64("max-idle-disk", 256<<10, "unattended bytes written by the app")
	maxHeap := flag.Uint64("max-heap", 200, "heap after GC, MiB")
	maxScanCPU := flag.Float64("max-scan-cpu", 1500, "CPU per attended rescan, milliseconds")
	flag.Parse()
	if *writeTo != "" {
		writer(*writeTo, *writeEvery)
		return
	}

	home, err := os.MkdirTemp("", "hopsesh-idlebench-")
	must(err)
	defer os.RemoveAll(home)
	home, _ = filepath.EvalSymlinks(home)
	for k, v := range testkit.Env(home) {
		os.Setenv(k, v)
	}
	must(testkit.DemoHome(home))
	target := must1(generate(home, *sessions, *large))

	svc := gui.NewApp(all.Registry())
	sh := &shell{s: desktop.State{Capabilities: desktop.Capabilities{Tray: true}, Effective: "both"}}
	svc.Desktop = sh
	defer svc.Shutdown()
	_, err = svc.InitialScan()
	must(err)

	// The other agent is its own process: its writes are not the app's.
	self := must1(os.Executable())
	other := exec.Command(self, "-writer", target, "-write-every", writeEvery.String())
	must(other.Start())
	defer func() { _ = other.Process.Kill(); _ = other.Wait() }()

	time.Sleep(*warmup)
	catalog := filepath.Join(home, "state", "catalog")
	results := []phase{
		measure("unattended", *idle, catalog),
	}
	sh.attended.Store(true)
	results = append(results, measure("attended", *attended, catalog))

	var fails []string
	u, at := results[0], results[1]
	check := func(ok bool, format string, args ...any) {
		if !ok {
			fails = append(fails, fmt.Sprintf(format, args...))
		}
	}
	check(u.CPUPercent <= *maxIdleCPU, "unattended CPU %.2f%% > %.2f%%", u.CPUPercent, *maxIdleCPU)
	check(u.Scans <= *maxIdleScans, "unattended rescans %d > %d", u.Scans, *maxIdleScans)
	check(u.ContentReads <= *maxIdleReads, "unattended content reads %d > %d", u.ContentReads, *maxIdleReads)
	check(u.DiskWritten < 0 || u.DiskWritten <= *maxIdleDisk, "unattended disk writes %d B > %d B", u.DiskWritten, *maxIdleDisk)
	check(!u.CatalogChanged || u.Scans > 0, "the catalog changed with no rescan")
	check(u.HeapAfterGCMiB <= *maxHeap && at.HeapAfterGCMiB <= *maxHeap, "heap after GC %d/%d MiB > %d MiB", u.HeapAfterGCMiB, at.HeapAfterGCMiB, *maxHeap)
	// Attended: refreshes follow writes, but spaced (the 5 s gap), and each one is cheap.
	maxAttended := int64(math.Ceil(at.Seconds/5)) + 1
	check(at.Scans <= maxAttended, "attended rescans %d > %d (one per 5 s)", at.Scans, maxAttended)
	check(at.Scans >= 1, "attended: writes never refreshed the list")
	check(at.CPUPerScanMilli <= *maxScanCPU, "CPU per attended rescan %.0f ms > %.0f ms", at.CPUPerScanMilli, *maxScanCPU)

	report(results, fails, *out)
	if len(fails) > 0 {
		os.Exit(1)
	}
}

// generate clones a demo Claude Code session into many, some with thousands of turns,
// and returns one for the writer.
func generate(home string, n, large int) (string, error) {
	var tmpl string
	_ = filepath.Walk(filepath.Join(home, ".claude", "projects"), func(p string, fi os.FileInfo, err error) error {
		if err == nil && tmpl == "" && strings.HasSuffix(p, ".jsonl") && !strings.Contains(p, "subagents") {
			tmpl = p
		}
		return nil
	})
	if tmpl == "" {
		return "", fmt.Errorf("no demo session to clone")
	}
	body, err := os.ReadFile(tmpl)
	if err != nil {
		return "", err
	}
	id := strings.TrimSuffix(filepath.Base(tmpl), ".jsonl")
	var target string
	for i := range n {
		nid := fmt.Sprintf("1d1e0000-0000-4000-8000-%012d", i)
		text := strings.ReplaceAll(string(body), id, nid)
		turns := 20
		if i < large {
			turns = 20000
		}
		var b strings.Builder
		b.WriteString(text)
		for t := range turns {
			fmt.Fprintf(&b, `{"type":"assistant","uuid":"%s-t%d","sessionId":"%s","message":{"role":"assistant","content":[{"type":"text","text":"step %d of a long piece of work, with enough words to look like a real turn"}]}}`+"\n", nid, t, nid, t)
		}
		p := filepath.Join(filepath.Dir(tmpl), nid+".jsonl")
		if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
			return "", err
		}
		if i == n-1 {
			target = p
		}
	}
	return target, nil
}

func writer(path string, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for i := 0; ; i++ {
		<-t.C
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			continue
		}
		fmt.Fprintf(f, `{"type":"system","uuid":"bench-%d","subtype":"other agent activity"}`+"\n", i)
		f.Close()
	}
}

func measure(name string, d time.Duration, catalog string) phase {
	c0, cat0, d0 := app.ResourceCounters(), catalogState(catalog), diskWritten()
	cpu0 := cpuTime()
	start := time.Now()
	time.Sleep(d)
	el := time.Since(start).Seconds()
	c1, cat1, d1 := app.ResourceCounters(), catalogState(catalog), diskWritten()
	cpu := (cpuTime() - cpu0).Seconds()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	p := phase{Name: name, Seconds: math.Round(el), Scans: c1.Scans - c0.Scans, ContentReads: c1.ContentReads - c0.ContentReads,
		CPUSeconds: round(cpu), CPUPercent: round(100 * cpu / el), CatalogChanged: cat0 != cat1, HeapAfterGCMiB: ms.HeapAlloc >> 20, DiskWritten: -1}
	if d0 >= 0 && d1 >= 0 {
		p.DiskWritten = d1 - d0
	}
	if p.Scans > 0 {
		p.CPUPerScanMilli = round(1000 * cpu / float64(p.Scans))
	}
	return p
}

// catalogState versions the catalog files (the shared memory file is excluded: SQLite
// maps it, which is not a write to the catalog).
func catalogState(dir string) string {
	var b strings.Builder
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() && !strings.HasSuffix(e.Name(), "-shm") {
			fmt.Fprintf(&b, "%s %d %d\n", e.Name(), info.Size(), info.ModTime().UnixNano())
		}
	}
	return b.String()
}

func cpuTime() time.Duration {
	var self, children syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &self)
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &children)
	return time.Duration(self.Utime.Nano() + self.Stime.Nano() + children.Utime.Nano() + children.Stime.Nano())
}

func report(results []phase, fails []string, out string) {
	b, _ := json.MarshalIndent(map[string]any{"phases": results, "failures": fails}, "", "  ")
	fmt.Println(string(b))
	if out != "" {
		_ = os.WriteFile(out, b, 0o644)
	}
	if s := os.Getenv("GITHUB_STEP_SUMMARY"); s != "" {
		f, err := os.OpenFile(s, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if err == nil {
			fmt.Fprintf(f, "### Idle resources (%s)\n\n| phase | s | rescans | content reads | CPU %% | CPU per rescan ms | disk written B | catalog changed | heap MiB |\n|---|---|---|---|---|---|---|---|---|\n", runtime.GOOS)
			for _, p := range results {
				fmt.Fprintf(f, "| %s | %.0f | %d | %d | %.2f | %.0f | %d | %v | %d |\n", p.Name, p.Seconds, p.Scans, p.ContentReads, p.CPUPercent, p.CPUPerScanMilli, p.DiskWritten, p.CatalogChanged, p.HeapAfterGCMiB)
			}
			for _, fl := range fails {
				fmt.Fprintf(f, "\n- ❌ %s", fl)
			}
			fmt.Fprintln(f)
			f.Close()
		}
	}
}

func round(f float64) float64 { return math.Round(f*100) / 100 }

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func must1[T any](v T, err error) T { must(err); return v }
