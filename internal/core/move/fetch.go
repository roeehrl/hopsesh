package move

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Bringing a session home from a vendor's cloud (a fetch). The core makes a worktree of
// the session's repository, so the user's own checkout stays as it is; the module says how
// its driver brings the conversation (Claude Code's teleport, in the user's terminal) and
// where the copy will appear. Once it is there the core adopts it: it checks the message
// count, renames the cloud's own branch, and records lineage and undo. Bringing it on into
// another agent is then an ordinary continuation.

// KindFetch is a session brought from a cloud.
const KindFetch = "fetch"

// Outcomes of a fetch.
const (
	FetchWaiting  = "waiting"  // the driver has not written its copy yet
	FetchComplete = "complete" // every message the cloud sent is here
	FetchPartial  = "partial"  // fewer messages than the cloud said it sent
	FetchEmpty    = "empty"    // a copy with no messages
	FetchCode     = "code"     // the code only, as asked
	// FetchUnchecked: a copy with messages that nothing can be checked against (the vendor
	// states no count, and hopsesh did not start the cloud session, so no briefing either).
	FetchUnchecked = "unchecked"
)

// States of a fetch's cloud branch.
const (
	BranchPushed  = "pushed"
	BranchMissing = "missing" // the session never pushed its work
	BranchUnknown = "unknown" // the driver finds it itself
)

// Check is one thing a plan checked, for people: ok, warn or err (a blocker).
type Check struct {
	State string `json:"state"`
	Text  string `json:"text"`
}

// FetchInput is what planning a fetch needs.
type FetchInput struct {
	Machine *host.Machine // this machine
	Module  agent.Module
	Install agent.Install
	// Host is the module's Host here for its cloud capabilities (it runs the driver
	// without the cloud's Unset variables).
	Host    agent.Host
	Cloud   agent.Cloud
	Session agent.CloudSession // Key.Session "" leaves the choice to the vendor's own picker
	// Checkout is the repository's checkout here ("" when unknown).
	Checkout string
	Account  *agent.Account // the login here, when known
	Lineage  *lineage.Manifest
	// Original is the local session the cloud session was handed off from, when it is
	// here.
	Original *Copy
	// Continue is the agent to continue in once it is here (nil: its own).
	Continue  agent.Module
	Worktrees []string
	// MirrorOf is the session a Remote Control mirror mirrors ("machine:agent/id"), when
	// the cloud session is one.
	MirrorOf string
	// ContinueInstall is Continue's install here.
	ContinueInstall agent.Install
	// Prompt is the first prompt the cloud session got, when hopsesh started it (a cloud
	// that brings back no prompt of its own shows its title in its place).
	Prompt string
}

