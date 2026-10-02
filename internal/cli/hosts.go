package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/claudeskill"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/hosts"
	"github.com/roeehrl/hopsesh/internal/core/lnp"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/integrate"
	"github.com/roeehrl/hopsesh/internal/inventory"
)

func hostsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hosts",
		Short: "List discovered machines and which ones hopsesh may access",
		Long: `Lists machines found in Tailscale and ~/.ssh/config (discovery connects to nothing),
merged with the ones you configured. Only machines you allow are ever contacted.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxTimeout(1)
			defer cancel()
			cands, _ := hosts.Discover(ctx)
			type row struct {
				hosts.Candidate
				Allowed    bool `json:"allowed"`
				Configured bool `json:"configured"`
			}
			var rows []row
			seen := map[string]bool{}
			for _, c := range cands {
				if c.Self {
					continue
				}
				r := row{Candidate: c}
				if h := a.cfg.FindHost(c.Name); h != nil {
					r.Allowed, r.Configured = h.Allowed, true
				}
				seen[c.Name] = true
				rows = append(rows, r)
			}
			for _, h := range a.cfg.Hosts {
				if !seen[h.Name] {
					rows = append(rows, row{Candidate: hosts.Candidate{Name: h.Name, Destination: h.Destination, Via: []string{h.Via}, OS: h.OS}, Allowed: h.Allowed, Configured: true})
				}
			}
			if a.jsonOut {
				return a.emitJSON(rows)
			}
			tw := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ALLOWED\tMACHINE\tSSH DESTINATION\tFOUND VIA\tOS\tTAILSCALE")
			for _, r := range rows {
				allowed := "no"
				if r.Allowed {
					allowed = "yes"
				}
				ts := ""
				if r.Online != nil {
					ts = map[bool]string{true: "online", false: "offline"}[*r.Online]
					if r.OtherOwner {
						ts += " (shared: " + r.Owner + ")"
					}
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", allowed, r.Name, r.Destination, strings.Join(r.Via, "+"), r.OS, ts)
			}
			tw.Flush()
			a.printf("\nAllow machines with: hopsesh hosts allow <machine>...   (add others: hopsesh hosts add <name> <ssh-destination>)\n")
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "output JSON")
	cmd.AddCommand(hostsAllowCmd(true), hostsAllowCmd(false), hostsAddCmd(), hostsHelperCmd())
	return cmd
}

func hostsAllowCmd(allow bool) *cobra.Command {
	use, short := "allow <machine>...", "Allow hopsesh to connect to machines (read-only until you pull)"
	if !allow {
		use, short = "deny <machine>...", "Stop hopsesh from connecting to machines"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxTimeout(1)
			defer cancel()
			cands, _ := hosts.Discover(ctx)
			for _, name := range args {
				h := a.cfg.FindHost(name)
				if h == nil {
					var found *hosts.Candidate
					for i := range cands {
						if cands[i].Name == name || cands[i].DNSName == name {
							found = &cands[i]
						}
					}
					if found == nil {
						return fmt.Errorf("unknown machine %q (see hopsesh hosts, or add it: hopsesh hosts add %s <ssh-destination>)", name, name)
					}
					a.cfg.Hosts = append(a.cfg.Hosts, config.Host{Name: found.Name, Destination: found.Destination, Via: strings.Join(found.Via, "+"), OS: found.OS, TailscaleName: found.DNSName})
					h = a.cfg.FindHost(found.Name)
				}
				if h.TailscaleName == "" {
					for i := range cands {
						if cands[i].Name == h.Name && cands[i].DNSName != "" {
							h.TailscaleName = cands[i].DNSName
						}
					}
				}
				h.Allowed = allow
				a.printf("%s: %s\n", h.Name, map[bool]string{true: "allowed", false: "denied"}[allow])
			}
			return config.Save(a.cfg)
		},
	}
}

func hostsAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <name> <ssh-destination>",
		Short: "Add and allow a machine by its ssh destination (alias, user@host or host)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			a.cfg.UpsertHost(config.Host{Name: args[0], Destination: args[1], Via: "manual", Allowed: true})
			a.printf("%s → %s: added and allowed\n", args[0], args[1])
			return config.Save(a.cfg)
		},
	}
}

func trustCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trust <machine>",
		Short: "Confirm a machine's SSH host key so hopsesh can connect",
		Long: `Fetches the machine's SSH host keys, shows their fingerprints (marked as verified when
