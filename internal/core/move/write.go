package move

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Writing what a cloud's driver brought back as text (Fetched.Segment) into a local agent.
// No vendor gives a cloud conversation back in an agent's own format except Claude Code's
// teleport, so for every other cloud the conversation comes as IR: Copilot's session log,
// Amp's thread, or, for a code-only cloud such as Codex cloud, just the task's title and
// what came of it. The core renders it the way a continuation renders another agent's
// session (convert.Render, then the target module's Writer, then its PostInstaller) into a
// new session of whichever agent the user chose, in the worktree that holds the cloud's
// code, and journals the session, the branch and the worktree so undo removes them.

// commitPatch applies the cloud's patch in the fetch's worktree (detached at the base),
// commits it, records the branch and puts the worktree on it. The patch is what the driver
// brought while planning, or (the code only) what it brings now.
func commitPatch(ctx context.Context, p *Plan, j *journal.Journal, branch string) error {
	fp, in := p.Fetch, p.fetchIn
	f := fp.fetched
	if f == nil {
		fetcher, ok := in.Module.(agent.CloudFetcher)
		if !ok {
			return fmt.Errorf("%w: %s cannot bring code", agent.ErrUnsupported, in.Module.Spec().Name)
		}
		got, err := fetcher.FetchCloud(ctx, in.Host, in.Install, fp.Session, agent.FetchTarget{Dir: fp.Worktree, Code: agent.ViaDiff})
		if err != nil {
			return fmt.Errorf("%s: %w", fp.CloudTitle, err)
		}
		f = &got
	}
	if len(f.Code.Diff) == 0 && !f.Code.Applied {
		return fmt.Errorf("%s brought no patch for %s", fp.CloudTitle, fp.Session)
	}
	sha, err := repos.CommitPatch(ctx, fp.Worktree, f.Code.Diff, f.Code.Applied, fmt.Sprintf("%s %s %s", fp.CloudTitle, fp.Noun, fp.Session))
	switch {
	case errors.Is(err, repos.ErrCommit):
		return fmt.Errorf("%s's patch: %w", fp.CloudTitle, err)
	case err != nil:
		return fmt.Errorf("%s's patch does not apply on %s: %w", fp.CloudTitle, short(fp.Base), err)
	}
	if err := j.Ref(in.Machine.Name, fp.Checkout, "refs/heads/"+branch, sha, ""); err != nil {
		return err
	}
	return repos.SwitchNew(ctx, fp.Worktree, branch)
}

