package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"os"
	"slices"
	"time"
)

func (a *App) CatalogScope() string {
	env := map[string]string{}
	for _, s := range a.Specs() {
		for _, r := range s.Roots {
			for _, k := range r.Env {
				env[k] = os.Getenv(k)
			}
		}
	}
	profiles, _ := a.Accounts()
	bindings := []any{}
	for _, p := range profiles {
		bindings = append(bindings, []any{p.ID, p.Endpoint, p.Agent, p.Root, p.Generation, p.Default, p.Name, p.Tags})
	}
	b, _ := json.Marshal([]any{a.Cfg.Hosts, a.Cfg.Agents, a.Cfg.Clouds, bindings, LocalName(), env, os.Getenv("HOME"), os.Getenv("USERPROFILE")})
	return fmt.Sprintf("inventory-1:%x", sha256.Sum256(b))
}
func (a *App) CachedInventory() *Inventory {
	saved := catalogSnapshot{}
	if a.Catalog != nil {
		a.Catalog.Read(a.CatalogScope(), &saved)
	}
	inv := &saved.Inventory
	for _, m := range inv.Machines {
		m.Phase = "saved"
	}
	for i := range inv.Entries {
		e := &inv.Entries[i]
		e.Cached = true
		e.Live = agent.LiveInfo{State: agent.Unknown}
		e.Returns = nil
		e.Movement = nil
		if e.Cloud != nil {
			c := *e.Cloud
			c.State = agent.CloudUnknown
			e.Cloud = &c
		}
	}
	return inv
}

// MergeDiscovery removes unseen rows only after a successful complete source scan.
func MergeDiscovery(old *Inventory, u ScanUpdate) *Inventory {
	next := &Inventory{Machines: slices.Clone(old.Machines), Clouds: slices.Clone(old.Clouds), Entries: slices.Clone(old.Entries), Adopted: slices.Clone(old.Adopted), Waiting: slices.Clone(old.Waiting), Discovering: true}
	if u.Machine != nil {
		name := u.Machine.Name
		var previous *Machine
		for _, m := range next.Machines {
			if m.Name == name {
				previous = m
			}
		}
		m := *u.Machine
		if previous != nil {
			if m.CheckedAt.IsZero() {
				m.CheckedAt = previous.CheckedAt
			}
			if len(m.Agents) == 0 {
				m.Agents = previous.Agents
			}
		}
		next.Machines = slices.DeleteFunc(next.Machines, func(m *Machine) bool { return m.Name == name })
		next.Machines = append(next.Machines, &m)
		complete := u.Complete && m.Status == StatusOK
		for _, st := range m.Agents {
			complete = complete && st.Error == ""
		}
		if complete {
			next.Entries = slices.DeleteFunc(next.Entries, func(e Entry) bool { return e.Machine == name })
		}
	}
	if u.Clouds != nil {
		for _, c := range u.Clouds {
			next.Clouds = slices.DeleteFunc(next.Clouds, func(v *Cloud) bool { return v.Name == c.Name })
			next.Clouds = append(next.Clouds, c)
			if c.Error == "" && u.Complete {
				next.Entries = slices.DeleteFunc(next.Entries, func(e Entry) bool { return e.Location.IsCloud() && e.Machine == c.Name })
			}
		}
	}
	by := map[string]int{}
	for i, e := range next.Entries {
		by[EntryIdentity(e.Machine, e.Session.Key.String())] = i
	}
	for _, e := range u.Entries {
		k := EntryIdentity(e.Machine, e.Session.Key.String())
		if i, ok := by[k]; ok {
			// Early summaries must not move an established row into a pending
			// repository/family and back again while enrichment is still running.
			// Retained metadata is provisional; every action revalidates it.
			if !u.Complete {
				old := next.Entries[i]
				if e.Git == nil {
					e.Git = old.Git
				}
				if e.Checkout == "" {
					e.Checkout = old.Checkout
				}
				if e.Lineage == nil && e.LineageError == "" {
					e.Lineage = old.Lineage
				}
				e.Cached = true
			}
			next.Entries[i] = e
		} else {
			by[k] = len(next.Entries)
			next.Entries = append(next.Entries, e)
		}
	}
	return next
}
func (a *App) listingContext(ctx context.Context, machine string, in agent.Install, found func(agent.Summary)) context.Context {
	if a.Catalog == nil {
		return ctx
	}
	b, _ := json.Marshal([]any{machine, in.Agent, in.ProfileID(), in.Roots})
	scope := fmt.Sprintf("summaries-1:%x", sha256.Sum256(b))
	return agent.WithListingHooks(ctx, agent.ListingHooks{Load: func(path, sig string) (*agent.Summary, bool) {
		var s *agent.Summary
		ok := a.Catalog.Load(scope, path, sig, &s)
		return s, ok
	}, Save: func(path, sig string, s *agent.Summary) { a.Catalog.Save(scope, path, sig, s) }, Found: found})
}

