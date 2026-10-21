//go:build !windows

package gui

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A test-owned stand-in, not the native CLI: real writer locks and actual process
// names feed the unmodified Codex LiveDetector and Presence. No vendor RPC or model
// requests are made. Test-owned files coordinate /new and lock release.
const ownedCodexFixtureSource = `package main
import ("encoding/json"; "fmt"; "os"; "path/filepath"; "syscall"; "time")
func main() {
 args:=os.Args[1:]
 if len(args)==1 && args[0]=="--version" { fmt.Println("codex-cli 0.160.1"); return }
 home:=os.Getenv("CODEX_HOME"); if home=="" { home=filepath.Join(os.Getenv("HOME"),".codex") }
 lock:=func(id string)*os.File {
   path:=filepath.Join(home,"thread-writer-locks",id+".lock")
   if err:=os.MkdirAll(filepath.Dir(path),0700);err!=nil {panic(err)}
   f,err:=os.OpenFile(path,os.O_CREATE|os.O_RDWR,0600);if err!=nil {panic(err)}
   if err=syscall.Flock(int(f.Fd()),syscall.LOCK_EX|syscall.LOCK_NB);err!=nil {panic(err)};return f
 }
 if len(args)>1 && args[0]=="shared" {
   files:=[]*os.File{};for _,id:=range args[1:] { files=append(files,lock(id)) }
   os.WriteFile(filepath.Join(home,"shared-ready"),[]byte("ready"),0600)
   for {time.Sleep(time.Hour)}
 }
 if len(args)!=5 || args[0]!="resume" || args[1]!="--no-daemon" || args[3]!="-C" { fmt.Println("owned CLI used shared daemon"); os.Exit(2) }
 f:=lock(args[2]);defer func(){if f!=nil {f.Close()}}()
 cwd,_:=os.Getwd(); b,_:=json.Marshal(map[string]any{"args":args,"cwd":cwd,"root":home})
 os.WriteFile(filepath.Join(home,"owned-ready"),b,0600)
 switched,released:=false,false
 for {
   if !switched {if b,err:=os.ReadFile(filepath.Join(home,"switch"));err==nil { f.Close();f=lock(string(b));switched=true }}
   if !released {if _,err:=os.Stat(filepath.Join(home,"release"));err==nil { f.Close();f=nil;released=true }}
   time.Sleep(10*time.Millisecond)
 }
}`

