package cli

import (
	"fmt"
	"strings"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/spf13/cobra"
)

func accountsCmd() *cobra.Command {
	root := &cobra.Command{Use: "accounts", Short: "Discover and manage named Claude Code and Codex account profiles"}
	list := &cobra.Command{Use: "list", Short: "List registered roots, tags and observed logins", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		ps, err := r.app.Accounts()
		if err != nil {
			return err
		}
		if r.jsonOut {
			return r.emitJSON(ps)
		}
		for _, p := range ps {
			status := "not checked"
			if p.Account != nil {
				status = p.Account.Label
				if p.Account.Email != "" {
					status += " · " + p.Account.Email
				}
				if !p.Account.LoggedIn {
					status = "signed out"
				} else {
					status = "signed in · " + status
				}
			}
			if p.Error != "" {
				status = p.Error
			}
			r.printf("%s  %s  %s  [%s]\n  %s\n  %s\n", p.ID, p.Agent, p.Name, strings.Join(p.Tags, ", "), p.Root, status)
		}
		return nil
	}}
	list.Flags().Bool("json", false, "output JSON")
	scan := &cobra.Command{Use: "scan", Short: "Discover supported roots locally and on allowed machines", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		machine, _ := cmd.Flags().GetString("machine")
		inv := r.scanWith(cmd, machine, app.ScanOptions{SkipGit: true, ForceAccounts: true})
		defer inv.Close()
		if r.jsonOut {
			return r.emitJSON(inv.Machines)
		}
		for _, m := range inv.Machines {
			r.printf("%s: %s %s\n", m.Name, m.Status, m.Error)
			for _, ag := range m.Agents {
				r.printf("  %s %s\n", ag.Name, ag.Error)
			}
		}
		return nil
	}}
	scan.Flags().String("machine", "", "only this machine (default: allowed machines)")
	scan.Flags().Bool("json", false, "output JSON")
	add := &cobra.Command{Use: "add <claude|codex> <name>", Short: "Create an empty local profile or adopt an existing root", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		machine, _ := cmd.Flags().GetString("machine")
		root, _ := cmd.Flags().GetString("root")
		tags, _ := cmd.Flags().GetStringSlice("tag")
		if machine == "" || machine == "local" || machine == app.LocalName() {
			root = expandHome(root)
		}
		p, err := r.app.RegisterAccount(cmd.Context(), machine, agent.ID(args[0]), args[1], root, tags)
		if err != nil {
			return err
		}
		if r.jsonOut {
			return r.emitJSON(p)
		}
		r.printf("Registered %s (%s)\nSign in: hopsesh accounts login %s --run\n", p.Name, p.ID, p.ID)
		return nil
	}}
	add.Flags().String("root", "", "existing absolute vendor state root; omit to create an empty private local root")
	add.Flags().String("machine", "", "machine hosting an existing root")
	add.Flags().StringSlice("tag", nil, "account tags (repeat or comma separate)")
	add.Flags().Bool("json", false, "output JSON")
	edit := &cobra.Command{Use: "edit <profile-id>", Short: "Rename a profile and replace its tags", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		ps, err := r.app.Accounts()
		if err != nil {
			return err
		}
		for _, p := range ps {
			if p.ID != args[0] {
				continue
			}
			name, _ := cmd.Flags().GetString("name")
			if !cmd.Flags().Changed("name") {
				name = p.Name
			}
			tags, _ := cmd.Flags().GetStringSlice("tag")
			if !cmd.Flags().Changed("tag") {
				tags = p.Tags
			}
			return r.app.EditAccount(p.ID, name, tags, p.Generation)
		}
		return fmt.Errorf("account not found")
	}}
	edit.Flags().String("name", "", "new display name")
	edit.Flags().StringSlice("tag", nil, "replacement tags; --tag= removes all tags")
	forget := &cobra.Command{Use: "forget <profile-id>", Short: "Remove registration; leave vendor files and login untouched", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		ps, err := r.app.Accounts()
		if err != nil {
			return err
		}
		for _, p := range ps {
			if p.ID == args[0] {
				return r.app.ForgetAccount(p.ID, p.Generation)
			}
		}
		return fmt.Errorf("account not found")
	}}
	login := &cobra.Command{Use: "login <profile-id>", Short: "Show or run the vendor sign-in in this profile", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		c, err := r.app.AccountLogin(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		run, _ := cmd.Flags().GetBool("run")
		if run {
			if err := r.runHere(cmd.Context(), c); err != nil {
				return err
			}
			_, err = r.app.RefreshAccount(cmd.Context(), args[0])
			return err
		}
		r.printf("%s\n", launch.Shell(c, "", launch.DefaultShell()))
		return nil
	}}
	login.Flags().Bool("run", false, "run the vendor-owned sign-in now")
	refresh := &cobra.Command{Use: "refresh <profile-id>", Short: "Check one profile’s public sign-in metadata", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		p, err := r.app.RefreshAccount(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if r.jsonOut {
			return r.emitJSON(p)
		}
		if p.Error != "" {
			return fmt.Errorf("account check: %s", p.Error)
		}
		r.printf("%s: checked on %s\n", p.Name, p.Machine)
		return nil
	}}
	refresh.Flags().Bool("json", false, "output JSON")
	root.AddCommand(list, scan, add, edit, forget, login, refresh)
	return root
}
