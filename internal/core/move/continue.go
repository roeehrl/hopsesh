package move

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/scan"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Relations between a session and its copy in the other agent.
const (
	RelationNew      = "new"      // no copy there yet: a new session
	RelationAppend   = "append"   // the copy there is as it was left: add the new work to it
	RelationSame     = "same"     // nothing new on either side
	RelationBehind   = "behind"   // only the copy there changed
	RelationDiverged = "diverged" // both changed
)

// ContinuePlan is how a session continues in another agent.
type ContinuePlan struct {
	Comparison     *Comparison         `json:"comparison,omitempty"`
	Instructions   []InstructionSource `json:"instructions"`
	From           string              `json:"from"` // source agent name
	Fidelity       convert.Fidelity    `json:"fidelity"`
	Relation       string              `json:"relation"`
	AppendTo       *agent.Summary      `json:"appendTo,omitempty"`
	Report         convert.Report      `json:"report"`
	Briefing       string              `json:"briefing"`
	RolloverCursor ir.Cursor           `json:"rolloverCursor,omitempty"`
	Rollover       *agent.Summary      `json:"rollover,omitempty"`
	archive        []byte
	Via            string `json:"via,omitempty"` // ViaImport: the target agent's importer converts it

	items  []ir.Item
	header ir.Header
	expect ir.Cursor // the target copy must still end here (append)
	head   ir.Cursor // the source's head now
}

