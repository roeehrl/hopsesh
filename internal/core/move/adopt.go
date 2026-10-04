package move

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
	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Fetch is a fetch from a cloud, kept in the state folder: waiting for the driver the
// user runs (Claude Code's teleport) to write its copy, then what the adoption found.
type Fetch struct {
	Journal    string          `json:"journal"`
	Time       time.Time       `json:"time"`
	Machine    string          `json:"machine"`
	Agent      agent.ID        `json:"agent"`
	Cloud      string          `json:"cloud"`
	CloudTitle string          `json:"cloudTitle"`
	Session    agent.SessionID `json:"session,omitempty"` // "" until the vendor's picker chose
	URL        string          `json:"url,omitempty"`
	Title      string          `json:"title"`
	// Untitled: hopsesh knew no title for it before the copy came (Title is made up).
	Untitled bool   `json:"untitled,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Checkout string `json:"checkout"`
	Worktree string `json:"worktree"`
	Base     string `json:"base"`
	// CloudBranch is the cloud's branch, when known before the driver ran.
	CloudBranch  string `json:"cloudBranch,omitempty"`
	VendorPrefix string `json:"vendorPrefix,omitempty"`
	Rename       bool   `json:"rename,omitempty"`
	// Before are the vendor's branches here before the driver ran (a branch it makes is the
	// fetch's to undo).
	Before   map[string]string `json:"before,omitempty"`
	Adopt    agent.Adopt       `json:"adopt"`
	Lineage  *lineage.Manifest `json:"lineage,omitempty"`
	Command  string            `json:"command"` // the driver's command for the user's shell
	Run      agent.Command     `json:"run"`
	Continue agent.ID          `json:"continue,omitempty"` // continue in this agent once here
	// ContinueName is that agent's name.
	ContinueName string `json:"continueName,omitempty"`
	// Original is the session the cloud's work is added to (Options.AppendOriginal), as it
	// was when planned.
	Original     *agent.Summary `json:"original,omitempty"`
	OriginalHead ir.Cursor      `json:"originalHead,omitempty"`
	Adopted      *Adopted       `json:"adopted,omitempty"`
	// Kept: the user chose to keep a partial copy.
	Kept bool `json:"kept,omitempty"`
	// Problems are the cloud's known upstream issues by outcome (agent.Cloud.Problems).
	Problems map[string]string `json:"problems,omitempty"`
	// Mirror: the cloud session is a Remote Control mirror of MirrorOf
	// ("machine:agent/id"), when known.
	Mirror   bool   `json:"mirror,omitempty"`
	MirrorOf string `json:"mirrorOf,omitempty"`
}

// Adopted is what a fetch brought, once the driver wrote it.
type Adopted struct {
	Time     time.Time        `json:"time"`
	Outcome  string           `json:"outcome"` // complete | partial | empty
	Key      agent.SessionKey `json:"key"`     // the copy here
	Path     string           `json:"path"`
	Restored int              `json:"restored"`
	Expected int              `json:"expected"`
	Stated   bool             `json:"stated"` // the cloud said how many it sent
	// Branch is the branch the code is on here; Renamed, the cloud's own name for it
	// (before hopsesh renamed it); NoBranch: the driver checked out none.
	Branch   string `json:"branch,omitempty"`
	Renamed  string `json:"renamed,omitempty"`
	NoBranch bool   `json:"noBranch,omitempty"`
	// Appended: the cloud's work was added to the session it was handed off from (Key).
	Appended bool          `json:"appended,omitempty"`
	Resume   agent.Command `json:"resume"`
	Command  string        `json:"command"` // Resume for the user's shell
	Issue    string        `json:"issue,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
}

// Waiting reports whether the fetch still waits for the driver.
func (f *Fetch) Waiting() bool { return f.Adopted == nil }

func fetchDir(stateDir string) string { return filepath.Join(stateDir, "fetches") }

