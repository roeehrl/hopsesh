package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/secrets"
)

// authLabel is how a machine logs in, for listings.
func authLabel(h config.Host) string {
	switch {
	case !h.UsesPassword():
		return "key"
	case h.Keychain:
		return "password (remembered)"
	default:
		return "password"
	}
}

func hostsAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth <machine> [key|password]",
		Short: "Choose how hopsesh logs in to a machine: SSH keys (default) or a password",
		Long: `With "password", ssh asks for the machine's password and hopsesh answers it: from
the macOS Keychain when the machine is set to remember it, otherwise by asking you once per
run (the full-screen view asks before it starts; scripts can pass --password-stdin).
The password is never written to hopsesh's files or to a command line.

With "key", hopsesh uses your SSH keys and agent again and forgets any remembered password.
Better still, let hopsesh set that up for you: hopsesh hosts setup-key <machine>.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			h := r.app.Cfg.FindHost(args[0])
			if h == nil {
				return fmt.Errorf("unknown machine %q (add it first: hopsesh hosts add %s <ssh-destination>)", args[0], args[0])
			}
			acct := secrets.Account(h.Name, h.Destination)
			forget, _ := cmd.Flags().GetBool("forget")
			if len(args) == 1 {
				if forget {
					if err := secrets.Delete(acct); err != nil && !errors.Is(err, secrets.ErrUnavailable) {
						return err
					}
					r.printf("%s: remembered password removed\n", h.Name)
					return nil
				}
				r.printf("%s logs in with: %s\n", h.Name, authLabel(*h))
				return nil
			}
			switch args[1] {
			case "key", "keys":
				h.Auth, h.Keychain = "", false
				_ = secrets.Delete(acct)
			case "password":
				h.Auth = "password"
				h.Keychain = secrets.Available()
				if cmd.Flags().Changed("keychain") {
					h.Keychain, _ = cmd.Flags().GetBool("keychain")
				}
				if h.Keychain && !secrets.Available() {
					return secrets.ErrUnavailable
				}
				if !h.Keychain || forget {
					_ = secrets.Delete(acct)
				}
			default:
				return fmt.Errorf("unknown login method %q (key or password)", args[1])
			}
			if err := config.Save(r.app.Cfg); err != nil {
				return err
			}
			r.app.Audit.Write(audit.Entry{Action: "hosts.auth", Host: h.Name, Detail: map[string]any{"auth": authLabel(*h)}})
			r.printf("%s now logs in with: %s\n", h.Name, authLabel(*h))
			if h.UsesPassword() {
				r.checkLogin(h)
			}
			return nil
		},
	}
	cmd.Flags().Bool("keychain", false, "remember the password in the macOS Keychain or Windows Credential Manager (the default there)")
	cmd.Flags().Bool("forget", false, "remove the remembered password")
	return cmd
}

// checkLogin logs in once when someone is at the terminal, so a wrong password shows up
// now (and a right one lands in the Keychain) rather than in the next listing.
func (r *run) checkLogin(h *config.Host) {
	if !r.interactive() && !r.pwStdin {
		r.printf("hopsesh asks for the password when it next connects.\n")
		return
	}
	ctx, cancel := ctxTimeout(2)
	defer cancel()
	m, err := r.app.Connect(ctx, *h)
	if err != nil {
		r.printf("! Could not log in: %v\n", err)
		return
	}
	m.Close()
	msg := "✓ Logged in."
	if h.Keychain {
		msg += " The password is in " + secrets.StoreName() + "."
	}
	r.printf("%s Tip: hopsesh hosts setup-key %s switches it to key login.\n", msg, h.Name)
}

func hostsSetupKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup-key <machine>",
		Short: "Switch a password machine to SSH key login (adds your public key there)",
		Long: `Logs in once with the machine's password, adds this machine's public SSH key
to ~/.ssh/authorized_keys there, checks that a login without a password now
works, and then switches the machine to key login and forgets its password.
For macOS and Linux machines.

The key is the first one ssh would offer to that machine (IdentityFile in ~/.ssh/config, or
the default ~/.ssh/id_* names). When there is none, hopsesh offers to create
~/.ssh/id_ed25519.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			h := r.app.Cfg.FindHost(args[0])
			if h == nil {
				return fmt.Errorf("unknown machine %q (add it first: hopsesh hosts add %s <ssh-destination> --password)", args[0], args[0])
			}
			ctx, cancel := ctxTimeout(3)
			defer cancel()
			hp := *h
			hp.Auth = "password"
			res, err := r.app.SetupKeyLogin(ctx, hp, false)
			if errors.Is(err, app.ErrNoLocalKey) {
				r.printf("This machine has no SSH key that ssh would use for %s.\n", h.Name)
				if !r.confirm("Create one (~/.ssh/id_ed25519, no passphrase)?") {
					return errors.New("no key to install (create one with ssh-keygen, or pass --yes to let hopsesh create it)")
				}
				res, err = r.app.SetupKeyLogin(ctx, hp, true)
			}
			if res != nil && res.Created {
				r.printf("Created an SSH key: %s\n", res.PublicKey)
			}
			if err != nil {
				return err
			}
			if res.Added {
				r.printf("Added %s to %s's authorized keys.\n", res.PublicKey, h.Name)
			} else {
				r.printf("%s already had %s.\n", h.Name, res.PublicKey)
			}
			h.Auth, h.Keychain = "", false
			_ = secrets.Delete(secrets.Account(h.Name, h.Destination))
			if err := config.Save(r.app.Cfg); err != nil {
				return err
			}
			r.printf("✓ %s now logs in with your key; its password is no longer used or remembered.\n", h.Name)
			return nil
		},
	}
	cmd.Flags().Bool("yes", false, "create an SSH key without asking if this machine has none")
	return cmd
}
