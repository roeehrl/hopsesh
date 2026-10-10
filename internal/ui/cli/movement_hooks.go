package cli

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/spf13/cobra"
)

const noticeHookInputLimit = 64 << 10
const noticeHookTextLimit = 2048
const noticeHookTimeout = 2 * time.Second

type preparedMovementNotice struct {
	Text    string
	Written func(context.Context) error
	// Block refuses the prompt (UserPromptSubmit) instead of adding context; Text is
	// then the reason the agent shows. A block repeats on every prompt.
	Block bool
}
type movementNoticePrepare func(context.Context, agent.ID, string, string, string, string) (preparedMovementNotice, error)

func noticeHookCmd() *cobra.Command {
	var id, profile string
	cmd := &cobra.Command{Use: "notice-hook", Short: "Block or advise a moved session's original from an agent lifecycle hook", Hidden: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			useDirFlags(cmd)
			// A hook fails open and emits no diagnostics or non-JSON output. Do not use
			// newRun: no audit log, skill offers, password readers, or session scan.
			return runPreparedNoticeHook(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), agent.ID(id), profile, func(ctx context.Context, id agent.ID, profile, session, path, event string) (preparedMovementNotice, error) {
				cfg, err := config.Load()
				if err != nil || cfg.OriginalGuard() == app.GuardOff {
					return preparedMovementNotice{}, nil
				}
				a := app.New(cfg, modules, config.StateDir(), nil)
				details, err := a.MovementNoticeDetailsForPath(ctx, id, profile, session, path)
				if err != nil || details.Key.Session == "" {
					return preparedMovementNotice{}, err
				}
				if a.OriginalGuard(details.Key, details.Operation, details.Status) == app.GuardBlock {
					return preparedMovementNotice{Text: app.BlockReason(details.Summary, details.Key), Block: true}, nil
				}
				identity := app.MovementNoticeIdentity{Operation: details.Operation, Status: details.Status}
				claim, err := a.ClaimMovementNotice(ctx, details.Key.Agent, details.Key.Profile, string(details.Key.Session), event, details.Text, identity)
				if err != nil || claim == nil {
					return preparedMovementNotice{}, err
				}
				return preparedMovementNotice{Text: details.Text, Written: claim.Written}, nil
			})
		},
	}
	cmd.Flags().StringVar(&id, "agent", "", "agent module id")
	cmd.Flags().StringVar(&profile, "profile", "", "exact registered profile id (empty selects the default root)")
	addDirFlags(cmd)
	return cmd
}