// FetchPlan is the cloud part of a fetch plan.
type FetchPlan struct {
	Cloud      string          `json:"cloud"`
	CloudTitle string          `json:"cloudTitle"`
	Session    agent.SessionID `json:"session,omitempty"` // "" : the vendor's own picker chooses
	URL        string          `json:"url,omitempty"`
	Fidelity   agent.Fidelity  `json:"fidelity"`
	// Conversation says what comes of the conversation, for people.
	Conversation string `json:"conversation"`
	Repo         string `json:"repo,omitempty"`
	Checkout     string `json:"checkout,omitempty"`
	CloudBranch  string `json:"cloudBranch,omitempty"`
	BranchState  string `json:"branchState"`
	Ref          string `json:"ref,omitempty"`  // refs/hopsesh/<cloud>/<branch>, when the branch is known
	Base         string `json:"base,omitempty"` // the commit the worktree starts at
	BaseNote     string `json:"baseNote,omitempty"`
	Worktree     string `json:"worktree"`
	// LocalBranch is the branch the code is on here: the cloud's branch renamed under
	// hopsesh/from/<cloud>/ (Rename), or kept.
	LocalBranch string `json:"localBranch,omitempty"`
	// FastForward: LocalBranch is here already, from an earlier fetch of the code: it moves
	// forward to the cloud's work (fast-forward only; a new branch when it cannot).
	FastForward bool `json:"fastForward,omitempty"`
	// Diff: the code comes as the cloud's patch (CodeDown ViaDiff), committed on LocalBranch
	// in the new worktree, on top of the branch the session started from (or of the
	// checkout's HEAD when hopsesh does not know it).
	Diff bool `json:"diff,omitempty"`
	// Write: the driver needs no terminal and brings the conversation as text (or, for a
	// code-only cloud, the task's title and summary); hopsesh writes it here as a new
	// session of the agent it goes to (Writer), with Messages messages.
	Write    bool   `json:"write,omitempty"`
	Writer   string `json:"writer,omitempty"` // the agent that gets it ("Codex")
	Messages int    `json:"messages,omitempty"`
	// Changes sums up the patch ("+12 −3 · 2 files").
	Changes string `json:"changes,omitempty"`
	Noun    string `json:"noun"` // what the cloud calls its sessions ("task")
	// Terminal: the driver needs the user's terminal (Claude Code's teleport); otherwise
	// hopsesh brings it all by itself.
	Terminal bool          `json:"terminal,omitempty"`
	Rename   bool          `json:"rename"`
	CodeOnly bool          `json:"codeOnly,omitempty"`
	Run      agent.Command `json:"run"`
	Command  string        `json:"command,omitempty"` // Run for the user's shell
	Adopt    *agent.Adopt  `json:"adopt,omitempty"`
	// Note is what the user must do in the driver's terminal for the copy to be saved.
	Note   string   `json:"note,omitempty"`
	Loss   []string `json:"loss,omitempty"`
	Checks []Check  `json:"checks"`
	// ContinueIn is the agent it continues in once it is here ("" : its own).
	ContinueIn   agent.ID `json:"continueIn,omitempty"`
	ContinueName string   `json:"continueName,omitempty"`
	// Original is the local session it was handed off from, here; CanAppend: it is as it
	// was left, so the cloud's work can be added to it instead (Append).
	Original  *agent.Summary `json:"original,omitempty"`
	CanAppend bool           `json:"canAppend,omitempty"`
	Append    bool           `json:"append,omitempty"`
	Relation  string         `json:"relation"`

	originalHead ir.Cursor
	fetched      *agent.Fetched // what the driver brought while planning (Write)
}

