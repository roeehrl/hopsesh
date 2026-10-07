// Package move moves a session between locations with the same agent: plan (pure
// decisions, nothing written) and apply (stage, rewrite, verify, install with a journal,
// record lineage, mark the copy left behind). Every agent-specific step is the module's.
package move

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Side is one end of a move: a machine, the agent module and its install there.
type Side struct {
	Machine *host.Machine
	Module  agent.Module
	Install agent.Install
	Account *agent.Account // nil when unknown
}

// Copy is a copy of the session already at the target.
type Copy struct {
	Summary agent.Summary
	Live    agent.LiveInfo
	Lineage *lineage.Manifest
}

// Input is what planning needs.
type Input struct {
	// SourceReceipt is a transport-owned local ledger for a read-only source.
	// It is supplied only by native code, never by a peer payload or UI path.
	SourceReceipt *ReceiptOwner
	// CheckSource rechecks current authorization before any destination write.
	CheckSource func(context.Context) error
	// AcknowledgeSource is a transport-owned commit of writes made to a source
	// snapshot. The app invokes it only after the native destination is durable.
	AcknowledgeSource func(context.Context, *Plan, *Result) error
	Source            Side
	Session           agent.Summary
	Live              agent.LiveInfo
	Git               *repos.GitState // the session's checkout on the source (nil when unknown)
	GitErr            string          // why the checkout could not be read ("" when it was)
	Lineage           *lineage.Manifest
	Target            Side
	// Copies are the target agent's sessions with the same key at the target.
	Copies []Copy
	// Worktrees are agent-managed worktree folders (from every module).
	Worktrees []string
	// Push pushes the session's branch on the source machine; GitFetch reaches the
	// source's repository over SSH. Both are nil when the source is this machine.
	Push     func(ctx context.Context, dir string) (string, error)
	GitFetch func(dir string) *repos.FetchSource
	// Native is the source's own agent on the target, for a continuation on another
	// machine: the session is also kept there byte for byte (nil: it is not installed).
	Native *NativeSide
}

type ReceiptOwner struct {
	FS         host.FS
	Machine    string
	NativePath string
}

// NativeSide is the source agent on the target and its copies of the session there.
type NativeSide struct {
	Target Side
	Copies []Copy
}

// NativeCopy is the source agent's own copy kept next to a continuation, so a later return
// to that agent there adds only the new work to the original turns.
type NativeCopy struct {
	Agent    string           `json:"agent"` // its name
	Key      agent.SessionKey `json:"key"`
	Replaces bool             `json:"replaces,omitempty"` // an older copy there makes way
}

// WorktreeMode chooses between the main checkout and a worktree.
type WorktreeMode string

const (
	WorktreeAuto   WorktreeMode = "auto"   // recreate the worktree if the session used one
	WorktreeCreate WorktreeMode = "create" // always work in a worktree on the session's branch
	WorktreeMain   WorktreeMode = "main"   // use the main checkout as it is
)

