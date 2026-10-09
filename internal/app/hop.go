package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Hops: a cloud session handed on to another cloud. No vendor's cloud hands a session to
// another vendor's, so the hop is a composition with this machine as the waypoint: the
// session comes here first (a fetch, as Bring here does), stays here as a native local copy,
// and then goes on as a hand-off with a briefing rendered from that copy. One journal (kind
// hop) names the legs' journals as its parts, so undo takes both back, the hand-off first;
// the lineage beside the copy here records both hops.

// HopTargets are the clouds a cloud session could be handed on to, each enabled or with its
// reason (never hidden).
func (a *App) HopTargets(inv *Inventory, e Entry) []HandoffTarget {
	if !e.Location.IsCloud() {
		return nil
	}
	fromMod, fromCloud, ok := a.cloudModule(e.Location.Name)
	why := ""
	switch _, fetches := fromMod.(agent.CloudFetcher); {
	case !ok:
		why = e.Location.Name + " is not enabled"
	case !fetches:
		why = "hopsesh cannot bring " + fromCloud.Title + " sessions here yet"
	case !bringsSession(fromCloud):
		why = fromCloud.Title + " brings back only code, so there is no session to hand on: bring its code here, then hand a session off"
	}
	repo := ""
	if e.Cloud != nil {
		repo = e.Cloud.Repo
	}
	host, _, _ := strings.Cut(repo, "/")
	var out []HandoffTarget
	for _, r := range a.clouds() {
		cl := r.cloud
		if cl.Name == e.Location.Name {
			continue
		}
		t := HandoffTarget{Cloud: cl.Name, Title: cl.Title, Agent: r.mod.Spec().Name, Limits: cl.Limits}
		c := inv.Cloud(cl.Name)
		_, sends := r.mod.(agent.CloudSender)
		switch {
		case why != "":
			t.Why = why
		case !sends:
			t.Why = "hopsesh does not reach " + cl.Title + " yet"
		case c == nil || !c.Allowed:
			t.Why = "turned off. Turn it on in Machines."
		case c.Status == CloudCLIMissing, c.Status == CloudNotEligible:
			t.Why = nonEmpty(c.Hint, c.Error)
		case c.Status == CloudSignedOut:
			t.Why = trimSentinel(c.Error)
		case c.Status == CloudError:
			t.Why = nonEmpty(c.Error, "it could not be reached")
		case repo != "" && !containsStr(cl.Hosts, host):
			t.Why = "this repository isn't on " + hostsWords(cl.Hosts)
		default:
			t.OK = true
			t.Note = fmt.Sprintf("Comes here from %s first, then gets a briefing and the code on a branch", fromCloud.Title)
		}
		out = append(out, t)
	}
	return out
}

// bringsSession reports whether a cloud's sessions come here as a session (a native copy,
// their messages as text, or a task's title and outcome), not as code only.
func bringsSession(cl agent.Cloud) bool {
	return cl.Down == agent.FidNative || cl.Down == agent.FidText || cl.Down == agent.FidCode && cl.Summary
}

