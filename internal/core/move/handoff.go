package move

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/core/scan"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Handing a session off to a vendor's cloud. No cloud takes a conversation, so a hand-off
// is a continuation: the cloud agent gets a briefing as its first prompt, and the code on
// a branch it clones. A clean branch already on the remote goes as it is; anything else
// goes onto a handoff branch built from a snapshot commit (the user's checkout stays as it
// is), or, where the cloud takes one, an upload the driver makes itself. Credential-like
// files never go. The session left here is marked as continued in the cloud, the lineage
// records the hop, and undo deletes the branch (with a lease) and the mark; the cloud
// session itself stays, as a step the user owes, when its CLI cannot archive it.

// KindHandoff is a session handed off to a cloud.
const KindHandoff = "handoff"

// Steps of a hand-off, in order.
const (
	StepSnapshot = "snapshot"
	StepPush     = "push"
	StepStart    = "start"
	StepLineage  = "lineage"
	StepMark     = "mark"
)

// Step states.
const (
	StepTodo    = "todo"
	StepDone    = "done"
	StepFailed  = "failed"
	StepSkipped = "skipped"
)

// HistoryPath is where the conversation goes on the handoff branch when the user asks.
const HistoryPath = ".hopsesh/handoff.md"

// HandoffSettings are a cloud's hand-off settings (the configuration's [clouds.<name>]).
type HandoffSettings struct {
	Code         string   // "branch" or "bundle"
	Untracked    []string // untracked files (globs) carried by default
	BranchPrefix string
	DeleteBranch string // never | after-merge | on-undo
}

// Branch cleanup choices.
const (
	CleanupNever      = "never"
	CleanupAfterMerge = "after-merge"
	CleanupOnUndo     = "on-undo"
)

// HandoffInput is what planning a hand-off needs.
type HandoffInput struct {
	Source  Side // the session's machine, module and install
	Session agent.Summary
	Live    agent.LiveInfo
	Git     *repos.GitState
	GitErr  string
	Lineage *lineage.Manifest
	// Runner is git on the session's machine (nil when hopsesh cannot run git there).
	Runner repos.Git
	// Here is this machine, which runs the cloud's driver (the vendor login is here).
	Here    *host.Machine
	Module  agent.Module  // the module that reaches the cloud
	Install agent.Install // its install here
	Host    agent.Host    // its Host here for the cloud capabilities (scrubbed environment)
	Cloud   agent.Cloud
	// Checkout is a checkout of the repository here, for the driver's worktree ("": a
	// temporary clone).
	Checkout  string
	Settings  HandoffSettings
	Allowed   bool
	Worktrees []string
	// Env is the cloud environment configured for the repository ("" for none); Envs, the
	// ones the cloud's listing shows (suggestions), for a cloud that needs one.
	Env  string
	Envs []EnvChoice
	// Tester probes the cloud read-only in the module's place (a recent probe, so replanning
	// does not ask the vendor each time); nil: the module's CloudTester runs.
	Tester func(context.Context) (agent.CloudTest, error)
	// Folder is the repository's hand-off folder here, where the driver runs
	// (repos.HandoffFolder; "": a temporary one).
	Folder string
	// Terminal: the front end can run a terminal step (Env.Step); a cloud whose driver needs
	// one (NeedTerminal) cannot be handed off without it.
	Terminal bool
}

// EnvChoice is a cloud environment the user can pick for a hand-off.
type EnvChoice struct {
	Value string `json:"value"` // what the driver gets: the id, else the name
	Name  string `json:"name"`  // for people: "acme-api"
	// Label is the choice in a list: "acme-api (used by your last 3 tasks)".
	Label string `json:"label"`
	// Tasks is how many of the listed sessions ran in it.
	Tasks int `json:"tasks,omitempty"`
	// Configured: the configuration names it for this repository.
	Configured bool `json:"configured,omitempty"`
}

// maxStartingDiff bounds the changes offered as a starting diff (they travel with the
// cloud session itself; more go on a branch).
const maxStartingDiff = 256 << 10

