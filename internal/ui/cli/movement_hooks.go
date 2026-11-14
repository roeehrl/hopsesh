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
type movementNoticePrepare func(ctx context.Context, id agent.ID, profile, session, path, event, source string) (preparedMovementNotice, error)

func noticeHookCmd() *cobra.Command {
	var id, profile string
	cmd := &cobra.Command{Use: "notice-hook", Short: "Block or advise a moved session's original from an agent lifecycle hook", Hidden: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			useDirFlags(cmd)
			// A hook fails open and emits no diagnostics or non-JSON output. Do not use
			// newRun: no audit log, skill offers, password readers, or session scan.
			if !takesNoticeHooks(agent.ID(id)) {
				return nil
			}
			return runPreparedNoticeHook(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), agent.ID(id), profile, func(ctx context.Context, id agent.ID, profile, session, path, event, source string) (preparedMovementNotice, error) {
				cfg, err := config.Load()
				if err != nil {
					return preparedMovementNotice{}, nil
				}
				a := app.New(cfg, modules, config.StateDir(), nil)
				// Which account the session runs under now, for its owner record.
				a.RecordOwner(ctx, id, profile, session)
				if cfg.OriginalGuard() == app.GuardOff {
					return preparedMovementNotice{}, nil
				}
				// Work was added while this window was open: reopening loads it.
				if text, block, handled := a.ReopenNotice(ctx, id, profile, session, event, source); handled {
					return preparedMovementNotice{Text: text, Block: block}, nil
				}
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

// takesNoticeHooks reports whether agent id's module installs notice hooks.
func takesNoticeHooks(id agent.ID) bool {
	if modules == nil {
		return false
	}
	m, ok := modules.Get(id)
	if !ok {
		return false
	}
	_, ok = m.(agent.MovementHookIntegrator)
	return ok
}

func runPreparedNoticeHook(ctx context.Context, in io.Reader, out io.Writer, id agent.ID, profile string, prepare movementNoticePrepare) error {
	return runNoticeHookWith(ctx, in, out, noticeProtocol(id), id, profile, prepare)
}

// noticeProtocol is the JSON agent id's notice hook speaks: its module's own (an agent
// plugin), else that of Claude Code's and Codex's command hooks.
func noticeProtocol(id agent.ID) agent.NoticeHookProtocol {
	if modules != nil {
		if m, ok := modules.Get(id); ok {
			if p, ok := m.(agent.NoticeHookProtocol); ok {
				return p
			}
		}
	}
	return commandHookProtocol{}
}

func runNoticeHookWith(ctx context.Context, in io.Reader, out io.Writer, proto agent.NoticeHookProtocol, id agent.ID, profile string, prepare movementNoticePrepare) error {
	if id == "" || len(profile) > 128 || strings.ContainsFunc(profile, unicode.IsControl) {
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
		call, ok := proto.DecodeNoticeHook(raw)
		if !ok || call.Session == "" || len(call.Session) > 256 || strings.ContainsFunc(call.Session, unicode.IsControl) || len(call.Path) > 4096 || strings.ContainsRune(call.Path, 0) || call.Event != "SessionStart" && call.Event != "UserPromptSubmit" || len(call.Source) > 64 {
			return
		}
		// Path is untrusted input, used solely by the app's bounded local resolver.
		prepared, err := prepare(ctx, id, profile, call.Session, call.Path, call.Event, call.Source)
		if err != nil || ctx.Err() != nil {
			return // fail open, saying nothing: a plugin counts that as a failure
		}
		notice := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || r == 0x2028 || r == 0x2029 || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
				return ' '
			}
			return r
		}, prepared.Text)
		if len(notice) > noticeHookTextLimit {
			notice = notice[:noticeHookTextLimit]
			for !utf8.ValidString(notice) {
				notice = notice[:len(notice)-1]
			}
		}
		b := proto.EncodeNoticeHook(call, agent.NoticeHookAnswer{Text: notice, Block: prepared.Block && notice != ""})
		if len(b) == 0 || ctx.Err() != nil {
			return
		}
		b = append(b, '\n')
		n, err := out.Write(b)
		if err == nil && n == len(b) && ctx.Err() == nil && notice != "" && prepared.Written != nil {
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

// commandHookProtocol is the JSON of Claude Code's and Codex's command hooks.
type commandHookProtocol struct{}

func (commandHookProtocol) DecodeNoticeHook(raw []byte) (agent.NoticeHookCall, bool) {
	var payload struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
		Event          string `json:"hook_event_name"`
		Source         string `json:"source"` // SessionStart: startup, resume, clear or compact
	}
	if json.Unmarshal(raw, &payload) != nil {
		return agent.NoticeHookCall{}, false
	}
	return agent.NoticeHookCall{Session: payload.SessionID, Path: payload.TranscriptPath, Event: payload.Event, Source: payload.Source}, true
}

func (commandHookProtocol) EncodeNoticeHook(call agent.NoticeHookCall, a agent.NoticeHookAnswer) []byte {
	var b []byte
	switch {
	case a.Text == "":
		return nil
	case a.Block && call.Event == "UserPromptSubmit":
		// Claude Code and Codex both refuse the prompt and show the reason.
		b, _ = json.Marshal(struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}{"block", a.Text})
	default:
		b, _ = json.Marshal(struct {
			SystemMessage      string `json:"systemMessage"`
			HookSpecificOutput struct {
				Event   string `json:"hookEventName"`
				Context string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}{SystemMessage: a.Text, HookSpecificOutput: struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		}{call.Event, a.Text}})
	}
	return b
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
				if err == nil {
					err = app.MovementHookErrors(action, statuses)
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