// PlanHop works out handing a cloud session on to another cloud through this machine: the
// bring-back (into via, or the cloud's own agent: "" picks as Bring here does) and the
// hand-off, as far as it can be planned before the session is here. opt holds the hand-off's
// choices (the environment, the mark, the clean-up); opt.TargetDir chooses the repository's
// checkout here. Nothing changes.
func (a *App) PlanHop(ctx context.Context, inv *Inventory, e Entry, to string, via agent.ID, opt move.Options) (*move.Plan, error) {
	if !e.Location.IsCloud() {
		return nil, fmt.Errorf("%s is not in a cloud; hand it off with: hopsesh handoff %s --to %s", e.Session.Key, e.Session.Key, to)
	}
	if to == e.Location.Name {
		return nil, fmt.Errorf("%s is already in %s", e.Session.Key, to)
	}
	fromMod, fromCloud, ok := a.cloudModule(e.Location.Name)
	if !ok {
		return nil, fmt.Errorf("%s is not enabled", e.Location.Name)
	}
	toMod, toCloud, ok := a.cloudModule(to)
	if !ok {
		return nil, fmt.Errorf("unknown cloud %q (see hopsesh clouds)", to)
	}
	here := inv.Local()
	if here == nil || here.host == nil {
		return nil, errors.New("this machine was not scanned")
	}
	bopt := a.DefaultOptions()
	bopt.TargetDir = opt.TargetDir
	bopt.RenameVendor = *a.Cfg.CloudSettings(fromCloud.Name).RenameVendorBranches
	bring, _, err := a.planFetch(ctx, inv, e, via, bopt)
	if err != nil {
		return nil, err
	}
	fp := bring.Fetch
	if !bringsSession(fromCloud) {
		bring.Blockers = append(bring.Blockers, fromCloud.Title+" brings back only code, so there is no session to hand on")
	}

	// The second leg: the cloud, its driver and login, the repository's host, the environment.
	h, in, err := cloudHost(ctx, here.host, toMod, toCloud)
	if err != nil {
		return nil, err
	}
	set := a.Cfg.CloudSettings(to)
	hin := move.HandoffInput{Here: here.host, Module: toMod, Install: in, Host: h, Cloud: toCloud, Allowed: a.Cfg.CloudAllowed(to), Terminal: a.Steps != nil,
		Settings: move.HandoffSettings{Code: set.Code, Untracked: set.Untracked, BranchPrefix: set.BranchPrefix, DeleteBranch: set.DeleteBranch}}
	if t, ok := toMod.(agent.CloudTester); ok {
		hin.Tester = func(ctx context.Context) (agent.CloudTest, error) {
			return a.recentTest(ctx, to, func(ctx context.Context) (agent.CloudTest, error) { return t.TestCloud(ctx, h, in, to) })
		}
	}
	if fp.Repo != "" {
		hin.Folder = repos.HandoffFolder(a.StateDir, fp.Repo)
		if needsEnv(toCloud) {
			hin.Env, hin.Envs = set.Environments[fp.Repo], a.EnvChoices(inv, to, fp.Repo)
		}
	}
	hopt := opt
	hopt.TargetDir, hopt.Bundle, hopt.StartingDiff, hopt.Brief = "", false, false, ""
	then := move.PreviewHandoff(ctx, hin, fp.Repo, hopt)

	p := &move.Plan{Kind: move.KindHop, Key: bring.Key, Title: bring.Title, Agent: bring.Agent, Source: bring.Source,
		Target: move.Endpoint{Location: to, Version: in.Version}, Options: hopt, Mark: then.Mark}
	p.Blockers = append(append(p.Blockers, bring.Blockers...), then.Blockers...)
	p.Warnings = append(append(p.Warnings, bring.Warnings...), then.Warnings...)
	agentName := nonEmpty(fp.Writer, fp.ContinueName)
	if agentName == "" {
		agentName = fromMod.Spec().Name
	}
	hp := &move.HopPlan{From: fromCloud.Name, FromTitle: fromCloud.Title, To: to, ToTitle: toCloud.Title, Via: here.Name, Agent: agentName,
		Fidelity: string(fromCloud.Down) + " → " + string(toCloud.Up), Bring: bring, Then: then.Handoff}
	hp.Conversation = fmt.Sprintf("%s Then %s gets a briefing of it, not the conversation; the copy here keeps everything that came back.",
		fp.Conversation, toCloud.Title)
	hp.Legs = []move.HopLeg{
		{Verb: "Bring here", From: fromCloud.Name, To: here.Name, FromTitle: fromCloud.Title, ToTitle: here.Name, Fidelity: string(fromCloud.Down), Words: fp.Conversation},
		{Verb: "Hand off", From: here.Name, To: to, FromTitle: here.Name, ToTitle: toCloud.Title, Fidelity: string(toCloud.Up), Words: fmt.Sprintf("%s gets a briefing and the code on a branch.", toCloud.Title)},
	}
	switch {
	case fp.Diff:
		hp.Code = fmt.Sprintf("The %s's patch is committed here on %s, then goes up on a new hopsesh/handoff/… branch for %s.", fp.Noun, nonEmpty(fp.LocalBranch, "a hopsesh/from/… branch"), toCloud.Title)
	case fp.CloudBranch != "":
		hp.Code = fmt.Sprintf("%s starts from %s as %s pushed it, when the copy here is still exactly that; otherwise from a new hopsesh/handoff/… branch.", toCloud.Title, fp.CloudBranch, fromCloud.Title)
	case fromCloud.VendorPrefix != "":
		hp.Code = fmt.Sprintf("%s starts from the %s… branch %s pushed, as it is on the remote, when the copy here is still exactly that; otherwise from a new hopsesh/handoff/… branch.",
			toCloud.Title, fromCloud.VendorPrefix, fromCloud.Title)
	default:
		hp.Code = fmt.Sprintf("%s starts from the branch %s brings here (as it is on the remote, or on a new hopsesh/handoff/… branch).", toCloud.Title, fromMod.Spec().Name)
	}
	if fp.Terminal && fp.Command != "" {
		hp.Terminal = fmt.Sprintf("The first leg runs %s in your terminal.", fp.Command)
		if fp.Note != "" {
			hp.Terminal += " " + fp.Note + "; hopsesh then hands the copy on."
		}
	}
	hp.Loss = append(append(hp.Loss, fp.Loss...), then.Handoff.Loss...)
	hp.Loss = append(hp.Loss, fmt.Sprintf("Both cloud sessions stay where they are: %s and %s", fromCloud.Title, toCloud.Title))
	p.Hop = hp
	if p.Mark == "" {
		p.Mark = move.MarkOff
	}
	return p, nil
}

