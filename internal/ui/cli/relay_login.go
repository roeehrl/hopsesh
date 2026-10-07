package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/spf13/cobra"
)

func relayLoginCmd() *cobra.Command {
	var origin, caFile string
	cmd := &cobra.Command{Use: "login", Short: "Approve this headless machine in a browser using a short-lived code", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		store := relayStore()
		public, err := store.Public()
		if err != nil {
			return errors.New("run hopsesh relay init before logging in")
		}
		identity, err := store.Identity(cmd.Context(), public.Endpoint)
		if err != nil {
			return err
		}
		httpClient, err := (relay.Connection{CAFile: caFile}).HTTPClient()
		if err != nil {
			return err
		}
		login := relay.Enrollment{Origin: origin, HTTP: httpClient}
		flow, err := login.Begin(cmd.Context(), identity)
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(cmd.OutOrStdout(), "Open %s in a browser and enter %s.\nCompare this machine's full fingerprint: %s\nApproval allows delivery only. Peer sharing and receiving require separate approval.\n", flow.VerificationURI, flow.UserCode, public.ID); err != nil {
			return err
		}
		connection, err := login.Wait(cmd.Context(), flow, public.ID)
		if err != nil {
			return err
		}
		connection.CAFile = caFile
		if err = store.SetConnection(cmd.Context(), connection); err != nil {
			return err
		}
		if _, err = config.SetSetting("relay.enabled", json.RawMessage("true"), nil); err != nil {
			return errors.New("relay credential saved, but delivery could not be enabled; reload settings and enable delivery")
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Relay delivery approved. A running runtime adopts the connection automatically.")
		return err
	}}
	cmd.Flags().StringVar(&origin, "origin", relay.DefaultOrigin, "verified relay HTTPS origin")
	cmd.Flags().StringVar(&caFile, "ca-file", "", "explicit absolute PEM trust root for a private relay; hostname verification stays enabled")
	return cmd
}