// writeFetched writes the brought conversation as a new session of the agent it goes to,
// in the worktree, records lineage, and keeps the fetch's record as adopted (there is no
// copy to wait for).
func writeFetched(ctx context.Context, p *Plan, env Env, j *journal.Journal, base, branch string, res *Result) error {
	fp, in := p.Fetch, p.fetchIn
	tm, tin := in.Module, in.Install
	if in.Continue != nil {
		tm, tin = in.Continue, in.ContinueInstall
	}
	spec := tm.Spec()
	writer, ok := tm.(agent.Writer)
	if !ok || fp.fetched == nil || fp.fetched.Segment == nil {
		return fmt.Errorf("%w: nothing to write as a %s session", agent.ErrUnsupported, spec.Name)
	}
	seg := fp.fetched.Segment
	nodes := append([]ir.Node(nil), seg.Nodes...)
	if prompt := strings.TrimSpace(in.Prompt); prompt != "" {
		// hopsesh started the session: its first prompt is known here, word for word.
		for i := range nodes {
			if nodes[i].Kind == ir.KindMessage && nodes[i].Actor == ir.User {
				nodes[i].Text = prompt
				break
			}
		}
		ir.Chain(nodes, "")
	}
	if err := prepareCloudGraph(ctx, p, &nodes); err != nil {
		return err
	}
	machine := in.Machine.Name
	h, err := in.Machine.For(ctx, spec, tin, j)
	if err != nil {
		return err
	}
	head, _ := repos.Head(ctx, fp.Worktree)
	if fp.Append && fp.Original != nil {
		return appendWritten(ctx, p, env, j, nodes, base, branch, res)
	}
	title := fmt.Sprintf("%s (from %s)", nonEmpty(nonEmpty(seg.Header.Title, in.Session.Title), "a "+fp.Noun), fp.CloudTitle)
	req, r, _, err := portableWrite(ctx, h, tm, tin, nil, nodes, ir.WriteRequest{OperationID: j.ID, Mode: ir.WriteNew, SessionID: newID(), Header: ir.Header{CWD: fp.Worktree, Title: title, GitBranch: branch, Created: seg.Header.Created}}, convert.Request{IncludeGenerated: true, Nodes: nodes, From: fp.CloudTitle, To: spec.Name, Fidelity: convert.History, Briefing: convert.Briefing{SourceID: string(fp.Session), SourceLoc: fp.Cloud, TargetLoc: machine, When: time.Now(), Branch: branch, Head: short(head), ToolNames: spec.Tools}})
	if err != nil {
		return err
	}
	if env.Progress != nil {
		env.Progress("writing bounded conversation for " + spec.Name)
	}
	w, err := writer.Write(ctx, h, tin, req)
	if err != nil {
		return fmt.Errorf("writing the %s session: %w", spec.Name, err)
	}
	key := agent.SessionKey{Agent: spec.ID, Profile: tin.ProfileID(), Session: agent.SessionID(w.SessionID)}
	j.AddKey(key)
	pl := agent.Placement{Key: key, SourceID: fp.Session, CWD: fp.Worktree, Location: machine, Name: title}
	if pi, ok := tm.(agent.PostInstaller); ok {
		if err := pi.AfterInstall(ctx, h, tin, key, pl); err != nil {
			res.Warnings = append(res.Warnings, "after writing: "+err.Error())
		}
	}
	way := agent.ViaBranch
	if fp.Diff {
		way = agent.ViaDiff
	}
	m := in.Lineage.Clone()
	if m == nil {
		m = lineage.NewNative(cloudEndpoint(fp.Cloud, in.Session.Account), agent.SessionKey{Agent: in.Module.Spec().ID, Session: fp.Session})
	}
	now := j.Time.UTC()
	from := m.Upsert(lineage.Replica{Key: agent.SessionKey{Agent: in.Module.Spec().ID, Session: fp.Session}, Endpoint: cloudEndpoint(fp.Cloud, in.Session.Account), Location: fp.Cloud, Time: now,
		URL: fp.URL, Branch: fp.CloudBranch})
	to := m.Upsert(lineage.Replica{Endpoint: in.Machine.Facts.Endpoint, Binding: tin.BindingID(), Key: key, Location: machine, AgentVersion: tin.Version, Time: now})
	source, _ := m.LatestState(from)
	m.Deliver(to, w.Cursor, w.Projection, source.Heads, append(append(append([]string(nil), source.Loss...), fp.Loss...), conversionLoss(r.Report)...))
	if err := m.AppendHop(lineage.Hop{ID: j.ID, Time: now, From: from, To: to, Kind: lineage.HopFetch, Fidelity: string(fp.Fidelity),
		Written: &lineage.Range{From: w.From, To: w.To}, Code: &lineage.CodeHop{Way: way, Remote: fp.Repo, Branch: codeBranch(fp, branch), Base: base}}); err != nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if err := j.WriteReceipt(host.LocalFS(), machine, lineage.PathFor(w.Path), m.Encode(), true); err != nil {
		return err
	}
	resume := tm.Resume(tin, key, pl, agent.ResumeOptions{})
	ad := &Adopted{Time: now, Outcome: FetchComplete, Key: key, Path: w.Path, Restored: fp.Messages, Branch: branch, Resume: resume,
		Command: launch.Shell(resume, "", launch.DefaultShell()), Written: true, Fidelity: string(fp.Fidelity), Changes: fp.Changes, Warnings: res.Warnings}
	pf := &Fetch{Journal: j.ID, Time: now, Machine: machine, Agent: in.Module.Spec().ID, Cloud: fp.Cloud, CloudTitle: fp.CloudTitle,
		Session: fp.Session, URL: fp.URL, Title: p.Title, Repo: fp.Repo, Checkout: fp.Checkout, Worktree: fp.Worktree, Base: base,
		CloudBranch: fp.CloudBranch, Lineage: m, Adopted: ad, Loss: fp.Loss, Noun: fp.Noun}
	if in.Continue != nil {
		pf.Continue, pf.ContinueName = fp.ContinueIn, fp.ContinueName
	}
	if err := SaveFetch(env.StateDir, pf); err != nil {
		return err
	}
	res.Fetch.Outcome, res.Fetch.Branch, res.Fetch.Key = FetchComplete, branch, key.String()
	res.Command, res.Run = ad.Command, ad.Resume
	env.Audit.Write(audit.Entry{Action: "cloud.fetch", Session: p.Key.String(), Detail: map[string]any{"cloud": fp.Cloud, "worktree": fp.Worktree,
		"branch": branch, "journal": j.ID}})
	env.Audit.Write(audit.Entry{Action: "cloud.write", Host: machine, Session: key.String(), Detail: map[string]any{"from": string(fp.Session),
		"cloud": fp.Cloud, "path": w.Path, "messages": fp.Messages, "fidelity": string(fp.Fidelity), "journal": j.ID}})
	return nil
}