// Hop is the record of a hop, kept in the state folder: its legs' journals, the hand-off's
// choices, and where it stands (a hop whose first leg waits for the user's terminal goes on
// with ContinueHop).
type Hop struct {
	Journal string          `json:"journal"`
	Time    time.Time       `json:"time"`
	Title   string          `json:"title"`
	From    string          `json:"from"`
	Session agent.SessionID `json:"session"`
	To      string          `json:"to"`
	ToTitle string          `json:"toTitle"`
	Fetch   string          `json:"fetch,omitempty"`
	Handoff string          `json:"handoff,omitempty"`
	Options move.Options    `json:"options"`
	Result  move.HopResult  `json:"result"`
}

func hopFile(stateDir, id string) string {
	return filepath.Join(stateDir, "hops", filepath.Base(id)+".json")
}

// LoadHop reads a hop's record by its journal.
func (a *App) LoadHop(id string) (*Hop, error) {
	var h Hop
	if err := readJSON(hopFile(a.StateDir, id), &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// Hops lists the hops, newest first.
func (a *App) Hops() []*Hop {
	files, _ := filepath.Glob(filepath.Join(a.StateDir, "hops", "*.json"))
	var out []*Hop
	for _, f := range files {
		var h Hop
		if readJSON(f, &h) == nil {
			out = append(out, &h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Journal > out[j].Journal })
	return out
}

func (a *App) saveHop(h *Hop) error { return writeJSON(hopFile(a.StateDir, h.Journal), h) }

// applyHop carries out a hop: the bring-back, then (once the copy is here) the hand-off.
// When the first leg waits for the user's terminal and this front end cannot run it here
// (RunHere nil), the hop is left waiting: ContinueHop goes on once the copy is here.
func (a *App) applyHop(ctx context.Context, p *move.Plan, progress func(string)) (*move.Result, error) {
	hp := p.Hop
	if hp == nil || hp.Bring == nil {
		return nil, errors.New("not a hop")
	}
	hj, err := journal.New(a.StateDir, journal.KindHop, fmt.Sprintf("%s from %s to %s, through %s", p.Title, hp.FromTitle, hp.ToTitle, hp.Via))
	if err != nil {
		return nil, err
	}
	hj.AddKey(p.Key)
	rec := &Hop{Journal: hj.ID, Time: time.Now().UTC(), Title: p.Title, From: hp.From, Session: p.Key.Session, To: hp.To, ToTitle: hp.ToTitle, Options: p.Options,
		Result: move.HopResult{From: hp.From, To: hp.To, ToTitle: hp.ToTitle}}
	res := &move.Result{Journal: hj.ID, Mark: move.MarkOff, Hop: &rec.Result}
	say := func(s string) {
		if progress != nil {
			progress(s)
		}
	}
	say(fmt.Sprintf("Bringing it here from %s", hp.FromTitle))
	r1, err := a.Apply(ctx, hp.Bring, move.Input{}, progress)
	if r1 != nil && r1.Journal != "" {
		rec.Fetch, rec.Result.Fetch = r1.Journal, r1.Journal
		a.addPart(hj, r1.Journal)
		res.Fetch = r1.Fetch
	}
	if err != nil {
		rec.Result.State, rec.Result.Message = move.HopFailed, fmt.Sprintf("Nothing went to %s: bringing it here from %s failed: %v", hp.ToTitle, hp.FromTitle, err)
		_ = a.saveHop(rec)
		return res, errors.New(rec.Result.Message)
	}
	if r1.Fetch != nil && r1.Fetch.Outcome == move.FetchWaiting {
		fp := hp.Bring.Fetch
		rec.Result.Command, rec.Result.Run, rec.Result.Note = r1.Command, fp.Run, fp.Note
		if a.RunHere == nil {
			rec.Result.State = move.HopWaiting
			rec.Result.Message = fmt.Sprintf("Waiting for %s to bring the copy here; then it goes on to %s.", hp.Bring.Agent, hp.ToTitle)
			if fp.Note != "" {
				rec.Result.Message += " " + fp.Note + "."
			}
			_ = a.saveHop(rec)
			return res, nil
		}
		say(fmt.Sprintf("Running %s here", strings.Join(fp.Run.Argv, " ")))
		runErr := a.RunHere(ctx, fp.Run)
		if _, err := a.Adopt(ctx, r1.Journal, true); err != nil && runErr == nil {
			runErr = err
		}
		if f, err := move.LoadFetch(a.StateDir, r1.Journal); err == nil && f.Waiting() {
			rec.Result.State = move.HopFailed
			rec.Result.Message = fmt.Sprintf("%s brought no copy here, so nothing went to %s.", hp.Bring.Agent, hp.ToTitle)
			if fp.Note != "" {
				rec.Result.Message += " " + fp.Note + "."
			}
			if runErr != nil {
				rec.Result.Message += fmt.Sprintf(" (%v)", runErr)
			}
			rec.Result.Message += " Undo removes the worktree: hopsesh undo " + hj.ID
			_ = a.saveHop(rec)
			return res, errors.New(rec.Result.Message)
		}
	}
	return a.continueHop(ctx, rec, hj, res, progress)
}

// RunHere runs a driver's command in this front end's own terminal and waits for it (the
// teleport a hop's first leg needs); nil: the front end opens it itself, and the hop waits.
type RunHere func(ctx context.Context, run agent.Command) error

// ContinueHop takes a waiting hop on once the copy is here: it adopts the copy if it has
// appeared, then hands it off. A hop still waiting is returned as it is.
func (a *App) ContinueHop(ctx context.Context, id string, progress func(string)) (*move.Result, error) {
	finished, err := a.beginRuntimeAction()
	if err != nil {
		return nil, err
	}
	defer finished()
	rec, err := a.LoadHop(id)
	if err != nil {
		return nil, fmt.Errorf("no hop %s: %w", id, err)
	}
	hj, err := journal.Load(a.StateDir, rec.Journal)
	if err != nil {
		return nil, err
	}
	res := &move.Result{Journal: hj.ID, Mark: move.MarkOff, Hop: &rec.Result}
	if hj.Undone {
		return res, errors.New("the hop was undone")
	}
	if rec.Result.State != move.HopWaiting {
		return res, nil
	}
	if _, err := a.Adopt(ctx, rec.Fetch, false); err != nil {
		return res, err
	}
	return a.continueHop(ctx, rec, hj, res, progress)
}

// continueHop is the second leg: the copy here, found by a scan of this machine, handed off
// with the hop's choices.
func (a *App) continueHop(ctx context.Context, rec *Hop, hj *journal.Journal, res *move.Result, progress func(string)) (*move.Result, error) {
	fail := func(msg string) (*move.Result, error) {
		rec.Result.State, rec.Result.Message = move.HopFailed, msg+" Undo takes back what came here: hopsesh undo "+hj.ID
		_ = a.saveHop(rec)
		return res, errors.New(rec.Result.Message)
	}
	f, err := move.LoadFetch(a.StateDir, rec.Fetch)
	if err != nil {
		return fail("hopsesh lost the bring-back's record: " + err.Error() + ".")
	}
	if f.Waiting() {
		rec.Result.State = move.HopWaiting
		_ = a.saveHop(rec)
		return res, nil
	}
	b := a.Brought(f)
	res.Fetch = &move.FetchResult{Outcome: b.Outcome, Worktree: b.Worktree, Branch: b.Branch, Key: b.Key}
	if !move.HasCopy(b.Outcome) || b.Key == "" {
		return fail(fmt.Sprintf("Nothing came here to hand on to %s: %s", rec.ToTitle, b.Message))
	}
	if b.Outcome == move.FetchPartial {
		res.Warnings = append(res.Warnings, b.Message+" The briefing covers what came.")
	}
	key, err := agent.ParseKey(b.Key)
	if err != nil {
		return fail(err.Error())
	}
	rec.Result.Key = b.Key
	if progress != nil {
		progress(fmt.Sprintf("Handing it on to %s", rec.ToTitle))
	}
	// This machine for the copy, and the next cloud for what its listing names (the
	// environments' names).
	inv := a.Scan(ctx, ScanOptions{Hosts: []string{LocalName(), rec.To}, NoCache: true, GitFor: func(e Entry) bool { return e.Session.Key.Agent == key.Agent && e.Session.Key.Session == key.Session }})
	defer inv.Close()
	var e *Entry
	for i := range inv.Entries {
		if x := inv.Entries[i]; (x.Session.Key == key || key.Profile == "" && x.Profile != nil && x.Profile.Default && x.Session.Key.Agent == key.Agent && x.Session.Key.Session == key.Session) && x.Machine == LocalName() {
			e = &inv.Entries[i]
		}
	}
	if e == nil {
		return fail(fmt.Sprintf("The copy %s is here but not listed yet, so nothing went to %s; hand it off with: hopsesh handoff %s --to %s.", b.Key, rec.ToTitle, b.Key, rec.To))
	}
	p2, err := a.PlanHandoff(ctx, inv, *e, rec.To, rec.Options)
	if err != nil {
		return fail(fmt.Sprintf("The session is here (%s), but it could not go on to %s: %v.", b.Key, rec.ToTitle, err))
	}
	if len(p2.Blockers) > 0 {
		return fail(fmt.Sprintf("The session is here (%s), but it can't go on to %s: %s.", b.Key, rec.ToTitle, strings.Join(p2.Blockers, "; ")))
	}
	r2, err := a.Apply(ctx, p2, move.Input{}, progress)
	if r2 != nil {
		if r2.Journal != "" {
			rec.Handoff, rec.Result.Handoff = r2.Journal, r2.Journal
			a.addPart(hj, r2.Journal)
		}
		res.Handoff, res.Mark, res.MarkError = r2.Handoff, r2.Mark, r2.MarkError
		res.Warnings = append(res.Warnings, r2.Warnings...)
	}
	if err != nil {
		rec.Result.State, rec.Result.Message = move.HopFailed, fmt.Sprintf("The session is here (%s), but handing it on failed: %v. Undo takes both legs back: hopsesh undo %s", b.Key, err, hj.ID)
		_ = a.saveHop(rec)
		return res, errors.New(rec.Result.Message)
	}
	rec.Result.Remembered = a.RememberEnv(p2)
	rec.Result.State = move.HopDone
	rec.Result.Message = fmt.Sprintf("Brought here from %s and handed on to %s", f.CloudTitle, rec.ToTitle)
	if err := a.saveHop(rec); err != nil {
		res.Warnings = append(res.Warnings, "could not keep the hop's record: "+err.Error())
	}
	a.Audit.Write(audit.Entry{Action: "cloud.hop", Session: b.Key, Detail: map[string]any{"from": rec.From, "to": rec.To, "journal": hj.ID,
		"fetch": rec.Fetch, "handoff": rec.Handoff}})
	return res, nil
}

// addPart records a leg's journal under the hop's, and the hop on the leg's.
func (a *App) addPart(hj *journal.Journal, id string) {
	_ = hj.AddPart(id)
	if leg, err := journal.Load(a.StateDir, id); err == nil {
		_ = leg.SetPartOf(hj.ID)
		for _, k := range leg.Keys {
			hj.AddKey(k)
		}
	}
}

// undoHop undoes a hop's legs, the hand-off first. Unless force, it refuses before undoing
// anything when a leg changed since (that later work would be lost).
func (a *App) undoHop(ctx context.Context, hj *journal.Journal, reach journal.Reach, force bool) error {
	var legs []*journal.Journal
	for i := len(hj.Parts) - 1; i >= 0; i-- {
		leg, err := journal.Load(a.StateDir, hj.Parts[i])
		if err != nil {
			return fmt.Errorf("the hop's leg %s: %w", hj.Parts[i], err)
		}
		if !leg.Undone {
			legs = append(legs, leg)
		}
	}
	if !force {
		for i, leg := range legs { // the hand-off first: the bring-back is checked besides what it wrote
			if err := leg.ChangedBesides(ctx, reach, legs[:i]); err != nil {
				return err
			}
		}
	}
	var manual []journal.Manual
	var kept, problems []string
	for i, leg := range legs {
		if err := leg.UndoAfter(ctx, reach, force, legs[:i]); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", leg.Title, err))
		}
		manual, kept = append(manual, leg.Manual...), append(kept, leg.Kept...)
	}
	if err := hj.Undo(ctx, reach, force); err != nil {
		problems = append(problems, err.Error())
	}
	hj.Manual, hj.Kept = manual, kept
	if len(problems) > 0 {
		hj.Undone = false
	}
	if err := hj.Save(); err != nil {
		problems = append(problems, err.Error())
	}
	if rec, err := a.LoadHop(hj.ID); err == nil && rec.Result.State == move.HopWaiting {
		rec.Result.State, rec.Result.Message = move.HopFailed, "Undone before the copy came here."
		_ = a.saveHop(rec)
	}
	if len(problems) > 0 {
		return fmt.Errorf("undo was incomplete:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

// readJSON and writeJSON keep a record in the state folder.
func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(p string, v any) error {
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