// HandoffPlan is the cloud part of a hand-off plan.
type HandoffPlan struct {
	Cloud      string         `json:"cloud"`
	CloudTitle string         `json:"cloudTitle"`
	Agent      string         `json:"agent"` // the agent that runs there ("Claude Code")
	Fidelity   agent.Fidelity `json:"fidelity"`
	// Conversation says what becomes of the conversation, for people.
	Conversation string `json:"conversation"`
	// Brief is the first prompt; Tokens, its size; Masked, the secrets masked in it (and
	// MaskedRules, which kinds); Edited: the user's own text.
	Brief       string   `json:"brief"`
	Tokens      int      `json:"tokens"`
	Masked      int      `json:"masked"`
	MaskedRules []string `json:"maskedRules,omitempty"`
	Shortened   bool     `json:"shortened,omitempty"`
	Edited      bool     `json:"edited,omitempty"`
	// Carried, Changed and Stays sum up the conversation's part (the plan's boxes).
	Carried []string `json:"carried"`
	Changed []string `json:"changed"`
	Stays   []string `json:"stays"`
	// The code: how it goes (branch or bundle), on which branch, from which commit.
	Code agent.CodeWay `json:"code"`
	// Reuse: the session's own branch is clean and on the remote, so the cloud clones it
	// and nothing is pushed.
	Reuse      bool   `json:"reuse,omitempty"`
	Branch     string `json:"branch,omitempty"`
	BranchURL  string `json:"branchUrl,omitempty"`
	Base       string `json:"base,omitempty"`       // the commit it builds on (HEAD)
	BaseBranch string `json:"baseBranch,omitempty"` // the session's branch
	Remote     string `json:"remote"`               // the remote's name ("origin")
	Repo       string `json:"repo,omitempty"`       // host/owner/repo
	Host       string `json:"host,omitempty"`
	Unpushed   int    `json:"unpushed,omitempty"`
	// Tracked and Untracked are the files the snapshot carries; Offered, untracked files
	// it carries only when chosen; Withheld, files that stay on the machine whatever is
	// chosen.
	Tracked   []string          `json:"tracked,omitempty"`
	Untracked []string          `json:"untracked,omitempty"`
	Offered   []repos.Candidate `json:"offered,omitempty"`
	Withheld  []repos.Withheld  `json:"withheld,omitempty"`
	// HistoryFile: the conversation is committed as HistoryPath on the branch;
	// HistoryWarning says why that may expose it.
	HistoryFile    bool   `json:"historyFile"`
	HistoryPath    string `json:"historyPath"`
	HistoryWarning string `json:"historyWarning"`
	// MarkTitle is the mark the session here gets (Plan.Mark says when).
	MarkTitle string `json:"markTitle"`
	// Cleanup is when hopsesh deletes the handoff branch; the choices for the user.
	Cleanup  string   `json:"cleanup"`
	Cleanups []Choice `json:"cleanups"`
	Checks   []Check  `json:"checks"`
	Usage    string   `json:"usage"`
	Notes    []string `json:"notes,omitempty"`
	// CanBundle: the cloud takes an upload; BundleOffer says why it is the way to go here
	// (a repository it cannot clone), "" when the branch works.
	CanBundle   bool   `json:"canBundle"`
	BundleOffer string `json:"bundleOffer,omitempty"`
	// CanStartingDiff: the session's branch is on the remote as it is here and the changes
	// are small, so they can go with the cloud session as a starting diff instead of on a
	// new branch (Options.StartingDiff); StartingDiffOffer says so for people.
	CanStartingDiff   bool   `json:"canStartingDiff,omitempty"`
	StartingDiffOffer string `json:"startingDiffOffer,omitempty"`
	// EnvNeeded: the cloud runs in an environment the user picks (Env, its name EnvName);
	// Envs are the choices hopsesh knows of; EnvNote says what to do when none is picked.
	EnvNeeded bool        `json:"envNeeded,omitempty"`
	Env       string      `json:"env,omitempty"`
	EnvName   string      `json:"envName,omitempty"`
	Envs      []EnvChoice `json:"envs,omitempty"`
	EnvNote   string      `json:"envNote,omitempty"`
	// Remember: the picked environment becomes the repository's in the configuration once
	// the hand-off works (none was configured).
	Remember bool `json:"remember,omitempty"`
	Attempts int  `json:"attempts,omitempty"` // asked of the cloud (0: its default)
	// Noun is what the cloud calls its sessions ("task"); Follow: it takes follow-ups from
	// hopsesh, else NoFollowUp may say why; Limits are what hopsesh cannot reach there.
	Noun       string   `json:"noun"`
	Follow     bool     `json:"follow,omitempty"`
	NoFollowUp string   `json:"noFollowUp,omitempty"`
	Limits     []string `json:"limits,omitempty"`
	// Terminal says, for a cloud whose driver starts the session in the user's terminal,
	// what happens there (the driver may ask whether Folder is trusted; the user answers).
	Terminal string `json:"terminal,omitempty"`
	Folder   string `json:"folder,omitempty"`
	// Steps are what applying does, in order (Step*).
	Steps []string `json:"steps"`
	// Loss is everything that stays here, for the loss list.
	Loss []string `json:"loss"`
	// Tally counts what the conversation holds.
	Tally convert.Tally `json:"tally"`

	snapshot repos.SnapshotPlan
	nodes    []ir.Node
	head     ir.Cursor
}

// Choice is one option of a choice, for people.
type Choice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// HandoffStep is one step of a hand-off as it went.
type HandoffStep struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// HandoffResult is what a hand-off did (the done screen, and the command line's JSON).
type HandoffResult struct {
	Cloud      string `json:"cloud"`
	CloudTitle string `json:"cloudTitle"`
	Agent      string `json:"agent"`
	Session    string `json:"session,omitempty"` // the cloud session's id
	URL        string `json:"url,omitempty"`
	State      string `json:"state,omitempty"`
	Repo       string `json:"repo,omitempty"`
	Branch     string `json:"branch,omitempty"`
	BranchURL  string `json:"branchUrl,omitempty"`
	Code       string `json:"code"`
	Reuse      bool   `json:"reuse,omitempty"`
	Pushed     bool   `json:"pushed,omitempty"` // the handoff branch went to the remote
	Snapshot   string `json:"snapshot,omitempty"`
	Tokens     int    `json:"tokens"`
	Masked     int    `json:"masked"`
	// Stayed is what stayed on the machine.
	Stayed []string      `json:"stayed"`
	Steps  []HandoffStep `json:"steps"`
	// Failed is the step that failed; Message, what happened, in words.
	Failed  string `json:"failed,omitempty"`
	Message string `json:"message"`
	// Retry is another way that may work after a failure ("bundle": as an upload).
	Retry string `json:"retry,omitempty"`
	// Manual is what undo cannot do and the user may: the cloud session stays there.
	Manual   string `json:"manual"`
	MarkText string `json:"markText,omitempty"`
	Hint     string `json:"hint"`
	// Noun is what the cloud calls its sessions ("task"); Follow: it takes follow-ups, else
	// NoFollowUp may say why.
	Noun       string `json:"noun"`
	Follow     bool   `json:"follow,omitempty"`
	NoFollowUp string `json:"noFollowUp,omitempty"`
	// Pasted: the session's link came from the user, not from what the driver printed.
	Pasted bool `json:"pasted,omitempty"`
	// Env and EnvName are the environment it runs in; Attempts, how many were asked for.
	Env      string `json:"env,omitempty"`
	EnvName  string `json:"envName,omitempty"`
	Attempts int    `json:"attempts,omitempty"`
}

var stepLabels = map[string]string{StepSnapshot: "Snapshot", StepPush: "Push branch", StepStart: "Start cloud session",
	StepLineage: "Record lineage", StepMark: "Mark this session"}

