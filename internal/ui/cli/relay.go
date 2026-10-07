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
	var send, bring, receive, share bool
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
		// Validation precedes every state mutation. Merely selecting roots does
		// not grant access to conversations or permission to receive sessions.
		validationName := name
		if validationName == "" && kind == "device" {
			validationName = public.ID[:12]
		}
		grant, err := relay.ValidatePair(relay.PairInput{Identity: string(b), Fingerprint: fingerprint, Kind: kind, Name: validationName, Roots: roots, Send: send, Bring: bring, Receive: receive, Export: share, ExpiresSeconds: int(ttl / time.Second)})
		if err != nil {
			return err
		}
		self, err := relayStore().Public()
		if err != nil {
			return errors.New("run hopsesh relay init before pairing")
		}
		if self.ID == public.ID {
			return errors.New("cannot pair this endpoint with itself")
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if receive && !cfg.Peer.Receive {
			return errors.New("enable receiving on this computer before approving repository access")
		}
		if old := cfg.FindHost(name); name != "" && old != nil && old.RelayID != public.ID {
			return errors.New("machine name already belongs to another destination")
		}
		if err = relayStore().Approve(cmd.Context(), grant); err != nil {
			return err
		}
		if name != "" {
			cfg.UpsertHost(config.Host{Name: name, RelayID: public.ID, Via: "relay", Allowed: true})
			if err = config.Save(&cfg); err != nil {
				return errors.New("peer approved, but machine registration was not saved; reload settings and approve again")
			}
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(grant)
	}}
	pair.Flags().StringVar(&fingerprint, "fingerprint", "", "peer fingerprint compared independently")
	pair.Flags().StringVar(&kind, "kind", "device", "device or cloud-session")
	pair.Flags().StringVar(&name, "name", "", "register an approved device as a machine")
	pair.Flags().StringSliceVar(&roots, "root", nil, "local repository roots approved for --receive or --share")
	pair.Flags().DurationVar(&ttl, "expires", time.Hour, "cloud session grant lifetime, up to 24h")
	pair.Flags().BoolVar(&send, "send", false, "allow sending sessions to the other machine; it must approve receiving independently")
	pair.Flags().BoolVar(&bring, "bring", false, "allow bringing conversations from the other machine; it must approve sharing independently")
	pair.Flags().BoolVar(&receive, "receive", false, "allow this peer to receive sessions into selected local roots")
	pair.Flags().BoolVar(&share, "share", false, "allow sharing local conversations from selected roots, or exporting the approved cloud session")
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
		identity, err := relayStore().Public()
		if err != nil {
			return err
		}
		if identity.ID != connection.Device {
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
		c, err := relayStore().PublicConnection()
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
			Device  string        `json:"device"`
			Expires int64         `json:"expires"`
			Health  *relay.Health `json:"health"`
		}{c.URL, c.Device, c.Expires, health})
	}}
	revoke := &cobra.Command{Use: "revoke <peer-id>", Short: "Revoke a peer's local permissions immediately", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return relayStore().Revoke(cmd.Context(), args[0]) }}
	disable := &cobra.Command{Use: "disable", Short: "Disable this namespace's outbound relay listener", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := config.SetSetting("relay.enabled", json.RawMessage("false"), nil)
		return err
	}}
	peers := &cobra.Command{Use: "peers", Short: "List public peer approvals and scopes without creating relay state", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		grants, err := relayStore().Grants()
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(grants)
	}}
	root.AddCommand(init, pair, enroll, relayLoginCmd(), status, peers, revoke, disable)
	return root
}
