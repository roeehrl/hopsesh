package cli

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func lsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List sessions of every agent on this and the allowed machines, grouped by repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			host, _ := cmd.Flags().GetString("host")
			agentID, _ := cmd.Flags().GetString("agent")
			repo, _ := cmd.Flags().GetString("repo")
			liveOnly, _ := cmd.Flags().GetBool("live")
			noGit, _ := cmd.Flags().GetBool("no-git")
			limit, _ := cmd.Flags().GetInt("limit")
			inv := r.scan(cmd, host, noGit)
			defer inv.Close()
			kept := inv.Entries[:0]
			for _, e := range inv.Entries {
				switch {
				case host != "" && host != "local" && host != "." && e.Machine != host:
				case agentID != "" && string(e.Agent) != agentID:
				case liveOnly && e.Live.State != agent.Live:
				case repo != "" && !strings.Contains(strings.ToLower(identity(e)+" "+e.Session.CWD), strings.ToLower(repo)):
				default:
					kept = append(kept, e)
				}
			}
			inv.Entries = kept
			groups := inv.Groups(r.app.LocalRoots())
			if r.jsonOut {
				return r.emitJSON(map[string]any{"machines": inv.Machines, "groups": groups})
			}
			for _, m := range inv.Machines {
				r.printf("%s\n", machineLine(m, inv))
			}
			r.printf("\n")
			for _, g := range groups {
				head := g.Name
				if g.Remote != "" {
					head += "  " + g.Remote
				}
				switch {
				case g.Local != "":
					head += "  [here: " + g.Local + "]"
				case g.Identity != "" && !strings.HasPrefix(g.Identity, "local:"):
					head += "  [not cloned here]"
				}
				r.printf("▾ %s\n", head)
				tw := tabwriter.NewWriter(r.out, 0, 2, 2, ' ', 0)
				for n, it := range g.Items {
					if limit > 0 && n >= limit {
						fmt.Fprintf(tw, "  …\t%d more\t\t\t\t\t\n", len(g.Items)-limit)
						break
					}
					e := it.Entry
					s := e.Session
					fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\n", e.Machine, e.AgentName, truncate(s.Title, 40), e.Status(), ago(s.LastActivity), shortID(s.Key.Session), truncate(s.CWD, 44))
					if len(it.Copies) > 1 {
						fmt.Fprintf(tw, "  \t\t  %s\t\t\t\t\n", copiesLine(it))
					}
					if bi := branchInfo(e.Git); bi != "" || s.LastPrompt != "" {
						fmt.Fprintf(tw, "  \t\t  %s\t\t\t\t%s\n", truncate("“"+s.LastPrompt+"”", 60), bi)
					}
				}
				tw.Flush()
				r.printf("\n")
			}
			r.printf("Move one here: hopsesh pull [<machine>:]<id-or-title>   Continue in another agent: add --in <%s>\n", strings.Join(agentIDs(), "|"))
			r.offerSkill()
			return nil
		},
	}
	cmd.Flags().String("host", "", "only this machine (\"local\" for this one)")
	cmd.Flags().String("agent", "", "only this agent ("+strings.Join(agentIDs(), ", ")+")")
	cmd.Flags().String("repo", "", "only sessions whose repository or path contains this")
	cmd.Flags().Bool("live", false, "only open sessions")
	cmd.Flags().Bool("no-git", false, "skip the git probe (faster)")
	cmd.Flags().Bool("no-local", false, "skip this machine")
	cmd.Flags().Int("limit", 8, "sessions shown per repository (0 = all)")
	cmd.Flags().Bool("json", false, "output JSON")
	return cmd
}

func agentIDs() []string {
	var out []string
	if modules == nil {
		return out
	}
	for _, id := range modules.IDs() {
		out = append(out, string(id))
	}
	return out
}

func identity(e app.Entry) string {
	if e.Git != nil {
		return e.Git.Identity
	}
	return ""
}

func machineLine(m *app.Machine, inv *app.Inventory) string {
	if m.Status != app.StatusOK {
		line := fmt.Sprintf("%s: %s", m.Name, m.Status)
		if m.Hint != "" {
			line += " — " + m.Hint
		} else if m.Error != "" {
			line += " — " + m.Error
		}
		return line
	}
	n := 0
	for _, e := range inv.Entries {
		if e.Machine == m.Name {
			n++
		}
	}
	line := fmt.Sprintf("%s: %d session(s)", m.Name, n)
	if m.Local {
		line += " (this machine)"
	}
	var agents []string
	for _, a := range m.Agents {
		switch {
		case a.Install.Present:
			agents = append(agents, strings.TrimSpace(a.Name+" "+a.Install.Version))
		case a.Install.Binary != "":
			agents = append(agents, a.Name+" (no sessions yet)")
		}
	}
	if len(agents) > 0 {
		line += " · " + strings.Join(agents, ", ")
	}
	return line
}

