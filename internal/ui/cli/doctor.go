package cli

import (
	"context"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/lnp"
)

func doctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor [machine]",
		Short: "Check the agents here, SSH access and host trust, and the skill",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			defer r.app.Catalog.Close()
			type check struct {
				Name   string `json:"name"`
				OK     bool   `json:"ok"`
				Detail string `json:"detail"`
			}
			var checks []check
			add := func(name string, ok bool, detail string) { checks = append(checks, check{name, ok, detail}) }
			ctx, cancel := ctxTimeout(2)
			defer cancel()
			o := app.ScanOptions{Hosts: []string{app.LocalName()}, SkipGit: true}
			if len(args) == 1 {
				o.Hosts = append(o.Hosts, args[0])
				if h := r.app.Cfg.FindHost(args[0]); h == nil {
					r.app.Cfg.Hosts = append(r.app.Cfg.Hosts, configHost(args[0]))
				} else {
					h.Allowed = true
				}
			}
			inv := r.app.Scan(ctx, o)
			defer inv.Close()
			for _, m := range inv.Machines {
				where := m.Name
				if m.Local {
					where = "this machine"
				}
				if m.Status != app.StatusOK {
					add(where+": ssh + sftp", false, m.Status+": "+nonEmpty(m.Hint, m.Error))
					continue
				}
				if !m.Local {
					add(where+": ssh + sftp", true, "reached ("+m.OS+")")
				}
				for _, a := range m.Agents {
					if mod, ok := r.app.Module(a.Agent); ok && len(mod.Spec().Roots) == 0 {
						continue // a cloud-only module: `hopsesh clouds` checks its driver
					}
					spec := ""
					if mod, ok := r.app.Module(a.Agent); ok && a.Install.Version != "" && !mod.Spec().TestedWith(a.Install.Version) {
						spec = " (not tested with hopsesh yet)"
					}
					switch {
					case a.Error != "":
						add(where+": "+a.Name, false, a.Error)
					case a.Install.Binary != "":
						add(where+": "+a.Name, true, a.Install.Version+spec+" · "+a.Install.Binary)
					case a.Install.Present:
						add(where+": "+a.Name, true, "sessions found, but the program is not on the PATH of non-interactive SSH sessions (listing still works)")
					default:
						add(where+": "+a.Name, true, "not installed")
					}
				}
			}
			_, gitErr := exec.LookPath("git")
			add("this machine: git", gitErr == nil, map[bool]string{true: "found", false: "not found (needed to match and clone repositories)"}[gitErr == nil])
			add("this machine: repos folder", true, r.app.Cfg.ReposDir)
			files, bin := r.skillFiles()
			rep := r.app.Skill(context.Background(), files, bin)
			add("skill", rep.State == integrate.Current, skillLine(rep))
			if lnp.Gated() {
				if who := lnp.Responsible(); who == "" {
					add("this machine: local network", true, "always allowed from Terminal and SSH sessions")
				} else {
					add("this machine: local network", true, "machines on this network need "+who+" turned on in System Settings → Privacy & Security → Local Network (Tailscale addresses don't)")
				}
			}
			if r.jsonOut {
				return r.emitJSON(checks)
			}
			for _, c := range checks {
				mark := "✓"
				if !c.OK {
					mark = "✗"
				}
				r.printf("%s %-30s %s\n", mark, c.Name, c.Detail)
			}
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "output JSON")
	return cmd
}

// configHost is a machine named only on the command line.
func configHost(dest string) config.Host {
	return config.Host{Name: dest, Destination: dest, Allowed: true}
}