// Options are the user's choices.
type Options struct {
	Bounded       bool   `json:"bounded,omitempty"`    // new bounded replica on the same logical branch
	NewReplica    bool   `json:"newReplica,omitempty"` // explicitly create a new native session; never auto-select an existing one
	TargetProfile string `json:"targetProfile,omitempty"`
	OperationID   string `json:"operationId,omitempty"`
	TargetSession string `json:"targetSession,omitempty"`
	TargetDir     string // resume here (skips repository matching)
	Clone         bool   // clone the repository when it is not here
	ReposDir      string
	ExtraRoots    []string // more folders to look for checkouts in
	GHQLayout     bool
	Worktree      WorktreeMode
	Fork          bool // keep the source session running (both continue)
	RemoteControl bool
	Notify        bool // tell the old session it moved on
	Redact        bool
	App           bool // open in the agent's desktop app
	Mark          bool // mark the copy left behind
	SyncCode      bool // bring the checkout to the session's commit
	Push          bool // push unpushed commits on the source first
	StopLocal     bool // quit a copy of this session that is open here
	Conflict      string
	OtherAccount  bool // the target is signed in to another account (set by the planner)
	// Continuing in another agent.
	Fidelity convert.Fidelity // history (default) or note
	Native   bool             // render exact tool calls as the target's own (when it can)
	Note     string           // a handoff note the source agent wrote
	// CarryRules adds the user's global instructions for the source agent to the briefing.
	CarryRules bool
	RuleFiles  []string // explicit subset of discovered source instruction paths
	// Via is how another agent gets the session: "" (hopsesh converts it) or ViaImport (the
	// target agent's own importer converts it; hopsesh adds its briefing).
	Via string
	Go  bool // start the continued session with "Continue."
	// Bringing a session from a cloud.
	CodeOnly       bool // the cloud's branch only, without the conversation
	RenameVendor   bool // the cloud's own branch (claude/…) comes home as hopsesh/from/<cloud>/…
	AppendOriginal bool // add the cloud's work to the session it was handed off from
	// Handing a session off to a cloud.
	Bundle      bool     // the cloud's driver uploads the repository; nothing is pushed
	HistoryFile bool     // also commit the conversation as .hopsesh/handoff.md
	Untracked   []string // untracked files (globs) to carry
	Brief       string   // the briefing as the user edited it ("": hopsesh's)
	Cleanup     string   // when the handoff branch is deleted (Cleanup*)
	// Env is the cloud's environment (an id or a name), for a cloud that needs one ("": the
	// one configured for the repository).
	Env string
	// Attempts asks the cloud for that many attempts at once (0: its default).
	Attempts int
	// StartingDiff sends the changes with the cloud session as a starting diff, on a branch
	// already on the remote, instead of pushing a handoff branch.
	StartingDiff bool
}

// Via choices: the target agent's own importer, or hopsesh's conversion even when the
// configuration prefers the importer.
const (
	ViaImport  = "import"
	ViaHopsesh = "hopsesh"
)

// Conflict choices when the copy here changed too.
const (
	ConflictReplace  = "replace"
	ConflictKeepBoth = "keep-both"
)

// Mark states.
const (
	MarkNow         = "now"          // right after the move
	MarkWhenStopped = "when-stopped" // the copy left behind is still open: on a later scan
	MarkOff         = "off"
)

// Kinds of plans.
const (
	KindMove     = "move"     // the same agent, another place
	KindContinue = "continue" // another agent
	// KindFetch (fetch.go) brings a session from a cloud; KindHandoff (handoff.go) hands
	// one off to a cloud.
)

// Plan is a move, worked out without changing anything.
type Plan struct {
	NoWork       bool             `json:"noWork,omitempty"`
	SyncTo       *agent.Summary   `json:"syncTo,omitempty"`
	OperationID  string           `json:"operationId"`
	Destinations []agent.Summary  `json:"destinations,omitempty"`
	Kind         string           `json:"kind"`
	Key          agent.SessionKey `json:"key"`
	Title        string           `json:"title"`
	Agent        string           `json:"agent"` // display name
	Source       Endpoint         `json:"source"`
	Target       Endpoint         `json:"target"`
	Live         bool             `json:"live"`
	Repo         RepoPlan         `json:"repo"`
	Placement    agent.Placement  `json:"placement"`
	Files        agent.MovePlan   `json:"files"`
	Bytes        int64            `json:"bytes"`
	// SetAside are copies at the target that make way (kept by the journal for undo).
	SetAside []agent.Summary `json:"setAside,omitempty"`
	Conflict string          `json:"conflict,omitempty"`
	Warnings []string        `json:"warnings,omitempty"`
	Blockers []string        `json:"blockers,omitempty"`
	Mark     string          `json:"mark"`
	StopHere bool            `json:"stopHere,omitempty"` // quit the copy open here first
	Push     bool            `json:"push,omitempty"`
	Sync     string          `json:"sync,omitempty"`
	// SyncFromSource: unpushed commits are fetched straight from the source machine.
	SyncFromSource bool          `json:"syncFromSource,omitempty"`
	NewName        string        `json:"newName"`
	Resume         agent.Command `json:"resume"`
	StartPrompt    string        `json:"-"`
	Options        Options       `json:"options"`
	OldName        string        `json:"oldName,omitempty"`
	Continue       *ContinuePlan `json:"continue,omitempty"`
	NativeCopy     *NativeCopy   `json:"nativeCopy,omitempty"`
	Fetch          *FetchPlan    `json:"fetch,omitempty"`
	Handoff        *HandoffPlan  `json:"handoff,omitempty"`
	Hop            *HopPlan      `json:"hop,omitempty"`

	ExpectedDestination    map[string]ir.Cursor `json:"expectedDestination,omitempty"`
	ExpectedAbsent         []string             `json:"expectedAbsent,omitempty"`
	manifest               *lineage.Manifest
	sourceLine, targetLine string
	sourceReplica          lineage.ReplicaID
	sourceState            lineage.State
	bundle                 agent.Bundle
	native                 *Plan // the move that keeps NativeCopy
	nativeIn               Input
	resumeOpts             agent.ResumeOptions
	fetchIn                *FetchInput
	handoffIn              *HandoffInput
}

