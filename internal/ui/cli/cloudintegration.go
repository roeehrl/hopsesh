package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/spf13/cobra"
)

func cloudIntegrationCmd() *cobra.Command {
	root := &cobra.Command{Use: "cloud-integration", Short: "Prepare verified installation for a specific cloud execution surface"}
	var provider, version, origin string
	var script bool
	plan := &cobra.Command{Use: "plan", Short: "Print an immutable verified install script and honest startup requirements", Args: cobra.NoArgs, Annotations: map[string]string{"hopsesh.passive": "true"}, RunE: func(cmd *cobra.Command, _ []string) error {
		p, err := cloudintegration.Plan(provider, version, origin)
		if err != nil {
			return err
		}
		if script {
			_, err = fmt.Fprint(cmd.OutOrStdout(), p.InstallScript)
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(p)
	}}
	plan.Flags().StringVar(&provider, "provider", "", "claude-hosted, codex-current, codex-legacy or work-cloud")
	plan.Flags().StringVar(&version, "version", "", "immutable signed 0.5 release version")
	plan.Flags().StringVar(&origin, "origin", cloudintegration.DownloadOrigin, "verified downloads HTTPS origin")
	plan.Flags().BoolVar(&script, "script", false, "print only the installation shell script")
	root.AddCommand(plan, cloudPrepareCmd(), cloudCurrentCmd(), cloudStartupInstallCmd(), cloudAuthorizeCmd(), cloudServeCmd(), cloudTicketCmd(), cloudTasksCmd(), cloudClaimCmd(), cloudRevokeTicketCmd(), cloudTicketStatusCmd(), cloudInspectCmd(), cloudCheckpointImportCmd(), cloudCheckpointCacheCmd())
	return root
}

func cloudInspectCmd() *cobra.Command {
	var preview bool
	c := &cobra.Command{Use: "inspect <approved-cloud-fingerprint>", Short: "Check one approved cloud incarnation; optionally preview its conversation checkpoint", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		r, err := newRun(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := ctxTimeout(1)
		defer cancel()
		if preview {
			out, err := r.app.CloudConnectorConversation(ctx, args[0])
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		}
		out, err := r.app.CloudConnectorObservation(ctx, args[0])
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}}
	c.Flags().BoolVar(&preview, "preview", false, "explicitly request up to 20 messages; export must be approved on both endpoints")
	return c
}

func cloudPrepareCmd() *cobra.Command {
	var scope cloudintegration.Scope
	var hook, quiet bool
	var ttl time.Duration
	c := &cobra.Command{Use: "prepare", Short: "Create fresh keys for one real cloud session; remains disconnected until authorized", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if hook {
			// The installed hook must be a no-op locally, not an error in the agent's context.
			if os.Getenv("CLAUDE_CODE_REMOTE") != "true" {
				return nil
			}
			in, err := cloudintegration.ReadSessionStart(cmd.InOrStdin(), os.Getenv("CLAUDE_CODE_REMOTE"))
			if err != nil {
				return err
			}
			scope.Provider, scope.Session, scope.Workspace, scope.Transcript = "claude-hosted", in.Session, in.Workspace, in.Transcript
		}
		if scope.Workspace != "" {
			p, err := filepath.Abs(scope.Workspace)
			if err != nil {
				return err
			}
			scope.Workspace, err = filepath.EvalSymlinks(p)
			if err != nil {
				return err
			}
		}
		if scope.Transcript != "" && scope.NativeRoot == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			switch scope.Provider {
			case "claude-hosted":
				scope.NativeRoot = filepath.Join(home, ".claude", "projects")
			case "codex-legacy":
				scope.NativeRoot = filepath.Join(home, ".codex", "sessions")
			}
		}
		parent := filepath.Join(config.StateDir(), "cloud-sessions")
		// Resolve an existing parent, never copy setup-time identity or credentials.
		if err := os.MkdirAll(parent, 0700); err != nil {
			return err
		}
		parent, err := filepath.EvalSymlinks(parent)
		if err != nil {
			return err
		}
		s, err := cloudintegration.Begin(cmd.Context(), parent, scope, ttl)
		if err != nil {
			return err
		}
		if quiet {
			return nil
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			cloudintegration.Incarnation
			State string `json:"state"`
		}{s, "awaiting-authorization"})
	}}
	c.Flags().StringVar(&scope.Provider, "provider", "", "cloud execution surface")
	c.Flags().StringVar(&scope.Session, "session", "", "this task's native session ID")
	c.Flags().StringVar(&scope.Workspace, "workspace", "", "this task's repository directory")
	c.Flags().StringVar(&scope.NativeRoot, "native-root", "", "approved native transcript root; never the workspace or credential root")
	c.Flags().StringVar(&scope.Transcript, "transcript", "", "exact native JSONL path for this session, if supported")
	c.Flags().BoolVar(&scope.ExportTranscript, "allow-transcript-export", false, "explicitly allow a pinned peer to export this session's native transcript")
	c.Flags().BoolVar(&hook, "claude-hook", false, "read documented SessionStart input; no-op outside Claude cloud")
	c.Flags().BoolVar(&quiet, "quiet", false, "save public incarnation metadata privately without injecting hook stdout into agent context")
	c.Flags().DurationVar(&ttl, "lease", time.Hour, "incarnation lease; one minute to 24 hours")
	return c
}

