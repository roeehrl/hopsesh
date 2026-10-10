package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/internal/localstate"
	"github.com/spf13/cobra"
)

func cloudTicketCmd() *cobra.Command {
	var provider, session, resume string
	var lease time.Duration
	c := &cobra.Command{Use: "ticket", Short: "Save a private one-use routing invitation for one real cloud session", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		store := relayStore()
		public, err := store.Public()
		if err != nil {
			return errors.New("initialize and enroll this native device before issuing cloud invitations")
		}
		identity, err := store.Identity(cmd.Context(), public.Endpoint)
		if err != nil {
			return err
		}
		connection, err := store.Connection(cmd.Context())
		if err != nil {
			return err
		}
		record, err := (relay.AdmissionStore{Directory: filepath.Join(config.StateDir(), "cloud-admissions")}).IssueTask(cmd.Context(), identity, connection, provider, session, lease, resume)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Private invitation saved at %s\nExpires %s. Supply this file through stdin to cloud-integration claim in that actual session. Keep it out of environment setup and cached images.\nCompare owner fingerprint independently: %s\nLogical task: %s\nThe fresh cloud identity still needs approval on this device before sharing.\n", record.Path, time.Unix(record.Expires, 0).Format(time.RFC3339), public.ID, record.TaskID)
		return err
	}}
	c.Flags().StringVar(&provider, "provider", "", "cloud execution surface")
	c.Flags().StringVar(&session, "session", "", "the actual cloud task's native ID; never an environment ID")
	c.Flags().StringVar(&resume, "resume-task", "", "explicitly preserve an owner-issued logical task ID across resume or rebuild; omit for a new task or fork")
	c.Flags().DurationVar(&lease, "lease", time.Hour, "cloud routing lease, one minute to 24 hours")
	return c
}

func readAdmission(r io.Reader) (relay.AdmissionTicket, error) {
	var ticket relay.AdmissionTicket
	body, err := io.ReadAll(io.LimitReader(r, 8193))
	if err != nil {
		return ticket, err
	}
	if len(body) > 8192 || json.Unmarshal(body, &ticket) != nil {
		return ticket, errors.New("cloud invitation JSON is invalid or exceeds limit")
	}
	return ticket, nil
}

func cloudClaimCmd() *cobra.Command {
	var fingerprint, caFile string
	c := &cobra.Command{Use: "claim <instance-directory>", Short: "Read a one-use invitation from stdin for this fresh session; remains provisional", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		instance, err := cloudintegration.LoadForClaim(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		ticket, err := readAdmission(cmd.InOrStdin())
		if err != nil {
			return err
		}
		store := relay.Store{Directory: instance.Directory}
		identity, err := store.Identity(cmd.Context())
		if err != nil || identity.Public.ID != instance.Public.ID {
			return errors.New("this session's fresh identity changed")
		}
		httpClient, err := (relay.Connection{CAFile: caFile}).HTTPClient()
		if err != nil {
			return err
		}
		if httpClient != nil {
			defer httpClient.CloseIdleConnections()
		}
		connection, err := relay.ClaimAdmission(cmd.Context(), ticket, fingerprint, identity, relay.CloudClaim{Provider: instance.Provider, Session: instance.Session, Incarnation: instance.ID, Expires: instance.Expires.Unix()}, httpClient)
		if err != nil {
			return err
		}
		// The invocation may have been superseded while waiting for the relay.
		if _, err = cloudintegration.LoadForClaim(cmd.Context(), instance.Directory); err != nil {
			return err
		}
		if err = instance.AssociateTask(cmd.Context(), ticket); err != nil {
			return err
		}
		connection.CAFile = caFile
		if err = store.SetConnection(cmd.Context(), connection); err != nil {
			return err
		}
		methods := []string{"observe"}
		if instance.ExportTranscript {
			methods = append(methods, "export")
		}
		if err = store.Approve(cmd.Context(), relay.Grant{Peer: ticket.Owner, Endpoint: ticket.Owner.Endpoint, Kind: "cloud-session", Methods: methods, Expires: connection.Expires}); err != nil {
			return err
		}
		// Only public pairing information crosses stdout; credentials stay private.
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			State      string               `json:"state"`
			Public     relay.PublicIdentity `json:"public"`
			Expires    int64                `json:"expires"`
			Task       string               `json:"taskId"`
			Generation int64                `json:"generation"`
		}{"provisional-awaiting-peer-approval", instance.Public, connection.Expires, ticket.Task.ID, ticket.Generation})
	}}
	c.Flags().StringVar(&fingerprint, "fingerprint", "", "owner's full fingerprint compared through a trusted channel")
	c.Flags().StringVar(&caFile, "ca-file", "", "absolute local PEM trust root for a private relay")
	return c
}

func cloudRevokeTicketCmd() *cobra.Command {
	return &cobra.Command{Use: "revoke-ticket <private-ticket.json>", Short: "Revoke an unclaimed invitation or its claimed cloud delivery lease", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		body, err := localstate.ReadPrivateFile(args[0], 8192)
		if err != nil {
			return err
		}
		var ticket relay.AdmissionTicket
		if err = json.Unmarshal(body, &ticket); err != nil {
			return errors.New("invalid cloud invitation file")
		}
		connection, err := relayStore().Connection(cmd.Context())
		if err != nil {
			return err
		}
		if filepath.Dir(filepath.Clean(args[0])) == filepath.Join(config.StateDir(), "cloud-admissions") {
			err = (relay.AdmissionStore{Directory: filepath.Join(config.StateDir(), "cloud-admissions")}).Revoke(cmd.Context(), connection, strings.TrimSuffix(filepath.Base(args[0]), ".json"))
		} else {
			err = relay.RevokeAdmission(cmd.Context(), connection, ticket)
		}
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Cloud admission revoked. Already delivered checkpoints remain available; peer permissions are independent.")
		return err
	}}
}

func cloudTicketStatusCmd() *cobra.Command {
	return &cobra.Command{Use: "status-ticket <private-ticket.json>", Short: "Check provisional cloud admission without granting peer permissions", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		body, err := localstate.ReadPrivateFile(args[0], 8192)
		if err != nil {
			return err
		}
		var ticket relay.AdmissionTicket
		if json.Unmarshal(body, &ticket) != nil {
			return errors.New("invalid cloud invitation file")
		}
		connection, err := relayStore().Connection(cmd.Context())
		if err != nil {
			return err
		}
		var state relay.AdmissionStatus
		if filepath.Dir(filepath.Clean(args[0])) == filepath.Join(config.StateDir(), "cloud-admissions") {
			state, err = (relay.AdmissionStore{Directory: filepath.Join(config.StateDir(), "cloud-admissions")}).Check(cmd.Context(), connection, strings.TrimSuffix(filepath.Base(args[0]), ".json"))
		} else {
			state, err = relay.CheckAdmission(cmd.Context(), connection, ticket)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(state)
	}}
}

func cloudTasksCmd() *cobra.Command {
	return &cobra.Command{Use: "tasks", Short: "List owner-issued logical cloud tasks available for explicit resume", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		rows, err := (relay.AdmissionStore{Directory: filepath.Join(config.StateDir(), "cloud-admissions")}).Tasks()
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
	}}
}