// BuildFetch works out a fetch. It asks git and the driver read-only questions; it writes
// nothing.
func BuildFetch(ctx context.Context, in FetchInput, opt Options) (*Plan, error) {
	spec, cl, s := in.Module.Spec(), in.Cloud, in.Session
	title := s.Title
	if title == "" {
		title = "a session from " + cl.Title
		if s.Key.Session != "" {
			title = "Session " + string(s.Key.Session) // as its row names it
		}
	}
	fp := &FetchPlan{Cloud: cl.Name, CloudTitle: cl.Title, Session: s.Key.Session, URL: s.URL, Fidelity: cl.Down, Repo: s.Repo,
		CloudBranch: s.Branch, BranchState: BranchUnknown, Rename: opt.RenameVendor, CodeOnly: opt.CodeOnly, Relation: RelationNew,
		Noun: cl.SessionNoun(), Changes: s.Changes, Terminal: needs(cl, agent.NeedTerminal)}
	if _, canFetch := in.Module.(agent.CloudFetcher); canFetch && s.Key.Session != "" && diffDown(cl, s.Branch) {
		fp.Diff = true // the code comes as the cloud's patch, whichever checkout it lands in
	}
	p := &Plan{Kind: KindFetch, Key: s.Key, Title: title, Agent: spec.Name, Source: Endpoint{Location: cl.Name},
		Target: Endpoint{Location: in.Machine.Name, OS: in.Machine.Facts.OS, Version: in.Install.Version}, Options: opt, Mark: MarkOff,
		Fetch: fp, fetchIn: &in}
	if err := assignCloudOperation(p, opt); err != nil {
		return nil, err
	}
	check := func(state, text string) {
		fp.Checks = append(fp.Checks, Check{State: state, Text: text})
		switch state {
		case "err":
			p.Blockers = append(p.Blockers, text)
		case "warn":
			p.Warnings = append(p.Warnings, text)
		}
	}
	if in.Continue != nil && in.Continue.Spec().ID != spec.ID {
		fp.ContinueIn, fp.ContinueName = in.Continue.Spec().ID, in.Continue.Spec().Name
		p.Agent = fp.ContinueName
	}
	fp.Conversation = conversationWords(spec, cl, fp)

	if in.Install.Binary == "" {
		check("err", fmt.Sprintf("%s is reached through the `%s` command, which isn't installed here", cl.Title, cl.Driver))
	} else if v := in.Install.Version; v != "" && !cl.TestedWith(v) {
		check("warn", fmt.Sprintf("%s %s hasn't been tested with %s; hopsesh tested %s. You can still go ahead", spec.Name, v, cl.Title, strings.Join(cl.Tested, ", ")))
	}

	// The repository: the worktree comes from the checkout here.
	top := planFetchRepo(ctx, in, fp, check)
	if top != "" {
		planFetchBranch(ctx, in, fp, opt, top, check)
		named := s
		if fp.Diff {
			named.Branch = "" // the branch it started from, not its own: name the worktree after the task
		}
		fp.Worktree = freePath(filepath.Join(filepath.Dir(top), filepath.Base(top)+"-"+worktreeName(named, cl)))
		if in.Machine.Local {
			fp.Worktree = realIntended(fp.Worktree)
		}
		p.Target.CWD = fp.Worktree
		if !fp.CodeOnly {
			check("ok", "Clean worktree (hopsesh uses a new one)")
		}
	}

	// The driver: the command the user runs, and the login it needs.
	if !fp.CodeOnly && top != "" && len(p.Blockers) == 0 {
		fetcher, ok := in.Module.(agent.CloudFetcher)
		if !ok {
			return nil, fmt.Errorf("%w: hopsesh cannot bring %s sessions to %s yet", agent.ErrUnsupported, cl.Title, spec.Name)
		}
		code := agent.ViaBranch
		if fp.Diff {
			code = agent.ViaDiff
		}
		f, err := fetcher.FetchCloud(ctx, in.Host, in.Install, s.Key.Session, agent.FetchTarget{Dir: fp.Worktree, Code: code})
		switch {
		case err != nil:
			check("err", refusal(err, spec, cl))
		case f.Run == nil && f.Segment != nil:
			if fp.CloudBranch == "" && !fp.Diff && f.Code.Branch != "" && (f.Code.Way == agent.ViaPR || f.Code.Way == agent.ViaBranch) {
				// The listing did not know the branch the cloud pushed (its pull request's head);
				// the driver did.
				fp.CloudBranch, fp.Base, fp.BaseNote, fp.LocalBranch = f.Code.Branch, "", "", ""
				planFetchBranch(ctx, in, fp, opt, top, check)
			}
			planFetchWrite(in, p, &f, opt, check)
		case f.Run == nil:
			// A driver that needs no terminal and brings no conversation brings the code only.
			check("err", fmt.Sprintf("hopsesh brings only the code of %s sessions; the conversation stays in the cloud. Choose the code only", cl.Title))
		default:
			fp.Run = *f.Run
			fp.Run.Unset = union(fp.Run.Unset, cl.Unset)
			fp.Command = launch.Shell(fp.Run, "", launch.DefaultShell())
			fp.Adopt, fp.Loss, fp.Note = f.Adopt, f.Loss, f.Note
			acct := "Signed in to the account " + cl.Title + " needs"
			if in.Account != nil && in.Account.Label != "" {
				acct += " (" + in.Account.Label + ")"
			}
			switch {
			case s.Account != "" && in.Account != nil && in.Account.Key != "" && s.Account != in.Account.Key:
				check("err", fmt.Sprintf("%s needs the account that owns the session; %s here is signed in to another one", cl.Title, spec.Name))
			case s.Account != "" && in.Account != nil && s.Account == in.Account.Key:
				check("ok", "Same account as the one that owns the session")
			default:
				check("ok", acct)
			}
		}
	}
	if s.State == agent.CloudRunning && !fp.Write {
		check("warn", fmt.Sprintf("The %s is still running in the cloud. You get the conversation as it is now", fp.Noun))
	}
	relateFetch(ctx, in, p, opt, check)
	if fp.Write {
		switch {
		case fp.Append:
			check("ok", fmt.Sprintf("Added to “%s”, the %s session it was handed off from (%s)", fp.Original.Title, fp.Writer, plural(fp.Messages, "message")))
		case opt.AppendOriginal && fp.Relation == RelationNew:
			check("err", fmt.Sprintf("The %s's work can be added only to the %s session it was handed off from, here and as it was left; this comes as a new session", fp.Noun, fp.Writer))
		case !opt.AppendOriginal || fp.Relation != RelationDiverged:
			check("ok", fmt.Sprintf("Written here as a new %s session (%s)", fp.Writer, plural(fp.Messages, "message")))
		}
	}
	return p, nil
}