func TestCodexOwnedPTYBindsFromRealLocksAndRejectsSharedDaemon(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	repo := home(t)
	root := filepath.Join(os.Getenv("HOME"), ".codex")
	t.Setenv("CODEX_HOME", root)
	t.Setenv("CODEX_SQLITE_HOME", root)
	ids := []string{"01a0fe1c-0000-7000-8000-000000000001", "01a0fe1c-0000-7000-8000-000000000002"}
	fixture := "rollout-2026-10-01T13-00-00-" + ids[0] + ".jsonl"
	b, err := os.ReadFile(filepath.Join("../../../agents/codex/testdata/0.153.2/sessions/2026/10/01", fixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		p := filepath.Join(root, "sessions", "2026", "10", "01", strings.ReplaceAll(fixture, ids[0], id))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		data := strings.ReplaceAll(strings.ReplaceAll(string(b), ids[0], id), "/home/u/git/demo", filepath.ToSlash(repo))
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	source := filepath.Join(bin, "helper.go")
	if err := os.WriteFile(source, []byte(ownedCodexFixtureSource), 0600); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(bin, "codex")
	build := exec.Command(goBin, "build", "-o", program, source)
	// Keep helper compilation independent of the repository and offline.
	build.Env = append(os.Environ(), "GOCACHE="+filepath.Join(bin, "cache"), "GOTOOLCHAIN=local", "GOPROXY=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("helper build: %v %s", err, out)
	}
	// lsof supplies native lock-holder evidence on macOS; this adds no agent binaries.
	t.Setenv("PATH", bin+string(os.PathListSeparator)+testPath()+string(os.PathListSeparator)+"/usr/sbin")
	reg, err := registry.New(codex.New())
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp(reg, WithoutSessionWatching())
	t.Cleanup(a.Shutdown)
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	owned := a.inv
	if len(owned.Entries) != 2 {
		t.Fatalf("fixture entries: %d", len(owned.Entries))
	}
	in, ok := owned.Local().Install("codex")
	if !ok || in.Version != "0.160.1" {
		t.Fatalf("fixture detection: %+v", in)
	}
	key := agent.SessionKey{Agent: "codex", Profile: in.ProfileID(), Session: agent.SessionID(ids[0])}
	c := codex.New().Resume(in, key, agent.Placement{CWD: repo}, agent.ResumeOptions{})
	original := append([]string{}, c.Argv...)
	opened, err := a.sessionTab(c, app.Launch{Run: c, Key: key}, TabMeta{Kind: TabSession, Machine: app.LocalName(), Key: key.String()}, "fixture")
	if err != nil || opened.Where != WhereHere {
		t.Fatalf("owned open: %+v %v", opened, err)
	}
	waitFile := func(name string) []byte {
		t.Helper()
		until := time.Now().Add(10 * time.Second)
		for time.Now().Before(until) {
			if b, err := os.ReadFile(filepath.Join(root, name)); err == nil {
				return b
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("helper did not create %s; tabs %+v", name, a.TerminalTabs())
		return nil
	}
	var startup struct {
		Args      []string
		CWD, Root string
	}
	if err := json.Unmarshal(waitFile("owned-ready"), &startup); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Argv, original) || startup.Root != root || startup.CWD != repo || !reflect.DeepEqual(startup.Args, []string{"resume", "--no-daemon", ids[0], "-C", repo}) {
		t.Fatalf("launch: %+v, original %v", startup, c.Argv)
	}
	info := a.TerminalTabs()[0].Info
	check := func(id, association string) {
		t.Helper()
		until := time.Now().Add(10 * time.Second)
		for time.Now().Before(until) {
			if _, err := a.Presence(); err != nil {
				t.Fatal(err)
			}
			tab := a.TerminalTabs()[0]
			if string(mustBindingKey(t, tab.Key).Session) == id && tab.Association == association && tab.PID == info.PID && tab.Started.Equal(info.Started) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("binding did not reach %s / %s: tabs %+v live %+v", id, association, a.TerminalTabs(), a.core.LiveHere(context.Background(), owned))
	}
	check(ids[0], "Confirmed by native process registry")
	if err := os.WriteFile(filepath.Join(root, "switch"), []byte(ids[1]), 0600); err != nil {
		t.Fatal(err)
	}
	check(ids[1], "Confirmed by native process registry")
	tab := a.TerminalTabs()[0]
	if tab.LaunchedKey != key.String() || tab.External || tab.Rerun {
		t.Fatalf("native /new retains unsafe old launch: %+v", tab)
	}
	if err := os.WriteFile(filepath.Join(root, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	check(ids[1], "Session association not confirmed")
	// A shared multi-thread daemon outside the PTY cannot confirm either key.
	shared := exec.Command(program, "shared", ids[0], ids[1])
	shared.Env = append(os.Environ(), "CODEX_HOME="+root)
	if err := shared.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shared.Process.Kill(); _ = shared.Wait() })
	waitFile("shared-ready")
	check(ids[1], "Session association not confirmed")
	if a.inv != owned {
		t.Fatal("Presence replaced the inventory")
	}
	// Delayed evidence must not confirm a process after its owned lifetime ends.
	if err := a.Terms.Manager().Close(opened.Tab); err != nil {
		t.Fatal(err)
	}
	a.commitTerminalBinding(owned, tab, key, &owned.Entries[0], false, owned.Relationships())
	if len(a.TerminalTabs()) != 0 {
		t.Fatal("late evidence recreated an exited owned tab")
	}
}

func mustBindingKey(t *testing.T, s string) agent.SessionKey {
	t.Helper()
	k, err := agent.ParseKey(s)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
