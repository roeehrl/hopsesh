package move

import (
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
	From     string           `json:"from"` // source agent name
	Fidelity convert.Fidelity `json:"fidelity"`
	Relation string           `json:"relation"`
	AppendTo *agent.Summary   `json:"appendTo,omitempty"`
	Report   convert.Report   `json:"report"`
	Briefing string           `json:"briefing"`
	Via      string           `json:"via,omitempty"` // ViaImport: the target agent's importer converts it

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
	relateContinue(ctx, p, in, &seg, opt)
	if opt.OtherAccount {
		p.Warnings = append(p.Warnings, "Account identity continuity is unverified. A new portable conversation is created; signed reasoning, opaque compaction and vendor-private state are not transferred. Original copies remain available.")
	}

	title := fmt.Sprintf("%s (from %s)", nonEmpty(s.Title, "session"), cp.From)
	if cp.AppendTo != nil {
		title = cp.AppendTo.Title // its own title again, in place of a "continued in" mark
	}
	cp.header = ir.Header{CWD: cwd, Title: title, GitBranch: nonEmpty(p.Repo.SourceBranch, s.GitBranch), Model: seg.Header.Model, Created: seg.Header.Created}
	prof := writer.Profile(tgt.Install)
	var redact func([]byte) ([]byte, int)
	if opt.Redact {
		redact = scan.Redact
	}
	fidelity := opt.Fidelity
	if opt.Via == ViaImport {
		fidelity = convert.Note // the importer brings the history; hopsesh adds only its briefing
	}
	r := convert.Render(convert.Request{
		Nodes: seg.Nodes, From: cp.From, To: spec.Name, Fidelity: fidelity,
		Native: opt.Native && prof.NativeReplay, Window: prof.Window, Mappings: p.Placement.Mappings, Redact: redact,
		Briefing: briefingFor(p, srcHost, src, s, spec, tgt.Machine.Name, cwd, opt),
	})
	cp.items, cp.Report = r.Items, r.Report
	if opt.Via == ViaImport {
		cp.Via = ViaImport
		cp.Report.Summary = fmt.Sprintf("converted by %s's own importer; hopsesh adds its briefing", spec.Name)
		if cp.Relation != RelationNew {
			p.Blockers = append(p.Blockers, "--via import only starts a new session; leave it off to add the new work to the copy here")
		}
	}
	for _, it := range r.Items {
		if it.Node == "hopsesh/briefing" || strings.Contains(it.Text, "[hopsesh] This conversation was moved") {
			cp.Briefing = it.Text[strings.LastIndex(it.Text, "[hopsesh] This conversation was moved"):]
		}
	}
	if cp.Report.Reasoning > 0 || cp.Report.Truncated > 0 || cp.Report.Summarised > 0 {
		p.Warnings = append(p.Warnings, "what does not carry over: "+cp.Report.Summary)
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
		When: time.Now(), Branch: p.Repo.SourceBranch, Head: short(p.Repo.SourceHead), Dirty: p.Repo.Dirty,
		Missing: instructionGaps(src.Module.Spec(), to, cwd), ToolNames: to.Tools, Note: opt.Note,
		Rules: globalRules(p, srcHost, src, to.Name, opt.CarryRules),
	}
}

