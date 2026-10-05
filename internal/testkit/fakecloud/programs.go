package fakecloud

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/term"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The version lines the stand-in claude and codex print for --version: the versions of
// the agents' newest fixtures (agents/<id>/testdata/<version>).
const (
	ClaudeVersionLine = "2.1.284 (Claude Code)"
	CodexVersionLine  = "codex-cli 0.153.2"
)

// Programs are the stand-in claude, codex, gh, jules, devin, amp and fakecloud for
// agenttest.FakeHost.Programs
// (and agenttest.RunCloud): they answer --version and the cloud verbs, in this process,
// with the store in dir. A run's RunOptions.Env (FAKE_CLOUD_FAIL, say) is its environment
// on top of vars, without its RunOptions.Unset.
func Programs(dir string, vars map[string]string) map[string]func(argv []string, o agent.RunOptions) agent.Result {
	run := func(main func(Proc) int) func([]string, agent.RunOptions) agent.Result {
		return func(argv []string, o agent.RunOptions) agent.Result {
			v := map[string]string{"FAKE_CLOUD_DIR": dir}
			for k, x := range vars {
				v[k] = x
			}
			for _, k := range o.Unset {
				v[k] = "" // as unset: Proc.Env reads Vars before the process's own environment
			}
			for _, kv := range o.Env {
				if k, x, ok := strings.Cut(kv, "="); ok {
					v[k] = x
				}
			}
			var out, errOut bytes.Buffer
			p := Proc{Args: argv[1:], Vars: v, Dir: o.Dir, Stdin: bytes.NewReader(o.Stdin), Stdout: &out, Stderr: &errOut}
			code := main(p)
			return agent.Result{Stdout: out.Bytes(), Stderr: errOut.Bytes(), Code: code}
		}
	}
	return map[string]func([]string, agent.RunOptions) agent.Result{
		"claude": run(func(p Proc) int {
			p.Log("claude " + strings.Join(p.Args, " "))
			if len(p.Args) == 1 && p.Args[0] == "--version" {
				fmt.Fprintln(p.Stdout, ClaudeVersionLine)
				return 0
			}
			_, code := Claude(p)
			return code
		}),
		"codex": run(func(p Proc) int {
			p.Log("codex " + strings.Join(p.Args, " "))
			if len(p.Args) == 1 && p.Args[0] == "--version" {
				fmt.Fprintln(p.Stdout, CodexVersionLine)
				return 0
			}
			_, code := Codex(p)
			return code
		}),
		"gh":        run(logged("gh", Gh)),
		"jules":     run(logged("jules", Jules)),
		"devin":     run(logged("devin", Devin)),
		"amp":       run(logged("amp", Amp)),
		"fakecloud": run(Main),
	}
}

// logged logs a call to a stand-in vendor CLI before answering it.
func logged(name string, main func(Proc) int) func(Proc) int {
	return func(p Proc) int {
		p.Log(name + " " + strings.Join(p.Args, " "))
		return main(p)
	}
}

// Vendor is the stand-in program for a second-wave vendor CLI by name (gh, jules, devin,
// amp), logged; nil for any other name.
func Vendor(name string) func(Proc) int {
	m := map[string]func(Proc) int{"gh": Gh, "jules": Jules, "devin": Devin, "amp": Amp}[name]
	if m == nil {
		return nil
	}
	return logged(name, m)
}

// TerminalStep runs a terminal step (a SendCloud's Sent.Run) with the stand-in claude in
// this process, as at a terminal 100 columns wide whose user types typed (nil: nothing),
// with the store in dir: for agenttest.CloudOptions.Step. Like Programs, the command's Env
// goes on top of vars, without its Unset.
func TerminalStep(dir string, vars map[string]string, typed io.Reader) func(agent.Command) agent.StepOutput {
	return func(c agent.Command) agent.StepOutput {
		v := map[string]string{"FAKE_CLOUD_DIR": dir}
		for k, x := range vars {
			v[k] = x
		}
		for _, k := range c.Unset {
			v[k] = ""
		}
		for _, kv := range c.Env {
			if k, x, ok := strings.Cut(kv, "="); ok {
				v[k] = x
			}
		}
		var out bytes.Buffer
		p := Proc{Args: c.Argv[1:], Vars: v, Dir: c.Dir, Stdout: &out, Stderr: &out, TTY: true, In: typed, Width: 100}
		code := -1
		if strings.TrimSuffix(filepath.Base(c.Argv[0]), ".exe") == "claude" {
			p.Log("claude " + strings.Join(p.Args, " "))
			_, code = Claude(p)
		} else {
			fmt.Fprintf(&out, "the fake has no terminal program %s\n", c.Argv[0])
		}
		return agent.StepOutput{Text: term.Plain(out.Bytes()), Width: 100, Code: code}
	}
}