// planFetchWrite plans writing what a driver brought without a terminal as a new session
// of the agent it goes to: the conversation as text, or (a code-only cloud) the task's
// title and summary, beside its code. Any module that writes sessions can take it.
func planFetchWrite(in FetchInput, p *Plan, f *agent.Fetched, opt Options, check func(string, string)) {
	fp, cl := p.Fetch, in.Cloud
	tm := in.Module
	if in.Continue != nil {
		tm = in.Continue
	}
	if _, own := in.Module.(agent.Writer); !own && in.Continue == nil {
		// A cloud-only module (Copilot's log, Amp's thread) has no sessions of its own here: its
		// text goes into the local agent the user picks.
		check("err", fmt.Sprintf("Choose the agent here that gets the %s's messages (Claude Code or Codex), or bring the code only", fp.Noun))
		return
	}
	if _, ok := tm.(agent.Writer); !ok {
		check("err", fmt.Sprintf("hopsesh can't write %s sessions yet; choose the code only", tm.Spec().Name))
		return
	}
	fp.Write, fp.Writer, fp.fetched, fp.Loss = true, tm.Spec().Name, f, f.Loss
	p.Agent = fp.Writer
	for _, n := range f.Segment.Nodes {
		if n.Kind == ir.KindMessage {
			fp.Messages++
		}
	}
	if t := f.Segment.Header.Title; t != "" && in.Session.Title == "" {
		p.Title = t
	}
	if fp.Diff {
		switch {
		case len(f.Code.Diff) == 0 && !f.Code.Applied && in.Session.State == agent.CloudRunning:
			check("err", fmt.Sprintf("The %s is still running in %s, so there is no code to bring yet. Try again when it is done", fp.Noun, cl.Title))
		case len(f.Code.Diff) == 0 && !f.Code.Applied:
			check("err", fmt.Sprintf("%s has no diff for this %s, so there is no code to bring", cl.Title, fp.Noun))
		default:
			fp.Changes = nonEmpty(fp.Changes, diffWords(f.Code.Diff))
			check("ok", fmt.Sprintf("%s's changes (%s) are committed on %s in the new worktree", cl.Title, fp.Changes, fp.LocalBranch))
		}
	}
	if !fp.Diff && in.Session.State == agent.CloudRunning {
		check("warn", fmt.Sprintf("The %s is still running in %s. You get its messages as they are now", fp.Noun, cl.Title))
	}
}

// diffWords sums up a unified diff for people: "+12 −3 · 2 files".
func diffWords(diff []byte) string {
	files, added, removed := 0, 0, 0
	for _, l := range strings.Split(string(diff), "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			files++
		case strings.HasPrefix(l, "+++ "), strings.HasPrefix(l, "--- "):
		case strings.HasPrefix(l, "+"):
			added++
		case strings.HasPrefix(l, "-"):
			removed++
		}
	}
	unit := "files"
	if files == 1 {
		unit = "file"
	}
	return fmt.Sprintf("+%d −%d · %d %s", added, removed, files, unit)
}

// diffDown reports whether a cloud's code comes down as a patch for this session: the
// cloud prefers it, or (a cloud that can bring either) the session has no branch.
func diffDown(cl agent.Cloud, branch string) bool {
	if len(cl.CodeDown) == 0 {
		return false
	}
	return cl.CodeDown[0] == agent.ViaDiff || branch == "" && hasWay(cl.CodeDown, agent.ViaDiff)
}