func buildContinue(ctx context.Context, in Input, opt Options) (*Plan, error) {
	src, tgt := in.Source, in.Target
	reader, okR := src.Module.(agent.Reader)
	writer, okW := tgt.Module.(agent.Writer)
	if !okR || !okW {
		return nil, fmt.Errorf("%w: hopsesh cannot read %s sessions or write %s sessions yet", agent.ErrUnsupported, src.Module.Spec().Name, tgt.Module.Spec().Name)
	}
	if opt.Worktree == "" {
		opt.Worktree = WorktreeAuto
	}
	if opt.Fidelity == "" {
		opt.Fidelity = convert.History
	}
	if opt.Bounded {
		opt.Via = ""
		opt.Native = false
	}
	if profileBoundary(in) {
		if opt.Native || opt.Via == ViaImport {
			return nil, fmt.Errorf("account transfers require portable history; native replay and vendor import are not supported")
		}
		opt.OtherAccount = true
	}
	s := in.Session
	spec := tgt.Module.Spec()
	if opt.Via == ViaImport {
		if imp, ok := tgt.Module.(agent.Importer); !ok || !imp.CanImport(src.Module.Spec().ID) {
			return nil, fmt.Errorf("%w: %s cannot import %s sessions itself", agent.ErrUnsupported, spec.Name, src.Module.Spec().Name)
		}
	}
	p := &Plan{
		Kind: KindContinue, Key: s.Key, Title: s.Title, Agent: spec.Name,
		Source:  Endpoint{Location: src.Machine.Name, OS: src.Machine.Facts.OS, CWD: s.CWD, Path: s.Path, Version: s.AgentVersion},
		Target:  Endpoint{Location: tgt.Machine.Name, OS: tgt.Machine.Facts.OS, Version: tgt.Install.Version},
		Live:    in.Live.State == agent.Live,
		Options: opt,
		OldName: s.Title,
	}
	cwd, err := planRepo(ctx, p, in, opt)
	if err != nil {
		return nil, err
	}
	if tgt.Machine.Local {
		cwd = realIntended(cwd)
	}
	p.Target.CWD = cwd
	p.Placement = agent.Placement{Key: agent.SessionKey{Agent: spec.ID, Profile: tgt.Install.ProfileID(), Session: agent.SessionID(newID())}, SourceID: s.Key.Session,
		CWD: cwd, OtherAccount: opt.OtherAccount, Location: tgt.Machine.Name, Mappings: withShortNames(ctx, src, continueMappings(p, src, tgt))}

	srcHost, err := src.Machine.For(ctx, src.Module.Spec(), src.Install, nil)
	if err != nil {
		return nil, err
	}
	seg, err := reader.Read(ctx, srcHost, src.Install, s, ir.Cursor{})
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.Key, err)
	}
	cp := &ContinuePlan{From: src.Module.Spec().Name, Fidelity: opt.Fidelity, Relation: RelationNew, head: seg.Cursor}
	p.Continue = cp
	if err := prepareLineage(ctx, p, in, &seg); err != nil {
		p.Blockers = append(p.Blockers, "cannot verify session lineage: "+err.Error())
	}
	fullNodes := append([]ir.Node(nil), seg.Nodes...)
	relateContinue(ctx, p, in, &seg, opt)
	if opt.OtherAccount {
		if src.Module.Spec().ID != spec.ID {
			p.Warnings = append(p.Warnings, "Moving between agents uses portable conversation text. Agent-private reasoning and internal state are not transferred; the original session is kept unchanged.")
		} else {
			p.Warnings = append(p.Warnings, "These are different runtime profiles. Login metadata cannot prove that account-bound session data is reusable, even when email addresses match. Hopsesh carries portable conversation text and keeps the original session.")
		}
	}

	// The destination keeps the session's own title: hopsesh shows movement in its own
	// views, never in titles. A return keeps the destination's title (which also clears a
	// legacy label an older hopsesh wrote there).
	title := s.Title
	if cp.AppendTo != nil {
		title = cp.AppendTo.Title
	}
	cp.header = ir.Header{CWD: cwd, Title: title, GitBranch: nonEmpty(p.Repo.SourceBranch, s.GitBranch), Model: seg.Header.Model, Created: seg.Header.Created}
	targetHost, err := tgt.Machine.For(ctx, spec, tgt.Install, nil)
	if err != nil {
		return nil, err
	}
	capacity, err := agent.CapacityFor(ctx, tgt.Module, targetHost, tgt.Install, cp.AppendTo)
	if err != nil {
		return nil, err
	}
	// A full destination remains untouched. The replacement stays on the same
	// logical branch; this is capacity rollover, never a user fork.
	if cp.AppendTo != nil && (capacity.Unknown || capacity.Allowance() < 4096) {
		cp.Rollover = cp.AppendTo
		cp.RolloverCursor = cp.expect
		cp.AppendTo = nil
		cp.Relation = RelationNew
		p.Placement.Key.Session = agent.SessionID(newID())
		seg.Nodes = fullNodes
		capacity, err = agent.CapacityFor(ctx, tgt.Module, targetHost, tgt.Install, nil)
		if err != nil {
			return nil, err
		}
		p.Warnings = append(p.Warnings, "The destination has insufficient verified context capacity. Create a bounded continuation on the same branch; the existing session remains available.")
	}
	prof := writer.Profile(tgt.Install)
	var redact func([]byte) ([]byte, int)
	if opt.Redact {
		redact = scan.Redact
	}
	fidelity := opt.Fidelity
	if opt.Via == ViaImport {
		fidelity = convert.Note // the importer brings the history; hopsesh adds only its briefing
	}
	archivePath := targetHost.Path().Join(tgt.Install.Root(spec.Roots[0].Name), "hopsesh", "archives", string(p.Placement.Key.Session)+".jsonl")
	bf := briefingFor(p, srcHost, src, s, spec, tgt.Machine.Name, cwd, opt)
	cp.archive, err = preservedArchive(srcHost, src.Module, src.Install, string(s.Key.Session), fullNodes, convert.Request{Mappings: p.Placement.Mappings, Redact: redact, Briefing: bf, Limits: opt.Limits})
	if err != nil {
		return nil, err
	}
	bf.HistoryFile = archivePath
	bf.HistoryRecords = bytes.Count(cp.archive, []byte{'\n'})
	allowance := capacity.Allowance()
	r := convert.Render(convert.Request{
		Nodes: seg.Nodes, From: cp.From, To: spec.Name, Fidelity: fidelity,
		Native: opt.Native && prof.NativeReplay, Window: capacity.EffectiveWindow(), Limit: &allowance, Mappings: p.Placement.Mappings, Redact: redact,
		Briefing: bf, Limits: opt.Limits,
	})
	cp.items, cp.Report = r.Items, r.Report
	cp.Report.Capacity = capacity
	cp.Report.Archive = archivePath
	if cp.Report.Blocked != "" {
		p.Blockers = append(p.Blockers, cp.Report.Blocked)
	}
	if opt.Via == ViaImport {
		cp.Via = ViaImport
		cp.Report.Method = "vendor-import"
		// The importer may flatten all source history. Use the whole input size as
		// an upper preflight; never count only the appended Hopsesh briefing.
		fi, e := srcHost.FS().Stat(s.Path)
		if e != nil {
			return nil, e
		}
		if fi.Size() > int64(max(0, allowance-cp.Report.Used))/2 {
			p.Blockers = append(p.Blockers, "vendor import is too large to verify safely; switch to portable history for a bounded continuation with a preserved archive")
		}
		cp.Report.Used += int(fi.Size()) * 2
		cp.Report.Summary = fmt.Sprintf("history converted by %s's own importer plus Hopsesh briefing; vendor fidelity unverified; import output will be capacity checked before opening", spec.Name)
		if cp.Relation != RelationNew {
			p.Blockers = append(p.Blockers, "--via import only starts a new session; leave it off to add the new work to the copy here")
		}
	}
	for _, it := range r.Items {
		if it.Node == "hopsesh/briefing" || strings.Contains(it.Text, "[hopsesh] This conversation was moved") {
			cp.Briefing = it.Text
		}
	}
	planContinueWarnings(p, in, opt)
	planRoundTrip(p, in, opt)
	p.NewName = launch.SessionName(p.Title, tgt.Machine.Name)
	prompt := ""
	if opt.Go {
		prompt = "Continue."
	}
	p.resumeOpts = agent.ResumeOptions{
		RemoteControl: opt.RemoteControl && agent.Has(tgt.Module, agent.CapRemoteControl), Name: p.NewName, Prompt: prompt,
		App: opt.App && agent.Has(tgt.Module, agent.CapApp),
	}
	if p.resumeOpts.App {
		if tgt.Install.Profile != nil && !tgt.Install.Profile.Default {
			p.Blockers = append(p.Blockers, "desktop opening cannot select an account profile; use a terminal")
		}
		if checker, ok := tgt.Module.(agent.AppChecker); ok {
			if err := checker.CheckApp(tgt.Install, p.Placement.Key, p.resumeOpts); err != nil {
				p.Blockers = append(p.Blockers, err.Error())
			}
		}
	}
	p.Resume = tgt.Module.Resume(tgt.Install, p.Placement.Key, p.Placement, p.resumeOpts)
	planNative(ctx, p, in, opt)
	if cp.Via == ViaImport && !(src.Machine.Local && src.Machine.Name == tgt.Machine.Name) && p.native == nil {
		p.Blockers = append(p.Blockers, fmt.Sprintf("--via import reads the session on this machine, so %s must be installed here to keep its copy here first", src.Module.Spec().Name))
	}
	return p, nil
}