they match the keys Tailscale publishes for that machine) and, after you confirm, records
them in hopsesh's own known_hosts. Your ~/.ssh/known_hosts is never modified.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			h := a.cfg.FindHost(args[0])
			dest := args[0]
			if h != nil {
				dest = h.Destination
			}
			conn, err := transport.NewConn(dest, config.StateDir(), a.log)
			if err != nil {
				return err
			}
			if h != nil && h.TailscaleName != "" && h.TailscaleName != dest {
				conn.Fallbacks = []string{h.TailscaleName}
			}
			ctx, cancel := ctxTimeout(1)
			defer cancel()
			keys, r, err := conn.ScanHostKeys(ctx)
			if err != nil {
				return err
			}
			var tsKeys []string
			cands, _ := hosts.Discover(ctx)
			for _, c := range cands {
				if c.Name == args[0] || c.Destination == dest {
					tsKeys = c.SSHHostKeys
				}
			}
			verified := transport.MatchesTailscale(keys, tsKeys)
			a.printf("Host keys of %s (%s:%s):\n", args[0], r.HostName, r.Port)
			for _, k := range keys {
				a.printf("  %-22s %s\n", k.Type, k.Fingerprint)
			}
			home, _ := os.UserHomeDir()
			known := transport.KnownElsewhere(keys, filepath.Join(home, ".ssh", "known_hosts"))
			switch {
			case verified:
				a.printf("✓ Matches the host key Tailscale reports for this machine.\n")
			case len(known) > 0:
				a.printf("✓ Matches a key you already trust in ~/.ssh/known_hosts for: %s\n", strings.Join(known, ", "))
			default:
				a.printf("! Not independently verified. Compare with the machine itself:\n    ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub   (run on %s)\n", args[0])
			}
			if !a.confirm("Trust these keys?") {
				return errors.New("not trusted (run interactively to confirm, or pass --yes)")
			}
			if err := conn.Trust(keys); err != nil {
				return err
			}
			a.printf("Trusted. hopsesh will now connect to %s with strict host-key checking.\n", args[0])
			return nil
		},
	}
	cmd.Flags().Bool("yes", false, "trust without asking")
	return cmd
}

func doctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor [machine]",
		Short: "Check SSH access, host trust, Claude Code version and Remote Control readiness",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxTimeout(2)
			defer cancel()
			local := inventory.LocalFacts(ctx)
			type check struct {
				Name   string `json:"name"`
				OK     bool   `json:"ok"`
				Detail string `json:"detail"`
			}
			var checks []check
			add := func(name string, ok bool, detail string) { checks = append(checks, check{name, ok, detail}) }
			add("this machine: Claude Code", local.ClaudeVersion != "", nonEmpty(local.ClaudeVersion, "not found on PATH"))
			add("this machine: git", local.HasGit, map[bool]string{true: "found", false: "not found (needed to match and clone repositories)"}[local.HasGit])
			add("this machine: repos folder", true, a.cfg.ReposDir)
			if local.ClaudePath != "" {
				auth := inventory.LocalTarget(ctx).Auth
				rcOK, why := auth.RemoteControl()
				if auth == nil {
					why = "could not read `claude auth status`"
				} else if rcOK {
					why = "available (" + nonEmpty(auth.Subscription, "claude.ai") + " login)"
				}
				add("this machine: Remote Control", rcOK && auth != nil, why)
			}
			if st, err := integrate.SkillStatus(); err == nil {
				add("Claude Code skill", st.State == claudeskill.Current, skillLine(st))
			}
			if lnp.Gated() {
				if who := lnp.Responsible(); who == "" {
					add("this machine: local network", true, "always allowed from Terminal and SSH sessions")
				} else {
					add("this machine: local network", true, "machines on this network need "+who+" turned on in System Settings → Privacy & Security → Local Network (Tailscale addresses don't)")
				}
			}
			if len(args) == 1 {
				h := a.cfg.FindHost(args[0])
				if h == nil {
					h = &config.Host{Name: args[0], Destination: args[0], Allowed: true}
				}
				m := a.scanner().ScanHost(ctx, *h)
				defer m.Close()
				ok := m.Status == inventory.StatusOK
				detail := m.Status
				if m.Error != "" {
					detail += ": " + m.Error
				}
				if m.Hint != "" {
					detail += " — " + m.Hint
				}
				add(args[0]+": ssh + sftp", ok, detail)
				if m.Facts != nil {
					add(args[0]+": system", true, fmt.Sprintf("%s %s, home %s", m.Facts.OS, m.Facts.Arch, m.Facts.Home))
					add(args[0]+": Claude Code", m.Facts.ClaudeVersion != "", nonEmpty(m.Facts.ClaudeVersion, "not found for non-interactive SSH (listing still works)"))
					add(args[0]+": git", m.Facts.HasGit, map[bool]string{true: "found", false: "not found (no repository details)"}[m.Facts.HasGit])
					add(args[0]+": sessions", ok, fmt.Sprintf("%d in %s", len(m.Sessions), m.Facts.ConfigDir))
				}
			}
			if a.jsonOut {
				return a.emitJSON(checks)
			}
			for _, c := range checks {
				mark := "✓"
				if !c.OK {
					mark = "✗"
				}
				a.printf("%s %-28s %s\n", mark, c.Name, c.Detail)
			}
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "output JSON")
	return cmd
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