// planFetchRepo checks the checkout the worktree comes from and returns its main folder
// ("" when there is none to use).
func planFetchRepo(ctx context.Context, in FetchInput, fp *FetchPlan, check func(string, string)) string {
	if in.Checkout == "" {
		check("err", fmt.Sprintf("hopsesh doesn't know which repository this cloud %s works on; choose its checkout here", fp.Noun))
		return ""
	}
	states, err := repos.ProbeLocal(ctx, []string{in.Checkout}, in.Worktrees)
	if err == nil && len(states) == 1 && states[0].Error != "" {
		check("err", fmt.Sprintf("hopsesh could not read %s (%s); try again", in.Checkout, states[0].Error))
		return ""
	}
	if err != nil || len(states) == 0 || !states[0].IsRepo {
		check("err", in.Checkout+" is not a git checkout")
		return ""
	}
	g := states[0]
	switch {
	case g.Identity == "":
		check("err", "This session isn't in a git repository with a remote, so a cloud can't get its code")
		return ""
	case fp.Repo != "" && g.Identity != fp.Repo:
		check("err", fmt.Sprintf("This checkout is %s, but the cloud session works on %s; it needs a checkout of the same repository (not a fork)", g.Identity, fp.Repo))
		return ""
	}
	fp.Repo = g.Identity
	if h, _, _ := strings.Cut(g.Identity, "/"); !contains(in.Cloud.Hosts, h) {
		check("warn", fmt.Sprintf("This repository's remote is %s; %s pushes only to %s, so its code may not come back", h, in.Cloud.Title, strings.Join(in.Cloud.Hosts, ", ")))
	}
	top := nonEmpty(g.MainWorktree, g.Toplevel)
	fp.Checkout = top
	return top
}

// planFetchBranch checks the cloud's branch on origin and names the commit the worktree
// starts at and the branch the code ends up on.
func planFetchBranch(ctx context.Context, in FetchInput, fp *FetchPlan, opt Options, top string, check func(string, string)) {
	noCode := "The cloud session never pushed its work, so there is no code to bring"
	if b := fp.CloudBranch; b != "" {
		sha, err := repos.RemoteBranch(ctx, top, b)
		switch {
		case err != nil:
			check("warn", "hopsesh could not ask origin about "+b+": "+firstLine(err.Error()))
		case sha == "" && fp.Diff:
			check("warn", fmt.Sprintf("%s, the branch the %s started from, is no longer on origin; its changes go on the checkout's HEAD here", b, fp.Noun))
		case sha == "":
			fp.BranchState = BranchMissing
			if fp.CodeOnly {
				check("err", noCode)
			} else {
				check("warn", noCode+"; only the conversation comes back")
			}
		case fp.Diff:
			fp.BranchState, fp.Base, fp.Ref = BranchPushed, sha, repos.CloudRef(in.Cloud.Name, b)
			check("ok", fmt.Sprintf("The %s started from %s, which is on origin", fp.Noun, b))
		default:
			fp.BranchState, fp.Base, fp.Ref = BranchPushed, sha, repos.CloudRef(in.Cloud.Name, b)
			check("ok", "Branch "+b+" is pushed")
		}
	} else if fp.CodeOnly && !fp.Diff {
		check("err", "hopsesh doesn't know this session's branch; bring it with its conversation and "+in.Module.Spec().Name+" fetches the branch itself")
	}
	if fp.Diff && fp.CodeOnly {
		check("ok", in.Cloud.Title+"'s changes come as a patch, committed on a new branch here")
	}
	if fp.Base == "" {
		head, err := repos.Head(ctx, top)
		if err != nil {
			check("err", top+" has no commit to start a worktree from")
			return
		}
		fp.Base = head
		fp.BaseNote = fmt.Sprintf("%s here is at %s", nonEmpty(repos.CurrentBranch(ctx, top), "the checkout"), short(head))
	}
	if fp.Diff {
		fp.LocalBranch = repos.FreeBranchName(ctx, top, repos.FromBranch(in.Cloud.Name, string(fp.Session), ""))
	}
	if b := fp.CloudBranch; b != "" && !fp.Diff {
		name := b
		if opt.RenameVendor && in.Cloud.VendorPrefix != "" && strings.HasPrefix(b, in.Cloud.VendorPrefix) {
			name = repos.FromBranch(in.Cloud.Name, b, in.Cloud.VendorPrefix)
		}
		fp.LocalBranch = repos.FreeBranchName(ctx, top, name)
		if fp.CodeOnly && repos.BranchExists(ctx, top, name) && repos.CheckedOutAt(ctx, top, name) == "" {
			fp.LocalBranch, fp.FastForward = name, true
		}
	}
}

