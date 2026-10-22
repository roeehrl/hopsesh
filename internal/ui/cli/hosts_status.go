package cli

import (
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
)

// What hopsesh can do on each machine (app.MachineStatus): whether it is installed and as
// new as the newest on your machines, whether it receives sessions, whether its command is
// on PATH, and each agent's hook and skill. Exit 3: a machine needs something.

// exitNeedsAttention is hosts status's exit code when a machine has something to fix.
const exitNeedsAttention = 3

func hostsStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [machine...]",
		Short: "Show what hopsesh can do on each machine: version, receiving, command, hooks and skill",
		Long: `Connects to this machine and every machine you allowed (or the ones you name) and
shows, for each: hopsesh's version against the newest on your machines, whether it runs in
the login session and receives sessions, whether its command is on PATH, and each agent's
hook and skill. Hooks, skill and command are read again every 10 minutes (--refresh: now).

Exit status: 0 when every machine is set, 3 when one needs something (a fix is listed),
1 on an error.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			defer r.app.Catalog.Close()
			refresh, _ := cmd.Flags().GetBool("refresh")
			ctx, cancel := ctxTimeout(4)
			defer cancel()
			o := app.ScanOptions{SkipGit: true}
			for _, a := range args {
				if a != app.LocalName() && r.app.Cfg.FindHost(a) == nil {
					return fmt.Errorf("%s isn't a machine you added (hopsesh hosts lists them)", a)
				}
				o.Hosts = append(o.Hosts, a)
			}
			if len(o.Hosts) > 0 {
				o.Hosts = append(o.Hosts, app.LocalName())
			}
			inv := r.app.Scan(ctx, o)
			defer inv.Close()
			ss := r.app.MachineStatuses(ctx, inv, app.StatusOptions{Refresh: refresh})
			if len(args) > 0 {
				ss = slices.DeleteFunc(ss, func(s app.MachineStatus) bool { return !slices.Contains(args, s.Machine) })
			}
			if r.jsonOut {
				if err := r.emitJSON(ss); err != nil {
					return err
				}
			} else {
				r.printStatuses(ss)
			}
			for _, s := range ss {
				if s.Todo > 0 || s.Reach.State == app.CheckError || s.Hopsesh.State == app.CheckError {
					return exitCode(exitNeedsAttention)
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.Bool("json", false, "output JSON: every check with its state, reason and fix")
	f.Bool("refresh", false, "read hooks, skill and command again now")
	return cmd
}

// mark is a check as a symbol and its words: the symbol is never alone.
func mark(c app.Check) string {
	sym := map[string]string{app.CheckOK: "✓", app.CheckWarn: "↑", app.CheckError: "✕", app.CheckOff: "○", app.CheckUnknown: "?"}[c.State]
	if c.State == app.CheckWarn && c.Fix != app.FixUpdate {
		sym = "!"
	}
	if c.Text == "–" || c.Text == "" {
		return "–"
	}
	return sym + " " + c.Text
}

func (r *run) printStatuses(ss []app.MachineStatus) {
	tw := tabwriter.NewWriter(r.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MACHINE\tHOPSESH\tRECEIVING\tLOGIN\tCLI\tHOOKS\tSKILL\tCHECKED")
	for _, s := range ss {
		name := s.Machine
		if s.Local {
			name += " *"
		}
		ver := mark(s.Hopsesh)
		if s.Hopsesh.State == app.CheckWarn || s.Hopsesh.State == app.CheckError {
			ver = mark(s.Hopsesh) + "→" + s.Newest
		}
		if s.Reach.State == app.CheckError {
			fmt.Fprintf(tw, "%s\t✕ not reached\t\t\t\t\t\t%s\n", name, ago(s.CheckedAt))
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, ver, short(s.Receiving), short(s.Login), short(s.CLI), setups(s, true), setups(s, false), ago(checked(s)))
	}
	tw.Flush()
	var notes []string
	for _, s := range ss {
		notes = append(notes, fixes(s)...)
	}
	if len(notes) > 0 {
		r.printf("\n%s\n", strings.Join(notes, "\n"))
	}
	r.printf("\n* this machine · --json for scripts · --refresh reads hooks, skill and command again\n")
}

func checked(s app.MachineStatus) time.Time {
	if !s.DetailsAt.IsZero() {
		return s.DetailsAt
	}
	return s.CheckedAt
}

// short is a check's mark without the details a table has no room for.
func short(c app.Check) string {
	m := mark(c)
	if i := strings.Index(m, " · "); i > 0 {
		m = m[:i]
	}
	return m
}

// setups sums up every agent folder's hooks (or skills) on a machine: "✓ claude ×2, codex".
func setups(s app.MachineStatus, hooks bool) string {
	if s.Version == "" {
		return "–"
	}
	if len(s.Agents) == 0 {
		return "? not checked"
	}
	worst, names := app.CheckOK, map[string]int{}
	var order []string
	rank := map[string]int{app.CheckOK: 0, app.CheckOff: 1, app.CheckUnknown: 2, app.CheckWarn: 3, app.CheckError: 4}
	for _, a := range s.Agents {
		c := a.Skill
		if hooks {
			c = a.Hook
		}
		if c.Text == "not supported" {
			continue
		}
		if rank[c.State] > rank[worst] {
			worst = c.State
		}
		if names[string(a.Agent)] == 0 {
			order = append(order, string(a.Agent))
		}
		names[string(a.Agent)]++
	}
	var parts []string
	for _, n := range order {
		if names[n] > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", n, names[n]))
		} else {
			parts = append(parts, n)
		}
	}
	sym := map[string]string{app.CheckOK: "✓", app.CheckWarn: "!", app.CheckError: "✕", app.CheckOff: "○", app.CheckUnknown: "?"}[worst]
	if len(parts) == 0 {
		return "–"
	}
	return sym + " " + strings.Join(parts, ", ")
}

// fixes are the lines that say what a machine needs, with the command that does it.
func fixes(s app.MachineStatus) []string {
	var out []string
	add := func(what string, c app.Check, command string) {
		if c.Fix == "" || c.State == app.CheckOK {
			return
		}
		line := fmt.Sprintf("%s %s: %s", s.Machine, what, c.Text)
		if c.Reason != "" {
			line += " (" + c.Reason + ")"
		}
		if command != "" {
			line += "\n  " + command
		}
		out = append(out, line)
	}
	setup := "hopsesh hosts setup " + s.Machine
	if s.Local {
		setup = ""
	}
	add("hopsesh", s.Hopsesh, setup)
	receive := "hopsesh receive on"
	if !s.Local {
		receive = "on " + s.Machine + ": hopsesh receive on"
	}
	add("receiving", s.Receiving, receive)
	add("command", s.CLI, "add the folder that holds hopsesh to your shell's PATH"+map[bool]string{true: "", false: " on " + s.Machine}[s.Local])
	for _, a := range s.Agents {
		where := a.Name
		if a.ProfileLabel != "" {
			where += " · " + a.ProfileLabel
		}
		hooks, skill := "hopsesh notices install", "hopsesh skill install"
		if !s.Local {
			hooks, skill = "Settings › Hooks & skills in the app, or on "+s.Machine+": "+hooks, "on "+s.Machine+": "+skill
		}
		if a.Hook.Fix == app.FixApprove {
			hooks = ""
		}
		add(where+" hook", a.Hook, hooks)
		add(where+" skill", a.Skill, skill)
	}
	return out
}