// Endpoint describes one end for people and JSON.
type Endpoint struct {
	Profile     string `json:"profile,omitempty"`
	Binding     string `json:"binding,omitempty"`
	ProfileName string `json:"profileName,omitempty"`
	ID          string `json:"id"`
	Location    string `json:"location"`
	OS          string `json:"os"`
	CWD         string `json:"cwd"`
	Path        string `json:"path,omitempty"`
	Version     string `json:"agentVersion,omitempty"`
}

// Build works out a move. It reads (the bundle's file list, the target's copies) but
// writes nothing.
func Build(ctx context.Context, in Input, opt Options) (*Plan, error) {
	if opt.NewReplica && opt.TargetSession != "" {
		return nil, fmt.Errorf("a new session cannot also select an existing destination session")
	}
	in.Copies = activeCopies(ctx, in, opt)
	src, tgt := in.Source, in.Target
	copies := currentBranchCopies(in)
	if opt.TargetSession != "" {
		var selected []Copy
		for _, c := range copies {
			if c.Summary.Key.String() == opt.TargetSession || string(c.Summary.Key.Session) == opt.TargetSession {
				selected = append(selected, c)
			}
		}
		copies = selected
	}
	if opt.NewReplica || opt.Bounded || profileBoundary(in) || src.Module.Spec().ID != tgt.Module.Spec().ID || len(copies) == 1 && copies[0].Summary.Key != in.Session.Key {
		return buildContinue(ctx, in, opt)
	}
	if opt.Worktree == "" {
		opt.Worktree = WorktreeAuto
	}
	s := in.Session
	spec := tgt.Module.Spec()
	p := &Plan{
		Kind: KindMove, Key: s.Key, Title: s.Title, Agent: spec.Name,
		Source:  Endpoint{Location: src.Machine.Name, OS: src.Machine.Facts.OS, CWD: s.CWD, Path: s.Path, Version: s.AgentVersion},
		Target:  Endpoint{Location: tgt.Machine.Name, OS: tgt.Machine.Facts.OS, Version: tgt.Install.Version},
		Live:    in.Live.State == agent.Live,
		Options: opt,
		OldName: s.Title,
	}
	if opt.RemoteControl && !agent.Has(tgt.Module, agent.CapRemoteControl) {
		p.Options.RemoteControl = false
		p.Warnings = append(p.Warnings, spec.Name+" has no remote control hopsesh can turn on")
	} else if opt.RemoteControl && tgt.Account != nil && !tgt.Account.RemoteControl {
		p.Options.RemoteControl = false
		p.Warnings = append(p.Warnings, "remote control stays off: "+tgt.Account.Why)
	}
	if src.Account != nil && tgt.Account != nil && src.Account.Key != "" && tgt.Account.Key != "" && src.Account.Key != tgt.Account.Key {
		if _, ok := tgt.Module.(agent.Sanitizer); ok {
			p.Options.OtherAccount = true
			p.Warnings = append(p.Warnings, fmt.Sprintf("this machine is signed in to another %s account than %s; content bound to that account (such as signed reasoning) is left out of the copy", spec.Name, src.Machine.Name))
		} else {
			p.Blockers = append(p.Blockers, fmt.Sprintf("this machine is signed in to another %s account than %s, and %s sessions cannot move between accounts", spec.Name, src.Machine.Name, spec.Name))
		}
	}

	cwd, err := planRepo(ctx, p, in, opt)
	if err != nil {
		return nil, err
	}
	if tgt.Machine.Local {
		cwd = realIntended(cwd)
	}
	p.Target.CWD = cwd
	destinationKey := s.Key
	destinationKey.Profile = tgt.Install.ProfileID()
	p.Placement = agent.Placement{Key: destinationKey, SourceID: s.Key.Session, CWD: cwd, Location: tgt.Machine.Name, Mappings: withShortNames(ctx, src, mappings(p, src, tgt)), OtherAccount: p.Options.OtherAccount}
	if s.TitleSource == "custom" {
		p.Placement.Name = s.Title
	}

	srcHost, err := src.Machine.For(ctx, src.Module.Spec(), src.Install, nil)
	if err != nil {
		return nil, err
	}
	if p.bundle, err = src.Module.Bundle(ctx, srcHost, src.Install, s); err != nil {
		return nil, err
	}
	for _, f := range p.bundle.Files {
		p.Bytes += f.Size
	}

	if src.Machine.Name == tgt.Machine.Name && (s.CWD == cwd || tgt.Machine.Local && realIntended(s.CWD) == cwd) {
		p.Blockers = append(p.Blockers, "this session is already here, in this folder; to move it to another folder on this machine use --to <dir>")
	}
	if r, ok := src.Module.(agent.Reader); ok {
		seg, e := r.Read(ctx, srcHost, src.Install, s, ir.Cursor{})
		if e != nil {
			return nil, e
		}
		if e = prepareLineage(ctx, p, in, &seg); e != nil {
			p.Blockers = append(p.Blockers, "cannot verify session lineage: "+e.Error())
		}
	}
	relate(ctx, p, in, opt)
	if p.Files, err = tgt.Module.PlanMove(src.Install, tgt.Install, s, p.bundle, p.Placement); err != nil {
		return nil, err
	}
	if !p.NoWork {
		fsys, e := tgt.Machine.FS(ctx)
		if e != nil {
			return nil, e
		}
		for _, file := range p.Files.Files {
			if file.From.Role != agent.RoleMain {
				continue
			}
			path := tgt.Machine.Path().Join(tgt.Install.Root(file.ToRoot), fromSlash(tgt.Machine.Path(), file.ToRel))
			if _, e = fsys.Stat(path); errors.Is(e, fs.ErrNotExist) {
				p.ExpectedAbsent = append(p.ExpectedAbsent, path)
			} else if e != nil {
				return nil, e
			} else {
				selected := false
				for _, c := range p.SetAside {
					if c.Path == path {
						selected = true
					}
				}
				if !selected {
					p.Blockers = append(p.Blockers, "destination native session already exists outside the selected branch; create a separate fork")
				}
			}
		}
	}
	planWarnings(p, in, opt)
	planRoundTrip(p, in, opt)

	p.NewName = launch.SessionName(p.Title, tgt.Machine.Name)
	p.StartPrompt = launch.StartPrompt(launch.Context{
		AgentName: spec.Name, SourceLocation: src.Machine.Name, SourceOS: launch.OSName(src.Machine.Facts.OS), SourceVersion: s.AgentVersion,
		SourceCWD: s.CWD, TargetLocation: tgt.Machine.Name, TargetOS: launch.OSName(tgt.Machine.Facts.OS), TargetCWD: cwd,
		Branch: p.Repo.SourceBranch, WorktreeNote: worktreeNote(p), Unpushed: p.Repo.Unpushed, Dirty: p.Repo.Dirty,
		Redacted: opt.Redact, OtherAccount: p.Options.OtherAccount, Live: p.Live, Fork: opt.Fork && p.Live,
	})
	p.resumeOpts = agent.ResumeOptions{RemoteControl: p.Options.RemoteControl, App: opt.App && agent.Has(tgt.Module, agent.CapApp), Name: p.NewName, Prompt: p.StartPrompt}
	if p.resumeOpts.App {
		if tgt.Install.Profile != nil && !tgt.Install.Profile.Default {
			p.Blockers = append(p.Blockers, "desktop opening cannot select an account profile; use a terminal")
		}
		if checker, ok := tgt.Module.(agent.AppChecker); ok {
			// Desktop links open an existing thread; they cannot submit the transfer prompt.
			p.resumeOpts.Prompt, p.StartPrompt = "", ""
			if err := checker.CheckApp(tgt.Install, p.Placement.Key, p.resumeOpts); err != nil {
				p.Blockers = append(p.Blockers, err.Error())
			}
		}
	}
	p.Resume = tgt.Module.Resume(tgt.Install, p.Placement.Key, p.Placement, p.resumeOpts)
	return p, nil
}

