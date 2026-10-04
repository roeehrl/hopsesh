package fakecloud

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Programs are the stand-in claude, codex and fakecloud for agenttest.FakeHost.Programs
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
			p := Proc{Args: argv[1:], Vars: v, Dir: o.Dir, Stdout: &out, Stderr: &errOut}
			code := main(p)
			return agent.Result{Stdout: out.Bytes(), Stderr: errOut.Bytes(), Code: code}
		}
	}
	return map[string]func([]string, agent.RunOptions) agent.Result{
		"claude": run(func(p Proc) int {
			p.Log("claude " + strings.Join(p.Args, " "))
			if len(p.Args) == 1 && p.Args[0] == "--version" {
				fmt.Fprintln(p.Stdout, "2.1.284 (Claude Code)")
				return 0
			}
			_, code := Claude(p)
			return code
		}),
		"codex": run(func(p Proc) int {
			p.Log("codex " + strings.Join(p.Args, " "))
			if len(p.Args) == 1 && p.Args[0] == "--version" {
				fmt.Fprintln(p.Stdout, "codex-cli 0.153.2")
				return 0
			}
			_, code := Codex(p)
			return code
		}),
		"fakecloud": run(Main),
	}
}