// briefingFor is what a briefing says about the source session: where it was, its code,
// the instruction files the target agent does not read, the target's own tools, the
// user's note and the instructions carried at their request (or a warning that they are
// not). It needs no target writer: a handoff to a cloud briefs from the same facts.
func briefingFor(p *Plan, srcHost agent.Host, src Side, s agent.Summary, to agent.Spec, targetLoc, cwd string, opt Options) convert.Briefing {
	return convert.Briefing{
		FromVersion: s.AgentVersion, SourceID: string(s.Key.Session), SourceLoc: src.Machine.Name, TargetLoc: targetLoc,
		When: time.Now(), TargetOS: p.Target.OS, Branch: p.Repo.SourceBranch, Head: short(p.Repo.SourceHead), Dirty: p.Repo.Dirty,
		Missing: instructionGaps(src.Module.Spec(), to, cwd), ToolNames: to.Tools, Note: opt.Note,
		Rules: instructionRules(p, srcHost, src, s.CWD, opt),
	}
}

// planNative plans keeping the source agent's own copy of the session on the target too,
// byte for byte, when the continuation crosses machines: a later return to that agent
// there then adds only the new work to the original turns (whose signed reasoning stays
// valid) instead of converting everything twice. Whatever is in its way skips it, never
// the continuation.
func planNative(ctx context.Context, p *Plan, in Input, opt Options) {
	ns := in.Native
	if opt.NewReplica || ns == nil || in.Source.Machine.Name == in.Target.Machine.Name || len(p.Blockers) > 0 {
		return
	}
	name := in.Source.Module.Spec().Name
	nin := Input{Source: in.Source, Session: in.Session, Live: in.Live, Git: in.Git, Lineage: p.manifest.ForBranch(p.sourceLine),
		Target: ns.Target, Copies: ns.Copies, Worktrees: in.Worktrees}
	np, err := Build(ctx, nin, Options{TargetDir: p.Target.CWD, Worktree: WorktreeMain, Redact: opt.Redact})
	switch {
	case err != nil:
		p.Warnings = append(p.Warnings, fmt.Sprintf("the %s session is not also kept here: %v", name, err))
		return
	case len(np.Blockers) > 0:
		p.Warnings = append(p.Warnings, fmt.Sprintf("the %s copy here is left as it is: %s", name, np.Blockers[0]))
		return
	}
	p.native, p.nativeIn = np, nin
	p.NativeCopy = &NativeCopy{Agent: name, Key: np.Placement.Key, Replaces: len(np.SetAside) > 0}
}