// relate compares the incoming session with copies already here.
func relate(ctx context.Context, p *Plan, in Input, opt Options) {
	conflict := ""
	copies := currentBranchCopies(in)
	if !p.Options.Fork {
		if opt.TargetSession != "" {
			var selected []Copy
			for _, c := range copies {
				if c.Summary.Key.String() == opt.TargetSession || string(c.Summary.Key.Session) == opt.TargetSession {
					selected = append(selected, c)
				}
			}
			copies = selected
			if len(copies) == 0 {
				p.Blockers = append(p.Blockers, "selected destination does not belong to this branch")
				return
			}
		}
		if len(copies) > 1 {
			for _, c := range copies {
				p.Destinations = append(p.Destinations, c.Summary)
			}
			p.Blockers = append(p.Blockers, "multiple destination replicas match this branch; select a destination session explicitly")
			return
		}
	}
	for _, c := range copies {
		if p.Options.Fork {
			continue
		}
		if c.Live.State == agent.Live {
			if opt.StopLocal && agent.Has(in.Target.Module, agent.CapStop) && in.Source.Machine.Name != in.Target.Machine.Name {
				p.StopHere = true
			} else {
				p.Blockers = append(p.Blockers, fmt.Sprintf("this session is open on this machine (%s); quit it first, or let hopsesh quit it (--stop-local)", nonEmpty(c.Live.Status, "live")))
			}
		}
		if why := changedSinceItLeft(ctx, p, in, c); why != "" {
			conflict = why
		}
		p.SetAside = append(p.SetAside, c.Summary)
	}
	if p.NoWork {
		p.SetAside = nil
		p.StopHere = false
	}
	if conflict == "" {
		if p.Options.Fork {
			keepBoth(p)
		}
		return
	}
	p.Conflict = conflict
	switch opt.Conflict {
	case ConflictReplace:
		p.Warnings = append(p.Warnings, conflict+"; it will be replaced (hopsesh undo brings it back)")
	case ConflictKeepBoth:
		if p.manifest != nil {
			forkLine(p)
		}
		keepBoth(p)
	default:
		p.Blockers = append(p.Blockers, conflict+"; choose --replace (the copy here is kept for undo) or --keep-both (bring this one in as a separate session)")
	}
}