// relateFetch finds the local session the cloud session was handed off from: as it was
// left, the cloud's work can be added to it instead of resumed apart (Options.Append);
// used since, both stay, and only keep-both is offered. A copy hopsesh writes (Write) can be
// added to the original when it goes into the original's own agent.
func relateFetch(ctx context.Context, in FetchInput, p *Plan, opt Options, check func(string, string)) {
	fp, o := p.Fetch, in.Original
	if o == nil || in.Lineage == nil {
		return
	}
	holder, hin := in.Module, in.Install
	if fp.Write && in.Continue != nil {
		holder, hin = in.Continue, in.ContinueInstall
	}
	if o.Summary.Key.Agent != holder.Spec().ID {
		return // the original is another agent's session: the work comes as a new one
	}
	var from *lineage.Replica
	for _, h := range in.Lineage.Hops {
		if h.Kind != lineage.HopHandoff || !in.Lineage.HasReplica(h.To) || !in.Lineage.HasReplica(h.From) {
			continue
		}
		if to := in.Lineage.Replica(h.To); to.Key.Session == fp.Session && to.Location == fp.Cloud {
			r := in.Lineage.Replica(h.From)
			from = &r
		}
	}
	if from == nil || from.Key != o.Summary.Key {
		return
	}
	reader, okR := holder.(agent.Reader)
	_, okW := holder.(agent.Writer)
	if !okR || !okW {
		return
	}
	h, err := in.Machine.For(ctx, holder.Spec(), hin, nil)
	if err != nil {
		return
	}
	cur, err := reader.Read(ctx, h, hin, o.Summary, ir.Cursor{})
	if err != nil {
		return
	}
	s := o.Summary
	fp.Original = &s
	if cur.Cursor.Head != from.Head {
		fp.Relation = RelationDiverged
		if fp.Write && !opt.AppendOriginal {
			return // a written copy is a new session anyway
		}
		p.Conflict = "you continued the local session after handing it off"
		if opt.Conflict == ConflictKeepBoth {
			check("warn", "You continued the local session after handing it off; the cloud work comes in as a separate session")
		} else {
			check("err", "You continued the local session after handing it off. Bring the cloud work as a separate session?")
		}
		return
	}
	fp.CanAppend, fp.originalHead = true, cur.Cursor
	if !opt.AppendOriginal {
		return
	}
	switch {
	case fp.ContinueIn != "" && !fp.Write:
		check("err", "Adding the cloud's work to the original keeps it in "+in.Module.Spec().Name+"; continue in "+fp.ContinueName+" separately")
	case o.Live.State == agent.Live:
		check("err", "The original session is open here; quit it first")
	default:
		fp.Append, fp.Relation = true, RelationAppend
	}
}

func conversationWords(spec agent.Spec, cl agent.Cloud, fp *FetchPlan) string {
	switch {
	case fp.CodeOnly:
		return "Only the code comes back; the conversation stays in the cloud."
	case cl.Down == agent.FidNative && fp.ContinueIn != "":
		return "Copied by " + spec.Name + ", then converted (tool calls become text)."
	case cl.Down == agent.FidNative:
		return spec.Name + " copies the whole conversation, once you send a message in it; hopsesh checks the copy."
	case cl.Down == agent.FidCode:
		return "Only the task title, summary and code come back. The steps stay in the cloud."
	case cl.Down == agent.FidText:
		return "The messages come back as text; tool calls stay in the cloud."
	}
	return "The conversation stays in the cloud."
}

// refusal is a driver's refusal in words.
func refusal(err error, spec agent.Spec, cl agent.Cloud) string {
	msg := err.Error()
	for _, e := range []error{agent.ErrSignedOut, agent.ErrNotEligible, agent.ErrRepoUnsupported, agent.ErrNoEnvironment, agent.ErrNotInstalled} {
		if errors.Is(err, e) {
			msg = strings.TrimPrefix(msg, e.Error()+": ")
		}
	}
	switch {
	case errors.Is(err, agent.ErrNotInstalled):
		return fmt.Sprintf("%s is reached through the `%s` command, which isn't installed here", cl.Title, cl.Driver)
	case errors.Is(err, agent.ErrSignedOut), errors.Is(err, agent.ErrNotEligible), errors.Is(err, agent.ErrRepoUnsupported), errors.Is(err, agent.ErrNoEnvironment):
		return msg
	}
	return fmt.Sprintf("%s could not be asked about the session: %s", spec.Name, firstLine(msg))
}

// worktreeName names a fetch's worktree: the cloud branch without the vendor's prefix,
// else the title, else the session.
func worktreeName(s agent.CloudSession, cl agent.Cloud) string {
	n := strings.TrimPrefix(s.Branch, cl.VendorPrefix)
	if n == "" {
		n = strings.SplitN(launch.SessionName(s.Title, "x"), "@", 2)[0]
		if s.Title == "" || n == "session" {
			n = "cloud"
			if id := string(s.Key.Session); id != "" {
				n += "-" + strings.ToLower(id[max(0, len(id)-6):])
			}
		}
	}
	n = unsafeName.ReplaceAllString(n, "-")
	if len(n) > 40 {
		n = n[:40]
	}
	return strings.Trim(n, "-.")
}

