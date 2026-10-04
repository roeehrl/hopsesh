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
// leaves it ready with no sessions. known are the ids hopsesh recorded for each cloud.
func (a *App) scanClouds(ctx context.Context, lm *host.Machine, refs []cloudRef, known map[string][]agent.SessionID) ([]*Cloud, []Entry) {
	out := make([]*Cloud, len(refs))
	entries := make([][]Entry, len(refs))
	var wg sync.WaitGroup
	for i, r := range refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i], entries[i] = a.scanCloud(ctx, lm, r, known[r.cloud.Name])
		}()
	}
	wg.Wait()
	var all []Entry
	for _, es := range entries {
		all = append(all, es...)
	}
	return out, all
}

func (a *App) scanCloud(ctx context.Context, lm *host.Machine, r cloudRef, known []agent.SessionID) (*Cloud, []Entry) {
	spec, cl := r.mod.Spec(), r.cloud
	c := &Cloud{Name: cl.Name, Title: cl.Title, Agent: spec.ID, AgentName: spec.Name, Driver: cl.Driver, Status: CloudReady}
	_, c.Listable = r.mod.(agent.CloudLister)
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
	h, in, err := moduleHost(ctx, lm, r.mod)
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
	l, err := lister.ListCloud(lctx, h, in, agent.CloudQuery{Cloud: cl.Name, Known: known})
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
		s.Cloud = cl.Name
		s.Key.Agent = spec.ID
		cs := s
		es = append(es, Entry{Location: agent.CloudLocation(cl.Name), Agent: spec.ID, AgentName: spec.Name,
			Session: cloudSummary(cs), Live: agent.LiveInfo{State: agent.Unknown}, Cloud: &cs})
	}
	return c, es
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
	return agent.Summary{Key: s.Key, Title: s.Title, LastActivity: s.Updated, GitBranch: s.Branch}
}

// knownCloudIDs are the cloud copies the lineage of these entries records, by cloud.
func knownCloudIDs(es []Entry, clouds []cloudRef) map[string][]agent.SessionID {
	agents := map[string]agent.ID{}
	for _, r := range clouds {
		agents[r.cloud.Name] = r.mod.Spec().ID
	}
	out := map[string][]agent.SessionID{}
	seen := map[string]bool{}
	for _, e := range es {
		if e.Lineage == nil {
			continue
		}
		for _, r := range e.Lineage.Replicas {
			id, ok := agents[r.Location]
			if !ok || r.Key.Agent != id || seen[r.Location+"\x00"+string(r.Key.Session)] {
				continue
			}
			seen[r.Location+"\x00"+string(r.Key.Session)] = true
			out[r.Location] = append(out[r.Location], r.Key.Session)
		}
	}
	for _, ids := range out {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	}
	return out
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
	h, in, err := moduleHost(ctx, u.a.localMachine(ctx), mod)
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
