package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/hosts"
	"github.com/roeehrl/hopsesh/internal/core/secrets"
	"github.com/roeehrl/hopsesh/internal/core/transport"
)

func hostsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hosts",
		Short: "List discovered machines and which ones hopsesh may access",
		Long: `Lists machines found in Tailscale and ~/.ssh/config (discovery connects to nothing),
merged with the ones you configured. Only machines you allow are ever contacted.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxTimeout(1)
			defer cancel()
			cands, _ := hosts.Discover(ctx)
			type row struct {
				hosts.Candidate
				Allowed    bool   `json:"allowed"`
				Configured bool   `json:"configured"`
				Auth       string `json:"auth,omitempty"`
			}
			var rows []row
			seen := map[string]bool{}
			for _, c := range cands {
				if c.Self {
					continue
				}
				rw := row{Candidate: c}
				if h := r.app.Cfg.FindHost(c.Name); h != nil {
					rw.Allowed, rw.Configured, rw.Auth = h.Allowed, true, authLabel(*h)
				}
				seen[c.Name] = true
				rows = append(rows, rw)
			}
			for _, h := range r.app.Cfg.Hosts {
				if !seen[h.Name] {
					rows = append(rows, row{Candidate: hosts.Candidate{Name: h.Name, Destination: h.Destination, Via: []string{h.Via}, OS: h.OS}, Allowed: h.Allowed, Configured: true, Auth: authLabel(h)})
				}
			}
			if r.jsonOut {
				return r.emitJSON(rows)
			}
			tw := tabwriter.NewWriter(r.out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "ALLOWED\tMACHINE\tSSH DESTINATION\tLOGIN\tFOUND VIA\tOS\tTAILSCALE")
			for _, rw := range rows {
				allowed := "no"
				if rw.Allowed {
					allowed = "yes"
				}
				ts := ""
				if rw.Online != nil {
					ts = map[bool]string{true: "online", false: "offline"}[*rw.Online]
					if rw.OtherOwner {
						ts += " (shared: " + rw.Owner + ")"
					}
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", allowed, rw.Name, rw.Destination, nonEmpty(rw.Auth, "key"), strings.Join(rw.Via, "+"), rw.OS, ts)
			}
			tw.Flush()
			r.printf("\nAllow machines with: hopsesh hosts allow <machine>...   (add others: hopsesh hosts add <name> <ssh-destination>)\n")
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "output JSON")
	cmd.AddCommand(hostsAllowCmd(true), hostsAllowCmd(false), hostsAddCmd(), hostsAuthCmd(), hostsSetupKeyCmd())
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
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxTimeout(1)
			defer cancel()
			cands, _ := hosts.Discover(ctx)
			for _, name := range args {
				h := r.app.Cfg.FindHost(name)
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
					r.app.Cfg.Hosts = append(r.app.Cfg.Hosts, config.Host{Name: found.Name, Destination: found.Destination, Via: strings.Join(found.Via, "+"), OS: found.OS, TailscaleName: found.DNSName})
					h = r.app.Cfg.FindHost(found.Name)
				}
				if h.TailscaleName == "" {
					for i := range cands {
						if cands[i].Name == h.Name && cands[i].DNSName != "" {
							h.TailscaleName = cands[i].DNSName
						}
					}
				}
				h.Allowed = allow
				r.printf("%s: %s\n", h.Name, map[bool]string{true: "allowed", false: "denied"}[allow])
			}
			return config.Save(r.app.Cfg)
		},
	}
}

func hostsAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <name> <ssh-destination>",
		Short: "Add and allow a machine by its ssh destination (alias, user@host or host)",
		Long: `Adds and allows a machine. The destination is anything ssh accepts: an alias from
~/.ssh/config, user@host, or a host name or address.

For a machine that logs in with a password rather than a key, add --password. hopsesh
asks for it when it connects and, on macOS, remembers it in the Keychain (--keychain=false
to be asked every time). hopsesh hosts setup-key <name> later switches it to key login.`,
		Example: `  hopsesh hosts add studio me@studio.local
  hopsesh hosts add nas admin@192.168.1.20 --password`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			h := config.Host{Name: args[0], Destination: args[1], Via: "manual", Allowed: true}
			if pw, _ := cmd.Flags().GetBool("password"); pw {
				h.Auth = "password"
				h.Keychain = secrets.Available()
				if cmd.Flags().Changed("keychain") {
					h.Keychain, _ = cmd.Flags().GetBool("keychain")
				}
				if h.Keychain && !secrets.Available() {
					return secrets.ErrUnavailable
				}
			}
			r.app.Cfg.UpsertHost(h)
			if err := config.Save(r.app.Cfg); err != nil {
				return err
			}
			r.printf("%s → %s: added and allowed (login: %s)\n", args[0], args[1], authLabel(h))
			if h.UsesPassword() {
				r.checkLogin(r.app.Cfg.FindHost(h.Name))
			}
			return nil
		},
	}
	cmd.Flags().Bool("password", false, "the machine logs in with a password (asked for when hopsesh connects)")
	cmd.Flags().Bool("keychain", false, "remember the password in the macOS Keychain (the default on macOS)")
	return cmd
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
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			h := r.app.Cfg.FindHost(args[0])
			dest := args[0]
			if h != nil {
				dest = h.Destination
			}
			conn, err := transport.NewConn(dest, config.StateDir(), r.app.Audit)
			if err != nil {
				return err
			}
			if h != nil && h.TailscaleName != "" && h.TailscaleName != dest {
				conn.Fallbacks = []string{h.TailscaleName}
			}
			ctx, cancel := ctxTimeout(1)
			defer cancel()
			keys, rh, err := conn.ScanHostKeys(ctx)
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
			r.printf("Host keys of %s (%s:%s):\n", args[0], rh.HostName, rh.Port)
			for _, k := range keys {
				r.printf("  %-22s %s\n", k.Type, k.Fingerprint)
			}
			home, _ := os.UserHomeDir()
			known := transport.KnownElsewhere(keys, filepath.Join(home, ".ssh", "known_hosts"))
			switch {
			case verified:
				r.printf("✓ Matches the host key Tailscale reports for this machine.\n")
			case len(known) > 0:
				r.printf("✓ Matches a key you already trust in ~/.ssh/known_hosts for: %s\n", strings.Join(known, ", "))
			default:
				r.printf("! Not independently verified. Compare with the machine itself:\n    ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub   (run on %s)\n", args[0])
			}
			if !r.confirm("Trust these keys?") {
				return errors.New("not trusted (run interactively to confirm, or pass --yes)")
			}
			if err := conn.Trust(keys); err != nil {
				return err
			}
			r.printf("Trusted. hopsesh will now connect to %s with strict host-key checking.\n", args[0])
			return nil
		},
	}
	cmd.Flags().Bool("yes", false, "trust without asking")
	return cmd
}
