package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/spf13/cobra"
)

func relayStore() relay.Store {
	return relay.Store{Directory: filepath.Join(config.StateDir(), "relay")}
}
func relayCmd() *cobra.Command {
	root := &cobra.Command{Use: "relay", Short: "Pair approved endpoints and manage optional encrypted internet delivery"}
	init := &cobra.Command{Use: "init", Short: "Create this endpoint's keys and print only its public pairing identity", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		m := &host.Machine{Local: true, Name: app.LocalName(), Facts: host.ObserveLocal(cmd.Context(), nil)}
		defer m.Close()
		if err := m.CommitIdentity(cmd.Context()); err != nil {
			return err
		}
		id, err := relayStore().Identity(cmd.Context(), m.Facts.Endpoint)
		if err != nil {
			return err
		}
		if id.Public.Endpoint != m.Facts.Endpoint {
			return errors.New("relay keys belong to another native endpoint; use a separate state namespace")
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(id.Public)
	}}
	var fingerprint, kind, name string
	var roots []string
	var ttl time.Duration
	pair := &cobra.Command{Use: "pair <public-identity.json>", Short: "Approve a peer after comparing its fingerprint through a trusted channel", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		f, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, 4097))
		if err != nil {
			return err
		}
		if len(b) > 4096 {
			return errors.New("pairing identity exceeds limit")
		}
		var public relay.PublicIdentity
		if err = json.Unmarshal(b, &public); err != nil {
			return err
		}
		if err = public.Check(); err != nil {
			return err
		}
		if fingerprint == "" || fingerprint != public.Fingerprint() {
			return errors.New("provide --fingerprint copied from the peer through a trusted channel; a relay response cannot approve its own keys")
		}
		methods := []string{"hello", "observe"}
		if kind == "device" && len(roots) > 0 {
			methods = append(methods, "plan", "apply", "undo", "export", "ack")
			if public.Endpoint == "" {
				return errors.New("receiving requires the peer's native endpoint binding")
			}
		}
		grant := relay.Grant{Peer: public, Endpoint: public.Endpoint, Kind: kind, Roots: roots, Methods: methods, SendMethods: []string{"hello", "observe", "plan", "apply", "undo", "export", "ack"}}
		if kind == "cloud-session" {
			grant.Methods = []string{"observe", "export"}
			grant.SendMethods = []string{"observe", "export"}
			grant.Expires = time.Now().Add(ttl).Unix()
		}
		for _, root := range roots {
			if !filepath.IsAbs(root) {
				return errors.New("approved repository roots must be absolute")
			}
			if _, err = filepath.EvalSymlinks(root); err != nil {
				return err
			}
		}
		if err = relayStore().Approve(cmd.Context(), grant); err != nil {
			return err
		}
		if name != "" {
			if kind != "device" {
				return errors.New("a cloud session cannot be registered as a machine")
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if old := cfg.FindHost(name); old != nil && old.RelayID != public.ID {
				return errors.New("machine name already belongs to another destination")
			}
			cfg.UpsertHost(config.Host{Name: name, RelayID: public.ID, Via: "relay", Allowed: true})
			if err := config.Save(&cfg); err != nil {
				return err
			}
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(grant)
	}}
	pair.Flags().StringVar(&fingerprint, "fingerprint", "", "peer fingerprint compared independently")
	pair.Flags().StringVar(&kind, "kind", "device", "device or cloud-session")
	pair.Flags().StringVar(&name, "name", "", "register an approved device as a machine for session push")
	pair.Flags().StringSliceVar(&roots, "root", nil, "locally approved repository roots; omit for observation only")
	pair.Flags().DurationVar(&ttl, "expires", time.Hour, "cloud session grant lifetime, up to 24h")
	enroll := &cobra.Command{Use: "connect <https-origin>", Short: "Read this device's scoped credential JSON from stdin; keep secrets out of arguments", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		b, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 8193))
		if err != nil {
			return err
		}
		if len(b) > 8192 {
			return errors.New("relay credential exceeds limit")
		}
		var connection relay.Connection
		if err = json.Unmarshal(b, &connection); err != nil {
			return errors.New("invalid relay credential JSON")
		}
		connection.URL = args[0]
		identity, err := relayStore().Identity(cmd.Context())
		if err != nil {
			return err
		}
		if identity.Public.ID != connection.Device {
			return errors.New("relay credential was enrolled for another device")
		}
		if err = relayStore().SetConnection(cmd.Context(), connection); err != nil {
			return err
		}
		_, err = config.SetSetting("relay.enabled", json.RawMessage("true"), nil)
		if err == nil {
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Relay enrolled. A running runtime connects automatically; otherwise start this namespace's runtime.")
		}
		return err
	}}
	status := &cobra.Command{Use: "status", Short: "Show enrollment metadata with all credentials redacted", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := relayStore().Connection(cmd.Context())
		if err != nil {
			return err
		}

		var health *relay.Health
		if client, e := runtimeClient(); e == nil {
			ctx, cancel := context.WithTimeout(cmd.Context(), time.Second)
			defer cancel()
			var current relay.Health
			if e = client.Call(ctx, "relay.status", nil, &current); e == nil {
				health = &current
			}
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			URL     string        `json:"url"`
			Space   string        `json:"space"`
			Device  string        `json:"device"`
			Expires int64         `json:"expires"`
			Health  *relay.Health `json:"health"`
		}{c.URL, c.Space, c.Device, c.Expires, health})
	}}
	revoke := &cobra.Command{Use: "revoke <peer-id>", Short: "Revoke a peer's local permissions immediately", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return relayStore().Revoke(cmd.Context(), args[0]) }}
	disable := &cobra.Command{Use: "disable", Short: "Disable this namespace's outbound relay listener", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := config.SetSetting("relay.enabled", json.RawMessage("false"), nil)
		return err
	}}
	root.AddCommand(init, pair, enroll, status, revoke, disable)
	return root
}