// relateContinue finds the session's earlier copy in the target agent here (from lineage)
// and decides between a new session and adding the new work to that copy. It returns
// how many source nodes the target already has.
func relateContinue(ctx context.Context, p *Plan, in Input, seg *ir.Segment, opt Options) {
	cp := p.Continue
	if opt.Bounded {
		if in.Source.Machine.Facts.Endpoint == in.Target.Machine.Facts.Endpoint && in.Source.Install.ProfileID() == in.Target.Install.ProfileID() && in.Source.Install.BindingID() == in.Target.Install.BindingID() && in.Source.Module.Spec().ID == in.Target.Module.Spec().ID && !opt.Fork {
			s := in.Session
			cp.Rollover = &s
			cp.expect = seg.Cursor
			cp.RolloverCursor = seg.Cursor
		}
		if opt.Fork {
			p.Warnings = append(p.Warnings, "A bounded continuation will be created on a separate fork. The original remains available.")
		} else {
			p.Warnings = append(p.Warnings, "A separate bounded continuation will be created. The original remains available; this is not a new conversation branch.")
		}
		return
	}
	if p.manifest == nil || opt.Fork {
		return
	}
	// A portable text append does not replay the source's native account-bound
	// state. Modules explicitly opt in; lineage, root, cursor and live-writer
	// checks below still apply to the exact original in the selected profile.
	portableAppend := in.Target.Module.(agent.Writer).Profile(in.Target.Install).PortableAppend
	if opt.NewReplica || opt.OtherAccount && !portableAppend {
		if opt.TargetSession != "" {
			p.ReviewNewSession = true
			p.Blockers = append(p.Blockers, "cannot update the original session across agent or account profiles without verified native compatibility; remove --target-session and use --new-session to review a portable session on the same lineage branch")
		}
		// A fresh return preserves existing copies, but must still detect independent
		// destination work instead of silently treating divergent histories as one line.
		for _, c := range currentBranchCopies(in) {
			st, targetSegment, err := targetState(ctx, p, in, c)
			cp.Comparison = BuildComparison(p, in, c, *seg, targetSegment, st, err)
			if err != nil || !lineage.Subset(p.manifest.Covered(st.Heads), p.manifest.Covered(p.sourceState.Heads)) {
				if opt.Conflict == ConflictKeepBoth {
					forkLine(p)
					return
				}
				p.Conflict = "destination has independent or unverifiable work"
				cp.Relation = RelationDiverged
				p.Blockers = append(p.Blockers, "destination has independent work; use --keep-both to preserve a separate branch")
				return
			}
		}
		return
	}
	var candidates []Copy
	for _, c := range currentBranchCopies(in) {
		if c.Summary.Key.Agent == in.Target.Module.Spec().ID {
			candidates = append(candidates, c)
		}
	}
	if opt.TargetSession != "" {
		selected := candidates[:0]
		for _, c := range candidates {
			if c.Summary.Key.String() == opt.TargetSession || string(c.Summary.Key.Session) == opt.TargetSession {
				selected = append(selected, c)
			}
		}
		candidates = selected
		if len(candidates) == 0 {
			p.Blockers = append(p.Blockers, "selected destination does not belong to this branch")
			return
		}
	}
	if len(candidates) == 0 {
		return
	}
	if len(candidates) > 1 {
		for _, c := range candidates {
			p.Destinations = append(p.Destinations, c.Summary)
		}
		p.Blockers = append(p.Blockers, "multiple destination replicas match this branch; select a destination session explicitly")
		return
	}
	c := candidates[0]
	st, targetSegment, err := targetState(ctx, p, in, c)
	cp.Comparison = BuildComparison(p, in, c, *seg, targetSegment, st, err)
	if err != nil {
		p.Conflict = "cannot safely append: " + err.Error()
		cp.Relation = RelationDiverged
	} else {
		source, target := p.manifest.Covered(p.sourceState.Heads), p.manifest.Covered(st.Heads)
		switch {
		case lineage.Subset(source, target) && lineage.Subset(target, source):
			cp.Relation = RelationSame
			if c.Summary.CWD != "" && realIntended(c.Summary.CWD) != realIntended(p.Target.CWD) {
				p.Blockers = append(p.Blockers, "destination is synchronized in "+c.Summary.CWD+"; choose that folder or create a separate fork for a different folder")
				return
			}
			p.NoWork = true
			p.SyncTo = &c.Summary
			p.Placement.Key = c.Summary.Key
			return
		case lineage.Subset(source, target):
			cp.Relation = RelationBehind
			p.Blockers = append(p.Blockers, "only the destination copy has new work; continue it there")
			return
		case lineage.Subset(target, source):
			if c.Summary.CWD != "" && realIntended(c.Summary.CWD) != realIntended(p.Target.CWD) {
				p.Blockers = append(p.Blockers, "the original session belongs to "+c.Summary.CWD+"; choose that folder or create a separate fork for a different folder")
				return
			}
			cp.Relation = RelationAppend
			s := c.Summary
			cp.AppendTo = &s
			cp.expect = ir.Cursor{Head: st.Head, Offset: st.Offset}
			s.Key.Profile = in.Target.Install.ProfileID()
			p.Placement.Key = s.Key
			if c.Live.State == agent.Live {
				p.Blockers = append(p.Blockers, "the destination copy is open; quit it first")
			}
			seg.Nodes = missingNodes(seg.Nodes, target)
			return
		default:
			cp.Relation = RelationDiverged
			p.Conflict = "conversation histories contain different work; review the differences before returning"
		}
	}
	if opt.Conflict == ConflictKeepBoth {
		forkLine(p)
		cp.Relation = RelationNew
		p.Warnings = append(p.Warnings, p.Conflict+"; a separate fork will be created")
	} else {
		p.Blockers = append(p.Blockers, p.Conflict+"; choose --keep-both")
	}
}