// changedSinceItLeft says why the copy here cannot simply be replaced: it changed after
// the incoming copy was taken from it. Lineage tells exactly (the head it had when it
// left); without lineage, a copy newer than the incoming one is treated as changed.
func changedSinceItLeft(ctx context.Context, p *Plan, in Input, c Copy) string {
	if p.manifest != nil {
		st, _, err := targetState(ctx, p, in, c)
		if err != nil {
			return "cannot verify the copy here: " + err.Error()
		}
		source, target := p.manifest.Covered(p.sourceState.Heads), p.manifest.Covered(st.Heads)
		if lineage.Subset(source, target) && lineage.Subset(target, source) {
			if c.Summary.CWD != "" && realIntended(c.Summary.CWD) != realIntended(p.Target.CWD) {
				p.Blockers = append(p.Blockers, "destination is synchronized in "+c.Summary.CWD+"; choose that folder or create a separate fork for a different folder")
				return ""
			}
			p.NoWork = true
			p.SyncTo = &c.Summary
		}
		if !lineage.Subset(target, source) {
			return "the copy here contains work that the incoming session does not have"
		}
		return ""
	}
	return "the copy here has no verified causal history"
}

// keepBoth brings the incoming copy in under a new id, so both stay.
func keepBoth(p *Plan) {
	p.Placement.Key.Session = agent.SessionID(newID())
	p.SetAside = nil
	p.StopHere = false
	kept := p.Blockers[:0]
	for _, b := range p.Blockers {
		if !strings.Contains(b, "open on this machine") {
			kept = append(kept, b)
		}
	}
	p.Blockers = kept
	p.Title = p.Title + " (from " + p.Source.Location + ")"
	p.Placement.Title = p.Title
	p.Warnings = append(p.Warnings, "both copies stay: this one comes in as a separate session, \""+p.Title+"\"")
}