// FreshSelection bypasses browsing summaries and only reaches the source and destination.
func (a *App) FreshSelection(ctx context.Context, e Entry) (*Inventory, Entry, error) {
	inv := a.Scan(ctx, ScanOptions{Hosts: []string{LocalName(), e.Machine}, NoCache: true, GitFor: a.GitFor(Ref{Machine: e.Machine, Agent: e.Agent, Profile: e.Session.Key.Profile, Query: string(e.Session.Key.Session)})})
	for _, fresh := range inv.Entries {
		if fresh.Machine == e.Machine && fresh.Session.Key == e.Session.Key && !fresh.Cached {
			return inv, fresh, nil
		}
	}
	if e.Location.IsCloud() {
		fresh, err := inv.CloudEntry(a, e.Machine, e.Session.Key.Session)
		if err == nil {
			fresh.Checkout = e.Checkout
			return inv, fresh, nil
		}
	}
	inv.Close()
	return nil, Entry{}, fmt.Errorf("session is no longer available on %s; refresh this machine", e.Machine)
}

type catalogSnapshot struct {
	Inventory
	Sources map[string]time.Time `json:"sourceGenerations"`
}

func (a *App) saveCatalog(inv *Inventory, started time.Time) {
	if a.Catalog == nil {
		return
	}
	a.Catalog.Update(a.CatalogScope(), func(old []byte) any {
		saved := catalogSnapshot{}
		_ = json.Unmarshal(old, &saved)
		if saved.Sources == nil {
			saved.Sources = map[string]time.Time{}
		}
		merged := &saved.Inventory
		for _, m := range inv.Machines {
			if saved.Sources[m.Name].After(started) {
				continue
			}
			merged = MergeDiscovery(merged, ScanUpdate{Machine: m, Entries: machineEntries(inv, m.Name), Complete: !inv.Discovering})
			saved.Sources[m.Name] = started
		}
		for _, c := range inv.Clouds {
			if saved.Sources[c.Name].After(started) {
				continue
			}
			merged = MergeDiscovery(merged, ScanUpdate{Clouds: []*Cloud{c}, Entries: machineEntries(inv, c.Name), Complete: !inv.Discovering})
			saved.Sources[c.Name] = started
		}
		merged.Discovering = false
		merged.Adopted = nil
		merged.Waiting = nil
		saved.Inventory = *merged
		return saved
	})
}

// discoveryLease lets simultaneous GUI, Quick, TUI and CLI browsers follow one
// collector. Explicit action validation bypasses this and rechecks its sources.
func (a *App) discoveryLease(ctx context.Context, o ScanOptions) (*Inventory, func()) {
	if a.Catalog == nil || o.NoCache {
		return nil, func() {}
	}
	binding, _ := json.Marshal([]any{a.CatalogScope(), a.Cfg.Hosts, a.Cfg.Agents, a.Cfg.Clouds, o.Hosts, o.NoLocal, o.SkipGit, o.ForceAccounts, LocalName(), os.Getenv("HOME"), os.Getenv("USERPROFILE"), os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("CODEX_HOME")})
	scope := fmt.Sprintf("collector-1:%x", sha256.Sum256(binding))
	owner := fmt.Sprintf("%d:%d", os.Getpid(), time.Now().UnixNano())
	waited := false
	for !a.Catalog.Claim(scope, owner) {
		waited = true
		select {
		case <-ctx.Done():
			return a.CachedInventory(), func() {}
		case <-time.After(200 * time.Millisecond):
		}
		if o.Progress != nil {
			inv := a.CachedInventory()
			for _, m := range inv.Machines {
				o.Progress(ScanUpdate{Machine: m, Entries: machineEntries(inv, m.Name)})
			}
		}
	}
	if waited {
		a.Catalog.Release(scope, owner)
		inv := a.CachedInventory()
		// An empty catalog can mean the prior collector failed before publishing.
		if len(inv.Machines) > 0 || len(inv.Clouds) > 0 {
			return inv, func() {}
		}
		return a.discoveryLease(ctx, o)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				a.Catalog.Claim(scope, owner)
			}
		}
	}()
	return nil, func() { close(stop); <-done; a.Catalog.Release(scope, owner) }
}