// continueMappings map the repository and the home folder (each agent's data folder is
// its own).
func continueMappings(p *Plan, src, tgt Side) []agent.Mapping {
	var ms []agent.Mapping
	for _, m := range mappings(p, src, tgt) {
		own := false
		for _, dir := range src.Install.Roots {
			own = own || m.From == dir
		}
		if !own {
			ms = append(ms, m)
		}
	}
	return ms
}

// maxRules bounds each carried instruction file.
const maxRules = 8000

// instructionGaps reports instruction files the source agent read that the target does
// not.
func instructionGaps(from, to agent.Spec, cwd string) []string {
	reads := map[string]bool{}
	for _, f := range to.Instructions {
		reads[f] = true
	}
	var out []string
	for _, f := range from.Instructions {
		if reads[f] {
			continue
		}
		if _, err := os.Stat(filepath.Join(cwd, f)); err == nil {
			out = append(out, fmt.Sprintf("this project has %s, which %s read and %s does not; read it if the task depends on it", f, from.Name, to.Name))
		}
	}
	return out
}

func planContinueWarnings(p *Plan, in Input, opt Options) {
	spec := in.Target.Module.Spec()
	if p.Live && !opt.Fork {
		p.Warnings = append(p.Warnings, liveSnapshotNotice(p))
	}
	if p.Repo.Unpushed > 0 || p.Repo.Dirty > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("on %s the repository has %d unpushed commit(s) and %d uncommitted file(s) that a checkout here will not contain", p.Source.Location, p.Repo.Unpushed, p.Repo.Dirty))
	}
	if in.Target.Install.Binary == "" {
		p.Warnings = append(p.Warnings, spec.Name+" was not found on this machine; install it before continuing")
	} else if v := in.Target.Install.Version; v != "" && !spec.TestedWith(v) {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%s %s has not been tested with hopsesh", spec.Name, v))
	}
	if agent.IsExperimental(in.Target.Module, agent.CapWrite) || spec.Stability == agent.Experimental {
		p.Warnings = append(p.Warnings, "Experimental session writer: Hopsesh writes "+spec.Name+"’s native session format. Automated checks cover supported fixtures; compatibility with every agent release is not guaranteed")
	}
}

