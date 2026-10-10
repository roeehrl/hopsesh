package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/observe"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
)

type guiProcessSample struct {
	PID       int    `json:"pid"`
	Kind      string `json:"kind"`
	Start     uint64 `json:"start"`
	User      uint64 `json:"userMach"`
	System    uint64 `json:"systemMach"`
	Numer     uint64 `json:"timebaseNumer"`
	Denom     uint64 `json:"timebaseDenom"`
	Interrupt uint64 `json:"interruptWakeups"`
	Idle      uint64 `json:"packageIdleWakeups"`
	RSS       uint64 `json:"residentBytes"`
	Footprint uint64 `json:"footprintBytes"`
}
type guiCoalitionSample struct {
	Coalition [2]uint64          `json:"coalition"`
	Members   []guiProcessSample `json:"members"`
}
type guiResourceResult struct {
	Label        string             `json:"label"`
	BinarySHA256 string             `json:"binarySHA256"`
	SourceSHA256 string             `json:"sourceBinarySHA256"`
	Subscribers  int                `json:"extraSubscribers"`
	Seconds      float64            `json:"seconds"`
	Before       guiCoalitionSample `json:"before"`
	After        guiCoalitionSample `json:"after"`
	CPUPercent   float64            `json:"cpuPercentOneCore"`
	Interrupt    uint64             `json:"interruptWakeups"`
	Idle         uint64             `json:"packageIdleWakeups"`
	Footprint    uint64             `json:"summedProcessFootprint"`
	Collections  uint64             `json:"collections"`
}