// BuildHandoff works out a hand-off. It reads (git's state, the conversation, the driver's
// login, the remote's branches) and writes nothing.
func BuildHandoff(ctx context.Context, in HandoffInput, opt Options) (*Plan, error) {
	cl := in.Cloud
	if _, ok := in.Module.(agent.CloudSender); !ok {
		return nil, fmt.Errorf("%w: hopsesh does not reach %s yet", agent.ErrUnsupported, cl.Title)
	}
	src, s := in.Source, in.Session
	spec, target := src.Module.Spec(), in.Module.Spec()
	_, follows := in.Module.(agent.CloudFollower)
	hp := &HandoffPlan{Cloud: cl.Name, CloudTitle: cl.Title, Agent: target.Name, Fidelity: cl.Up, Code: agent.ViaBranch, Remote: "origin",
		HistoryFile: opt.HistoryFile, HistoryPath: HistoryPath, CanBundle: hasWay(cl.CodeUp, agent.ViaBundle),
		Conversation: "The cloud agent receives a briefing, not this conversation. Tool calls and hidden reasoning stay here.",
		Usage:        fmt.Sprintf("Cloud %ss use your plan's allowance.", cl.SessionNoun()), Noun: cl.SessionNoun(), Follow: follows,
		Limits: cl.Limits, Attempts: opt.Attempts, Folder: in.Folder}
	if !follows {
		hp.NoFollowUp = cl.NoFollowUp
	}
	p := &Plan{Kind: KindHandoff, Key: s.Key, Title: s.Title, Agent: spec.Name, Live: in.Live.State == agent.Live, Options: opt,
		Source:  Endpoint{Location: src.Machine.Name, OS: src.Machine.Facts.OS, CWD: s.CWD, Path: s.Path, Version: s.AgentVersion},
		Target:  Endpoint{Location: cl.Name, Version: in.Install.Version},
		Handoff: hp, handoffIn: &in}
	if p.Title == "" {
		p.Title = "Session " + shortStr(string(s.Key.Session), 8)
	}
	check := func(state, text string) {
		hp.Checks = append(hp.Checks, Check{State: state, Text: text})
		switch state {
		case "err":
			p.Blockers = append(p.Blockers, text)
		case "warn":
			p.Warnings = append(p.Warnings, text)
		}
	}
	if !in.Allowed {
		check("err", fmt.Sprintf("hopsesh leaves %s alone until you allow it (hopsesh clouds allow %s)", cl.Title, cl.Name))
	}

	// The driver and its login.
	switch v := in.Install.Version; {
	case in.Install.Binary == "":
		check("err", fmt.Sprintf("%s is reached through the `%s` command, which isn't installed here", cl.Title, cl.Driver))
	case v != "" && !cl.TestedWith(v):
		check("warn", fmt.Sprintf("%s %s hasn't been tested for cloud hand-off; hopsesh tested %s. You can still go ahead", target.Name, v, strings.Join(cl.Tested, ", ")))
	case v != "":
		check("ok", cl.Driver+" "+v)
	}
	if t, ok := in.Module.(agent.CloudTester); ok && in.Install.Binary != "" {
		test := in.Tester
		if test == nil {
			test = func(ctx context.Context) (agent.CloudTest, error) {
				return t.TestCloud(ctx, in.Host, in.Install, cl.Name)
			}
		}
		ct, err := test(ctx)
		switch {
		case err != nil:
			check("err", refusal(err, target, cl))
		default:
			if ct.Account != "" {
				check("ok", "Signed in with "+ct.Account)
			}
			for _, c := range ct.Checks[1:] {
				if !c.OK {
					check("warn", c.Text)
				}
			}
		}
	}

	// The repository.
	g := in.Git
	switch {
	case g == nil && in.GitErr != "":
		check("err", fmt.Sprintf("hopsesh could not read the session's git checkout on %s (%s); try again", src.Machine.Name, in.GitErr))
	case g == nil || !g.IsRepo || g.Identity == "":
		check("err", "This session isn't in a git repository with a remote, so a cloud can't get its code")
	}
	bundle := opt.Bundle || in.Settings.Code == "bundle"
	if g != nil && g.IsRepo && g.Identity != "" {
		hp.Repo, hp.BaseBranch = g.Identity, g.Branch
		hp.Host, _, _ = strings.Cut(g.Identity, "/")
		onHost := contains(cl.Hosts, hp.Host)
		switch {
		case !onHost && hp.CanBundle:
			hp.BundleOffer = fmt.Sprintf("This repository's remote is %s. %s needs %s to clone it; it can take it as an upload but can't push results back.", hp.Host, cl.Title, strings.Join(cl.Hosts, ", "))
			if !bundle {
				check("err", hp.BundleOffer)
			} else {
				check("warn", fmt.Sprintf("%s takes the repository as an upload; it can't push its work back to %s", cl.Title, hp.Host))
			}
		case !onHost:
			check("err", fmt.Sprintf("This repository's remote is %s. %s needs %s", hp.Host, cl.Title, strings.Join(cl.Hosts, ", ")))
		default:
			check("ok", hp.Host)
		}
		if bundle && !hp.CanBundle {
			check("err", cl.Title+" takes no uploads; hand it off over a branch")
		}
		if bundle {
			hp.Code = agent.ViaBundle
		}
		if g.Branch == "" {
			check("err", "The session's checkout is not on a branch (detached HEAD); check out a branch first")
		}
		if in.Runner == nil {
			check("err", fmt.Sprintf("hopsesh can't run git on %s to snapshot the code; bring the session here first, then hand it off", src.Machine.Name))
		} else if bundle && !src.Machine.Local {
			check("err", fmt.Sprintf("An upload goes from this machine: bring the session here from %s first, or hand it off over a branch", src.Machine.Name))
		}
	}
	if len(p.Blockers) == 0 {
		planHandoffCode(ctx, p, in, opt, check)
	}
	planHandoffEnv(p, in, opt, check)
	// The driver's terminal, after the code is planned (the plan still shows it).
	if needs(cl, agent.NeedTerminal) {
		hp.Terminal = fmt.Sprintf("%s starts the %s in a terminal: it runs `%s` in hopsesh's hand-off folder for this repository, where it may first ask whether you trust that folder. You answer it there; hopsesh only reads the %s's link it prints.",
			target.Name, cl.SessionNoun(), cl.Driver, cl.SessionNoun())
		if !in.Terminal {
			check("err", fmt.Sprintf("%s starts the %s only in a terminal you can answer, and this has none: run the hand-off in your own terminal (hopsesh handoff … --to %s), or from the app", target.Name, cl.SessionNoun(), cl.Name))
		}
	}

	// The conversation.
	planBrief(ctx, p, in, opt, check)

	// The session left here.
	mk := agent.Mark{Kind: agent.MarkContinued, AgentName: target.Name, Location: cl.Name}
	hp.MarkTitle = agent.MarkTitle(mk, "")
	_, canMark := src.Module.(agent.Marker)
	switch {
	case !opt.Mark || !canMark:
		p.Mark = MarkOff
	case p.Live:
		p.Mark = MarkWhenStopped
		check("warn", fmt.Sprintf("The session is still open on %s. The cloud gets it as it is now; it is marked once it ends", src.Machine.Name))
	default:
		p.Mark = MarkNow
	}
	if m := s.Mark; m != nil {
		check("warn", fmt.Sprintf("This copy was already moved on (%s); the newest copy is probably elsewhere", agent.MarkTitle(*m, "")))
	}

	hp.Cleanup = nonEmpty(opt.Cleanup, nonEmpty(in.Settings.DeleteBranch, CleanupAfterMerge))
	hp.Cleanups = []Choice{{CleanupAfterMerge, "Delete it after I bring the work back and it is merged"}, {CleanupOnUndo, "Delete it only if I undo"}, {CleanupNever, "Keep it"}}
	if hp.HistoryFile {
		hp.Notes = append(hp.Notes, "Private history is safer in a cloud environment whose network access is None or Trusted; hopsesh never changes environment settings")
	}
	for _, st := range []string{StepSnapshot, StepPush, StepStart, StepLineage, StepMark} {
		switch {
		case st == StepSnapshot && hp.Reuse, st == StepPush && (hp.Reuse || hp.Code != agent.ViaBranch), st == StepMark && p.Mark == MarkOff:
			continue
		}
		hp.Steps = append(hp.Steps, st)
	}
	hp.Loss = handoffLoss(hp, spec)
	return p, nil
}