// importThen has the target agent's importer convert the session (from this machine:
// the source itself, or the native copy just kept here), adopts the file it created for
// undo, and appends hopsesh's briefing to it.
func importThen(ctx context.Context, p *Plan, in Input, h agent.Host, j *journal.Journal, env Env, nativeDst string, step func(string)) (ir.WriteResult, error) {
	tgt, cp := in.Target, p.Continue
	path := in.Session.Path
	if !in.Source.Machine.Local || in.Source.Machine.Name != tgt.Machine.Name {
		path = nativeDst
	}
	if path == "" {
		return ir.WriteResult{}, fmt.Errorf("the %s session is not on this machine to import", in.Source.Module.Spec().Name)
	}
	name := tgt.Module.Spec().Name
	step(name + "'s importer is converting the session")
	id, importErr := tgt.Module.(agent.Importer).Import(ctx, h, tgt.Install, in.Source.Module.Spec().ID, path, p.Target.CWD, cp.header.Title)
	if importErr != nil && id == "" {
		return ir.WriteResult{}, importErr
	}
	l, err := tgt.Module.List(ctx, h, tgt.Install)
	if err != nil {
		return ir.WriteResult{}, err
	}
	var s *agent.Summary
	for i := range l.Sessions {
		if l.Sessions[i].Key.Session == id {
			s = &l.Sessions[i]
		}
	}
	if s == nil {
		return ir.WriteResult{}, fmt.Errorf("%s imported the session as %s, but it is not in its list", name, id)
	}
	if err := j.RecordCreated(tgt.Machine.Name, s.Path); err != nil {
		return ir.WriteResult{}, err
	}
	j.AddKey(s.Key)
	// Rejected vendor output can be inspected before undo. Pin it immediately so
	// undo refuses to delete any work added after this failed import.
	if err := j.Seal(machinesOf(ctx, in)); err != nil {
		return ir.WriteResult{}, err
	}
	if importErr != nil {
		return ir.WriteResult{}, importErr
	}
	newArchive := h.Path().Join(tgt.Install.Root(tgt.Module.Spec().Roots[0].Name), "hopsesh", "archives", string(id)+".jsonl")
	if err = h.FS().WriteFile(newArchive, cp.archive, 0o600); err != nil {
		return ir.WriteResult{}, err
	}
	for i := range cp.items {
		cp.items[i].Text = strings.ReplaceAll(cp.items[i].Text, cp.Report.Archive, newArchive)
	}
	cp.Report.Archive = newArchive
	current, e := readSegment(ctx, in.Source, in.Session)
	if e != nil || current.Cursor != cp.head {
		return ir.WriteResult{}, fmt.Errorf("source changed while importing; imported file retained for undo; refresh the plan")
	}
	p.Placement.Key = s.Key
	if p.resumeOpts.App {
		if checker, ok := tgt.Module.(agent.AppChecker); ok {
			if err := checker.CheckApp(tgt.Install, p.Placement.Key, p.resumeOpts); err != nil {
				p.Blockers = append(p.Blockers, err.Error())
			}
		}
	}
	p.Resume = tgt.Module.Resume(tgt.Install, s.Key, p.Placement, p.resumeOpts)
	seg, err := tgt.Module.(agent.Reader).Read(ctx, h, tgt.Install, *s, ir.Cursor{})
	if err != nil {
		return ir.WriteResult{}, err
	}
	capacity, err := agent.CapacityFor(ctx, tgt.Module, h, tgt.Install, s)
	if err != nil {
		return ir.WriteResult{}, err
	}
	if err = capacity.Check(cp.items); err != nil {
		return ir.WriteResult{}, fmt.Errorf("imported session was preserved but will not be launched: %w", err)
	}
	cp.Report.Used = capacity.Existing + ir.ItemsCost(cp.items)
	// The vendor attests that this new native session was imported from this input.
	// Treat its history as a whole snapshot, retaining an explicit fidelity loss.
	var projection []ir.Projection
	for _, n := range seg.Nodes {
		projection = append(projection, ir.Projection{Anchor: nativeAnchor(n), Hash: ir.ContentHash(n), Coverage: p.sourceState.Heads, Fidelity: "vendor snapshot"})
	}
	idReplica := p.manifest.Upsert(lineage.Replica{Endpoint: in.Target.Machine.Facts.Endpoint, Binding: in.Target.Install.BindingID(), Key: s.Key, Line: p.targetLine, Location: p.Target.Location, AgentVersion: in.Target.Install.Version, Time: seg.Header.Created})
	p.manifest.ReplaceProjection(idReplica, seg.Cursor, projection, p.sourceState.Heads, []string{"vendor import; per-turn fidelity not verified"})
	req := ir.WriteRequest{OperationID: p.OperationID, Mode: ir.WriteAppend, SessionID: string(id), Expect: seg.Cursor, Header: cp.header, Items: cp.items}
	if err = operationRequest(env, p, req, nativeDst, ir.Cursor{}); err != nil {
		return ir.WriteResult{}, err
	}
	step("adding hopsesh's briefing")
	w, err := tgt.Module.(agent.Writer).Write(ctx, h, tgt.Install, req)
	if err != nil {
		return w, fmt.Errorf("adding the briefing to the imported session: %w", err)
	}
	w.From = 0 // the whole file is written for this hop
	return w, nil
}

