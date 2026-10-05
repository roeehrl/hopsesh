package winshim

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeEnv is a Windows machine: the shims in testdata as if they were in dir, the files
// that exist there, and node on PATH (or not).
func fakeEnv(t *testing.T, files map[string]bool, node string) Env {
	t.Helper()
	return Env{
		Read: func(p string) ([]byte, error) {
			return os.ReadFile(filepath.Join("testdata", p[strings.LastIndex(p, `\`)+1:]))
		},
		Exists: func(p string) bool { return files[strings.ToLower(p)] },
		LookPath: func(name string) (string, error) {
			if name == "node" && node != "" {
				return node, nil
			}
			return "", errors.New("not found")
		},
	}
}

const npmDir = `C:\Users\alice\AppData\Roaming\npm`

func TestResolveShims(t *testing.T) {
	sysNode := `C:\Program Files\nodejs\node.exe`
	codexJS := npmDir + `\node_modules\@openai\codex\bin\codex.js`
	for _, tc := range []struct {
		name  string
		shim  string
		files []string
		node  string
		want  []string
	}{
		{"npm, node on PATH", "npm-node.cmd", []string{codexJS}, sysNode, []string{sysNode, codexJS, "exec", "--json"}},
		{"npm, node beside the shim", "npm-node.cmd", []string{codexJS, npmDir + `\node.exe`}, sysNode, []string{npmDir + `\node.exe`, codexJS, "exec", "--json"}},
		{"npm, a native program", "npm-exe.cmd", []string{npmDir + `\node_modules\@example\tool\bin\tool.exe`}, "", []string{npmDir + `\node_modules\@example\tool\bin\tool.exe`, "exec", "--json"}},
		{"npm 6, node on PATH", "npm6.cmd", nil, sysNode, []string{sysNode, npmDir + `\node_modules\@anthropic-ai\claude-code\cli.js`, "exec", "--json"}},
		{"npm 6, node beside the shim", "npm6.cmd", []string{npmDir + `\node.exe`}, "", []string{npmDir + `\node.exe`, npmDir + `\node_modules\@anthropic-ai\claude-code\cli.js`, "exec", "--json"}},
		{"pnpm", "pnpm.cmd", nil, sysNode, []string{sysNode, npmDir + `\global\5\node_modules\@openai\codex\bin\codex.js`, "exec", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]bool{}
			for _, f := range tc.files {
				files[strings.ToLower(f)] = true
			}
			got, err := Program([]string{npmDir + `\` + tc.shim, "exec", "--json"}, fakeEnv(t, files, tc.node))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// Anything that is not a known shim is refused, saying why; a program that is not a batch
// file is left as it is.
func TestRefusedShims(t *testing.T) {
	env := fakeEnv(t, map[string]bool{}, `C:\node\node.exe`)
	for shim, want := range map[string]string{
		"nothing.cmd":  "passes no arguments on",
		"othervar.cmd": `uses "%APPDATA%`,
		"chain.cmd":    "runs call, which is not there",
		"npm-exe.cmd":  "tool.exe, which is not there",
	} {
		_, err := Program([]string{npmDir + `\` + shim}, env)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", shim, err, want)
		}
	}
	argv := []string{`C:\Program Files\Claude\claude.exe`, "--resume", "x"}
	if got, err := Program(argv, env); err != nil || !slices.Equal(got, argv) {
		t.Errorf("an .exe changed: %q %v", got, err)
	}
	// A shim whose program is another batch file.
	chain := Env{Read: func(string) ([]byte, error) { return []byte("\"%~dp0\\inner.cmd\" %*\r\n"), nil },
		Exists: func(string) bool { return true }, LookPath: env.LookPath}
	if _, err := Resolve(npmDir+`\outer.cmd`, nil, chain); err == nil || !strings.Contains(err.Error(), "another batch file") {
		t.Errorf("chained shim: %v", err)
	}
}

func TestWinClean(t *testing.T) {
	for in, want := range map[string]string{
		`C:\a\\b\.\c\..\d`:    `C:\a\b\d`,
		`C:\a/b/../../..\x`:   `C:\x`,
		`\\server\share\a\..`: `\\server\share`,
		`rel\..\..\x`:         `..\x`,
	} {
		if got := winClean(in); got != want {
			t.Errorf("winClean(%q) = %q, want %q", in, got, want)
		}
	}
}

// On Windows a bare claude is found on PATH as npm's claude.cmd and runs as node with
// Claude Code's script, its arguments passed on as they are (never through cmd.exe);
// elsewhere, and for a program that is not a batch file, nothing changes.
func TestCommand(t *testing.T) {
	sysNode := `C:\Program Files\nodejs\node.exe`
	cli := npmDir + `\node_modules\@anthropic-ai\claude-code\cli.js`
	env := fakeEnv(t, map[string]bool{strings.ToLower(cli): true}, sysNode)
	look := env.LookPath
	env.LookPath = func(name string) (string, error) {
		if name == "claude" {
			return npmDir + `\claude.cmd`, nil
		}
		return look(name)
	}
	brief := `[hopsesh] fix "the bug" & say 100% done`
	want := []string{sysNode, cli, "--cloud", brief}
	for _, argv := range [][]string{{"claude", "--cloud", brief}, {npmDir + `\claude.cmd`, "--cloud", brief}} {
		in := slices.Clone(argv)
		got, err := Command(argv, "windows", env)
		if err != nil {
			t.Fatalf("%s: %v", argv[0], err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s:\n got  %q\n want %q", argv[0], got, want)
		}
		if !slices.Equal(argv, in) {
			t.Errorf("the caller's argv changed: %q", argv)
		}
	}
	exe := []string{`C:\Users\alice\.local\bin\claude.exe`, "--cloud", brief}
	if got, err := Command(exe, "windows", env); err != nil || !slices.Equal(got, exe) {
		t.Errorf("a program that is not a batch file: %q %v", got, err)
	}
	for _, goos := range []string{"darwin", "linux"} {
		argv := []string{"claude", "--cloud", brief}
		if got, err := Command(argv, goos, env); err != nil || !slices.Equal(got, argv) {
			t.Errorf("%s: %q %v", goos, got, err)
		}
	}
	// A shim hopsesh cannot read is refused, not run.
	if _, err := Command([]string{npmDir + `\othervar.cmd`}, "windows", env); !errors.Is(err, ErrUnknownShim) {
		t.Errorf("an unknown shim: %v", err)
	}
}