// freePath is p, or p-2, p-3, … when something is there.
func freePath(p string) string {
	n := p
	for i := 2; i < 100; i++ {
		if _, err := os.Lstat(n); errors.Is(err, os.ErrNotExist) {
			return n
		}
		n = fmt.Sprintf("%s-%d", p, i)
	}
	return n
}

// FetchResult is what applying a fetch did.
type FetchResult struct {
	Outcome  string `json:"outcome"` // waiting, or code (the code only)
	Worktree string `json:"worktree"`
	Branch   string `json:"branch,omitempty"` // the branch the code is on (code only)
	// FastForwarded: that branch was here already and moved forward to the cloud's work.
	FastForwarded bool   `json:"fastForwarded,omitempty"`
	Ref           string `json:"ref,omitempty"`
	Base          string `json:"base"`
	// Key is the session hopsesh wrote here (agent/session), when it wrote one.
	Key string `json:"key,omitempty"`
}

// applyFetch fetches the cloud's branch (when known) into refs/hopsesh/<cloud>/…, makes
// the worktree and records the fetch as waiting for the user's terminal.
func applyFetch(ctx context.Context, p *Plan, env Env) (*Result, error) {
	fp, in := p.Fetch, p.fetchIn
	step := func(s string) {
		if env.Progress != nil {
			env.Progress(s)
		}
	}
	j, err := journal.New(env.StateDir, journal.KindFetch, fmt.Sprintf("%s from %s", p.Title, fp.CloudTitle))
	if err != nil {
		return nil, err
	}
	if err := operationJournal(env, p, j); err != nil {
		return nil, err
	}
	if fp.Session != "" {
		j.AddKey(p.Key)
	}
	machine, top := in.Machine.Name, fp.Checkout
	res := &Result{Journal: j.ID, Mark: MarkOff, Fetch: &FetchResult{Worktree: fp.Worktree, Base: fp.Base}}
	base := fp.Base
	if fp.BranchState == BranchPushed {
		step("fetching " + fp.CloudBranch + " into " + fp.Ref)
		prev, _ := repos.LocalGit{}.Ref(ctx, top, fp.Ref)
		ref, sha, err := repos.FetchCloudBranch(ctx, top, fp.Cloud, fp.CloudBranch)
		if err != nil {
			return res, fmt.Errorf("fetching %s: %w", fp.CloudBranch, err)
		}
		// Recorded once fetched (its commit is known only then): a ref under refs/hopsesh/
		// that a crash leaves behind holds nothing anyone else uses.
		if err := j.Ref(machine, top, ref, sha, prev); err != nil {
			return res, err
		}
		base, res.Fetch.Ref = sha, ref
		res.Fetch.Base = sha
	}
	branch, existing := "", false
	if (fp.CodeOnly || fp.Write) && !fp.Diff && (fp.CodeOnly || fp.LocalBranch != "") {
		branch = fp.LocalBranch
		if branch == "" {
			return res, errors.New("no branch to bring the code on")
		}
		ref := "refs/heads/" + branch
		if fp.FastForward {
			// The branch an earlier fetch made moves forward, when the cloud only added to it.
			prev, _ := repos.LocalGit{}.Ref(ctx, top, ref)
			if prev != "" && repos.IsAncestor(ctx, top, prev, base) && repos.CheckedOutAt(ctx, top, branch) == "" {
				step("fast-forwarding " + branch)
				if err := j.Ref(machine, top, ref, base, prev); err != nil {
					return res, err
				}
				if err := (repos.LocalGit{}).SetRef(ctx, top, ref, base, prev); err != nil {
					return res, err
				}
				existing, res.Fetch.FastForwarded = true, true
			} else {
				branch = repos.FreeBranchName(ctx, top, branch)
				ref = "refs/heads/" + branch
				res.Warnings = append(res.Warnings, fp.LocalBranch+" could not move forward to the cloud's work, so the code is on "+branch)
			}
		}
		if !existing && !fp.Diff { // a patch's branch is made once the patch is committed
			if err := j.Ref(machine, top, ref, base, ""); err != nil {
				return res, err
			}
		}
		res.Fetch.Branch = branch
	}
	step("creating the worktree " + fp.Worktree)
	if err := j.Worktree(machine, top, fp.Worktree); err != nil {
		return res, err
	}
	add := func() error { return repos.AddWorktreeAt(ctx, top, fp.Worktree, branch, base) }
	switch {
	case existing:
		add = func() error { return repos.AddWorktreeOn(ctx, top, fp.Worktree, branch) }
	case fp.Diff:
		add = func() error { return repos.AddWorktreeAt(ctx, top, fp.Worktree, "", base) }
	}
	if err := add(); err != nil {
		return res, fmt.Errorf("worktree: %w", err)
	}
	if fp.Diff {
		branch = fp.LocalBranch
		step("committing " + fp.CloudTitle + "'s patch on " + branch)
		if err := commitPatch(ctx, p, j, branch); err != nil {
			_ = j.Seal(func(string) (host.FS, error) { return host.LocalFS(), nil })
			return res, err
		}
		if fp.CodeOnly {
			res.Fetch.Branch = branch
		}
	}
	res.Worktree = fp.Worktree
	env.Audit.Write(audit.Entry{Action: "git.worktree", Session: p.Key.String(), Detail: map[string]any{"path": fp.Worktree, "commit": base, "branch": branch}})
	switch {
	case fp.CodeOnly:
		res.Fetch.Outcome = FetchCode
		env.Audit.Write(audit.Entry{Action: "cloud.fetch", Session: p.Key.String(), Detail: map[string]any{"cloud": fp.Cloud, "code": true, "journal": j.ID}})
	case fp.Write:
		if err := writeFetched(ctx, p, env, j, base, branch, res); err != nil {
			_ = j.Seal(func(string) (host.FS, error) { return host.LocalFS(), nil })
			return res, err
		}
	default:
		pf := &Fetch{Profile: in.Install.Profile, Journal: j.ID, Time: time.Now().UTC(), Machine: machine, Agent: in.Module.Spec().ID, Cloud: fp.Cloud, CloudTitle: fp.CloudTitle,
			Session: fp.Session, URL: fp.URL, Title: p.Title, Untitled: in.Session.Title == "", Repo: fp.Repo, Checkout: top, Worktree: fp.Worktree, Base: base,
			CloudBranch: fp.CloudBranch, VendorPrefix: in.Cloud.VendorPrefix, Rename: fp.Rename, Lineage: in.Lineage,
			Continue: fp.ContinueIn, ContinueName: fp.ContinueName, Command: fp.Command, Run: fp.Run, Problems: in.Cloud.Problems,
			Mirror: in.Session.Mirror, MirrorOf: in.MirrorOf, Note: fp.Note, Brief: in.Prompt}
		if fp.Adopt != nil {
			pf.Adopt = *fp.Adopt
		}
		pf.Adopt.Since = pf.Time
		if fp.Append && fp.Original != nil {
			o := *fp.Original
			pf.Original, pf.OriginalHead = &o, fp.originalHead
		}
		if pf.VendorPrefix != "" {
			pf.Before, _ = repos.Branches(ctx, top, pf.VendorPrefix)
		}
		if err := SaveFetch(env.StateDir, pf); err != nil {
			return res, err
		}
		res.Fetch.Outcome = FetchWaiting
		res.Command, res.Run = fp.Command, fp.Run
		env.Audit.Write(audit.Entry{Action: "cloud.fetch", Session: p.Key.String(), Detail: map[string]any{"cloud": fp.Cloud, "worktree": fp.Worktree, "journal": j.ID}})
	}
	if err := j.Seal(func(string) (host.FS, error) { return host.LocalFS(), nil }); err != nil {
		res.Warnings = append(res.Warnings, "could not record what this changed, for a safe undo: "+err.Error())
	}
	step("done")
	return res, nil
}

func union(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, x := range b {
		if !contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func firstLine(s string) string { return strings.SplitN(strings.TrimSpace(s), "\n", 2)[0] }

var issueNumber = regexp.MustCompile(`/issues/(\d+)$`)

// IssueRef is a known problem's link as "#94836".
func IssueRef(url string) string {
	if m := issueNumber.FindStringSubmatch(url); m != nil {
		return "#" + m[1]
	}
	return url
}
