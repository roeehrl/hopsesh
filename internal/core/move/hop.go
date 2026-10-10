package move

import (
	"context"
	"fmt"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A hop from one cloud to another is a composition: no cloud hands a session to another
// vendor's cloud, so the session comes here first (a fetch: Claude Code's teleport, Codex's
// diff and task, Copilot's log), stays here as a native local copy, and then goes on as a
// hand-off with a briefing rendered from that copy. This machine is the waypoint. The plan
// shows both legs and what the whole trip loses; the app carries the legs out one after
// the other, under one journal whose parts are the legs' own.

// KindHop is a cloud session handed on to another cloud through this machine.
const KindHop = "hop"

// HopPlan is a hop: the bring-back (Bring, a fetch plan) and the hand-off as far as it can
// be planned before the session is here (Then: the cloud, its driver and login, the
// environment, what it gets).
type HopPlan struct {
	From      string `json:"from"`
	FromTitle string `json:"fromTitle"`
	To        string `json:"to"`
	ToTitle   string `json:"toTitle"`
	// Via is this machine, where the session stops between the legs; Agent is the agent it
	// is in here ("Claude Code").
	Via   string `json:"via"`
	Agent string `json:"agent"`
	// Fidelity is each leg's ("native → brief"); Conversation says what reaches the second
	// cloud, for people.
	Fidelity     string   `json:"fidelity"`
	Conversation string   `json:"conversation"`
	Legs         []HopLeg `json:"legs"`
	// Code says how the code reaches the second cloud.
	Code  string       `json:"code"`
	Bring *Plan        `json:"bring"`
	Then  *HandoffPlan `json:"then"`
	// Terminal says what the first leg needs of the user's terminal ("" : nothing).
	Terminal string `json:"terminal,omitempty"`
	// Loss is everything the trip leaves behind, both legs.
	Loss []string `json:"loss"`
}

// Where a hop stands.
const (
	HopWaiting = "waiting" // the first leg waits for the driver in the user's terminal
	HopDone    = "done"
	HopFailed  = "failed"
)

// HopResult is where a hop stands: the journals of its legs, and while the first leg waits,
// the command the user runs (and what to do in it).
type HopResult struct {
	State   string `json:"state"`
	Message string `json:"message"`
	From    string `json:"from"`
	To      string `json:"to"`
	ToTitle string `json:"toTitle"`
	Fetch   string `json:"fetch,omitempty"`   // the first leg's journal
	Handoff string `json:"handoff,omitempty"` // the second leg's journal
	// Command and Run are the first leg's driver command while it waits; Note, what the
	// user must do in it.
	Command string        `json:"command,omitempty"`
	Run     agent.Command `json:"run"`
	Note    string        `json:"note,omitempty"`
	// Key is the copy here the second leg handed on (agent/session).
	Key string `json:"key,omitempty"`
	// Remembered: the second leg's environment became the repository's in the configuration
	// (the front end saves it).
	Remembered bool `json:"remembered,omitempty"`
}

// HopLeg is one leg of a hop, for people.
type HopLeg struct {
	Verb string `json:"verb"` // "Bring here", "Hand off"
	From string `json:"from"`
	To   string `json:"to"`
	// FromTitle and ToTitle are From and To for people: a cloud's title ("Claude Code
	// cloud"), a machine's name.
	FromTitle string `json:"fromTitle,omitempty"`
	ToTitle   string `json:"toTitle,omitempty"`
	Fidelity  string `json:"fidelity"`
	Words     string `json:"words"`
}

// PreviewHandoff plans a hand-off to a cloud before the session exists here (the second
// leg of a hop): the cloud's consent, its driver and login, the repository's host, the
// environment, the terminal. The code and the briefing are planned once the session is
// here. It writes nothing.
func PreviewHandoff(ctx context.Context, in HandoffInput, repo string, opt Options) *Plan {
	cl, target := in.Cloud, in.Module.Spec()
	_, follows := in.Module.(agent.CloudFollower)
	hp := &HandoffPlan{Cloud: cl.Name, CloudTitle: cl.Title, Agent: target.Name, Fidelity: cl.Up, Code: agent.ViaBranch, Remote: "origin",
		Conversation: "The cloud agent receives a briefing, not the conversation. Tool calls and hidden reasoning stay here.",
		Usage:        fmt.Sprintf("Cloud %ss use your plan's allowance.", cl.SessionNoun()), Noun: cl.SessionNoun(), Follow: follows,
		Limits: cl.Limits, Attempts: opt.Attempts, Repo: repo, HistoryPath: HistoryPath, Folder: in.Folder}
	if !follows {
		hp.NoFollowUp = cl.NoFollowUp
	}
	hp.Host, _, _ = strings.Cut(repo, "/")
	p := &Plan{Kind: KindHandoff, Target: Endpoint{Location: cl.Name, Version: in.Install.Version}, Options: opt, Handoff: hp}
	check := func(state, text string) {
		hp.Checks = append(hp.Checks, Check{State: state, Text: text})
		switch state {
		case "err":
			p.Blockers = append(p.Blockers, text)
		case "warn":
			p.Warnings = append(p.Warnings, text)
		}
	}
	if _, ok := in.Module.(agent.CloudSender); !ok {
		check("err", fmt.Sprintf("hopsesh does not reach %s yet", cl.Title))
		return p
	}
	if !in.Allowed {
		check("err", fmt.Sprintf("hopsesh leaves %s alone until you allow it (hopsesh clouds allow %s)", cl.Title, cl.Name))
	}
	checkHandoffDriver(ctx, in, check)
	switch {
	case repo == "":
		check("err", "hopsesh doesn't know which repository the session works on, so the next cloud can't get its code")
	case !contains(cl.Hosts, hp.Host):
		check("err", fmt.Sprintf("This repository's remote is %s. %s needs %s", hp.Host, cl.Title, strings.Join(cl.Hosts, ", ")))
	default:
		check("ok", hp.Host)
	}
	planHandoffEnv(p, in, opt, check)
	if needs(cl, agent.NeedTerminal) {
		hp.Terminal = fmt.Sprintf("%s starts the %s in a terminal: it runs `%s` in hopsesh's hand-off folder for this repository, where it may first ask whether you trust that folder. You answer it there; hopsesh only reads the %s's link it prints.",
			target.Name, cl.SessionNoun(), cl.Driver, cl.SessionNoun())
		if !in.Terminal {
			check("err", fmt.Sprintf("%s starts the %s only in a terminal you can answer, and this has none: run it in your own terminal, or from the app", target.Name, cl.SessionNoun()))
		}
	}
	hp.Cleanup = nonEmpty(opt.Cleanup, nonEmpty(in.Settings.DeleteBranch, CleanupAfterMerge))
	hp.Steps = []string{StepSnapshot, StepPush, StepStart, StepLineage}
	hp.Loss = append([]string{fmt.Sprintf("%s gets a briefing of the session here, not the session itself: its messages, tool calls and reasoning stay here", cl.Title)}, cl.Limits...)
	return p
}