func planWarnings(p *Plan, in Input, opt Options) {
	spec := in.Target.Module.Spec()
	if p.Live {
		if opt.Fork {
			p.Warnings = append(p.Warnings, fmt.Sprintf("the session is still open on %s; you chose fork, so both copies continue independently", p.Source.Location))
		} else {
			p.Warnings = append(p.Warnings, fmt.Sprintf("the session is still open on %s (%s); hopsesh copies it as it is now and asks the old session to stop (handoff)", p.Source.Location, nonEmpty(in.Live.Status, "live")))
		}
	}
	if p.Repo.Unpushed > 0 || p.Repo.Dirty > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("on %s the repository has %d unpushed commit(s) and %d uncommitted file(s) that a checkout here will not contain", p.Source.Location, p.Repo.Unpushed, p.Repo.Dirty))
	}
	if p.Repo.InWorktree {
		if p.Repo.Worktree != "" {
			p.Warnings = append(p.Warnings, fmt.Sprintf("the session ran in a worktree on branch %s; a matching worktree will be created at %s", p.Repo.SourceBranch, p.Repo.Worktree))
		} else {
			p.Warnings = append(p.Warnings, fmt.Sprintf("the session ran in a worktree on branch %s; it will resume in the main checkout instead", p.Repo.SourceBranch))
		}
	}
	switch v := in.Target.Install.Version; {
	case in.Target.Install.Binary == "":
		p.Warnings = append(p.Warnings, spec.Name+" was not found on this machine; install it before resuming")
	case p.Source.Version != "" && versionLess(v, p.Source.Version):
		p.Warnings = append(p.Warnings, fmt.Sprintf("%s here (%s) is older than the version that wrote the session (%s); update it before resuming", spec.Name, v, p.Source.Version))
	case v != "" && !spec.TestedWith(v):
		p.Warnings = append(p.Warnings, fmt.Sprintf("%s %s has not been tested with hopsesh", spec.Name, v))
	}
	for _, c := range p.SetAside {
		p.Warnings = append(p.Warnings, "the copy of this session already here is set aside (hopsesh undo brings it back): "+c.Path)
	}
	if (p.Source.OS == "windows") != (p.Target.OS == "windows") {
		p.Warnings = append(p.Warnings, "moving between Windows and macOS/Linux: mapped paths get the new separators; paths outside the mapped folders are left as they were")
	}
	if p.Bytes > 200<<20 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("this session is large (%d MB)", p.Bytes>>20))
	}
}