// planHandoffCode decides how the code goes: the session's branch as it is when it is
// clean and on the remote, else a snapshot on a new handoff branch (pushed, or uploaded
// by the driver).
func planHandoffCode(ctx context.Context, p *Plan, in HandoffInput, opt Options, check func(string, string)) {
	hp, g := p.Handoff, in.Git
	top := g.Toplevel
	sp, err := repos.PlanSnapshot(ctx, in.Runner, top, repos.SnapshotOptions{Include: append(append([]string(nil), in.Settings.Untracked...), opt.Untracked...), Exclude: in.Worktrees})
	if err != nil {
		check("err", "hopsesh could not read the checkout's changes: "+firstLine(err.Error()))
		return
	}
	hp.snapshot, hp.Base = sp, sp.Head
	hp.Tracked, hp.Untracked, hp.Offered, hp.Withheld = sp.Tracked, sp.Untracked, sp.Offered, sp.Withheld
	unpushed, _ := g.LeftBehind()
	hp.Unpushed = unpushed
	onRemote := ""
	if g.Branch != "" {
		onRemote, err = repos.RemoteRef(ctx, in.Runner, top, hp.Remote, "refs/heads/"+g.Branch)
		if err != nil {
			check("warn", "hopsesh could not ask the remote about "+g.Branch+": "+firstLine(err.Error()))
		}
	}
	clean := g.Branch != "" && onRemote == sp.Head
	if clean && sp.Changes() && !hp.HistoryFile && hp.Code == agent.ViaBranch && hasWay(in.Cloud.CodeUp, agent.ViaStartingDiff) && sp.Bytes <= maxStartingDiff {
		hp.CanStartingDiff = true
		hp.StartingDiffOffer = fmt.Sprintf("%s is on %s as it is here, so the %s can go with the %s as a starting diff instead of on a new branch",
			g.Branch, nonEmpty(hp.Host, "the remote"), plural(len(sp.Tracked)+len(sp.Untracked), "changed file"), in.Cloud.SessionNoun())
	}
	switch {
	case opt.StartingDiff && !hasWay(in.Cloud.CodeUp, agent.ViaStartingDiff):
		check("err", in.Cloud.Title+" takes no starting diff; hand it off over a branch")
	case opt.StartingDiff && !hp.CanStartingDiff:
		why := "the changes are too large"
		switch {
		case !clean:
			why = "the session's branch is not on the remote as it is here"
		case !sp.Changes():
			why = "there are no changes to send"
		case hp.HistoryFile:
			why = "the conversation file goes on a branch"
		}
		check("err", "A starting diff needs a branch already on the remote and a few changed files; here "+why)
	case opt.StartingDiff:
		hp.Code, hp.Branch, hp.Unpushed = agent.ViaStartingDiff, g.Branch, 0
	}
	if hp.Code == agent.ViaStartingDiff {
		// The branch is the session's own; the changes go with the session.
	} else if g.Branch != "" && onRemote == sp.Head && !sp.Changes() && !hp.HistoryFile {
		hp.Reuse, hp.Branch, hp.Unpushed = true, g.Branch, 0
	} else {
		name := repos.HandoffBranch(in.Settings.BranchPrefix, time.Now().Format("20060102"), string(p.Key.Session))
		hp.Branch = repos.FreeRemoteBranch(ctx, in.Runner, top, hp.Remote, name)
	}
	if hp.Code != agent.ViaBundle && hp.Host == "github.com" {
		hp.BranchURL = "https://github.com/" + strings.TrimPrefix(hp.Repo, "github.com/") + "/tree/" + hp.Branch
	}
	if sp.Bytes > 50<<20 {
		check("warn", fmt.Sprintf("The snapshot carries %s of changes", Human(sp.Bytes)))
	}
}

// planHandoffEnv picks the cloud's environment, for a cloud that needs one: the one asked
// for, else the one configured for the repository. With neither, the plan waits for the
// user's pick among the environments the cloud's listing shows.
func planHandoffEnv(p *Plan, in HandoffInput, opt Options, check func(string, string)) {
	hp, cl := p.Handoff, in.Cloud
	if !needs(cl, agent.NeedEnvironment) {
		return
	}
	hp.EnvNeeded, hp.Envs = true, in.Envs
	env := strings.TrimSpace(nonEmpty(opt.Env, in.Env))
	hp.Env, hp.EnvName = env, env
	for _, e := range in.Envs {
		if env != "" && (e.Value == env || strings.EqualFold(e.Name, env)) {
			hp.Env, hp.EnvName = e.Value, e.Name
		}
	}
	hp.Remember = env != "" && in.Env == "" && hp.Repo != ""
	if env != "" {
		check("ok", "Environment "+hp.EnvName)
		return
	}
	hp.EnvNote = fmt.Sprintf("Pick a %s environment for %s.", cl.Title, nonEmpty(hp.Repo, "this repository"))
	if cl.EnvHint != "" {
		hp.EnvNote += " If you have none, " + cl.EnvHint + "."
	}
	check("err", hp.EnvNote)
}

func needs(cl agent.Cloud, n agent.Need) bool {
	for _, x := range cl.Needs {
		if x == n {
			return true
		}
	}
	return false
}

