package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/spf13/cobra"
)

// cloudConnectHookCmd is the Claude cloud UserPromptSubmit hook. It acts only on
// two messages the person sends themselves: "hopsesh status" and a connect code
// from their machine. Both are blocked, so they never reach the model; every
// other prompt passes through untouched. The agent never claims, serves or exports.
func cloudConnectHookCmd() *cobra.Command {
	var caFile string
	c := &cobra.Command{Use: "connect-hook", Short: "Claude cloud prompt hook: show this session's identity or claim a pasted connect code", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		in, ours, err := cloudintegration.ReadUserPrompt(cmd.InOrStdin(), os.Getenv("CLAUDE_CODE_REMOTE"))
		if err != nil || !ours {
			return nil // never interfere with ordinary messages
		}
		return writePromptDecision(cmd.OutOrStdout(), connectHookReply(cmd, in, caFile))
	}}
	// Only for a private test relay; the installed hook uses the public relay's TLS.
	c.Flags().StringVar(&caFile, "ca-file", "", "absolute local PEM trust root for a private relay")
	_ = c.Flags().MarkHidden("ca-file")
	return c
}

func connectHookReply(cmd *cobra.Command, in cloudintegration.UserPrompt, caFile string) string {
	workspace := os.Getenv("CLAUDE_PROJECT_DIR")
	if workspace == "" {
		workspace = in.Workspace
	}
	if p, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = p
	}
	parent, err := filepath.EvalSymlinks(filepath.Join(config.StateDir(), "cloud-sessions"))
	if err != nil {
		return "hopsesh: this session has no prepared identity. The repository's SessionStart hook prepares one; start a new session or check that the hopsesh cloud startup files are committed."
	}
	instance, err := cloudintegration.Current(cmd.Context(), parent, "claude-hosted", in.Session, workspace)
	if err != nil {
		return "hopsesh: this session has no current prepared identity (" + err.Error() + "). A resumed or rebuilt session prepares a new one at startup."
	}
	if in.IsStatus() {
		state := "not connected"
		if c, err := (relay.Store{Directory: instance.Directory}).PublicConnection(); err == nil && c.Device == instance.Public.ID && c.Expires > time.Now().Unix() {
			state = "connected until " + time.Unix(c.Expires, 0).UTC().Format(time.RFC3339)
		}
		export := "not allowed by this repository's startup"
		if instance.ExportTranscript {
			export = "allowed once you approve this identity on your machine"
		}
		return fmt.Sprintf("hopsesh (not sent to Claude): native session %s, identity %s, expires %s, %s. Conversation checkpoints: %s. To connect, run on your machine:\nhopsesh cloud-integration ticket --provider claude-hosted --session %s --connect-code\nthen send the printed hopsesh-connect line here. To bring this session home instead, run hopsesh pull with its claude.ai link.",
			instance.Session, instance.Public.ID, instance.Expires.UTC().Format(time.RFC3339), state, export, instance.Session)
	}
	fingerprint, invitation, err := cloudintegration.ParseConnectCode(in.Prompt)
	if err != nil {
		return "hopsesh could not connect (not sent to Claude): " + err.Error()
	}
	ticket, err := readAdmission(bytes.NewReader(invitation))
	if err != nil {
		return "hopsesh could not connect (not sent to Claude): " + err.Error()
	}
	claimed, err := claimCloudAdmission(cmd.Context(), instance.Directory, ticket, fingerprint, caFile)
	if err != nil {
		return "hopsesh could not connect (not sent to Claude): " + err.Error()
	}
	if err = startDetachedServe(instance.Directory); err != nil {
		return "hopsesh claimed the invitation but could not start the connector (not sent to Claude): " + err.Error()
	}
	return fmt.Sprintf("hopsesh connected this session (not sent to Claude). Owner %s, routing until %s, logical task %s. On your machine, approve this session's identity %s, comparing it with this message, before observing or importing it.",
		fingerprint, time.Unix(claimed.Expires, 0).UTC().Format(time.RFC3339), claimed.Task, claimed.Public.ID)
}

// writePromptDecision blocks the prompt: Claude Code shows the reason to the person
// and does not add the prompt to the conversation.
func writePromptDecision(w io.Writer, reason string) error {
	return json.NewEncoder(w).Encode(struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}{"block", reason})
}