func cloudCurrentCmd() *cobra.Command {
	var provider, session, workspace string
	c := &cobra.Command{Use: "current", Short: "Read the current incarnation for one exact cloud task; never initialize keys", Args: cobra.NoArgs, Annotations: map[string]string{"hopsesh.passive": "true"}, RunE: func(cmd *cobra.Command, _ []string) error {
		if workspace == "" {
			return errors.New("the actual cloud task's workspace is required")
		}
		p, err := filepath.Abs(workspace)
		if err != nil {
			return err
		}
		p, err = filepath.EvalSymlinks(p)
		if err != nil {
			return err
		}
		parent, err := filepath.EvalSymlinks(filepath.Join(config.StateDir(), "cloud-sessions"))
		if err != nil {
			return err
		}
		instance, err := cloudintegration.Current(cmd.Context(), parent, provider, session, p)
		if err != nil {
			return err
		}
		state := "awaiting-authorization"
		store := relay.Store{Directory: instance.Directory}
		if connection, err := store.PublicConnection(); err == nil && connection.Device == instance.Public.ID && connection.Expires > time.Now().Unix() {
			state = "authorized; connector liveness unconfirmed"
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			cloudintegration.Incarnation
			State string `json:"state"`
		}{instance, state})
	}}
	c.Flags().StringVar(&provider, "provider", "", "cloud execution surface")
	c.Flags().StringVar(&session, "session", "", "actual task session ID")
	c.Flags().StringVar(&workspace, "workspace", "", "absolute checked-out repository for this task")
	return c
}

func cloudStartupInstallCmd() *cobra.Command {
	var provider, version, origin string
	var preview bool
	c := &cobra.Command{Use: "install-startup <repository>", Short: "Install reviewed provider startup files; never create a cloud identity during setup", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		repo, err = filepath.EvalSymlinks(repo)
		if err != nil {
			return err
		}
		plan, err := cloudintegration.PlanRepository(provider, version, origin, repo)
		if err != nil {
			return err
		}
		if !preview {
			if err = plan.Apply(cmd.Context()); err != nil {
				return err
			}
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			*cloudintegration.RepositorySetup
			Applied bool `json:"applied"`
		}{plan, !preview})
	}}
	c.Flags().StringVar(&provider, "provider", "", "claude-hosted or codex-current")
	c.Flags().StringVar(&version, "version", "", "immutable signed 0.5 release version")
	c.Flags().StringVar(&origin, "origin", cloudintegration.DownloadOrigin, "verified downloads HTTPS origin")
	c.Flags().BoolVar(&preview, "dry-run", false, "preview exact changed file names without writing configuration")
	return c
}

func cloudAuthorizeCmd() *cobra.Command {
	var peerFile, fingerprint, origin string
	c := &cobra.Command{Use: "authorize <instance-directory>", Short: "Bind one session to a pinned peer and read its routing credential from stdin", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := cloudintegration.Load(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		f, err := os.Open(peerFile)
		if err != nil {
			return err
		}
		defer f.Close()
		body, err := io.ReadAll(io.LimitReader(f, 4097))
		if err != nil {
			return err
		}
		if len(body) > 4096 {
			return errors.New("peer identity exceeds limit")
		}
		var p relay.PublicIdentity
		if err = json.Unmarshal(body, &p); err != nil {
			return err
		}
		if err = p.Check(); err != nil {
			return err
		}
		if fingerprint == "" || fingerprint != p.Fingerprint() {
			return errors.New("peer fingerprint must be compared independently")
		}
		body, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), 8193))
		if err != nil {
			return err
		}
		if len(body) > 8192 {
			return errors.New("routing credential exceeds limit")
		}
		var connection relay.Connection
		if err = json.Unmarshal(body, &connection); err != nil {
			return errors.New("invalid routing credential JSON")
		}
		connection.URL = origin
		if connection.Device != s.Public.ID || connection.Expires > s.Expires.Unix() {
			return errors.New("routing credential must match this incarnation and expire within its lease")
		}
		store := relay.Store{Directory: s.Directory}
		if err = store.SetConnection(cmd.Context(), connection); err != nil {
			return err
		}
		methods := []string{"observe"}
		if s.ExportTranscript {
			methods = append(methods, "export")
		}
		if err = store.Approve(cmd.Context(), relay.Grant{Peer: p, Endpoint: p.Endpoint, Kind: "cloud-session", Methods: methods, Expires: connection.Expires}); err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Session authorized. Start its connector with cloud-integration serve; no device access was granted.")
		return err
	}}
	c.Flags().StringVar(&peerFile, "peer", "", "peer's public identity JSON")
	c.Flags().StringVar(&fingerprint, "fingerprint", "", "peer fingerprint from a trusted channel")
	c.Flags().StringVar(&origin, "origin", "", "verified relay HTTPS origin")
	return c
}

func cloudServeCmd() *cobra.Command {
	return &cobra.Command{Use: "serve <instance-directory>", Short: "Serve only this authorized session until its lease expires", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := cloudintegration.Load(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), runtimeSignals()...)
		defer stop()
		return s.Run(ctx)
	}}
}