// applyContinue writes the converted session and records the hop.
func applyContinue(ctx context.Context, p *Plan, in Input, env Env) (*Result, error) {
	step := func(s string) {
		if env.Progress != nil {
			env.Progress(s)
		}
	}
	cp := p.Continue
	src, tgt := in.Source, in.Target
	preflightHost, err := tgt.Machine.For(ctx, tgt.Module.Spec(), tgt.Install, nil)
	if err != nil {
		return nil, err
	}
	capacity, err := agent.CapacityFor(ctx, tgt.Module, preflightHost, tgt.Install, cp.AppendTo)
	if err != nil {
		return nil, err
	}
	if cp.AppendTo != nil {
		if detector, ok := tgt.Module.(agent.LiveDetector); ok {
			live, err := detector.Live(ctx, preflightHost, tgt.Install, []agent.SessionID{cp.AppendTo.Key.Session})
			if err != nil {
				return nil, fmt.Errorf("cannot recheck destination activity: %w", err)
			}
			if live[cp.AppendTo.Key.Session].State == agent.Live {
				return nil, fmt.Errorf("the destination copy is open; quit it first and refresh the plan")
			}
		}
	}
	if capacity != cp.Report.Capacity {
		return nil, fmt.Errorf("destination capacity changed since planning; refresh the plan")
	}
	if err = capacity.Check(cp.items); err != nil {
		return nil, err
	}
	for path, expect := range p.ExpectedDestination {
		seg, e := readSegment(ctx, tgt, agent.Summary{Path: path})
		if e != nil || seg.Cursor != expect {
			return nil, fmt.Errorf("destination changed since planning; refresh the plan")
		}
	}
	j, err := journal.New(env.StateDir, journal.KindContinue, fmt.Sprintf("%s → %s", p.Title, tgt.Module.Spec().Name))
	if err != nil {
		return nil, err
	}
	if err := operationJournal(env, p, j); err != nil {
		return nil, err
	}
	j.TransferID = p.OperationID
	if err := j.Save(); err != nil {
		return nil, err
	}
	j.AddKey(p.Placement.Key)
	res := &Result{Journal: j.ID}
	if err := applyRepo(ctx, p, in, env, res, step); err != nil {
		return res, err
	}
	h, err := tgt.Machine.For(ctx, tgt.Module.Spec(), tgt.Install, j)
	if err != nil {
		return res, err
	}
	if err := h.FS().WriteFile(cp.Report.Archive, cp.archive, 0o600); err != nil {
		return res, fmt.Errorf("preserving portable archive: %w", err)
	}
	// The source agent's own copy here too (planNative).
	var nativeDst string
	var nativeHead ir.Cursor
	if np := p.native; np != nil {
		name := src.Module.Spec().Name
		step("keeping the " + name + " session here too")
		if np.NoWork {
			nativeDst = np.SyncTo.Path
			nativeHead = np.ExpectedDestination[nativeDst]
		} else if dst, _, nh, err := install(ctx, np, p.nativeIn, env, j, &Result{}, func(string) {}); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("the %s session was not also kept here: %v", name, err))
		} else {
			nativeDst, nativeHead = dst, nh
		}
	}

	var w ir.WriteResult
	if cp.Via == ViaImport {
		w, err = importThen(ctx, p, in, h, j, env, nativeDst, step)
	} else {
		req := ir.WriteRequest{OperationID: p.OperationID, Mode: ir.WriteNew, SessionID: string(p.Placement.Key.Session), Header: cp.header, Items: cp.items}
		if cp.Relation == RelationAppend {
			req.Mode, req.Expect = ir.WriteAppend, cp.expect
		}
		if err := operationRequest(env, p, req, nativeDst, nativeHead); err != nil {
			return res, err
		}
		step(fmt.Sprintf("writing the session for %s", tgt.Module.Spec().Name))
		w, err = tgt.Module.(agent.Writer).Write(ctx, h, tgt.Install, req)
	}
	if err != nil {
		return res, err
	}
	res.Files, res.Bytes = 1, w.To-w.From
	if err := operationNative(env, p, w.Path, w.Cursor, res); err != nil {
		return res, err
	}
	if env.Failpoint != nil {
		if err := env.Failpoint("native-written"); err != nil {
			return res, err
		}
	}
	env.Audit.Write(audit.Entry{Action: "continue.write", Host: p.Source.Location, Session: p.Placement.Key.String(),
		Detail: map[string]any{"from": p.Key.String(), "path": w.Path, "bytes": res.Bytes, "relation": cp.Relation, "via": cp.Via, "journal": j.ID}})

	if err := recordContinuation(ctx, p, in, j, w, nativeDst, nativeHead, res); err != nil {
		return res, err
	}
	if pi, ok := tgt.Module.(agent.PostInstaller); ok {
		pl := p.Placement
		pl.Name = cp.header.Title
		if err := pi.AfterInstall(ctx, h, tgt.Install, pl.Key, pl); err != nil {
			res.Warnings = append(res.Warnings, "after writing: "+err.Error())
		}
	}
	res.Command, res.Run = launch.Shell(p.Resume, "", launch.DefaultShell()), p.Resume
	if err := j.Seal(machinesOf(ctx, in)); err != nil {
		res.Warnings = append(res.Warnings, "could not record what this changed, for a safe undo: "+err.Error())
	}
	step("done")
	return res, nil
}