// planRoundTrip decides marking, pushing and code sync.
func planRoundTrip(p *Plan, in Input, opt Options) {
	switch {
	case p.NoWork:
		p.Mark = MarkOff
	case p.Kind == KindMove && p.Placement.Key != p.Key:
		p.Mark = MarkOff // keep-both: both copies stay
	case !opt.Mark || (p.Kind == KindMove && p.Source.Location == p.Target.Location):
		p.Mark = MarkOff
	case p.Options.Fork:
		p.Mark = MarkOff // both continue on purpose
	case p.Live:
		p.Mark = MarkWhenStopped
	default:
		p.Mark = MarkNow
	}
	if m := in.Session.Mark; m != nil {
		p.Warnings = append(p.Warnings, fmt.Sprintf("this copy on %s was already moved on (%s); the newest copy is probably elsewhere", p.Source.Location, agent.MarkTitle(*m, "")))
	}
	r := p.Repo
	remote := p.Source.Location != p.Target.Location
	if opt.Push && r.Unpushed > 0 && in.Push != nil && remote {
		if r.HasUpstream {
			p.Push = true
		} else {
			p.Warnings = append(p.Warnings, fmt.Sprintf("branch %s on %s has no upstream, so hopsesh cannot push it; push it yourself", r.SourceBranch, p.Source.Location))
		}
	}
	if opt.SyncCode && r.SourceHead != "" && (r.Action == RepoUse || r.Action == RepoClone || r.Action == RepoDir) {
		p.Sync = fmt.Sprintf("bring the checkout here to %s (fetch if needed; fast-forward only if it is clean and on %s)", short(r.SourceHead), nonEmpty(r.SourceBranch, "the same branch"))
		if r.Unpushed > 0 && in.GitFetch != nil && remote && !p.Push {
			p.SyncFromSource = true
			p.Sync += fmt.Sprintf("; the %d unpushed commit(s) are fetched straight from %s", r.Unpushed, p.Source.Location)
		}
		if p.SyncFromSource || p.Push {
			kept := p.Warnings[:0]
			for _, w := range p.Warnings {
				if !strings.Contains(w, "unpushed commit(s) and") {
					kept = append(kept, w)
				}
			}
			p.Warnings = kept
			if r.Dirty > 0 {
				p.Warnings = append(p.Warnings, fmt.Sprintf("on %s %d uncommitted file(s) stay behind; commit them there to bring them", p.Source.Location, r.Dirty))
			}
		}
	}
}

func worktreeNote(p *Plan) string {
	r := p.Repo
	switch {
	case r.Worktree != "" && r.InWorktree:
		return "It ran in a worktree; a matching worktree was created here at " + r.Worktree + "."
	case r.Worktree != "":
		return "A worktree for this branch was created here at " + r.Worktree + "."
	case r.InWorktree:
		return "It ran in a worktree on the old machine; here it runs in the main checkout, which may be on a different branch."
	}
	return ""
}

// mappings are the path prefixes to rewrite: the repository, the agent's data folders,
// the home folder.
func mappings(p *Plan, src, tgt Side) []agent.Mapping {
	var ms []agent.Mapping
	add := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		for _, m := range ms {
			if m.From == from {
				return
			}
		}
		ms = append(ms, agent.Mapping{From: from, To: to})
	}
	r := p.Repo
	top := r.LocalPath
	if r.Worktree != "" {
		add(r.SourceTop, r.Worktree)
	}
	if r.Action == RepoDir {
		if r.SourceTop != "" {
			add(r.SourceTop, top)
		} else {
			add(p.Source.CWD, top)
		}
	}
	if r.SourceMain != "" {
		add(r.SourceMain, top)
	}
	if r.SourceTop != "" && r.Worktree == "" {
		add(r.SourceTop, top)
	}
	if r.Action == RepoNone {
		add(p.Source.CWD, p.Target.CWD)
	}
	for name, dir := range src.Install.Roots {
		add(dir, tgt.Install.Roots[name])
	}
	add(src.Machine.Facts.Home, tgt.Machine.Facts.Home)
	if (src.Machine.Facts.OS == "windows") != (tgt.Machine.Facts.OS == "windows") {
		sep := "/"
		if tgt.Machine.Facts.OS == "windows" {
			sep = `\`
		}
		for i := range ms {
			ms[i].ToSep = sep
		}
	}
	return ms
}

// withShortNames adds, for a Windows source, each mapped folder's 8.3 short form
// ("C:\Users\LIHUA~1") mapped to the same place: a session may name a folder either
// way. Sessions are always placed under long names.
func withShortNames(ctx context.Context, src Side, ms []agent.Mapping) []agent.Mapping {
	froms := make([]string, len(ms))
	for i, m := range ms {
		froms[i] = m.From
	}
	return addShortNames(ms, src.Machine.ShortNames(ctx, froms))
}

// addShortNames maps each folder's short form (shorts, by long form) where the long form
// goes.
func addShortNames(ms []agent.Mapping, shorts map[string]string) []agent.Mapping {
	n := len(ms)
	for i := 0; i < n; i++ {
		if s, ok := shorts[ms[i].From]; ok {
			ms = append(ms, agent.Mapping{From: s, To: ms[i].To, ToSep: ms[i].ToSep})
		}
	}
	return ms
}

func short(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// versionLess compares dotted versions numerically.
func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, y := num(pa[i]), num(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

func num(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