func runPreparedNoticeHook(ctx context.Context, in io.Reader, out io.Writer, id agent.ID, profile string, prepare movementNoticePrepare) error {
	if id != "claude" && id != "codex" || len(profile) > 128 || strings.ContainsFunc(profile, unicode.IsControl) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, noticeHookTimeout)
	defer cancel()
	done := make(chan struct{})
	// The deadline includes a stalled stdin and even a misbehaving lookup. This
	// command is a one-shot executable. A blocked write cannot delay its exit,
	// and a completion after cancellation cannot acknowledge delivery.
	go func() {
		defer close(done)
		raw, err := io.ReadAll(io.LimitReader(in, noticeHookInputLimit+1))
		if err != nil || len(raw) > noticeHookInputLimit {
			return
		}
		var payload struct {
			SessionID      string `json:"session_id"`
			TranscriptPath string `json:"transcript_path"`
			Event          string `json:"hook_event_name"`
		}
		if json.Unmarshal(raw, &payload) != nil || payload.SessionID == "" || len(payload.SessionID) > 256 || strings.ContainsFunc(payload.SessionID, unicode.IsControl) || len(payload.TranscriptPath) > 4096 || strings.ContainsRune(payload.TranscriptPath, 0) || payload.Event != "SessionStart" && payload.Event != "UserPromptSubmit" {
			return
		}
		// Path is untrusted input, used solely by the app's bounded local resolver.
		prepared, err := prepare(ctx, id, profile, payload.SessionID, payload.TranscriptPath, payload.Event)
		notice := prepared.Text
		if err != nil || notice == "" || ctx.Err() != nil {
			return
		}
		notice = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || r == 0x2028 || r == 0x2029 || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
				return ' '
			}
			return r
		}, notice)
		if len(notice) > noticeHookTextLimit {
			notice = notice[:noticeHookTextLimit]
			for !utf8.ValidString(notice) {
				notice = notice[:len(notice)-1]
			}
		}
		var b []byte
		switch {
		case prepared.Block && payload.Event == "UserPromptSubmit":
			// Claude Code and Codex both refuse the prompt and show the reason.
			b, _ = json.Marshal(struct {
				Decision string `json:"decision"`
				Reason   string `json:"reason"`
			}{"block", notice})
		default:
			b, _ = json.Marshal(struct {
				SystemMessage      string `json:"systemMessage"`
				HookSpecificOutput struct {
					Event   string `json:"hookEventName"`
					Context string `json:"additionalContext"`
				} `json:"hookSpecificOutput"`
			}{SystemMessage: notice, HookSpecificOutput: struct {
				Event   string `json:"hookEventName"`
				Context string `json:"additionalContext"`
			}{payload.Event, notice}})
		}
		b = append(b, '\n')
		if ctx.Err() != nil {
			return
		}
		n, err := out.Write(b)
		if err == nil && n == len(b) && ctx.Err() == nil && prepared.Written != nil {
			_ = prepared.Written(ctx)
		}
	}()
	select {
	case <-ctx.Done():
		return nil
	case <-done:
		return nil
	}
}

func noticesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "notices", Short: "Install, remove, or inspect the protection hooks that block or advise moved sessions' originals", Args: cobra.NoArgs}
	for _, action := range []string{"install", "remove", "status"} {
		var id, profile string
		sub := &cobra.Command{Use: action, Short: strings.ToUpper(action[:1]) + action[1:] + " protection hooks", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				useDirFlags(cmd)
				r, err := newRun(cmd)
				if err != nil {
					return err
				}
				defer r.app.Catalog.Close()
				// The same executable as the app chooses: a different command string would be a
				// second hook, and Codex would ask the user to review it again.
				var statuses []app.MovementHookStatus
				switch action {
				case "install":
					statuses, err = r.app.InstallNoticeHooks(cmd.Context(), agent.ID(id), profile)
				case "remove":
					statuses, err = r.app.RemoveNoticeHooks(cmd.Context(), agent.ID(id), profile)
				default:
					statuses, err = r.app.NoticeHooksFor(cmd.Context(), agent.ID(id), profile)
				}
				if r.jsonOut {
					_ = r.emitJSON(statuses)
				} else {
					for _, st := range statuses {
						state := "absent"
						if st.Installed {
							state = "installed"
						}
						if t := st.Trust; t != nil {
							state += map[string]string{agent.HookTrusted: "; trusted by the agent", agent.HookNeedsReview: "; NOT RUNNING: waiting for your approval in the agent", agent.HookDisabled: "; NOT RUNNING: turned off in the agent", agent.HookMissing: "; not seen by the agent", agent.HookUnknown: "; trust not verified"}[t.State]
						}
						if !st.Enabled {
							state += "; protection off"
						}
						r.printf("%s profile=%q: %s\n  %s\n", st.Agent, st.Profile, state, st.Path)
						if st.Reason != "" {
							r.printf("  %s\n", st.Reason)
						}
						if t := st.Trust; t != nil && t.State != agent.HookTrusted && t.Fix != "" {
							r.printf("  %s\n", t.Fix)
						}
					}
				}
				return err
			},
		}
		sub.Flags().StringVar(&id, "agent", "", "agent module id (default: all supported agents)")
		sub.Flags().StringVar(&profile, "profile", "", "exact registered local profile id (default: all local profiles)")
		sub.Flags().Bool("json", false, "output JSON")
		addDirFlags(sub)
		cmd.AddCommand(sub)
	}
	return cmd
}