// copiesLine describes where else a session is.
func copiesLine(it app.Item) string {
	var parts []string
	behindHere := false
	for _, c := range it.Copies {
		p := c.Machine
		if c.Local {
			p += " (this machine)"
		}
		p += " " + c.AgentName
		switch {
		case c.Newest:
			p += ": newest"
		case c.Mark != nil:
			p += ": " + app.MarkWords(*c.Mark)
		default:
			p += ": older copy"
		}
		if !c.Newest && c.Local {
			behindHere = true
		}
		parts = append(parts, p)
	}
	line := "copies: " + strings.Join(parts, ", ")
	if behindHere {
		line += "  → bring it back: hopsesh pull " + shortID(it.Entry.Session.Key.Session)
	}
	return line
}

func showCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show [<machine>:][<agent>/]<id-or-title>",
		Short: "Show one session: where it is, its repository, branch, worktree and lineage",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			ref := app.ParseRef(args[0])
			inv := r.scan(cmd, ref.Machine, false)
			defer inv.Close()
			e, err := inv.Find(ref)
			if err != nil {
				return err
			}
			if r.jsonOut {
				return r.emitJSON(e)
			}
			s := e.Session
			r.printf("%s\n", s.Title)
			r.printf("  machine      %s\n", e.Machine)
			r.printf("  agent        %s %s\n", e.AgentName, s.AgentVersion)
			r.printf("  session      %s\n", s.Key)
			r.printf("  status       %s, last active %s\n", e.Status(), ago(s.LastActivity))
			r.printf("  directory    %s\n", s.CWD)
			r.printf("  last prompt  “%s”\n", s.LastPrompt)
			r.printf("  size         %d KB", s.Size/1024)
			if s.Subagents > 0 {
				r.printf(", %d subagent(s)", s.Subagents)
			}
			r.printf("\n")
			if g := e.Git; g != nil && g.IsRepo {
				r.printf("  repository   %s\n", nonEmpty(g.Remote, "(no remote)"))
				r.printf("  branch       %s\n", branchInfo(g))
				if g.LinkedWorktree {
					r.printf("  worktree of  %s (main folder on %s)\n", g.MainWorktree, nonEmpty(g.MainBranch, "detached"))
				}
			}
			if l := e.Lineage; l != nil {
				r.printf("  lineage      %d cop(ies), %d hop(s)\n", len(l.Replicas), len(l.Hops))
				hops := append(l.Hops[:0:0], l.Hops...)
				sort.Slice(hops, func(i, j int) bool { return hops[i].Time.Before(hops[j].Time) })
				for _, h := range hops {
					from, to := l.Replicas[h.From], l.Replicas[h.To]
					r.printf("    %s  %s %s → %s %s (%s)\n", h.Time.Local().Format("2 Jan 15:04"), from.Location, from.Key, to.Location, to.Key, h.Kind)
				}
			}
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "output JSON")
	return cmd
}

func agentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "List the agents hopsesh supports and what is installed on this machine",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxTimeout(1)
			defer cancel()
			inv := r.app.Scan(ctx, app.ScanOptions{Hosts: []string{app.LocalName()}, SkipGit: true})
			defer inv.Close()
			type row struct {
				ID           agent.ID           `json:"id"`
				Name         string             `json:"name"`
				Vendor       string             `json:"vendor"`
				Stability    agent.Stability    `json:"stability"`
				Tested       []string           `json:"tested"`
				Capabilities []agent.Capability `json:"capabilities"`
				Enabled      bool               `json:"enabled"`
				Install      *agent.Install     `json:"install,omitempty"`
			}
			var rows []row
			here := inv.Local()
			for _, m := range r.app.Reg.All() {
				s := m.Spec()
				rw := row{ID: s.ID, Name: s.Name, Vendor: s.Vendor, Stability: s.Stability, Tested: s.Tested,
					Capabilities: agent.Capabilities(m), Enabled: r.app.Cfg.AgentEnabled(string(s.ID))}
				if here != nil {
					for _, a := range here.Agents {
						if a.Agent == s.ID {
							in := a.Install
							rw.Install = &in
						}
					}
				}
				rows = append(rows, rw)
			}
			if r.jsonOut {
				return r.emitJSON(rows)
			}
			tw := tabwriter.NewWriter(r.out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "AGENT\tNAME\tHERE\tDATA FOLDER\tCAN")
			for _, rw := range rows {
				here, folder := "not installed", ""
				if rw.Install != nil {
					switch {
					case rw.Install.Binary != "":
						here = rw.Install.Version
					case rw.Install.Present:
						here = "data only"
					}
					for _, p := range rw.Install.Roots {
						folder = p
					}
				}
				if !rw.Enabled {
					here += " (disabled)"
				}
				caps := make([]string, len(rw.Capabilities))
				for i, c := range rw.Capabilities {
					caps[i] = string(c)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", rw.ID, rw.Name, here, folder, strings.Join(caps, " "))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().Bool("json", false, "output JSON")
	return cmd
}
