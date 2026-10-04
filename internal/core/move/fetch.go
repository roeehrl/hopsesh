package move

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	// Diff: the code comes as the cloud's patch (CodeDown ViaDiff; a session with no
	// branch), committed on LocalBranch in the new worktree.
	Diff     bool          `json:"diff,omitempty"`
	Rename   bool          `json:"rename"`
	CodeOnly bool          `json:"codeOnly,omitempty"`
	Run      agent.Command `json:"run"`
	Command  string        `json:"command,omitempty"` // Run for the user's shell
	Adopt    *agent.Adopt  `json:"adopt,omitempty"`
	Loss     []string      `json:"loss,omitempty"`
	Checks   []Check       `json:"checks"`
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
		CloudBranch: s.Branch, BranchState: BranchUnknown, Rename: opt.RenameVendor, CodeOnly: opt.CodeOnly, Relation: RelationNew}
	p := &Plan{Kind: KindFetch, Key: s.Key, Title: title, Agent: spec.Name, Source: Endpoint{Location: cl.Name},
		Target: Endpoint{Location: in.Machine.Name, OS: in.Machine.Facts.OS, Version: in.Install.Version}, Options: opt, Mark: MarkOff,
		Fetch: fp, fetchIn: &in}
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
		fp.Worktree = freePath(filepath.Join(filepath.Dir(top), filepath.Base(top)+"-"+worktreeName(s, cl)))
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
		f, err := fetcher.FetchCloud(ctx, in.Host, in.Install, s.Key.Session, agent.FetchTarget{Dir: fp.Worktree, Code: agent.ViaBranch})
		switch {
		case err != nil:
			check("err", refusal(err, spec, cl))
		case f.Run == nil:
			// A driver that needs no terminal brings the code (and perhaps the messages as
			// text, which are not written into an agent here yet): only the code comes home.
			check("err", fmt.Sprintf("hopsesh brings only the code of %s sessions so far; the conversation stays in the cloud. Choose the code only", cl.Title))
		default:
			fp.Run = *f.Run
			fp.Run.Unset = union(fp.Run.Unset, cl.Unset)
			fp.Command = launch.Shell(fp.Run, "", launch.DefaultShell())
			fp.Adopt, fp.Loss = f.Adopt, f.Loss
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
	if s.State == agent.CloudRunning {
		check("warn", "The session is still running in the cloud. You get the conversation as it is now")
	}
	relateFetch(ctx, in, p, opt, check)
	return p, nil
}

