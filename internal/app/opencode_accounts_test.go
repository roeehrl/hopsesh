package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// An OpenCode account folder holds its own data, state and config folders, copies only
// display settings from the default config folder, and gets its own background-service
// port, set through OpenCode's program with the folder's variables: the default folder's
// service, running here, is never stopped or moved (decision D8). The stand-in plays
// OpenCode (internal/testkit/fakeopencode).
func TestOpenCodeAccountFolderGetsItsOwnPort(t *testing.T) {
	_, log := cloudEnv(t, "opencode")
	for _, k := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "OPENCODE_CONFIG_DIR", "OPENCODE_DB", "FAKE_OPENCODE_SERVICE", "FAKE_OPENCODE_FAIL"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("HOPSESH_APP_RUNTIMES", "off")
	home := os.Getenv("HOME")
	// The default folder: its service runs (the stand-in's registration), its config
	// holds a provider key and display settings.
	defState := filepath.Join(home, ".local", "state", "opencode")
	os.MkdirAll(defState, 0o700)
	os.WriteFile(filepath.Join(defState, "fake-service"), []byte("http://127.0.0.1:49374\n"), 0o600)
	defConfig := filepath.Join(home, ".config", "opencode")
	os.MkdirAll(defConfig, 0o700)
	os.WriteFile(filepath.Join(defConfig, "opencode.json"), []byte(`{"provider":{"x":{"options":{"apiKey":"sk-never-copied"}}}}`), 0o600)
	os.WriteFile(filepath.Join(defConfig, "cli.json"), []byte(`{"theme":{"name":"nord"},"plugins":[{"package":"p","options":{"token":"never"}}]}`), 0o600)
	os.MkdirAll(filepath.Join(home, ".local", "share", "opencode"), 0o700)

	a := cloudApp(t, all.Registry())
	ctx := context.Background()
	p, err := a.AddAccount(ctx, "", "opencode", "Work", nil, AddAccountOptions{Command: runtime.GOOS != "windows"})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".hopsesh", "accounts", "opencode", "work")
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	def := 49374 // OpenCode's service port, 28894 on Windows
	if runtime.GOOS == "windows" {
		def = 28894
	}
	if p.Root != root || p.Port <= def || p.Port > def+200 {
		t.Fatalf("root %s port %d", p.Root, p.Port)
	}
	var settings struct {
		Port int `json:"port"`
	}
	b, err := os.ReadFile(filepath.Join(root, "config", "service.json"))
	if err != nil || json.Unmarshal(b, &settings) != nil || settings.Port != p.Port {
		t.Fatalf("the folder's service settings: %s %v", b, err)
	}
	cli, _ := os.ReadFile(filepath.Join(root, "config", "cli.json"))
	if !strings.Contains(string(cli), "nord") || strings.Contains(string(cli), "never") {
		t.Errorf("cli.json: %s", cli)
	}
	if _, err := os.Stat(filepath.Join(root, "config", "opencode.json")); err == nil {
		t.Error("opencode.json was copied")
	}
	if _, err := os.Stat(filepath.Join(defState, "fake-service")); err != nil {
		t.Error("the default folder's service was stopped")
	}
	calls, _ := os.ReadFile(log)
	for _, want := range []string{"opencode service set port " + strconv.Itoa(p.Port), "opencode service get port"} {
		if !strings.Contains(string(calls), want+"\n") {
			t.Errorf("did not run %q:\n%s", want, calls)
		}
	}
	for _, never := range []string{"stopped the background service", "started the background service", "--standalone service"} {
		if strings.Contains(string(calls), never) {
			t.Errorf("%q:\n%s", never, calls)
		}
	}
	if runtime.GOOS != "windows" {
		s, _ := os.ReadFile(filepath.Join(home, ".local", "bin", "opencode-work"))
		for _, want := range []string{"export XDG_DATA_HOME='" + filepath.Join(root, "data") + "'", "export OPENCODE_CONFIG_DIR='" + filepath.Join(root, "config") + "'",
			"export OPENCODE_DB='" + filepath.Join(root, "data", "opencode", "opencode.db") + "'", "unset OPENCODE_PASSWORD"} {
			if !strings.Contains(string(s), want) {
				t.Errorf("the command lacks %s:\n%s", want, s)
			}
		}
	}

	// A second folder gets another port.
	q, err := a.AddAccount(ctx, "", "opencode", "Home", nil, AddAccountOptions{})
	if err != nil || q.Port == p.Port || q.Port <= def {
		t.Fatalf("second folder: port %d (first %d): %v", q.Port, p.Port, err)
	}

	// A folder OpenCode could not set up is not kept: nothing of the person's was in it.
	t.Setenv("FAKE_OPENCODE_FAIL", "service set")
	if _, err := a.AddAccount(ctx, "", "opencode", "Broken", nil, AddAccountOptions{}); err == nil || !strings.Contains(err.Error(), "service set") {
		t.Fatalf("a failed setup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".hopsesh", "accounts", "opencode", "broken")); !os.IsNotExist(err) {
		t.Error("the failed folder was kept")
	}
	ps, _ := a.Accounts()
	for _, x := range ps {
		if x.Name == "Broken" {
			t.Error("the failed folder was registered")
		}
	}
}