// planBrief renders the briefing (or takes the user's edit), and sums up what the
// conversation keeps and loses.
func planBrief(ctx context.Context, p *Plan, in HandoffInput, opt Options, check func(string, string)) {
	hp, src, s := p.Handoff, in.Source, in.Session
	spec, target := src.Module.Spec(), in.Module.Spec()
	if r, ok := src.Module.(agent.Reader); ok {
		if h, err := src.Machine.For(ctx, spec, src.Install, nil); err == nil {
			if seg, err := r.Read(ctx, h, src.Install, s, ir.Cursor{}); err == nil {
				hp.nodes, hp.head = seg.Nodes, seg.Cursor
			} else {
				check("warn", "hopsesh could not read the conversation ("+firstLine(err.Error())+"); the briefing says only where the code is")
			}
		}
	} else {
		check("warn", fmt.Sprintf("hopsesh can't read %s sessions, so the briefing says only where the code is", spec.Name))
	}
	hp.Tally = convert.Count(hp.nodes)
	bf := convert.Briefing{FromVersion: s.AgentVersion, SourceID: string(s.Key.Session), SourceLoc: src.Machine.Name, TargetLoc: in.Cloud.Name,
		When: time.Now(), Title: s.Title, Branch: hp.Branch, Head: short(hp.Base), Unpushed: hp.Unpushed,
		Dirty: len(hp.Tracked) + len(hp.Untracked), Note: opt.Note}
	for _, w := range hp.Withheld {
		bf.Withheld = append(bf.Withheld, w.Path)
	}
	if h, err := src.Machine.For(ctx, spec, src.Install, nil); err == nil {
		for _, gi := range spec.GlobalInstructions {
			path := agent.Expand(gi, h.Facts().Home, src.Install.Roots, h.Path())
			b, err := h.FS().ReadFile(path, 1<<20)
			text := strings.TrimSpace(string(b))
			if err != nil || text == "" {
				continue
			}
			if !opt.CarryRules {
				bf.NotCarried = append(bf.NotCarried, "the user's own "+h.Path().Base(path)+" for every project")
				p.Warnings = append(p.Warnings, fmt.Sprintf("your instructions for every %s project (%s) do not reach %s; --carry-rules adds them to the briefing", spec.Name, path, in.Cloud.Title))
				continue
			}
			if len(text) > maxRules {
				text = strings.ToValidUTF8(text[:maxRules], "") + "\n[shortened]"
			}
			bf.Rules = append(bf.Rules, convert.Rules{File: path, Text: text})
		}
	}
	if hp.HistoryFile {
		bf.HistoryFile = HistoryPath
	}
	if hp.Code == agent.ViaBundle {
		bf.Branch = "" // uploaded: there is no such branch on the remote
	}
	r := convert.Brief(hp.nodes, convert.BriefRequest{Profile: convert.BriefCloud, From: spec.Name, To: target.Name, Briefing: bf})
	hp.Brief, hp.Tokens, hp.Masked, hp.Shortened = r.Text, r.Tokens, r.Masked, r.Shortened
	// The cloud has its own checkout: paths in the checkout here become relative to it, and
	// the home folder is not named.
	relative := false
	if g := in.Git; g != nil && g.Toplevel != "" {
		for _, top := range []string{g.Toplevel, nonEmpty(g.MainWorktree, g.Toplevel)} {
			if strings.Contains(hp.Brief, top+"/") {
				hp.Brief, relative = strings.ReplaceAll(hp.Brief, top+"/", ""), true
			}
		}
	}
	if home := src.Machine.Facts.Home; home != "" && len(home) > 1 && strings.Contains(hp.Brief, home+"/") {
		hp.Brief, relative = strings.ReplaceAll(hp.Brief, home+"/", "~/"), true
	}
	hp.Tokens = len(hp.Brief) / 4
	if e := strings.TrimSpace(opt.Brief); e != "" {
		if !strings.HasPrefix(e, agent.NotePrefix) {
			e = agent.NotePrefix + e
		}
		masked, n := scan.Redact([]byte(e))
		hp.Brief, hp.Masked, hp.Tokens, hp.Edited, hp.Shortened = string(masked), n, len(masked)/4, true, false
	}
	hp.MaskedRules = maskedRules(hp.Brief)
	hp.HistoryWarning = fmt.Sprintf("hopsesh can't tell whether %s is public. Anyone who can see the branch could read this file.", nonEmpty(hp.Repo, "the repository"))
	t := hp.Tally
	hp.Carried = []string{"A briefing: the goal, what's done, what's next"}
	switch n := len(latestUser(hp.nodes)); {
	case n == 1:
		hp.Carried = append(hp.Carried, "Your last prompt, quoted")
	case n > 1:
		hp.Carried = append(hp.Carried, fmt.Sprintf("Your last %d prompts, quoted", n))
	}
	if hp.HistoryFile {
		hp.Carried = append(hp.Carried, "The conversation as "+HistoryPath+" on the branch")
	}
	if hp.Masked > 0 {
		hp.Changed = append(hp.Changed, plural(hp.Masked, "likely secret")+" masked")
	}
	if hp.Shortened {
		hp.Changed = append(hp.Changed, "Shortened to fit about 2,000 tokens")
	}
	if relative && !hp.Edited {
		hp.Changed = append(hp.Changed, "Paths made relative to the repository")
	}
	if hp.Edited {
		hp.Changed = append(hp.Changed, "Edited by you")
	}
	if len(hp.Changed) == 0 {
		hp.Changed = append(hp.Changed, "Nothing")
	}
	hp.Stays = append(hp.Stays, fmt.Sprintf("%s, %s", plural(t.Messages, "message"), plural(t.ToolCalls, "tool call")))
	if t.Reasoning > 0 {
		hp.Stays = append(hp.Stays, plural(t.Reasoning, "reasoning block"))
	}
}

// handoffLoss lists everything that stays here.
func handoffLoss(hp *HandoffPlan, spec agent.Spec) []string {
	var out []string
	t := hp.Tally
	out = append(out, fmt.Sprintf("%s, %s and %s stay in %s here; the cloud gets a briefing of about %s tokens",
		plural(t.Messages, "message"), plural(t.ToolCalls, "tool call"), plural(t.Reasoning, "reasoning block"), spec.Name, thousands(hp.Tokens)))
	out = append(out, "No tool output goes up: the briefing names the last command and its exit code only")
	for _, w := range hp.Withheld {
		why := map[string]string{repos.WithheldCredential: "its name looks like a credential's", repos.WithheldLFS: "Git LFS manages it",
			repos.WithheldSize: "it is larger than " + Human(repos.MaxSnapshotFile)}[w.Why]
		out = append(out, fmt.Sprintf("%s stays (%s)", w.Path, why))
	}
	for _, c := range hp.Offered {
		out = append(out, c.Path+" stays (untracked; tick it to carry it)")
	}
	switch hp.Code {
	case agent.ViaBundle:
		out = append(out, "An upload can't push results back to the remote; the cloud's work comes back with teleport")
	case agent.ViaStartingDiff:
		out = append(out, fmt.Sprintf("The changes go with the %s as a starting diff; nothing is pushed", hp.Noun))
	}
	return append(out, hp.Limits...)
}

