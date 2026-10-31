package cli

import (
	"context"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/lnp"
	"github.com/roeehrl/hopsesh/sdk/agent"
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
			all, _ := cmd.Flags().GetBool("all-machines")
			if all {
				o.Hosts = nil // every allowed machine
			} else if len(args) == 1 {
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
					switch m.Presence {
					case app.PresenceLogin:
						add(where+": hopsesh", true, m.Hopsesh+" in the login session")
					case app.PresenceInstalled:
						add(where+": hopsesh", false, m.Hopsesh+" there, not in a login session: nobody is logged in, or run hopsesh hosts setup "+m.Name)
					case app.PresenceOlder:
						add(where+": hopsesh", false, m.Hopsesh+" is older than this one: hopsesh hosts setup "+m.Name+" updates it")
					default:
						add(where+": hopsesh", false, "not there: sessions are read over SSH only; hopsesh hosts setup "+m.Name+" sets it up")
					}
				}
				for _, a := range m.Agents {
					if mod, ok := r.app.Module(a.Agent); ok && len(mod.Spec().Roots) == 0 {
						continue // a cloud-only module: `hopsesh clouds` checks its driver
					}
					spec := ""
					if mod, ok := r.app.Module(a.Agent); ok && a.Install.Version != "" && !mod.Spec().TestedWith(a.Install.Version) {
						spec = " (not tested with hopsesh yet)"
					}
					// One line per account folder: name the ones besides the default.
					name := a.Name
					if p := a.Install.Profile; p != nil && !p.Default && p.Name != "" {
						name += " · " + p.Name
					}
					switch {
					case a.Error != "":
						add(where+": "+name, false, a.Error)
					case a.Install.Binary != "":
						runtime := ""
						if a.Install.Runtime == agent.RuntimeApp {
							runtime = " (built into its desktop app)"
						}
						add(where+": "+name, true, a.Install.Version+spec+runtime+" · "+a.Install.Binary)
					case a.Install.Present:
						add(where+": "+name, true, "sessions found, but the program is not on the PATH of non-interactive SSH sessions (listing still works)")
					default:
						add(where+": "+name, true, "not installed")
					}
				}
			}
			_, gitErr := exec.LookPath("git")
			add("this machine: git", gitErr == nil, map[bool]string{true: "found", false: "not found (needed to match and clone repositories)"}[gitErr == nil])
			add("this machine: repos folder", true, r.app.Cfg.ReposDir)
			if c := app.DetectCompanion(); c.Supported {
				// Optional: never a failed check.
				detail := "not installed (optional: control this Mac from your iPhone or iPad, " + c.Page + ")"
				switch {
				case c.Running:
					detail = c.Name + " Host runs here" + map[bool]string{true: " · " + c.Version, false: ""}[c.Version != ""]
				case c.Installed:
					detail = c.Name + " Host is installed, not running"
				}
				add("this machine: phone ("+c.Name+")", true, detail)
			}
			files, bin := r.skillFiles()
			rep := r.app.Skill(context.Background(), files, bin)
			add("skill", rep.State == integrate.Current, skillLine(rep))
			// Protection on each machine: this one here; the others through hopsesh there.
			addProtection := func(where string, p app.Protection) {
				for _, h := range p.Hooks {
					name := where + ": " + string(h.Agent) + " hook"
					if h.ProfileLabel != "" {
						name += " (" + h.ProfileLabel + ")"
					}
					switch {
					case !h.Supported:
						add(name, false, h.Reason)
					case !h.Installed:
						add(name, false, "not installed")
					case h.Trust != nil && h.Trust.State != agent.HookTrusted && h.Trust.State != agent.HookUnknown:
						add(name, false, h.Trust.State+": "+nonEmpty(h.Trust.Fix, h.Trust.Detail))
					default:
						add(name, true, "installed")
					}
				}
				if !p.Local {
					add(where+": skill", p.Skill.State == integrate.Current, skillLine(p.Skill))
				}
			}
			pctx, pcancel := ctxTimeout(1)
			addProtection("this machine", r.app.LocalProtection(pctx))
			pcancel()
			for _, m := range inv.Machines {
				if m.Local || m.Presence != app.PresenceLogin {
					continue
				}
				pctx, pcancel := ctxTimeout(1)
				p, err := r.app.MachineProtection(pctx, m.Name)
				pcancel()
				if err != nil {
					add(m.Name+": protection", false, err.Error())
					continue
				}
				addProtection(m.Name, p)
			}
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
	cmd.Flags().Bool("all-machines", false, "check every allowed machine: hopsesh, its protection hooks and the skill there")
	return cmd
}

// configHost is a machine named only on the command line.
func configHost(dest string) config.Host {
	return config.Host{Name: dest, Destination: dest, Allowed: true}
}
