package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/inventory"
)

func (a *app) scanMachines(cmd *cobra.Command, onlyHost string, skipGit bool) []*inventory.Machine {
	ctx, cancel := ctxTimeout(3)
	defer cancel()
	sc := a.scanner()
	sc.SkipGit = skipGit
	var hostsToScan []config.Host
	includeLocal := onlyHost == "" || onlyHost == "local" || onlyHost == "." || onlyHost == inventory.LocalHostName()
	for _, h := range a.cfg.Hosts {
		if onlyHost == "" || h.Name == onlyHost {
			hostsToScan = append(hostsToScan, h)
		}
	}
	if onlyHost != "" && !includeLocal && len(hostsToScan) == 1 && !hostsToScan[0].Allowed {
		a.printf("note: %s is not allowed yet; scanning it once because you named it\n", onlyHost)
		hostsToScan[0].Allowed = true
	}
	if noLocal, _ := cmd.Flags().GetBool("no-local"); noLocal {
		includeLocal = false
	}
	return sc.Scan(ctx, hostsToScan, includeLocal)
}

func lsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List sessions on allowed machines, grouped by repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			host, _ := cmd.Flags().GetString("host")
			repoFilter, _ := cmd.Flags().GetString("repo")
			liveOnly, _ := cmd.Flags().GetBool("live")
			noGit, _ := cmd.Flags().GetBool("no-git")
			limit, _ := cmd.Flags().GetInt("limit")
			machines := a.scanMachines(cmd, host, noGit)
			defer func() {
				for _, m := range machines {
					m.Close()
				}
			}()
			for _, m := range machines {
				filtered := m.Sessions[:0]
				for _, s := range m.Sessions {
					if liveOnly && s.Live == nil {
						continue
					}
					if repoFilter != "" {
						id := ""
						if s.Git != nil {
							id = s.Git.Identity
						}
						if !strings.Contains(strings.ToLower(id+" "+s.CWD), strings.ToLower(repoFilter)) {
							continue
						}
					}
					filtered = append(filtered, s)
				}
				m.Sessions = filtered
			}
			groups := inventory.GroupByRepo(machines, a.localRoots())
			if a.jsonOut {
				return a.emitJSON(map[string]any{"machines": machines, "groups": groups})
			}
			for _, m := range machines {
				line := fmt.Sprintf("%s: %d session(s)", m.Name, len(m.Sessions))
				if m.Local {
					line += " (this machine)"
				}
				if m.Status != inventory.StatusOK && m.Status != inventory.StatusLocal {
					line = fmt.Sprintf("%s: %s", m.Name, m.Status)
					if m.Hint != "" {
						line += " — " + m.Hint
					}
				}
				a.printf("%s\n", line)
			}
			a.printf("\n")
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
				a.printf("▾ %s\n", head)
				tw := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
				n := 0
				for _, e := range g.Entries {
					if limit > 0 && n >= limit {
						fmt.Fprintf(tw, "  …\t%d more\t\t\t\t\n", len(g.Entries)-limit)
						break
					}
					n++
					s := e.Session
					fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", e.Machine, truncate(s.Title, 40), statusOf(s), ago(s.LastActivity), shortID(s.ID), truncate(s.CWD, 48))
					if len(e.Copies) > 1 {
						fmt.Fprintf(tw, "  \t  %s\t\t\t\t\n", copiesLine(e))
					}
					if bi := branchInfo(s.Git); bi != "" || s.LastPrompt != "" {
						fmt.Fprintf(tw, "  \t  %s\t\t\t\t%s\n", truncate("“"+s.LastPrompt+"”", 60), bi)
					}
				}
				tw.Flush()
				a.printf("\n")
			}
			a.printf("Move one here: hopsesh pull <machine>:<id-or-title>\n")
			return nil
		},
	}
	cmd.Flags().String("host", "", "only this machine (\"local\" for this one)")
	cmd.Flags().String("repo", "", "only sessions whose repository or path contains this")
	cmd.Flags().Bool("live", false, "only running sessions")
	cmd.Flags().Bool("no-git", false, "skip the git probe (faster)")
	cmd.Flags().Bool("no-local", false, "skip this machine")
	cmd.Flags().Int("limit", 8, "sessions shown per repository (0 = all)")
	cmd.Flags().Bool("json", false, "output JSON")
	return cmd
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func showCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <machine>:<id-or-title>",
		Short: "Show one session's details, repository, branch and worktree state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			m, s, err := a.resolveSession(cmd, args[0])
			if m != nil {
				defer m.Close()
			}
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.emitJSON(map[string]any{"machine": m.Name, "session": s})
			}
			a.printf("%s\n", s.Title)
			a.printf("  machine      %s (%s)\n", m.Name, m.Facts.OS)
			a.printf("  id           %s\n", s.ID)
			a.printf("  status       %s, last active %s\n", statusOf(s), ago(s.LastActivity))
			a.printf("  directory    %s\n", s.CWD)
			if s.LastCWD != "" && s.LastCWD != s.CWD {
				a.printf("  last in      %s\n", s.LastCWD)
			}
			a.printf("  last prompt  “%s”\n", s.LastPrompt)
			a.printf("  size         %d KB, %d subagent(s), Claude Code %s\n", s.Size/1024, s.Subagents, s.Version)
			if g := s.Git; g != nil && g.IsRepo {
				a.printf("  repository   %s\n", nonEmpty(g.Remote, "(no remote)"))
				a.printf("  branch       %s\n", branchInfo(g))
				if g.LinkedWorktree {
					a.printf("  worktree of  %s (main folder on %s)\n", g.MainWorktree, nonEmpty(g.MainBranch, "detached"))
				}
				for _, w := range g.Worktrees {
					label := w.Branch
					if w.Detached {
						label = "detached"
					}
					mark := " "
					if w.Main {
						mark = "*"
					}
					a.printf("    %s %-24s %s\n", mark, label, w.Path)
				}
			}
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "output JSON")
	return cmd
}