// SaveFetch writes a fetch's record.
func SaveFetch(stateDir string, f *Fetch) error {
	if err := os.MkdirAll(fetchDir(stateDir), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(fetchDir(stateDir), filepath.Base(f.Journal)+".json")
	if err := os.WriteFile(p+".tmp", b, 0o600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

// LoadFetch reads a fetch's record by its journal.
func LoadFetch(stateDir, journalID string) (*Fetch, error) {
	b, err := os.ReadFile(filepath.Join(fetchDir(stateDir), filepath.Base(journalID)+".json"))
	if err != nil {
		return nil, err
	}
	var f Fetch
	return &f, json.Unmarshal(b, &f)
}

// LoadFetches returns every fetch's record, newest first.
func LoadFetches(stateDir string) ([]*Fetch, error) {
	es, err := os.ReadDir(fetchDir(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Fetch
	for _, e := range es {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if f, err := LoadFetch(stateDir, strings.TrimSuffix(e.Name(), ".json")); err == nil {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Journal > out[j].Journal })
	return out, nil
}

// settle is how long a copy must stay unchanged before it is adopted while the driver may
// still be writing it.
var settle = 1500 * time.Millisecond

// ErrUndone means the fetch was undone before its copy appeared.
var ErrUndone = errors.New("the fetch was undone")

// AdoptFetch adopts what the driver wrote for a fetch, once it is there: it journals the
// copy, renames the cloud's own branch (claude/… → hopsesh/from/<cloud>/…), records
// lineage, tells a complete copy from a partial or an empty one, and (when asked) adds
// the cloud's work to the session it was handed off from. It returns nil while the copy
// is not there yet. exited: the driver has ended, so the copy is complete as it is.
func AdoptFetch(ctx context.Context, f *Fetch, side Side, env Env, exited bool) (*Adopted, error) {
	if f.Adopted != nil {
		return f.Adopted, nil
	}
	mod, in := side.Module, side.Install
	adopter, ok := mod.(agent.CloudAdopter)
	if !ok {
		return nil, fmt.Errorf("%w: %s cannot pick up what its driver wrote", agent.ErrUnsupported, mod.Spec().Name)
	}
	j, err := journal.Load(env.StateDir, f.Journal)
	if err != nil {
		return nil, err
	}
	if j.Undone {
		return nil, ErrUndone
	}
	h, err := side.Machine.For(ctx, mod.Spec(), in, nil)
	if err != nil {
		return nil, err
	}
	a, err := adopter.Adopted(ctx, h, in, f.Adopt)
	if errors.Is(err, agent.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	fsys, err := side.Machine.FS(ctx)
	if err != nil {
		return nil, err
	}
	if fi, err := fsys.Stat(a.Session.Path); err != nil {
		return nil, err
	} else if !exited && time.Since(fi.ModTime()) < settle {
		return nil, nil // still being written
	}
	machine := side.Machine.Name
	if err := j.Adopt(fsys, machine, a.Session.Path); err != nil {
		return nil, err
	}
	j.AddKey(a.Session.Key)
	if f.Session == "" {
		f.Session = a.Remote
	}
	if t := a.Session.Title; t != "" && f.Untitled {
		f.Title = t // the copy names it better than its id did
	}
	if f.Session != "" {
		j.AddKey(agent.SessionKey{Agent: f.Agent, Session: f.Session})
	}
	ad := &Adopted{Time: time.Now().UTC(), Key: a.Session.Key, Path: a.Session.Path, Restored: a.Restored, Expected: a.Expected, Stated: a.Stated}
	switch {
	case a.Restored == 0:
		ad.Outcome = FetchEmpty
	case a.Stated && a.Restored < a.Expected:
		ad.Outcome = FetchPartial
	default:
		ad.Outcome = FetchComplete
	}
	ad.Issue = f.Problems[ad.Outcome]
	cloudBranch := adoptBranch(ctx, f, j, ad)
	recordFetchLineage(ctx, f, side, j, a, ad, cloudBranch)
	ad.Resume = mod.Resume(in, ad.Key, agent.Placement{Key: ad.Key, SourceID: ad.Key.Session, CWD: f.Worktree, Location: machine}, agent.ResumeOptions{})
	if f.Original != nil && ad.Outcome != FetchEmpty {
		if err := appendOriginal(ctx, f, side, j, a, ad); err != nil {
			ad.Warnings = append(ad.Warnings, "the cloud's work was not added to the original: "+err.Error())
		}
	}
	ad.Command = launch.Shell(ad.Resume, "", launch.DefaultShell())
	if err := j.Seal(func(string) (host.FS, error) { return fsys, nil }); err != nil {
		ad.Warnings = append(ad.Warnings, "could not record what this changed, for a safe undo: "+err.Error())
	}
	f.Adopted = ad
	if err := SaveFetch(env.StateDir, f); err != nil {
		return ad, err
	}
	env.Audit.Write(audit.Entry{Action: "cloud.adopt", Host: f.Cloud, Session: ad.Key.String(),
		Detail: map[string]any{"from": string(f.Session), "outcome": ad.Outcome, "restored": ad.Restored, "expected": ad.Expected, "journal": f.Journal}})
	return ad, nil
}

// adoptBranch finds the branch the driver checked out in the worktree and renames the
// cloud's own name for it (when asked); a branch the driver made is the fetch's to undo.
// It returns the cloud's name for the branch ("" when there is none).
func adoptBranch(ctx context.Context, f *Fetch, j *journal.Journal, ad *Adopted) string {
	b := repos.CurrentBranch(ctx, f.Worktree)
	if b == "" || b == "HEAD" {
		ad.NoBranch = true
		return f.CloudBranch
	}
	ad.Branch = b
	if f.VendorPrefix == "" || !strings.HasPrefix(b, f.VendorPrefix) {
		return b
	}
	sha, err := repos.Head(ctx, f.Worktree)
	if err != nil {
		return b
	}
	from := "refs/heads/" + b
	created := f.Before[from] == ""
	if !f.Rename {
		if created {
			_ = j.Ref(f.Machine, f.Checkout, from, sha, "")
		}
		return b
	}
	to := repos.FreeBranchName(ctx, f.Checkout, repos.FromBranch(f.Cloud, b, f.VendorPrefix))
	if err := j.RenamedBranch(f.Machine, f.Checkout, from, "refs/heads/"+to, sha, created); err != nil {
		ad.Warnings = append(ad.Warnings, "kept the branch "+b+": "+err.Error())
		return b
	}
	if err := (repos.LocalGit{}).RenameBranch(ctx, f.Checkout, from, "refs/heads/"+to); err != nil {
		ad.Warnings = append(ad.Warnings, "kept the branch "+b+": "+err.Error())
		return b
	}
	ad.Branch, ad.Renamed = to, b
	return b
}

// recordFetchLineage records the hop from the cloud copy to the copy here, beside the copy
// (and beside the original it was handed off from, when that is here).
func recordFetchLineage(ctx context.Context, f *Fetch, side Side, j *journal.Journal, a agent.Adoption, ad *Adopted, cloudBranch string) {
	m := f.Lineage
	if m == nil {
		m = lineage.New(newID())
	}
	now := time.Now().UTC()
	from := m.Upsert(lineage.Replica{Key: agent.SessionKey{Agent: f.Agent, Session: f.Session}, Location: f.Cloud, Time: now, URL: f.URL, Branch: cloudBranch})
	local := lineage.Replica{Key: ad.Key, Location: side.Machine.Name, AgentVersion: nonEmpty(a.Session.AgentVersion, side.Install.Version), Time: now}
	if r, ok := side.Module.(agent.Reader); ok {
		if h, err := side.Machine.For(ctx, side.Module.Spec(), side.Install, nil); err == nil {
			if seg, err := r.Read(ctx, h, side.Install, a.Session, ir.Cursor{}); err == nil {
				local.Head, local.Offset = seg.Cursor.Head, seg.Cursor.Offset
			}
		}
	}
	to := m.Upsert(local)
	m.Hops = append(m.Hops, lineage.Hop{Time: now, From: from, To: to, Kind: lineage.HopFetch, Fidelity: string(agent.FidNative),
		Code: &lineage.CodeHop{Way: agent.ViaBranch, Remote: f.Repo, Branch: cloudBranch, Base: f.Base}})
	f.Lineage = m
	body := m.Encode()
	if err := j.WriteFile(host.LocalFS(), side.Machine.Name, lineage.PathFor(ad.Path), body, 0o600); err != nil {
		ad.Warnings = append(ad.Warnings, "could not record the session's lineage here: "+err.Error())
	}
	if f.Original != nil && f.Original.Path != "" {
		if err := j.WriteFile(host.LocalFS(), side.Machine.Name, lineage.PathFor(f.Original.Path), body, 0o600); err != nil {
			ad.Warnings = append(ad.Warnings, "could not record the lineage beside the original: "+err.Error())
		}
	}
}

// appendOriginal adds the cloud's work, as the brought copy holds it, to the session it
// was handed off from (a delta append, so the original's own turns, and their signed
// reasoning, stay exactly as they were); the original is then what resumes.
func appendOriginal(ctx context.Context, f *Fetch, side Side, j *journal.Journal, a agent.Adoption, ad *Adopted) error {
	reader, okR := side.Module.(agent.Reader)
	writer, okW := side.Module.(agent.Writer)
	if !okR || !okW {
		return fmt.Errorf("%w: %s cannot add to its sessions", agent.ErrUnsupported, side.Module.Spec().Name)
	}
	h, err := side.Machine.For(ctx, side.Module.Spec(), side.Install, j)
	if err != nil {
		return err
	}
	seg, err := reader.Read(ctx, h, side.Install, a.Session, ir.Cursor{})
	if err != nil {
		return err
	}
	// The cloud session began with hopsesh's briefing; the work is what follows it.
	skip := 0
	for i, n := range seg.Nodes {
		if n.Kind == ir.KindMessage && n.Actor == ir.User && strings.HasPrefix(strings.TrimSpace(n.Text), agent.NotePrefix) {
			skip = i + 1
			break
		}
	}
	o := *f.Original
	name := side.Module.Spec().Name
	prof := writer.Profile(side.Install)
	r := convert.Render(convert.Request{Nodes: seg.Nodes, SkipBefore: skip, From: name + " in " + f.CloudTitle, To: name,
		Fidelity: convert.History, Window: prof.Window,
		Briefing: convert.Briefing{SourceID: string(f.Session), SourceLoc: f.Cloud, TargetLoc: side.Machine.Name, When: time.Now(), Branch: ad.Branch}})
	w, err := writer.Write(ctx, h, side.Install, ir.WriteRequest{Mode: ir.WriteAppend, SessionID: string(o.Key.Session), Expect: f.OriginalHead,
		Header: ir.Header{CWD: o.CWD, Title: o.Title, GitBranch: ad.Branch}, Items: r.Items})
	if err != nil {
		return err
	}
	m := f.Lineage
	now := time.Now().UTC()
	from := m.Upsert(lineage.Replica{Key: ad.Key, Location: side.Machine.Name, Time: now})
	to := m.Upsert(lineage.Replica{Key: o.Key, Location: side.Machine.Name, Head: w.Cursor.Head, Offset: w.Cursor.Offset, Time: now})
	m.Hops = append(m.Hops, lineage.Hop{Time: now, From: from, To: to, Kind: lineage.HopContinue, Fidelity: string(convert.History), Written: &lineage.Range{From: w.From, To: w.To}})
	if err := j.WriteFile(host.LocalFS(), side.Machine.Name, lineage.PathFor(o.Path), m.Encode(), 0o600); err != nil {
		return err
	}
	ad.Appended, ad.Key = true, o.Key
	ad.Resume = side.Module.Resume(side.Install, o.Key, agent.Placement{Key: o.Key, SourceID: o.Key.Session, CWD: o.CWD, Location: side.Machine.Name}, agent.ResumeOptions{})
	return nil
}