func recordContinuation(ctx context.Context, p *Plan, in Input, j *journal.Journal, w ir.WriteResult, nativeDst string, nativeHead ir.Cursor, res *Result) error {
	// Lineage beside every copy.
	m := p.manifest.Clone()
	now := j.Time.UTC()
	from := p.sourceReplica
	to := m.Upsert(lineage.Replica{Endpoint: in.Target.Machine.Facts.Endpoint, Binding: in.Target.Install.BindingID(), Key: p.Placement.Key, Line: p.targetLine, Location: p.Target.Location, AgentVersion: p.Target.Version, Time: now})
	st := m.Deliver(to, w.Cursor, w.Projection, p.sourceState.Heads, append(append([]string(nil), p.sourceState.Loss...), conversionLoss(p.Continue.Report)...))
	var rollover *lineage.Rollover
	if p.Continue.Rollover != nil {
		_, id, ok := m.FindBinding(p.Continue.Rollover.Key, in.Target.Machine.Facts.Endpoint, in.Target.Install.BindingID())
		if ok {
			rollover = &lineage.Rollover{Replica: id, Cursor: p.Continue.RolloverCursor}
		}
	}
	if err := m.AppendHop(lineage.Hop{Notify: p.Options.Notify, Rollover: rollover, ID: p.OperationID, Time: now, From: from, To: to, Source: p.sourceState.ID, Target: st.ID, Kind: lineage.HopContinue, Fork: p.targetLine != p.sourceLine, Fidelity: string(p.Continue.Fidelity), Written: &lineage.Range{From: w.From, To: w.To}}); err != nil {
		return err
	}
	if nativeDst != "" {
		if err := recordNativeBackup(ctx, p, in, m, nativeDst, nativeHead, j.Time.UTC()); err != nil {
			return err
		}
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if err := j.WriteReceipt(host.LocalFS(), p.Target.Location, lineage.PathFor(w.Path), m.ForBranch(p.targetLine).Encode(), true); err != nil {
		return fmt.Errorf("recording destination receipt: %w", err)
	}
	if nativeDst != "" {
		if err := j.WriteReceipt(host.LocalFS(), p.Target.Location, lineage.PathFor(nativeDst), m.ForBranch(p.sourceLine).Encode(), true); err != nil {
			return err
		}
	}
	srcFS, reachErr := in.Source.Machine.FS(ctx)
	if reachErr != nil {
		srcFS = nil
	}
	if e := j.WriteReceipt(srcFS, p.Source.Location, lineage.PathFor(in.Session.Path), m.ForBranch(p.sourceLine).Encode(), false); e != nil {
		res.Warnings = append(res.Warnings, "destination committed; source receipt acknowledgement pending: "+e.Error())
	}

	return nil
}

func nativeAnchor(n ir.Node) string {
	if n.Native != nil {
		return n.Native.Anchor
	}
	return ""
}

func liveSnapshotNotice(p *Plan) string {
	place := p.Source.Location
	if p.Source.Location == p.Target.Location {
		place += " (this machine)"
	}
	notice := "The source session on " + place + " is still running. This transfer uses a snapshot; later source messages are not automatically synchronized."
	return notice
}