// The notice hook finds a session's anchor (and its lineage) under the main root of its
// account folder: the folder itself for a one-root agent and for an unregistered or
// default folder, OpenCode's data root inside an added one.
func TestNoticeMainOfAnAccountFolder(t *testing.T) {
	oc, ok := all.Registry().Get("opencode")
	if !ok {
		t.Fatal("no OpenCode")
	}
	cl, _ := all.Registry().Get("claude")
	root := filepath.Join(string(filepath.Separator)+"h", ".hopsesh", "accounts", "opencode", "work")
	if runtime.GOOS == "windows" {
		root = `C:\h\.hopsesh\accounts\opencode\work`
	}
	for _, c := range []struct {
		name  string
		spec  agent.Spec
		scope agent.RuntimeProfile
		want  string
	}{
		{"added OpenCode folder", oc.Spec(), agent.RuntimeProfile{ID: "w", Root: root}, filepath.Join(root, "data", "opencode")},
		{"default OpenCode folder", oc.Spec(), agent.RuntimeProfile{ID: "d", Default: true, Root: "/h/.local/share/opencode"}, "/h/.local/share/opencode"},
		{"unregistered default", oc.Spec(), agent.RuntimeProfile{Root: "/h/.local/share/opencode"}, "/h/.local/share/opencode"},
		{"added Claude Code folder", cl.Spec(), agent.RuntimeProfile{ID: "c", Root: "/h/claude-work"}, "/h/claude-work"},
	} {
		if got := noticeMain(c.spec, c.scope); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// A port is free only when nothing answers on it and it can be bound on the loopback and
// on every IPv4 address: a wildcard listener holds it too.
func TestPortFree(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "0.0.0.0"} {
		l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			t.Fatal(err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		if portFree(port) {
			t.Errorf("a port held on %s reads as free", host)
		}
		l.Close()
		// (Windows may keep a port that was connected to a while longer.)
		if runtime.GOOS != "windows" && !portFree(port) {
			t.Errorf("a port let go on %s reads as taken", host)
		}
	}
}

// A new OpenCode session runs a program of the running service's version, as a resume
// does (decision D2): with none here, it is refused until the person agrees, in the
// default folder and in an added one (asked there with the folder's variables). The
// version is one no desktop app here bundles.
func TestNewOpenCodeSessionAsksBeforeRestartingTheService(t *testing.T) {
	_, log := cloudEnv(t, "opencode")
	for _, k := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "OPENCODE_CONFIG_DIR", "OPENCODE_DB", "FAKE_OPENCODE_FAIL"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	if b, err := os.ReadFile("/Applications/OpenCode.app/Contents/Resources/opencode-cli.version"); err == nil && strings.TrimSpace(string(b)) == "2.2.5" {
		t.Skip("the desktop app here bundles opencode 2.2.5")
	}
	t.Setenv("HOPSESH_APP_RUNTIMES", "off")
	home := os.Getenv("HOME")
	os.MkdirAll(filepath.Join(home, ".local", "share", "opencode"), 0o700)
	a := cloudApp(t, all.Registry())
	ctx := context.Background()
	p, err := a.AddAccount(ctx, "", "opencode", "Work", nil, AddAccountOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_OPENCODE_SERVICE", "running")
	t.Setenv("FAKE_OPENCODE_SERVICE_VERSION", "2.2.5")
	for _, profile := range []string{"", p.ID} {
		if _, err := a.NewSessionCommand(ctx, "opencode", profile, home, false); !errors.Is(err, agent.ErrRestartsService) {
			t.Fatalf("folder %q, not asked: %v", profile, err)
		}
		c, err := a.NewSessionCommand(ctx, "opencode", profile, home, true)
		if err != nil || filepath.Base(c.Argv[0]) != "opencode"+exe() || len(c.Argv) != 1 {
			t.Fatalf("folder %q, agreed: %+v %v", profile, c, err)
		}
	}
	// The services were asked about, never started, stopped or replaced.
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "service status") {
		t.Fatalf("no service probe:\n%s", calls)
	}
	for _, never := range []string{"started the background service", "stopped the background service", "replaced the background service"} {
		if strings.Contains(string(calls), never) {
			t.Errorf("%q:\n%s", never, calls)
		}
	}
	t.Setenv("FAKE_OPENCODE_SERVICE_VERSION", "")
	if c, err := a.NewSessionCommand(ctx, "opencode", p.ID, home, false); err != nil || len(c.Argv) != 1 {
		t.Fatalf("same version: %+v %v", c, err)
	}
}

func exe() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// A request from another machine cannot ask to restart the agent's service: the refusal
// says how to agree on this one. Other errors pass as they are.
func TestRemoteRestartSaysHowToAgree(t *testing.T) {
	err := remoteRestart(fmt.Errorf("opening it: %w", agent.ErrRestartsService))
	if !errors.Is(err, agent.ErrRestartsService) || !strings.Contains(err.Error(), "hopsesh open --restart-service") || !strings.Contains(err.Error(), LocalName()) {
		t.Fatalf("%v", err)
	}
	if other := errors.New("x"); remoteRestart(other) != other {
		t.Fatal("another error was changed")
	}
}