// codeBranch is the branch a fetch's lineage names for its code: the cloud's own branch on
// the remote when the code came on one (a hand-off from here can reuse it), else the branch
// it is on here.
func codeBranch(fp *FetchPlan, local string) string {
	if !fp.Diff && fp.CloudBranch != "" {
		return fp.CloudBranch
	}
	return nonEmpty(local, fp.CloudBranch)
}

// appendWritten adds what the cloud brought to the session it was handed off from (as it
// was left: a delta append, so the original's own turns stay exactly as they were), instead
// of writing a new session; the original is then what resumes. The briefing hopsesh sent
// is left out: the original holds what it summed up.
func appendWritten(ctx context.Context, p *Plan, env Env, j *journal.Journal, nodes []ir.Node, base, branch string, res *Result) error {
	fp, in := p.Fetch, p.fetchIn
	tm, tin := in.Module, in.Install
	if in.Continue != nil {
		tm, tin = in.Continue, in.ContinueInstall
	}
	spec, o := tm.Spec(), *fp.Original
	writer := tm.(agent.Writer)
	h, err := in.Machine.For(ctx, spec, tin, j)
	if err != nil {
		return err
	}
	fullNodes := append([]ir.Node(nil), nodes...)
	machine := in.Machine.Name
	if in.Lineage != nil {
		m := in.Lineage
		id := m.Upsert(lineage.Replica{Endpoint: in.Machine.Facts.Endpoint, Binding: tin.BindingID(), Key: o.Key, Location: machine, AgentVersion: tin.Version, Time: time.Now().UTC()})
		segment, e := tm.(agent.Reader).Read(ctx, h, tin, o, ir.Cursor{})
		if e != nil {
			return e
		}
		target, e := m.Observe(id, &segment)
		if e != nil {
			return e
		}
		sourceID := cloudReplica(m, agent.SessionKey{Agent: in.Module.Spec().ID, Session: fp.Session}, fp.Cloud, in.Session.Account)
		source, _ := m.LatestState(sourceID)
		if !lineage.Subset(m.Covered(target.Heads), m.Covered(source.Heads)) {
			return fmt.Errorf("original contains independent work; preserve a separate branch")
		}
		nodes = missingNodes(nodes, m.Covered(target.Heads))
	}
	head, _ := repos.Head(ctx, fp.Worktree)
	req, r, rolled, err := portableWrite(ctx, h, tm, tin, &o, fullNodes, ir.WriteRequest{OperationID: j.ID, Mode: ir.WriteAppend, SessionID: string(o.Key.Session), Expect: fp.originalHead, Header: ir.Header{CWD: o.CWD, Title: o.Title, GitBranch: branch}}, convert.Request{Nodes: nodes, From: fp.CloudTitle, To: spec.Name, Fidelity: convert.History, Briefing: convert.Briefing{SourceID: string(fp.Session), SourceLoc: fp.Cloud, TargetLoc: machine, When: time.Now(), Branch: branch, Head: short(head), ToolNames: spec.Tools}})
	if err != nil {
		return err
	}
	w, err := writer.Write(ctx, h, tin, req)
	if err != nil {
		return fmt.Errorf("adding to %s: %w", o.Title, err)
	}
	old := o
	if rolled {
		o.Key.Session = agent.SessionID(w.SessionID)
		o.Path = w.Path
		res.Warnings = append(res.Warnings, "Original context was full; prepared a bounded continuation on the same branch. Original retained.")
	}
	j.AddKey(o.Key)
	m := in.Lineage.Clone()
	if m == nil {
		m = lineage.NewNative(cloudEndpoint(fp.Cloud, in.Session.Account), agent.SessionKey{Agent: in.Module.Spec().ID, Session: fp.Session})
	}
	now := j.Time.UTC()
	way := agent.ViaBranch
	if fp.Diff {
		way = agent.ViaDiff
	}
	from := m.Upsert(lineage.Replica{Key: agent.SessionKey{Agent: in.Module.Spec().ID, Session: fp.Session}, Endpoint: cloudEndpoint(fp.Cloud, in.Session.Account), Location: fp.Cloud, Time: now, URL: fp.URL, Branch: fp.CloudBranch})
	to := m.Upsert(lineage.Replica{Endpoint: in.Machine.Facts.Endpoint, Binding: tin.BindingID(), Key: o.Key, Location: machine, AgentVersion: tin.Version, Time: now})
	source, _ := m.LatestState(from)
	m.Deliver(to, w.Cursor, w.Projection, source.Heads, append(append(append([]string(nil), source.Loss...), fp.Loss...), conversionLoss(r.Report)...))
	var rollover *lineage.Rollover
	if rolled {
		_, oldID, ok := m.FindEndpoint(old.Key, in.Machine.Facts.Endpoint)
		if ok {
			rollover = &lineage.Rollover{Replica: oldID, Cursor: fp.originalHead}
		}
	}
	if err := m.AppendHop(lineage.Hop{Rollover: rollover, ID: j.ID, Time: now, From: from, To: to, Kind: lineage.HopFetch, Fidelity: string(fp.Fidelity),
		Written: &lineage.Range{From: w.From, To: w.To}, Code: &lineage.CodeHop{Way: way, Remote: fp.Repo, Branch: codeBranch(fp, branch), Base: base}}); err != nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if err := j.WriteReceipt(host.LocalFS(), machine, lineage.PathFor(o.Path), m.Encode(), true); err != nil {
		return err
	}
	pl := agent.Placement{Key: o.Key, SourceID: o.Key.Session, CWD: o.CWD, Location: machine, Name: o.Title}
	if rolled {
		if pi, ok := tm.(agent.PostInstaller); ok {
			if err := pi.AfterInstall(ctx, h, tin, o.Key, pl); err != nil {
				res.Warnings = append(res.Warnings, "after writing: "+err.Error())
			}
		}
	}
	resume := tm.Resume(tin, o.Key, pl, agent.ResumeOptions{})
	ad := &Adopted{Time: now, Outcome: FetchComplete, Key: o.Key, Path: o.Path, Restored: fp.Messages, Branch: branch, Resume: resume,
		Command: launch.Shell(resume, "", launch.DefaultShell()), Written: true, Appended: !rolled, Fidelity: string(fp.Fidelity), Changes: fp.Changes, Warnings: res.Warnings}
	pf := &Fetch{Journal: j.ID, Time: now, Machine: machine, Agent: in.Module.Spec().ID, Cloud: fp.Cloud, CloudTitle: fp.CloudTitle,
		Session: fp.Session, URL: fp.URL, Title: p.Title, Repo: fp.Repo, Checkout: fp.Checkout, Worktree: fp.Worktree, Base: base,
		CloudBranch: fp.CloudBranch, Lineage: m, Adopted: ad, Loss: fp.Loss, Noun: fp.Noun, Original: &old, OriginalHead: fp.originalHead}
	if in.Continue != nil {
		pf.Continue, pf.ContinueName = fp.ContinueIn, fp.ContinueName
	}
	if err := SaveFetch(env.StateDir, pf); err != nil {
		return err
	}
	res.Fetch.Outcome, res.Fetch.Branch, res.Fetch.Key = FetchComplete, branch, o.Key.String()
	res.Command, res.Run = ad.Command, ad.Resume
	env.Audit.Write(audit.Entry{Action: "cloud.fetch", Session: p.Key.String(), Detail: map[string]any{"cloud": fp.Cloud, "worktree": fp.Worktree,
		"branch": branch, "journal": j.ID, "appended": true}})
	env.Audit.Write(audit.Entry{Action: "cloud.write", Host: machine, Session: o.Key.String(), Detail: map[string]any{"from": string(fp.Session),
		"cloud": fp.Cloud, "path": o.Path, "messages": fp.Messages, "fidelity": string(fp.Fidelity), "journal": j.ID, "appended": true}})
	return nil
}

func cloudEndpoint(cloud, account string) string { return "cloud:" + cloud + ":" + account }
func prepareCloudGraph(ctx context.Context, p *Plan, nodes *[]ir.Node) error {
	in, fp := p.fetchIn, p.Fetch
	if err := in.Machine.CommitIdentity(ctx); err != nil {
		return err
	}
	m := in.Lineage.Clone()
	if m == nil {
		m = lineage.NewNative(cloudEndpoint(fp.Cloud, in.Session.Account), agent.SessionKey{Agent: in.Module.Spec().ID, Session: fp.Session})
	}
	id := cloudReplica(m, agent.SessionKey{Agent: in.Module.Spec().ID, Session: fp.Session}, fp.Cloud, in.Session.Account)
	seg := ir.Segment{Nodes: *nodes}
	ir.Chain(seg.Nodes, "")
	if len(seg.Nodes) > 0 {
		seg.Cursor.Head = seg.Nodes[len(seg.Nodes)-1].ID
	}
	if err := seedCloudPrefix(m, id, &seg, in.Prompt); err != nil {
		return err
	}
	if _, err := m.Observe(id, &seg); err != nil {
		return err
	}
	in.Lineage = m
	*nodes = seg.Nodes
	return nil
}