// planFetchRepo checks the checkout the worktree comes from and returns its main folder
// ("" when there is none to use).
func planFetchRepo(ctx context.Context, in FetchInput, fp *FetchPlan, check func(string, string)) string {
	if in.Checkout == "" {
		check("err", "hopsesh doesn't know which repository this cloud session works on; choose its checkout here")
		return ""
	}
	states, err := repos.ProbeLocal(ctx, []string{in.Checkout}, in.Worktrees)
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
		case sha == "":
			fp.BranchState = BranchMissing
			if fp.CodeOnly {
				check("err", noCode)
			} else {
				check("warn", noCode+"; only the conversation comes back")
			}
		default:
			fp.BranchState, fp.Base, fp.Ref = BranchPushed, sha, repos.CloudRef(in.Cloud.Name, b)
			check("ok", "Branch "+b+" is pushed")
		}
	} else if fp.CodeOnly {
		if _, ok := in.Module.(agent.CloudFetcher); ok && fp.Session != "" && slices.Contains(in.Cloud.CodeDown, agent.ViaDiff) {
			fp.Diff = true
			check("ok", in.Cloud.Title+"'s changes come as a patch, committed on a new branch here")
		} else {
			check("err", "hopsesh doesn't know this session's branch; bring it with its conversation and "+in.Module.Spec().Name+" fetches the branch itself")
		}
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
		fp.LocalBranch = repos.FreeBranchName(ctx, top, repos.FromBranch(in.Cloud.Name, worktreeName(in.Session, in.Cloud), ""))
	}
	if b := fp.CloudBranch; b != "" {
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
// used since, both stay, and only keep-both is offered.
func relateFetch(ctx context.Context, in FetchInput, p *Plan, opt Options, check func(string, string)) {
	fp, o := p.Fetch, in.Original
	if o == nil || in.Lineage == nil {
		return
	}
	var from *lineage.Replica
	for _, h := range in.Lineage.Hops {
		if h.Kind != lineage.HopHandoff || h.To < 0 || h.To >= len(in.Lineage.Replicas) || h.From < 0 || h.From >= len(in.Lineage.Replicas) {
			continue
		}
		if to := in.Lineage.Replicas[h.To]; to.Key.Session == fp.Session && to.Location == fp.Cloud {
			r := in.Lineage.Replicas[h.From]
			from = &r
		}
	}
	if from == nil || from.Key != o.Summary.Key {
		return
	}
	reader, okR := in.Module.(agent.Reader)
	_, okW := in.Module.(agent.Writer)
	if !okR || !okW {
		return
	}
	h, err := in.Machine.For(ctx, in.Module.Spec(), in.Install, nil)
	if err != nil {
		return
	}
	cur, err := reader.Read(ctx, h, in.Install, o.Summary, ir.Cursor{})
	if err != nil {
		return
	}
	s := o.Summary
	fp.Original = &s
	if cur.Cursor.Head != from.Head {
		fp.Relation = RelationDiverged
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
	case fp.ContinueIn != "":
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
		return spec.Name + " copies the whole conversation; hopsesh checks the message count."
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
	for _, e := range []error{agent.ErrSignedOut, agent.ErrNotEligible, agent.ErrRepoUnsupported, agent.ErrNotInstalled} {
		if errors.Is(err, e) {
			msg = strings.TrimPrefix(msg, e.Error()+": ")
		}
	}
	switch {
	case errors.Is(err, agent.ErrNotInstalled):
		return fmt.Sprintf("%s is reached through the `%s` command, which isn't installed here", cl.Title, cl.Driver)
	case errors.Is(err, agent.ErrSignedOut), errors.Is(err, agent.ErrNotEligible):
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
	if fp.CodeOnly {
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
		step("committing " + fp.CloudTitle + "'s patch on " + branch)
		if err := commitPatch(ctx, p, j, branch); err != nil {
			return res, err
		}
	}
	res.Worktree = fp.Worktree
	env.Audit.Write(audit.Entry{Action: "git.worktree", Session: p.Key.String(), Detail: map[string]any{"path": fp.Worktree, "commit": base, "branch": branch}})
	if fp.CodeOnly {
		res.Fetch.Outcome = FetchCode
		env.Audit.Write(audit.Entry{Action: "cloud.fetch", Session: p.Key.String(), Detail: map[string]any{"cloud": fp.Cloud, "code": true, "journal": j.ID}})
	} else {
		pf := &Fetch{Journal: j.ID, Time: time.Now().UTC(), Machine: machine, Agent: in.Module.Spec().ID, Cloud: fp.Cloud, CloudTitle: fp.CloudTitle,
			Session: fp.Session, URL: fp.URL, Title: p.Title, Untitled: in.Session.Title == "", Repo: fp.Repo, Checkout: top, Worktree: fp.Worktree, Base: base,
			CloudBranch: fp.CloudBranch, VendorPrefix: in.Cloud.VendorPrefix, Rename: fp.Rename, Lineage: in.Lineage,
			Continue: fp.ContinueIn, ContinueName: fp.ContinueName, Command: fp.Command, Run: fp.Run, Problems: in.Cloud.Problems,
			Mirror: in.Session.Mirror, MirrorOf: in.MirrorOf}
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
		res.Command = fp.Command
		env.Audit.Write(audit.Entry{Action: "cloud.fetch", Session: p.Key.String(), Detail: map[string]any{"cloud": fp.Cloud, "worktree": fp.Worktree, "journal": j.ID}})
	}
	if err := j.Seal(func(string) (host.FS, error) { return host.LocalFS(), nil }); err != nil {
		res.Warnings = append(res.Warnings, "could not record what this changed, for a safe undo: "+err.Error())
	}
	step("done")
	return res, nil
}

// commitPatch asks the driver for the session's patch, applies it in the fetch's worktree
// (detached at the base), commits it, records the branch and puts the worktree on it.
func commitPatch(ctx context.Context, p *Plan, j *journal.Journal, branch string) error {
	fp, in := p.Fetch, p.fetchIn
	fetcher, ok := in.Module.(agent.CloudFetcher)
	if !ok {
		return fmt.Errorf("%w: %s cannot bring code", agent.ErrUnsupported, in.Module.Spec().Name)
	}
	f, err := fetcher.FetchCloud(ctx, in.Host, in.Install, fp.Session, agent.FetchTarget{Dir: fp.Worktree, Code: agent.ViaDiff})
	if err != nil {
		return fmt.Errorf("%s: %w", fp.CloudTitle, err)
	}
	if len(f.Code.Diff) == 0 && !f.Code.Applied {
		return fmt.Errorf("%s brought no patch for %s", fp.CloudTitle, fp.Session)
	}
	sha, err := repos.CommitPatch(ctx, fp.Worktree, f.Code.Diff, f.Code.Applied, fmt.Sprintf("%s session %s", fp.CloudTitle, fp.Session))
	if err != nil {
		return fmt.Errorf("%s's patch does not apply on %s: %w", fp.CloudTitle, short(fp.Base), err)
	}
	if err := j.Ref(in.Machine.Name, fp.Checkout, "refs/heads/"+branch, sha, ""); err != nil {
		return err
	}
	return repos.SwitchNew(ctx, fp.Worktree, branch)
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