// planNative plans keeping the source agent's own copy of the session on the target too,
// byte for byte, when the continuation crosses machines: a later return to that agent
// there then adds only the new work to the original turns (whose signed reasoning stays
// valid) instead of converting everything twice. Whatever is in its way skips it, never
// the continuation.
func planNative(ctx context.Context, p *Plan, in Input, opt Options) {
	ns := in.Native
	if ns == nil || in.Source.Machine.Name == in.Target.Machine.Name || len(p.Blockers) > 0 {
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
	if p.manifest == nil || opt.Fork {
		return
	}
	if opt.OtherAccount {
		if opt.TargetSession != "" {
			p.Blockers = append(p.Blockers, "cannot append to a native replica under an unverified account binding; clear the destination session to create a portable copy")
		}
		// A portable return preserves existing copies, but must still detect independent
		// destination work instead of silently treating divergent histories as one line.
		for _, c := range currentBranchCopies(in) {
			st, _, err := targetState(ctx, p, in, c)
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
	st, _, err := targetState(ctx, p, in, c)
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
			p.Conflict = "both sessions contain independent work; preserve them as separate branches"
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

// globalRules reads the user's instructions for every project of the source agent: carried
// into the briefing when asked, otherwise reported.
func globalRules(p *Plan, h agent.Host, src Side, to string, carry bool) []convert.Rules {
	spec := src.Module.Spec()
	var out []convert.Rules
	for _, g := range spec.GlobalInstructions {
		path := agent.Expand(g, h.Facts().Home, src.Install.Roots, h.Path())
		b, err := h.FS().ReadFile(path, 1<<20)
		text := strings.TrimSpace(string(b))
		if err != nil || text == "" {
			continue
		}
		if !carry {
			p.Warnings = append(p.Warnings, fmt.Sprintf("your instructions for every %s project (%s) do not carry over to %s; --carry-rules adds them to the briefing", spec.Name, path, to))
			continue
		}
		if len(text) > maxRules {
			text = strings.ToValidUTF8(text[:maxRules], "") + "\n[shortened]"
		}
		out = append(out, convert.Rules{File: path, Text: text})
	}
	return out
}

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
		p.Warnings = append(p.Warnings, fmt.Sprintf("the session is still open on %s; it continues in %s from what it has now", p.Source.Location, spec.Name))
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
		p.Warnings = append(p.Warnings, "writing "+spec.Name+" sessions is experimental")
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
	id, err := tgt.Module.(agent.Importer).Import(ctx, h, tgt.Install, in.Source.Module.Spec().ID, path, p.Target.CWD, cp.header.Title)
	if err != nil {
		return ir.WriteResult{}, err
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
	// The vendor attests that this new native session was imported from this input.
	// Treat its history as a whole snapshot, retaining an explicit fidelity loss.
	var projection []ir.Projection
	for _, n := range seg.Nodes {
		projection = append(projection, ir.Projection{Anchor: n.Native.Anchor, Hash: ir.ContentHash(n), Coverage: p.sourceState.Heads, Fidelity: "vendor snapshot"})
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

// markNative marks the source agent's copy kept here as continued in the target agent, so
// it is not resumed by mistake (returning to it with hopsesh clears the mark).
func markNative(ctx context.Context, p *Plan, j *journal.Journal, path string, res *Result) {
	ns := p.nativeIn.Target
	marker, ok := ns.Module.(agent.Marker)
	if !ok {
		return
	}
	h, err := ns.Machine.For(ctx, ns.Module.Spec(), ns.Install, j)
	if err == nil {
		s := agent.Summary{Key: p.native.Placement.Key, Title: p.Title, CWD: p.Target.CWD, Path: path}
		err = marker.Mark(ctx, h, ns.Install, s, agent.Mark{Kind: agent.MarkContinued, Location: p.Target.Location, AgentName: p.Agent})
	}
	if err != nil {
		res.Warnings = append(res.Warnings, "could not mark the native copy here: "+err.Error())
	}
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
	mark := agent.Mark{Kind: agent.MarkContinued, Location: p.Target.Location, AgentName: tgt.Module.Spec().Name}
	markWith(ctx, p, in, j, env, cp.head, mark, res)
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
	if err := m.AppendHop(lineage.Hop{ID: p.OperationID, Time: now, From: from, To: to, Source: p.sourceState.ID, Target: st.ID, Kind: lineage.HopContinue, Fork: p.targetLine != p.sourceLine, Fidelity: string(p.Continue.Fidelity), Written: &lineage.Range{From: w.From, To: w.To}}); err != nil {
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
		if !p.Options.Fork {
			markNative(ctx, p, j, nativeDst, res)
		}
	}
	if srcFS, e := in.Source.Machine.FS(ctx); e == nil {
		if e = j.WriteReceipt(srcFS, p.Source.Location, lineage.PathFor(in.Session.Path), m.ForBranch(p.sourceLine).Encode(), false); e != nil {
			res.Warnings = append(res.Warnings, "destination committed; source receipt acknowledgement pending: "+e.Error())
		}
	}

	return nil
}
