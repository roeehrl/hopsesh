package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Cloud statuses.
const (
	CloudReady       = "ready"
	CloudCLIMissing  = "cli-missing"  // the driver is not installed here
	CloudCLIOld      = "cli-old"      // the driver is older than any version the cloud was tested with
	CloudSignedOut   = "signed-out"   // the driver is not signed in with the account the cloud needs
	CloudNotEligible = "not-eligible" // the plan or the organization does not allow cloud sessions
	CloudNotAllowed  = "not-allowed"  // the user has not allowed hopsesh to use it
	CloudError       = "error"
)

// DefaultCloudTimeout is how long a scan waits for one cloud's listing.
const DefaultCloudTimeout = 8 * time.Second

// Cloud is a vendor cloud as a scan found it. Clouds are reached from this machine only:
// the vendor's login lives with the driver here.
type Cloud struct {
	Name      string   `json:"name"`
	Title     string   `json:"title"`
	Agent     agent.ID `json:"agent"`
	AgentName string   `json:"agentName"`
	Driver    string   `json:"driver"`
	Version   string   `json:"version,omitempty"` // the driver's version here
	// Tested: the driver's version is one the cloud was tested with.
	Tested bool   `json:"tested"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Hint   string `json:"hint,omitempty"`
	// Listable: the module can list this cloud's sessions; otherwise Sessions is 0 because
	// nothing was asked.
	Listable bool `json:"listable"`
	Sessions int  `json:"sessions"`
	// Partial: the vendor cannot list everything, so the sessions are the ones hopsesh
	// could find.
	Partial bool `json:"partial,omitempty"`
	// Fetchable: the module can bring this cloud's sessions here.
	Fetchable bool `json:"fetchable"`
	// Mirrors are local sessions the vendor mirrors (Remote Control), among Sessions.
	Mirrors int `json:"mirrors,omitempty"`
	// Allowed: the user allowed hopsesh to use this cloud.
	Allowed bool `json:"allowed"`
	// Noun is what the cloud calls its sessions ("task"); Limits, what hopsesh cannot reach
	// there; NeedsEnv: a hand-off runs in an environment the user picks (EnvHint says how to
	// make one).
	Noun     string   `json:"noun"`
	Limits   []string `json:"limits,omitempty"`
	NeedsEnv bool     `json:"needsEnv,omitempty"`
	EnvHint  string   `json:"envHint,omitempty"`
}

// Cloud returns a scanned cloud by name.
func (inv *Inventory) Cloud(name string) *Cloud {
	for _, c := range inv.Clouds {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// cloudRef is one declared cloud with its module.
type cloudRef struct {
	mod   agent.Module
	cloud agent.Cloud
}

// clouds are the clouds the enabled modules declare, in module order.
func (a *App) clouds() []cloudRef {
	var out []cloudRef
	for _, m := range a.Modules() {
		for _, c := range m.Spec().Clouds {
			out = append(out, cloudRef{m, c})
		}
	}
	return out
}

// scanClouds lists the wanted clouds in parallel, through this machine. A cloud the user
// has not allowed is reported and never run; a module that cannot list its cloud yet
// leaves it ready with no sessions. known is what hopsesh knows of each cloud's sessions;
// local, this machine's sessions (for mirrors).
func (a *App) scanClouds(ctx context.Context, lm *host.Machine, refs []cloudRef, known map[string][]*knownCloud, local []Entry) ([]*Cloud, []Entry) {
	out := make([]*Cloud, len(refs))
	entries := make([][]Entry, len(refs))
	var wg sync.WaitGroup
	for i, r := range refs {
		var mine []agent.Summary
		for _, e := range local {
			if e.Agent == r.mod.Spec().ID {
				mine = append(mine, e.Session)
			}
		}
		if mine == nil {
			mine = []agent.Summary{}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i], entries[i] = a.scanCloud(ctx, lm, r, known[r.cloud.Name], mine)
		}()
	}
	wg.Wait()
	var all []Entry
	for _, es := range entries {
		all = append(all, es...)
	}
	return out, all
}

func (a *App) scanCloud(ctx context.Context, lm *host.Machine, r cloudRef, known []*knownCloud, local []agent.Summary) (*Cloud, []Entry) {
	spec, cl := r.mod.Spec(), r.cloud
	c := &Cloud{Name: cl.Name, Title: cl.Title, Agent: spec.ID, AgentName: spec.Name, Driver: cl.Driver, Status: CloudReady, Allowed: a.Cfg.CloudAllowed(cl.Name),
		Noun: cl.SessionNoun(), Limits: cl.Limits, NeedsEnv: needsEnv(cl), EnvHint: cl.EnvHint}
	_, c.Listable = r.mod.(agent.CloudLister)
	_, c.Fetchable = r.mod.(agent.CloudFetcher)
	if !a.Cfg.CloudAllowed(cl.Name) {
		c.Status, c.Hint = CloudNotAllowed, "hopsesh leaves "+cl.Title+" alone until you allow it"
		return c, nil
	}
	bf := lm.Facts.Binaries[cl.Driver]
	if bf.Path == "" {
		c.Status, c.Hint = CloudCLIMissing, fmt.Sprintf("%s is reached through the `%s` command, which isn't installed here", cl.Title, cl.Driver)
		return c, nil
	}
	c.Version = agent.VersionOf(bf.Version)
	c.Tested = cl.TestedWith(c.Version)
	if c.Version != "" && olderThanTested(c.Version, cl.Tested) {
		c.Status = CloudCLIOld
		c.Hint = fmt.Sprintf("%s %s hasn't been tested with %s; hopsesh tested %s", spec.Name, c.Version, cl.Title, strings.Join(cl.Tested, ", "))
	}
	lister, ok := r.mod.(agent.CloudLister)
	if !ok {
		return c, nil
	}
	h, in, err := cloudHost(ctx, lm, r.mod, cl)
	if err != nil {
		c.Status, c.Error = CloudError, err.Error()
		return c, nil
	}
	timeout := a.CloudTimeout
	if timeout == 0 {
		timeout = DefaultCloudTimeout
	}
	lctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ids := make([]agent.SessionID, len(known))
	byID := map[agent.SessionID]*knownCloud{}
	for i, k := range known {
		ids[i], byID[k.ID] = k.ID, k
	}
	l, err := lister.ListCloud(lctx, h, in, agent.CloudQuery{Cloud: cl.Name, Known: ids, Local: local})
	if err != nil {
		if lctx.Err() != nil && ctx.Err() == nil {
			err = fmt.Errorf("%s did not answer within %s", cl.Title, timeout)
		}
		c.Status, c.Error, c.Hint = cloudStatus(err, spec, cl)
		return c, nil
	}
	c.Partial, c.Sessions = l.Partial, len(l.Sessions)
	if n := len(l.Errors); n > 0 {
		c.Error = fmt.Sprintf("%d session(s) could not be read; the first, %s: %v", n, l.Errors[0].Path, l.Errors[0].Err)
	}
	var es []Entry
	for _, s := range l.Sessions {
		if s.Mirror {
			c.Mirrors++
			continue // a local session: its row shows the mirror (Summary.Mirror)
		}
		s.Cloud = cl.Name
		s.Key.Agent = spec.ID
		if s.Repo == "" && (s.Env != "" || s.EnvLabel != "") && byID[s.Key.Session] == nil {
			// The cloud lists the environment, not the repository: the configuration may say.
			s.Repo = repoForEnv(a.Cfg.CloudSettings(cl.Name).Environments, s)
		}
		es = append(es, cloudEntry(spec, cl, s, byID[s.Key.Session]))
	}
	return c, es
}

// cloudEntry is a listed cloud session as an entry, with what hopsesh knows of it.
func cloudEntry(spec agent.Spec, cl agent.Cloud, s agent.CloudSession, k *knownCloud) Entry {
	e := Entry{Location: agent.CloudLocation(cl.Name), Machine: cl.Name, Agent: spec.ID, AgentName: spec.Name, Live: agent.LiveInfo{State: agent.Unknown}}
	if k != nil {
		s.Title, s.Repo, s.Branch, s.URL = nonEmpty(s.Title, k.Title), nonEmpty(s.Repo, k.Repo), nonEmpty(s.Branch, k.Branch), nonEmpty(s.URL, k.URL)
		if s.Updated.IsZero() {
			s.Updated = k.Time
		}
		e.Lineage, e.Checkout, e.Original = k.Lineage, k.Checkout, k.Original
	}
	if s.Repo != "" {
		e.Git = &repos.GitState{IsRepo: true, Identity: s.Repo, Branch: s.Branch}
		if k != nil {
			e.Git.Remote = k.Remote
		}
	}
	cs := s
	e.Session, e.Cloud = cloudSummary(cs), &cs
	return e
}

// cloudStatus is a listing's failure as a status, in words, with a remedy.
func cloudStatus(err error, spec agent.Spec, cl agent.Cloud) (status, msg, hint string) {
	switch {
	case errors.Is(err, agent.ErrSignedOut):
		return CloudSignedOut, err.Error(), fmt.Sprintf("%s here isn't signed in with the account %s needs; sign in with `%s`, then refresh", spec.Name, cl.Title, cl.Driver)
	case errors.Is(err, agent.ErrNotEligible):
		return CloudNotEligible, err.Error(), fmt.Sprintf("your plan or organization doesn't include %s", cl.Title)
	case errors.Is(err, agent.ErrNotInstalled):
		return CloudCLIMissing, err.Error(), fmt.Sprintf("%s is reached through the `%s` command, which isn't installed here", cl.Title, cl.Driver)
	}
	return CloudError, err.Error(), ""
}

// cloudSummary describes a cloud session the way the front ends show every session.
func cloudSummary(s agent.CloudSession) agent.Summary {
	title := s.Title
	if title == "" {
		title = "Session " + string(s.Key.Session)
	}
	return agent.Summary{Key: s.Key, Title: title, LastActivity: s.Updated, GitBranch: s.Branch}
}

// knownCloud is what hopsesh knows of one cloud session: from the lineage of a session
// here (a copy brought from it, or the session handed off to it), or a link the user
// pasted.
type knownCloud struct {
	ID       agent.SessionID
	URL      string
	Branch   string
	Title    string
	Repo     string
	Remote   string
	Checkout string // the repository's checkout here
	Time     time.Time
	Lineage  *lineage.Manifest
	// Original is the session it was handed off from ("machine:agent/id").
	Original string
}

// knownClouds gathers what the entries' lineage and the pasted links name in each cloud.
func knownClouds(es []Entry, clouds []cloudRef, pasted []Pasted) map[string][]*knownCloud {
	agents := map[string]agent.ID{}
	for _, r := range clouds {
		agents[r.cloud.Name] = r.mod.Spec().ID
	}
	out := map[string][]*knownCloud{}
	at := map[string]*knownCloud{}
	get := func(cloud string, id agent.SessionID) *knownCloud {
		k := at[cloud+"\x00"+string(id)]
		if k == nil {
			k = &knownCloud{ID: id}
			at[cloud+"\x00"+string(id)] = k
			out[cloud] = append(out[cloud], k)
		}
		return k
	}
	for _, e := range es {
		if e.Lineage == nil || e.Location.IsCloud() {
			continue
		}
		for ri, r := range e.Lineage.Replicas {
			id, ok := agents[r.Location]
			if !ok || r.Key.Agent != id {
				continue
			}
			k := get(r.Location, r.Key.Session)
			k.URL, k.Branch = nonEmpty(k.URL, r.URL), nonEmpty(k.Branch, r.Branch)
			k.Title = nonEmpty(k.Title, e.Session.Title)
			if r.Time.After(k.Time) {
				k.Time = r.Time
			}
			if k.Lineage == nil {
				k.Lineage = e.Lineage
			} else {
				k.Lineage.Merge(e.Lineage)
			}
			if g := e.Git; g != nil && g.Identity != "" {
				k.Repo, k.Remote = nonEmpty(k.Repo, g.Identity), nonEmpty(k.Remote, g.Remote)
				if e.Location.Name == LocalName() {
					k.Checkout = nonEmpty(k.Checkout, nonEmpty(g.MainWorktree, g.Toplevel))
				}
			}
			for _, h := range e.Lineage.Hops {
				if h.Kind == lineage.HopHandoff && h.To == ri && h.From >= 0 && h.From < len(e.Lineage.Replicas) && e.Lineage.Replicas[h.From].Key == e.Session.Key {
					k.Original = e.Machine + ":" + e.Session.Key.String()
				}
			}
		}
	}
	for _, p := range pasted {
		if _, ok := agents[p.Cloud]; !ok {
			continue
		}
		k := get(p.Cloud, p.ID)
		k.Repo, k.Checkout, k.Title = nonEmpty(k.Repo, p.Repo), nonEmpty(k.Checkout, p.Checkout), nonEmpty(k.Title, p.Title)
		if p.Time.After(k.Time) {
			k.Time = p.Time
		}
	}
	for _, ks := range out {
		sort.Slice(ks, func(i, j int) bool { return ks[i].ID < ks[j].ID })
	}
	return out
}

// cloudHost is a module's Host on this machine for its cloud capabilities: read-only, and
// running the driver without the variables the cloud must not inherit.
func cloudHost(ctx context.Context, m *host.Machine, mod agent.Module, cl agent.Cloud) (agent.Host, agent.Install, error) {
	h, in, err := moduleHost(ctx, m, mod)
	if err != nil {
		return nil, in, err
	}
	return host.Unsetting(h, cl.Unset), in, nil
}

// moduleHost is a module's confined, read-only Host on a machine, with its install there.
func moduleHost(ctx context.Context, m *host.Machine, mod agent.Module) (agent.Host, agent.Install, error) {
	spec := mod.Spec()
	h, err := m.For(ctx, spec, agent.Install{}, nil)
	if err != nil {
		return nil, agent.Install{}, err
	}
	in, err := mod.Detect(ctx, h)
	if err != nil {
		return nil, agent.Install{}, err
	}
	if in.Agent == "" {
		in.Agent = spec.ID
	}
	h, err = m.For(ctx, spec, in, nil)
	return h, in, err
}

// olderThanTested reports whether a version is below the oldest tested version prefix
// (newer untested versions are only reported as untested).
func olderThanTested(version string, tested []string) bool {
	if len(tested) == 0 {
		return false
	}
	oldest := tested[0]
	for _, t := range tested[1:] {
		if versionBelow(t, oldest) {
			oldest = t
		}
	}
	return versionBelow(version, oldest) && !strings.HasPrefix(version, oldest+".")
}

// versionBelow compares dotted numeric versions, as far as the shorter one goes.
func versionBelow(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, errx := strconv.Atoi(as[i])
		y, erry := strconv.Atoi(bs[i])
		if errx != nil || erry != nil {
			return false
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// undoClouds reaches the clouds for undo through the modules' cloud capabilities.
func (a *App) undoClouds() journal.Clouds { return undoClouds{a} }

type undoClouds struct{ a *App }

func (u undoClouds) module(ctx context.Context, cloud string, key agent.SessionKey) (agent.Module, agent.Host, agent.Install, error) {
	mod, ok := u.a.Module(key.Agent)
	if !ok {
		return nil, nil, agent.Install{}, fmt.Errorf("%w (%s is not in use here)", journal.ErrManual, key.Agent)
	}
	if _, ok := mod.Spec().FindCloud(cloud); !ok {
		return nil, nil, agent.Install{}, fmt.Errorf("%w (%s does not reach %s)", journal.ErrManual, mod.Spec().Name, cloud)
	}
	cl, _ := mod.Spec().FindCloud(cloud)
	h, in, err := cloudHost(ctx, u.a.localMachine(ctx), mod, cl)
	return mod, h, in, err
}

// Updated is when the session last changed, from the cloud's listing (zero when the module
// cannot list the cloud or the listing leaves the session out).
func (u undoClouds) Updated(ctx context.Context, cloud string, key agent.SessionKey) (time.Time, error) {
	mod, h, in, err := u.module(ctx, cloud, key)
	if err != nil {
		return time.Time{}, err
	}
	lister, ok := mod.(agent.CloudLister)
	if !ok {
		return time.Time{}, nil
	}
	l, err := lister.ListCloud(ctx, h, in, agent.CloudQuery{Cloud: cloud, Known: []agent.SessionID{key.Session}})
	if err != nil {
		return time.Time{}, err
	}
	for _, s := range l.Sessions {
		if s.Key.Session == key.Session {
			return s.Updated, nil
		}
	}
	return time.Time{}, nil
}

// Archive archives the session where the module can; otherwise the user does it.
func (u undoClouds) Archive(ctx context.Context, cloud string, key agent.SessionKey) error {
	mod, h, in, err := u.module(ctx, cloud, key)
	if err != nil {
		return err
	}
	arch, ok := mod.(agent.CloudArchiver)
	if !ok {
		return journal.ErrManual
	}
	return arch.Archive(ctx, h, in, key.Session)
}