// resolveSession finds exactly one session from "<machine>:<query>" (or a bare query
// searched on every allowed machine).
func (a *app) resolveSession(cmd *cobra.Command, ref string) (*inventory.Machine, *inventory.Session, error) {
	host, query := a.splitRef(ref)
	machines := a.scanMachines(cmd, host, false)
	type hit struct {
		m *inventory.Machine
		s *inventory.Session
	}
	var hits []hit
	var keep *inventory.Machine
	for _, m := range machines {
		found, _ := m.Find(query)
		for _, s := range found {
			hits = append(hits, hit{m, s})
		}
	}
	// Without a machine name, copies of one session on several machines (it was moved)
	// resolve to the newest copy: the way to bring a session back.
	if host == "" && len(hits) > 1 {
		same := true
		for _, h := range hits[1:] {
			if h.s.ID != hits[0].s.ID {
				same = false
			}
		}
		if same {
			ms := make([]*inventory.Machine, len(hits))
			ss := make([]*inventory.Session, len(hits))
			for i, h := range hits {
				ms[i], ss[i] = h.m, h.s
			}
			pick := hits[inventory.NewestOf(ms, ss)]
			if pick.m.Local {
				for _, m := range machines {
					m.Close()
				}
				return nil, nil, fmt.Errorf("the newest copy of %q is already on this machine (%s, %s); resume it with:\n  cd %s && claude --resume %s",
					pick.s.Title, pick.s.CWD, ago(pick.s.LastActivity), shellQuoteArg(pick.s.CWD), pick.s.ID)
			}
			hits = []hit{pick}
		}
	}
	for _, m := range machines {
		if len(hits) == 1 && hits[0].m == m {
			keep = m
			continue
		}
		m.Close()
	}
	switch len(hits) {
	case 0:
		var problems []string
		for _, m := range machines {
			if m.Status != inventory.StatusOK && m.Status != inventory.StatusLocal {
				problems = append(problems, m.Name+": "+m.Status)
			}
		}
		msg := "no session matches " + query
		if len(problems) > 0 {
			msg += " (unreachable: " + strings.Join(problems, ", ") + ")"
		}
		return nil, nil, fmt.Errorf("%s", msg)
	case 1:
		return keep, hits[0].s, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d sessions match %q; be more specific:\n", len(hits), query)
	for _, h := range hits {
		fmt.Fprintf(&b, "  %s:%s  %s  (%s)\n", h.m.Name, shortID(h.s.ID), h.s.Title, ago(h.s.LastActivity))
	}
	return nil, nil, fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}

// copiesLine describes the copies of a session that was moved between machines.
func copiesLine(e inventory.GroupedEntry) string {
	var parts []string
	staleHere := false
	for _, c := range e.Copies {
		p := c.Machine
		if c.Local {
			p += " (this machine)"
		}
		switch {
		case c.Newest:
			p += ": newest"
		case c.MovedTo != "":
			p += ": moved to " + c.MovedTo
		default:
			p += ": older copy"
		}
		if !c.Newest && c.Local {
			staleHere = true
		}
		parts = append(parts, p)
	}
	line := "copies: " + strings.Join(parts, ", ")
	if staleHere {
		line += "  → bring it back: hopsesh pull " + shortID(e.Session.ID)
	}
	return line
}

func shellQuoteArg(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@,+~", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