// Opt-in native qualification. Baseline is the local three-profile 0.4 desktop;
// current adds three real relay peers. No installed app or user's namespace is
// used. Same-coalition WebKit/system XPC services are included, not all WebKit.
func TestGUIRelayResourcesSQLiteR2(t *testing.T) {
	appBinary, baseline, baselineCLI, output := os.Getenv("HOPSESH_GUI_RESOURCE_APP"), os.Getenv("HOPSESH_GUI_BASELINE_APP"), os.Getenv("HOPSESH_GUI_BASELINE_CLI"), os.Getenv("HOPSESH_GUI_RESOURCE_REPORT")
	if testing.Short() || os.Getenv("HOPSESH_RELAY_PLATFORM") != "1" || appBinary == "" {
		t.Skip("opt-in native GUI and attributed helper resource qualification")
	}
	for _, p := range []string{appBinary, baseline, baselineCLI} {
		info, err := os.Lstat(p)
		if !filepath.IsAbs(p) || strings.HasPrefix(p, "/Applications/") || err != nil || !info.Mode().IsRegular() {
			t.Fatal("provide built disposable app and baseline CLI executables")
		}
	}
	if !filepath.IsAbs(output) {
		t.Fatal("provide an absolute new report path")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("report path must not exist")
	}
	ctx, _, origin, cert, client := startSQLiteRelayFixture(t, 5*time.Minute)
	bin := buildHopsesh(t)
	root := t.TempDir()
	counter := filepath.Join(root, "counter")
	if out, err := exec.CommandContext(ctx, "xcrun", "clang", "-Wall", "-Wextra", "-Werror", "../../scripts/resource-counter-darwin.c", "-o", counter).CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	ready := filepath.Join(root, "ready.js")
	if err := os.WriteFile(ready, []byte(`(async()=>{const end=Date.now()+90000;while(Date.now()<end){if(document.querySelectorAll('.row').length>=1){const {Call}=await import('/wails/runtime.js');await Call.ByName('github.com/roeehrl/hopsesh/internal/ui/gui.App.SetReceive',true);return;}await new Promise(r=>setTimeout(r,300));}})();`), 0600); err != nil {
		t.Fatal(err)
	}
	digest := func(binary string) string {
		t.Helper()
		f, err := os.Open(binary)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		h := sha256.New()
		if _, err = io.Copy(h, f); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	executables := map[int]string{}
	launch := func(binary string, home machineHome) (int, func()) {
		t.Helper()
		// LaunchServices gives the disposable GUI its own app coalition. Direct
		// exec would share the test runner's group, including the local Worker.
		bundle := filepath.Join(root, home.name+".app")
		exe := filepath.Join(bundle, "Contents", "MacOS", "hopsesh-resource")
		if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
			t.Fatal(err)
		}
		source, err := os.Open(binary)
		if err != nil {
			t.Fatal(err)
		}
		dest, err := os.OpenFile(exe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			_ = source.Close()
			t.Fatal(err)
		}
		_, err = io.Copy(dest, source)
		_ = source.Close()
		closeErr := dest.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
		id := sha256.Sum256([]byte(home.home))
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>hopsesh-resource</string><key>CFBundleIdentifier</key><string>dev.hopsesh.resource.%x</string><key>CFBundleName</key><string>Hopsesh Resource Qualification</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleVersion</key><string>1.0.0</string><key>NSHighResolutionCapable</key><true/></dict></plist>`, id[:8])
		if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.CommandContext(ctx, "/usr/bin/codesign", "--force", "--sign", "-", bundle).CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			t.Fatal(err)
		}
		log := filepath.Join(root, home.name+"-app.log")
		args := []string{"-n", "-W", "--stdout", log, "--stderr", log}
		for _, kv := range append(home.env(), "HOPSESH_TAILSCALE=off", "SHELL=/bin/sh", "HOPSESH_E2E_SCRIPT="+ready) {
			args = append(args, "--env", kv)
		}
		args = append(args, bundle)
		cmd := exec.CommandContext(ctx, "/usr/bin/open", args...)
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		pid := 0
		processStart := uint64(0)
		var once sync.Once
		stop := func() {
			once.Do(func() {
				if pid != 0 {
					body, e := exec.Command(counter, strconv.Itoa(pid)).Output()
					var s guiProcessSample
					if e == nil && json.Unmarshal(body, &s) == nil && s.Start == processStart {
						if p, e := os.FindProcess(pid); e == nil {
							_ = p.Kill()
						}
					}
				}
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			})
		}
		t.Cleanup(stop)
		deadline := time.Now().Add(90 * time.Second)
		for {
			if pid == 0 {
				body, e := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,comm=").Output()
				if e != nil {
					t.Fatal(e)
				}
				for _, line := range strings.Split(string(body), "\n") {
					fields := strings.Fields(line)
					if len(fields) < 2 {
						continue
					}
					path, e := filepath.EvalSymlinks(strings.Join(fields[1:], " "))
					if e == nil && path == exe {
						pid, _ = strconv.Atoi(fields[0])
						break
					}
				}
				if pid != 0 {
					body, e := exec.CommandContext(ctx, counter, strconv.Itoa(pid)).Output()
					var s guiProcessSample
					if e != nil || json.Unmarshal(body, &s) != nil {
						t.Fatal("cannot bind disposable GUI process identity", e)
					}
					processStart = s.Start
				}
			}
			body, e := os.ReadFile(filepath.Join(home.home, "config", "config.toml"))
			if pid != 0 && e == nil && (strings.Contains(string(body), "receive = true") || strings.Contains(string(body), "receive=true")) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("disposable native window did not render its session")
			}
			time.Sleep(200 * time.Millisecond)
		}
		time.Sleep(10 * time.Second)
		executables[pid] = exe
		return pid, stop
	}
	sample := func(pid int) guiCoalitionSample {
		t.Helper()
		body, err := exec.CommandContext(ctx, counter, "--coalition", strconv.Itoa(pid)).Output()
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				t.Logf("native counter: %s", e.Stderr)
			}
			t.Fatal("ambiguous, changing or unavailable native process attribution", err)
		}
		var s guiCoalitionSample
		if json.Unmarshal(body, &s) != nil {
			t.Fatal("invalid native counter result")
		}
		content := false
		for _, m := range s.Members {
			content = content || m.Kind == "com.apple.WebKit.WebContent"
		}
		if !content {
			t.Fatal("no attributed WebKit content process")
		}
		return s
	}
	measure := func(label, binary string, pid, subscribers int) guiResourceResult {
		t.Helper()
		r := guiResourceResult{Label: label, BinarySHA256: digest(executables[pid]), SourceSHA256: digest(binary), Subscribers: subscribers, Before: sample(pid)}
		start := time.Now()
		time.Sleep(30 * time.Second)
		r.After = sample(pid)
		r.Seconds = time.Since(start).Seconds()
		if r.Before.Coalition != r.After.Coalition || len(r.Before.Members) != len(r.After.Members) {
			t.Fatal("coalition changed during sample")
		}
		before := map[int]guiProcessSample{}
		for _, m := range r.Before.Members {
			before[m.PID] = m
		}
		for _, m := range r.After.Members {
			old, ok := before[m.PID]
			if !ok || m.Start != old.Start || m.Kind != old.Kind || m.Numer != old.Numer || m.Denom != old.Denom || m.Denom == 0 || m.User < old.User || m.System < old.System || m.Interrupt < old.Interrupt || m.Idle < old.Idle {
				t.Fatal("process membership or counters changed during sample")
			}
			r.CPUPercent += 100 * float64(m.User-old.User+m.System-old.System) * float64(m.Numer) / float64(m.Denom) / 1e9 / r.Seconds
			r.Interrupt += m.Interrupt - old.Interrupt
			r.Idle += m.Idle - old.Idle
			r.Footprint += m.Footprint
		}
		t.Logf("%s clients=%d processes=%d CPU=%.3f%% footprint=%d interrupts=%d idle=%d", label, subscribers, len(r.After.Members), r.CPUPercent, r.Footprint, r.Interrupt, r.Idle)
		return r
	}
	old := newMachineHome(t, root, "baseline", true)
	if err := os.MkdirAll(filepath.Join(old.home, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old.home, "config", "config.toml"), []byte("schema = 4\nlayout = \"flat\"\nupdate_check = \"off\"\n[desktop]\nmode = \"both\"\nclose = \"keep\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"accounts", "scan", "--machine", "local", "--json"}, {"accounts", "add", "claude", "Disposable third profile", "--json"}} {
		cmd := exec.CommandContext(ctx, baselineCLI, args...)
		cmd.Env = old.env()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("baseline profile preparation: %v %s", err, out)
		}
	}
	list := exec.CommandContext(ctx, baselineCLI, "accounts", "list", "--json")
	list.Env = old.env()
	listed, err := list.Output()
	var profiles []json.RawMessage
	if err != nil || json.Unmarshal(listed, &profiles) != nil || len(profiles) != 3 {
		t.Fatal("baseline must have exactly three registered profiles", err)
	}
	oldApp, stopOld := launch(baseline, old)
	results := []guiResourceResult{measure("0.4-three-local-profiles", baseline, oldApp, 0)}
	stopOld()
	fleet := newRelayFleet(t, ctx, bin, origin, cert, client, 'D')
	owner := fleet.homes['A']
	fleet.run(t, owner, "accounts", "add", "claude", "Disposable third profile", "--json")
	fleet.stop['A']()
	for _, kv := range owner.env() {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Peer.Receive = false
	cfg.Desktop.Mode = "both"
	cfg.UpdateCheck = "off"
	owner.writeConfig(t, cfg)
	current, stopCurrent := launch(appBinary, owner)
	defer stopCurrent()
	ns, err := localruntime.NewNamespace(filepath.Join(owner.home, "config"), filepath.Join(owner.home, "state"))
	if err != nil {
		t.Fatal(err)
	}
	local := localruntime.Client{Namespace: ns}
	var status localruntime.Status
	if err = local.Call(ctx, "status", nil, &status); err != nil || status.PID != current {
		t.Fatal("GUI does not own its shared runtime", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		var s observe.Snapshot
		var o app.Observation
		err = local.Call(ctx, "snapshot", nil, &s)
		ready := err == nil && json.Unmarshal(s.Data, &o) == nil && len(o.Remotes) == 3 && s.Fresh(time.Now())
		profiles := 0
		for _, a := range o.Agents {
			if a.Install.Profile != nil && a.Install.Present {
				profiles++
			}
		}
		ready = ready && profiles >= 3
		for _, r := range o.Remotes {
			ready = ready && r.Phase == "done" && r.Status == app.StatusOK && r.Snapshot.Fresh(time.Now())
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("GUI did not observe three fresh peers and profiles", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	var baselineMetrics observe.Metrics
	if err = local.Call(ctx, "metrics", nil, &baselineMetrics); err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{0, 1, 5} {
		watchCtx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		defer func() { cancel(); wg.Wait() }()
		for range count {
			ready := make(chan struct{}, 1)
			wg.Go(func() {
				_ = local.Watch(watchCtx, func(observe.Snapshot) error {
					select {
					case ready <- struct{}{}:
					default:
					}
					return nil
				})
			})
			select {
			case <-ready:
			case <-time.After(5 * time.Second):
				cancel()
				wg.Wait()
				t.Fatal("extra GUI watcher unavailable")
			}
		}
		var before, after observe.Metrics
		if err = local.Call(ctx, "metrics", nil, &before); err != nil {
			cancel()
			wg.Wait()
			t.Fatal(err)
		}
		if before.Subscribers != baselineMetrics.Subscribers+count {
			t.Fatal("extra clients did not share the GUI owner")
		}
		r := measure("0.5-three-profiles-three-relay-peers", appBinary, current, count)
		if err = local.Call(ctx, "metrics", nil, &after); err != nil {
			cancel()
			wg.Wait()
			t.Fatal(err)
		}
		cancel()
		wg.Wait()
		r.Collections = after.Collections - before.Collections
		results = append(results, r)
		if after.Subscribers != before.Subscribers {
			t.Fatal("unexpected subscription count changed during idle sample")
		}
		if r.Collections > 1 {
			t.Error("idle clients multiplied collection beyond one scheduled reconciliation")
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			var joined observe.Metrics
			if err = local.Call(ctx, "metrics", nil, &joined); err != nil {
				t.Fatal(err)
			}
			if joined.Subscribers == baselineMetrics.Subscribers {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("GUI owner retained cancelled subscribers")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	report := map[string]any{"schema": 1, "workload": "three-profile local GUI baseline versus three-profile three-relay-peer GUI", "attribution": "same resource and jetsam coalition; owned app and system XPC only", "wholeMachineEnergyQualified": false, "samples": results, "baselineRef": os.Getenv("HOPSESH_GUI_BASELINE_REF"), "currentRef": os.Getenv("HOPSESH_GUI_CURRENT_REF")}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(append(body, '\n'))
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	for _, r := range results[1:] {
		if r.CPUPercent >= 1 {
			t.Errorf("idle CPU target exceeded: %.3f%%", r.CPUPercent)
		}
		if int64(r.Footprint)-int64(results[0].Footprint) >= 50<<20 {
			t.Error("incremental process footprint exceeds 50 MiB target")
		}
		if float64(r.Interrupt)/r.Seconds > 50 {
			t.Error("idle attributed interrupt wakeups exceed 50 per second")
		}
	}
}