var redactedRule = regexp.MustCompile(`\[REDACTED:([^\]]+)\]`)

// maskedRules are the kinds of secrets masked in a text, once each.
func maskedRules(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range redactedRule.FindAllStringSubmatch(text, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// latestUser are the user's own last messages (at most 3, as the briefing quotes).
func latestUser(nodes []ir.Node) []string {
	var out []string
	for i := len(nodes) - 1; i >= 0 && len(out) < 3; i-- {
		if n := nodes[i]; n.Kind == ir.KindMessage && n.Actor == ir.User && !n.Generated && agent.OwnText(n.Text) != "" {
			out = append(out, n.Text)
		}
	}
	return out
}

func hasWay(ws []agent.CodeWay, w agent.CodeWay) bool {
	for _, x := range ws {
		if x == w {
			return true
		}
	}
	return false
}

func plural(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %ss", n, one)
}

func thousands(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%d,%03d", n/1000, n%1000)
}

func shortStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// applyHandoff carries out a hand-off, step by step. A failure stops it and says which
// step failed and what had happened already (undo reverses that); the result is returned
// with the error.
func applyHandoff(ctx context.Context, p *Plan, env Env) (*Result, error) {
	hp, in := p.Handoff, p.handoffIn
	src, s, cl := in.Source, in.Session, in.Cloud
	target := in.Module.Spec()
	j, err := journal.New(env.StateDir, journal.KindHandoff, fmt.Sprintf("%s to %s", p.Title, cl.Title))
	if err != nil {
		return nil, err
	}
	j.AddKey(p.Key)
	hr := &HandoffResult{Cloud: cl.Name, CloudTitle: cl.Title, Agent: target.Name, Repo: hp.Repo, Branch: hp.Branch, BranchURL: hp.BranchURL,
		Code: string(hp.Code), Reuse: hp.Reuse, Tokens: hp.Tokens, Masked: hp.Masked, Snapshot: "",
		Manual: fmt.Sprintf("The %s stays in %s; archive it there if you want it gone.", hp.Noun, cloudPlace(cl)),
		Hint:   fmt.Sprintf("When it finishes: Clouds → %s → Bring here", cl.Title),
		Noun:   hp.Noun, Follow: hp.Follow, NoFollowUp: hp.NoFollowUp, Env: hp.Env, EnvName: hp.EnvName, Attempts: hp.Attempts}
	for _, st := range hp.Steps {
		hr.Steps = append(hr.Steps, HandoffStep{Name: st, Label: stepLabels[st], State: StepTodo})
	}
	for _, w := range hp.Withheld {
		hr.Stayed = append(hr.Stayed, w.Path)
	}
	for _, c := range hp.Offered {
		hr.Stayed = append(hr.Stayed, c.Path+" (untracked)")
	}
	hr.Stayed = append(hr.Stayed, "the "+plural(hp.Tally.Messages, "message"))
	res := &Result{Journal: j.ID, Mark: "off", Handoff: hr}
	machine := src.Machine.Name
	step := func(name, state, detail string) {
		for i := range hr.Steps {
			if hr.Steps[i].Name == name {
				hr.Steps[i].State, hr.Steps[i].Detail = state, detail
			}
		}
		if env.Progress != nil && state == StepTodo {
			env.Progress(stepLabels[name])
		}
	}
	fail := func(name, msg string, cause error) (*Result, error) {
		step(name, StepFailed, firstLine(cause.Error()))
		hr.Failed, hr.Message = name, msg
		_ = j.Seal(fsOf(ctx, in))
		return res, errors.New(msg)
	}
	fsys := fsOf(ctx, in)
	var top string
	if in.Git != nil {
		top = in.Git.Toplevel
	}
	lin := in.Lineage
	if lin == nil {
		lin = lineage.New(newID())
	}

	// 1. The snapshot.
	sha := hp.Base
	if !hp.Reuse {
		step(StepSnapshot, StepTodo, "")
		var extra []repos.ExtraFile
		if hp.HistoryFile {
			text, _ := convert.HistoryFile(hp.nodes, src.Module.Spec().Name, s.Title)
			extra = append(extra, repos.ExtraFile{Path: HistoryPath, Data: []byte(text)})
		}
		sha, err = repos.Snapshot(ctx, in.Runner, top, hp.snapshot, repos.SnapshotMessage(lin.Logical), extra...)
		if err != nil {
			return fail(StepSnapshot, "Nothing changed: hopsesh could not make the snapshot: "+firstLine(err.Error()), err)
		}
		hr.Snapshot = sha
		step(StepSnapshot, StepDone, describeSnapshot(hp, sha))
		env.Audit.Write(audit.Entry{Action: "git.snapshot", Host: machine, Session: p.Key.String(), Detail: map[string]any{"commit": sha, "files": len(hp.Tracked) + len(hp.Untracked), "withheld": len(hp.Withheld)}})
	}

	// 2. The branch.
	ref := "refs/heads/" + hp.Branch
	if hp.Code == agent.ViaBranch && !hp.Reuse {
		step(StepPush, StepTodo, "")
		if err := j.PushRef(machine, top, hp.Remote, ref, sha); err != nil {
			return fail(StepPush, "Nothing went to the remote: "+err.Error(), err)
		}
		if err := repos.PushRef(ctx, in.Runner, top, hp.Remote, sha, ref); err != nil {
			msg := fmt.Sprintf("Pushing %s failed: %s. Nothing went to the cloud.", hp.Branch, firstLine(err.Error()))
			switch {
			case errors.Is(err, repos.ErrPushRefused):
				msg = fmt.Sprintf("%s refused the handoff branch (branch protection or permissions). Nothing went to the cloud.", hostName(hp.Host))
				if hp.CanBundle && src.Machine.Local {
					hr.Retry = "bundle"
				}
			case errors.Is(err, repos.ErrRefExists):
				msg = fmt.Sprintf("The remote already has a branch named %s; hand off again for a new name. Nothing went to the cloud.", hp.Branch)
			}
			return fail(StepPush, msg, err)
		}
		hr.Pushed = true
		step(StepPush, StepDone, hp.Branch+" · on "+hp.Host+" now")
		env.Audit.Write(audit.Entry{Action: "git.handoff", Host: machine, Session: p.Key.String(), Detail: map[string]any{"branch": hp.Branch, "commit": sha}})
	}

	// 3. The cloud session, started from a worktree on the branch.
	step(StepStart, StepTodo, "")
	checkout := in.Checkout
	keep := false
	start := ""
	if hp.Code == agent.ViaBundle {
		checkout, start, keep = top, sha, !hp.Reuse
		if !hp.Reuse {
			if err := j.Ref(in.Here.Name, top, ref, sha, ""); err != nil {
				return fail(StepStart, "Nothing went to the cloud: "+err.Error(), err)
			}
		}
	}
	remoteURL := ""
	if in.Git != nil {
		remoteURL = in.Git.Remote
	}
	var diff []byte
	if hp.Code == agent.ViaStartingDiff {
		if diff, err = repos.DiffCommits(ctx, in.Runner, top, hp.Base, sha); err != nil {
			return fail(StepStart, startFailed(hr, target, cl, "hopsesh could not make the starting diff: "+firstLine(err.Error())), err)
		}
	}
	waiting := func() {
		if env.Progress != nil {
			env.Progress("Waiting for another hand-off of " + nonEmpty(hp.Repo, "this repository") + " to finish with its folder")
		}
	}
	dir, done, err := repos.DriverDir(ctx, repos.DriverOptions{Folder: in.Folder, Checkout: checkout, RemoteURL: remoteURL, Branch: hp.Branch,
		Start: start, Keep: keep, Waiting: waiting})
	if err != nil {
		return fail(StepStart, startFailed(hr, target, cl, "hopsesh could not prepare a worktree for it: "+firstLine(err.Error())), err)
	}
	sent, err := in.Module.(agent.CloudSender).SendCloud(ctx, in.Host, in.Install, agent.SendRequest{Cloud: cl.Name, Dir: dir, Repo: hp.Repo,
		Branch: hp.Branch, Base: sha, Brief: hp.Brief, Title: p.Title, Code: hp.Code, Diff: diff, Env: hp.Env, Attempts: hp.Attempts})
	cs := sent.Session
	if err == nil && sent.Run != nil {
		var sr StepResult
		sr, err = runStep(ctx, env, in, p, *sent.Run, dir)
		cs, hr.Pasted = sr.Session, sr.Pasted
	}
	done()
	if errors.Is(err, agent.ErrNoSession) {
		return fail(StepStart, noSessionStarted(hr, cl, err), err)
	}
	if err != nil {
		return fail(StepStart, startFailed(hr, target, cl, refusal(err, target, cl)), err)
	}
	if cs.Title == "" {
		cs.Title = p.Title
	}
	if cs.Repo == "" {
		cs.Repo, cs.Branch, cs.Base = hp.Repo, hp.Branch, sha
		if hp.Code == agent.ViaBundle {
			cs.Branch = "" // uploaded: the cloud has no branch of it on the remote
		}
	}
	cs.Cloud, cs.Key.Agent = cl.Name, target.ID
	if err := j.Cloud(in.Here.Name, cs); err != nil {
		res.Warnings = append(res.Warnings, "could not record the cloud session for undo: "+err.Error())
	}
	hr.Session, hr.URL, hr.State = string(cs.Key.Session), cs.URL, string(cs.State)
	step(StepStart, StepDone, string(cs.Key.Session))
	env.Audit.Write(audit.Entry{Action: "cloud.send", Session: p.Key.String(), Detail: map[string]any{"cloud": cl.Name, "session": string(cs.Key.Session),
		"branch": hp.Branch, "code": string(hp.Code), "tokens": hp.Tokens, "masked": hp.Masked}})

	// 4. Lineage: the session here and the cloud's copy are one logical session.
	step(StepLineage, StepTodo, "")
	now := time.Now().UTC()
	from := lin.Upsert(lineage.Replica{Key: s.Key, Location: machine, AgentVersion: s.AgentVersion, Head: hp.head.Head, Offset: hp.head.Offset, Time: now})
	cloudCopy := lineage.Replica{Key: cs.Key, Location: cl.Name, AgentVersion: in.Install.Version, Time: now, URL: cs.URL}
	if len(cl.CodeDown) > 0 && cl.CodeDown[0] == agent.ViaDiff {
		// The cloud works on this branch and brings back a diff of it: the branch is where the
		// diff applies when it comes back.
		cloudCopy.Branch = hp.Branch
	}
	to := lin.Upsert(cloudCopy)
	var withheld []string
	for _, w := range hp.Withheld {
		withheld = append(withheld, w.Path)
	}
	lin.Hops = append(lin.Hops, lineage.Hop{Time: now, From: from, To: to, Kind: lineage.HopHandoff, Fidelity: string(cl.Up),
		Code: &lineage.CodeHop{Way: hp.Code, Remote: hp.Repo, Branch: hp.Branch, Base: hp.Base, Snapshot: hr.Snapshot, Withheld: withheld, Redactions: hp.Masked}})
	if f, err := fsys(machine); err != nil {
		step(StepLineage, StepFailed, err.Error())
		res.Warnings = append(res.Warnings, "could not record the session's lineage: "+err.Error())
	} else if err := j.WriteFile(f, machine, lineage.PathFor(s.Path), lin.Encode(), 0o600); err != nil {
		step(StepLineage, StepFailed, err.Error())
		res.Warnings = append(res.Warnings, "could not record the session's lineage: "+err.Error())
	} else {
		step(StepLineage, StepDone, "")
	}

	// 5. The mark.
	mk := agent.Mark{Kind: agent.MarkContinued, AgentName: target.Name, Location: cl.Name}
	switch p.Mark {
	case MarkNow:
		step(StepMark, StepTodo, "")
		res.Mark = "done"
		marker := src.Module.(agent.Marker)
		h, err := src.Machine.For(ctx, src.Module.Spec(), src.Install, j)
		if err == nil {
			err = marker.Mark(ctx, h, src.Install, s, mk)
		}
		if err != nil {
			res.Mark, res.MarkError = "failed", err.Error()
			step(StepMark, StepFailed, err.Error())
		} else {
			step(StepMark, StepDone, hp.MarkTitle)
		}
	case MarkWhenStopped:
		res.Mark = "pending"
		owed := lineage.Pending{Time: now, Location: machine, Key: s.Key, Path: s.Path, Title: p.Title, Mark: mk, Head: string(hp.head.Head)}
		if err := lineage.AddPending(env.StateDir, owed); err != nil {
			res.Mark, res.MarkError = "failed", err.Error()
			step(StepMark, StepFailed, err.Error())
		} else {
			step(StepMark, StepDone, "once it ends")
		}
	}
	if res.Mark != "off" {
		hr.MarkText = hp.MarkTitle
		env.Audit.Write(audit.Entry{Action: "move.mark", Host: machine, Session: p.Key.String(), Detail: map[string]any{"mark": res.Mark, "error": res.MarkError}})
	}
	hr.Message = fmt.Sprintf("Handed off to %s", cl.Title)
	if err := SaveHandoff(env.StateDir, &Handoff{Journal: j.ID, Time: now, Machine: machine, Checkout: top, Remote: hp.Remote, Branch: hp.Branch,
		Snapshot: hr.Snapshot, Pushed: hr.Pushed, Cleanup: hp.Cleanup, Cloud: cl.Name, Session: cs.Key, URL: cs.URL, Title: p.Title,
		Brief: hp.Brief, Env: hp.Env, Repo: hp.Repo}); err != nil {
		res.Warnings = append(res.Warnings, "could not keep the hand-off's record: "+err.Error())
	}
	if err := j.Seal(fsys); err != nil {
		res.Warnings = append(res.Warnings, "could not record what this changed, for a safe undo: "+err.Error())
	}
	return res, nil
}

// startFailed says what happened when the cloud session could not start.
func startFailed(hr *HandoffResult, target agent.Spec, cl agent.Cloud, why string) string {
	why = strings.TrimSuffix(why, ".")
	if hr.Pushed {
		return fmt.Sprintf("The branch was pushed, but %s refused the %s: %s. Undo removes the branch.", target.Name, cl.SessionNoun(), why)
	}
	return fmt.Sprintf("%s refused the %s: %s. Nothing went to the cloud.", target.Name, cl.SessionNoun(), why)
}

// noSessionStarted says what happened when a terminal step ended without a session
// (agent.ErrNoSession).
func noSessionStarted(hr *HandoffResult, cl agent.Cloud, err error) string {
	why := strings.TrimSuffix(strings.TrimPrefix(err.Error(), agent.ErrNoSession.Error()+": "), ".")
	if hr.Pushed {
		return fmt.Sprintf("The branch was pushed, but no %s started: %s. Undo removes the branch.", cl.SessionNoun(), why)
	}
	return fmt.Sprintf("No %s started: %s. Nothing went to the cloud.", cl.SessionNoun(), why)
}

// runStep runs the driver's terminal step for the hand-off, where the user answers it.
func runStep(ctx context.Context, env Env, in *HandoffInput, p *Plan, run agent.Command, dir string) (StepResult, error) {
	cl, target := in.Cloud, in.Module.Spec()
	if env.Step == nil {
		return StepResult{}, fmt.Errorf("%s starts the %s only in a terminal you answer, and hopsesh has none here", target.Name, cl.SessionNoun())
	}
	if run.Dir == "" {
		run.Dir = dir
	}
	if len(run.Argv) > 0 && run.Argv[0] == cl.Driver && in.Install.Binary != "" {
		run.Argv = append([]string{in.Install.Binary}, run.Argv[1:]...)
	}
	run.Unset = append(append([]string(nil), run.Unset...), cl.Unset...)
	if env.Progress != nil {
		env.Progress(fmt.Sprintf("Starting the %s in your terminal (%s)", cl.SessionNoun(), cl.Driver))
	}
	return env.Step(ctx, TermStep{Agent: target.ID, Cloud: cl.Name, CloudTitle: cl.Title, Title: p.Title, Run: run, Folder: dir})
}

func describeSnapshot(hp *HandoffPlan, sha string) string {
	d := short(hp.Base)
	if n := len(hp.Tracked) + len(hp.Untracked); n > 0 {
		d += " + " + plural(n, "changed file")
	}
	return d + " → " + short(sha)
}

func hostName(h string) string {
	if h == "github.com" {
		return "GitHub"
	}
	return "The remote (" + h + ")"
}

// cloudPlace is where the user manages a cloud's sessions.
func cloudPlace(cl agent.Cloud) string {
	switch cl.Name {
	case "claude-cloud":
		return "Claude Code on the web"
	case "codex-cloud":
		return "your Codex cloud list"
	}
	return cl.Title
}

// fsOf reaches the machines of a hand-off by name, for the journal.
func fsOf(ctx context.Context, in *HandoffInput) func(string) (host.FS, error) {
	return func(name string) (host.FS, error) {
		switch name {
		case in.Source.Machine.Name:
			return in.Source.Machine.FS(ctx)
		case in.Here.Name:
			return in.Here.FS(ctx)
		}
		return nil, fmt.Errorf("%s is not part of this hand-off", name)
	}
}

// Handoff is the record of a hand-off, kept beside its journal: where the branch is and
// what the user chose for it, for the done screen and for cleaning the branch up later.
type Handoff struct {
	Journal  string           `json:"journal"`
	Time     time.Time        `json:"time"`
	Machine  string           `json:"machine"`
	Checkout string           `json:"checkout"`
	Remote   string           `json:"remote"`
	Branch   string           `json:"branch"`
	Snapshot string           `json:"snapshot,omitempty"`
	Pushed   bool             `json:"pushed,omitempty"`
	Cleanup  string           `json:"cleanup"`
	Cloud    string           `json:"cloud"`
	Session  agent.SessionKey `json:"session"`
	URL      string           `json:"url,omitempty"`
	Title    string           `json:"title"`
	// Brief is the first prompt the cloud got (kept here only, never pushed), so a cloud that
	// brings back no prompt of its own can show it; Env is the environment it ran in; Repo,
	// the repository's identity.
	Brief string `json:"brief,omitempty"`
	Env   string `json:"env,omitempty"`
	Repo  string `json:"repo,omitempty"`
}

// FindHandoff is the hand-off that started a cloud session, when it was made here (nil
// otherwise).
func FindHandoff(stateDir, cloud string, id agent.SessionID) *Handoff {
	files, _ := filepath.Glob(filepath.Join(stateDir, "handoffs", "*.json"))
	var found *Handoff
	for _, f := range files {
		var h Handoff
		if loadJSON(f, &h) == nil && h.Cloud == cloud && h.Session.Session == id && (found == nil || h.Time.After(found.Time)) {
			found = &h
		}
	}
	return found
}

func handoffFile(stateDir, journal string) string {
	return filepath.Join(stateDir, "handoffs", filepath.Base(journal)+".json")
}

// SaveHandoff keeps a hand-off's record.
func SaveHandoff(stateDir string, h *Handoff) error {
	return saveJSON(handoffFile(stateDir, h.Journal), h)
}

// LoadHandoff reads a hand-off's record.
func LoadHandoff(stateDir, journal string) (*Handoff, error) {
	var h Handoff
	if err := loadJSON(handoffFile(stateDir, journal), &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// saveJSON writes v to p atomically.
func saveJSON(p string, v any) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(p+".tmp", b, 0o600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

func loadJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
