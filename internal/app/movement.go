package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

type ReturnCandidate struct {
	Replica      string   `json:"replica"`
	Machine      string   `json:"machine"`
	Agent        agent.ID `json:"agent"`
	AgentName    string   `json:"agentName"`
	Profile      string   `json:"profile"`
	ProfileLabel string   `json:"profileLabel"`
	Title        string   `json:"title,omitempty"`
	Key          string   `json:"key"`
	Status       string   `json:"status"`
	Reason       string   `json:"reason"`
	Local        bool     `json:"local"`
}

type MovementNotice struct {
	ProfileLabel string   `json:"profileLabel,omitempty"`
	Operation    string   `json:"operation"`
	Status       string   `json:"status"`
	Text         string   `json:"text"`
	Machine      string   `json:"machine"`
	Agent        agent.ID `json:"agent"`
	AgentName    string   `json:"agentName"`
	// Cloud is the destination cloud's title, when the session went to a cloud.
	Cloud     string    `json:"cloud,omitempty"`
	Key       string    `json:"key"`
	Profile   string    `json:"profile"`
	CheckedAt time.Time `json:"checkedAt"`
	Delivery  string    `json:"delivery"`
}

type movementObservation struct {
	entry        *Entry
	state        lineage.State
	checked      time.Time
	valid        bool
	agentAnchors map[string]bool
}

// EnrichMovement joins receipt metadata and observes native content in memory. It
// never writes remote files or alters journaled sidecars during a scan.
func (a *App) EnrichMovement(ctx context.Context, inv *Inventory) {
	graphs := map[string]*lineage.Manifest{}
	invalid := map[string]bool{}
	for _, e := range inv.Entries {
		if e.Lineage == nil || e.LineageError != "" {
			continue
		}
		family := e.Lineage.Family
		if graphs[family] == nil {
			graphs[family] = e.Lineage.Clone()
		} else if graphs[family].Merge(e.Lineage) != nil {
			invalid[family] = true
		}
	}
	for family, g := range graphs {
		if invalid[family] {
			continue
		}
		observed := map[lineage.ReplicaID]movementObservation{}
		ids := map[int]lineage.ReplicaID{}
		for i := range inv.Entries {
			e := &inv.Entries[i]
			if e.Lineage == nil || e.Lineage.Family != family || e.LineageError != "" {
				continue
			}
			m := inv.Machine(e.Machine)
			if m == nil || m.host == nil {
				continue
			}
			in, installed := m.InstallProfile(e.Agent, e.Session.Key.Profile)
			branch := g.ForBranch(e.Lineage.Branch)
			r, id, ok := branch.FindBinding(e.Session.Key, m.host.Facts.Endpoint, in.BindingID())
			if !ok {
				r, id, ok = branch.FindEndpoint(e.Session.Key, m.host.Facts.Endpoint)
			}
			if !ok {
				continue
			}
			ids[i] = id
			observation := movementObservation{entry: e}
			mod, enabled := a.Module(e.Agent)
			writer, writable := mod.(agent.Writer)
			portable := writable && writer.Profile(in).PortableAppend
			if !installed || !enabled || in.BindingID() != r.Binding && !portable {
				observed[id] = observation
				continue
			}
			reader, ok := mod.(agent.Reader)
			if !ok {
				continue
			}
			h, err := m.host.For(ctx, mod.Spec(), in, nil)
			if err == nil {
				seg, readErr := a.readMovementSegment(ctx, reader, h, in, *e, m.host.Facts.Endpoint)
				if readErr == nil {
					observation.agentAnchors = map[string]bool{}
					for ni, n := range seg.Nodes {
						if n.Actor == ir.Agent || n.Kind == ir.KindToolCall {
							anchor := fmt.Sprintf("node:%d", ni)
							if n.Native != nil && n.Native.Anchor != "" {
								anchor = n.Native.Anchor
							}
							observation.agentAnchors[anchor] = true
						}
					}
					if in.BindingID() != r.Binding {
						_, observation.state, err = branch.ObserveBinding(lineage.Replica{Key: r.Key, Endpoint: r.Endpoint, Binding: in.BindingID(), Location: r.Location, Line: r.Line}, &seg)
						if err == nil {
							err = g.Merge(branch)
						}
						// Observation is ephemeral; route ancestry remains the committed graph.
					} else {
						observation.state, err = g.Observe(id, &seg)
					}
					observation.valid = err == nil
					observation.checked = e.ObservedAt
				}
			}
			observed[id] = observation
			// Historical bindings name the same native file. They share the fresh
			// observation for presentation, without rewriting their authorship.
			for _, old := range g.Replicas {
				if old.Key == r.Key && old.Endpoint == r.Endpoint && old.Line == r.Line {
					observed[old.ID] = observation
				}
			}
		}
		for i, id := range ids {
			e := &inv.Entries[i]
			e.Returns = nil
			e.Movement = nil
			e.Departure, e.Arrival = nil, nil
			if hop, ok := g.Departed(id); ok {
				e.Departure = a.describeDeparture(g, id, hop, observed)
			} else {
				e.Arrival = a.arrival(g, id)
			}
			seen := map[string]bool{}
			for _, r := range g.ReturnReplicas(id) {
				physical := r.Endpoint + "\x00" + r.Key.String()
				current := g.Replica(id)
				if seen[physical] || r.Endpoint == current.Endpoint && r.Key == current.Key {
					continue
				}
				seen[physical] = true
				if retiredReturn(g, r.ID, observed) {
					continue
				}
				c := ReturnCandidate{Replica: string(r.ID), Machine: r.Location, Agent: r.Key.Agent, AgentName: a.agentName(r.Key.Agent), Profile: r.Key.Profile, Key: r.Key.String(), Status: "verify", Reason: "Verify this destination before returning"}
				m := movementMachine(inv, r)
				if m != nil {
					c.Machine = m.Name
					c.Local = m.Local
				}
				if o, ok := observed[r.ID]; ok {
					c.Machine = o.entry.Machine
					if current := inv.Machine(c.Machine); current != nil {
						c.Local = current.Local
					}
					c.ProfileLabel = profileLabel(o.entry.Profile)
					c.Title = o.entry.Session.Title
					if o.entry.Live.Name != "" {
						c.Title = o.entry.Live.Name
					}
					if o.valid && observed[id].valid {
						s, t := g.Covered(observed[id].state.Heads), g.Covered(o.state.Heads)
						switch {
						case lineage.Subset(s, t) && lineage.Subset(t, s):
							c.Status = "same"
							c.Reason = "Conversation already synchronized"
						case lineage.Subset(s, t):
							c.Status = "behind"
							c.Reason = "Destination contains newer work"
						case lineage.Subset(t, s):
							c.Status = "available"
							c.Reason = "Review and add the missing conversation"
						default:
							c.Status = "diverged"
							c.Reason = "Conversation histories differ; review the messages before returning"
						}
						if o.entry.Live.State == agent.Live && c.Status == "available" {
							c.Status = "live"
							c.Reason = "Exit the original conversation before adding new work; its saved history is preserved"
						}
					}
				} else if m != nil && m.Status == StatusOK && m.host != nil && m.host.Facts.Endpoint == r.Endpoint {
					in, exists := m.InstallProfile(r.Key.Agent, r.Key.Profile)
					if exists && in.BindingID() == r.Binding && completeAgentListing(m, r.Key) {
						c.Status = "missing"
						c.Reason = "Original session was not found; review creating a new session"
					}
				}
				e.Returns = append(e.Returns, c)
			}
			if a.Cfg.MovementNoticesOn() {
				e.Movement = a.movementNotice(g, id, observed)
			}
			if m := inv.Machine(e.Machine); m != nil && m.Local {
				a.retainMovement(e)
				a.cacheMovement(*e)
				if e.Movement != nil && a.MovementNoticeDelivery(e.Agent, e.Session.Key.Profile, string(e.Session.Key.Session), hookNoticeText(e.Movement), MovementNoticeIdentity{Operation: e.Movement.Operation, Status: e.Movement.Status}) {
					e.Movement.Delivery = "supplied-to-hook"
				}
			}
		}
	}
}

func (a *App) agentName(id agent.ID) string {
	if m, ok := a.Module(id); ok {
		return m.Spec().Name
	}
	return string(id)
}
func profileLabel(p *agent.RuntimeProfile) string {
	if p == nil {
		return ""
	}
	if p.Account != nil && p.Account.Email != "" {
		return p.Name + " · " + p.Account.Email
	}
	return p.Name
}

func (a *App) movementNotice(g *lineage.Manifest, id lineage.ReplicaID, observed map[lineage.ReplicaID]movementObservation) *MovementNotice {
	hop, ok := g.Departure(id)
	if !ok {
		return nil
	}
	return a.describeDeparture(g, id, hop, observed)
}

// describeDeparture says where a hop away from replica id went and what is known of the
// work there (prepared, continued, diverged or forked), from lineage and observation.
func (a *App) describeDeparture(g *lineage.Manifest, id lineage.ReplicaID, hop lineage.Hop, observed map[lineage.ReplicaID]movementObservation) *MovementNotice {
	dst := g.Replica(hop.To)
	n := &MovementNotice{Operation: hop.ID, Status: "prepared", Machine: dst.Location, Agent: dst.Key.Agent, AgentName: a.agentName(dst.Key.Agent), Key: dst.Key.String(), Profile: dst.Key.Profile, Delivery: "pending"}
	if _, cl, ok := a.cloudModule(dst.Location); ok {
		n.Cloud = cl.Title
	}
	target := observed[hop.To]
	if target.entry != nil {
		n.Machine = target.entry.Machine
		n.ProfileLabel = profileLabel(target.entry.Profile)
	}
	baseline := g.State(hop.Target)
	continued := target.valid && authoredAfter(g, dst.ID, baseline, target.state, target.agentAnchors)
	if continued {
		n.Status = "continued"
	}
	if hop.Fork {
		n.Status = "forked"
	}
	if target.valid {
		n.CheckedAt = target.checked
	}
	if source := observed[id]; source.valid && target.valid && !hop.Fork {
		s, t := g.Covered(source.state.Heads), g.Covered(target.state.Heads)
		if !lineage.Subset(s, t) && !lineage.Subset(t, s) {
			n.Status = "diverged"
		}
	}
	where := noticeLabel(n.AgentName) + " on " + noticeLabel(n.Machine)
	switch n.Status {
	case "continued":
		n.Text = "This branch continued in " + where + ". This copy may lack later work."
	case "diverged":
		n.Text = "This conversation and the copy in " + where + " contain different work. Compare the histories before moving."
	case "forked":
		n.Text = "A separate fork was prepared in " + where + ". This original branch remains available."
		if continued {
			n.Text = "A separate fork continued in " + where + ". This original branch remains available."
		}
	default:
		n.Text = "Prepared in " + where + ". Work there has not yet been observed."
	}
	if !n.CheckedAt.IsZero() {
		n.Text += " Last checked " + n.CheckedAt.UTC().Format(time.RFC3339) + "."
	}
	return n
}

// Imported projections, title records and delivery bookkeeping do not establish
// new authored work. Native readers expose conversation nodes, not hook UI output.
func authoredAfter(g *lineage.Manifest, id lineage.ReplicaID, before, after lineage.State, agentAnchors map[string]bool) bool {
	old, now := g.Covered(before.Heads), g.Covered(after.Heads)
	for _, r := range g.Revisions {
		if r.Replica == id && now[r.ID] && !old[r.ID] && agentAnchors[r.Anchor] {
			return true
		}
	}
	return false
}
func noticeLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) > 160 {
		s = string([]rune(s)[:min(80, len([]rune(s)))])
	}
	return s
}

type cachedMovement struct {
	Key    agent.SessionKey `json:"key"`
	Path   string           `json:"path"`
	Digest string           `json:"digest"`
	Notice *MovementNotice  `json:"notice"`
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (a *App) movementCachePath(key agent.SessionKey) string {
	return filepath.Join(a.StateDir, "movement", digest([]byte(key.String()))+".json")
}
func (a *App) cacheMovement(e Entry) {
	if e.Lineage == nil || e.Session.Path == "" {
		return
	}
	b, err := host.LocalFS().ReadFile(lineage.PathFor(e.Session.Path), 16<<20)
	if err != nil {
		return
	}
	current, err := lineage.Parse(b)
	if err != nil || digest(current.Encode()) != digest(e.Lineage.Encode()) {
		return
	}
	rec := cachedMovement{Key: e.Session.Key, Path: e.Session.Path, Digest: digest(b), Notice: e.Movement}
	path := a.movementCachePath(e.Session.Key)
	if os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".notice-")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return
	}
	if f.Close() != nil {
		return
	}
	_ = os.Rename(f.Name(), path)
}

func (a *App) SessionMovementNotice(ctx context.Context, id agent.ID, profile, sessionID string) (string, error) {
	return a.SessionMovementNoticeForPath(ctx, id, profile, sessionID, "")
}

// MovementNoticeDetails keeps delivery identity and text in the same validated
// lookup snapshot. Receipt writers must use this source Key and Operation/Status,
// not reread the scan cache, which may be absent, older, or concurrently refreshed.
// Empty Text means no notice; a successfully resolved Key is still returned.
type MovementNoticeDetails struct {
	Key       agent.SessionKey
	Text      string
	Summary   string // where the session went, without the advice (for a block reason)
	Operation string
	Status    string
}

func (a *App) SessionMovementNoticeForPath(ctx context.Context, id agent.ID, profile, sessionID, path string) (string, error) {
	details, err := a.MovementNoticeDetailsForPath(ctx, id, profile, sessionID, path)
	return details.Text, err
}

// MovementNoticeDetailsForPath is deliberately local-only and bounded: no Scan,
// agent process, network, account probe or hook-supplied command is executed.
// Lookup never writes scan evidence: a concurrent refresh may have newer facts.
func (a *App) MovementNoticeDetailsForPath(ctx context.Context, id agent.ID, profile, sessionID, path string) (MovementNoticeDetails, error) {
	var details MovementNoticeDetails
	if !a.Cfg.MovementNoticesOn() || ctx.Err() != nil {
		return details, nil
	}
	if sessionID == "" || strings.ContainsAny(sessionID, "/\\\x00") {
		return details, fmt.Errorf("invalid session id")
	}
	scope, err := a.noticeScope(ctx, id, profile)
	if err != nil {
		return details, err
	}
	key := agent.SessionKey{Agent: id, Profile: scope.ID, Session: agent.SessionID(sessionID)}
	details.Key = key
	var cache cachedMovement
	if b, err := host.LocalFS().ReadFile(a.movementCachePath(key), 16<<10); err == nil {
		if json.Unmarshal(b, &cache) != nil || cache.Key != key {
			cache = cachedMovement{}
		}
	}
	if path == "" {
		path = cache.Path
	}
	if path == "" {
		return details, nil
	}
	side, err := filepath.EvalSymlinks(lineage.PathFor(path))
	if err != nil {
		return details, nil
	}
	rel, err := filepath.Rel(scope.Root, side)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return details, fmt.Errorf("notice transcript is outside the selected agent profile")
	}
	b, err := host.LocalFS().ReadFile(side, 16<<20)
	if err != nil {
		return details, nil
	}
	g, err := lineage.Parse(b)
	if err != nil {
		return details, err
	}
	var replica lineage.ReplicaID
	for _, r := range g.Replicas {
		if r.Key == key && r.Line == g.Branch && r.Binding == scope.Binding && ((scope.Endpoint != "" && r.Endpoint == scope.Endpoint) || (scope.Endpoint == "" && scope.ID == "" && r.Endpoint == LocalName())) {
			if replica != "" {
				return details, nil
			}
			replica = r.ID
		}
	}
	if replica == "" {
		return details, nil
	}
	n := a.movementNotice(g, replica, nil)
	// A verified nil records a return, opt-out or ambiguity learned during scan.
	// Falling back to the older source-only graph would resurrect that notice.
	if cache.Digest == digest(b) {
		n = cache.Notice
	}
	if n == nil {
		return details, nil
	}
	details.Text, details.Operation, details.Status = hookNoticeText(n), n.Operation, n.Status
	details.Summary = strings.TrimSuffix(strings.TrimPrefix(hookNoticeText(n), "Hopsesh status: "), " Check the destination before continuing; continuing here may create separate work.")
	return details, nil
}

// The default root may have been registered since hook installation. Resolve its
// exact endpoint/root pair before constructing any cache key; labels and Default
// flags cannot establish profile identity. This lookup never registers a root.
func (a *App) noticeScope(ctx context.Context, id agent.ID, profile string) (agent.RuntimeProfile, error) {
	var empty agent.RuntimeProfile
	mod, ok := a.Module(id)
	if !ok {
		return empty, fmt.Errorf("unknown agent")
	}
	endpoint, err := localNoticeEndpoint(ctx)
	if err != nil {
		return empty, err
	}
	if profile != "" && endpoint == "" {
		return empty, fmt.Errorf("local endpoint is not initialized")
	}
	profiles, err := a.Accounts()
	if err != nil {
		return empty, err
	}
	root := ""
	if profile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return empty, err
		}
		if len(mod.Spec().Roots) == 0 {
			return empty, fmt.Errorf("agent has no local root")
		}
		r := mod.Spec().Roots[0]
		for _, env := range r.Env {
			if root = os.Getenv(env); root != "" {
				break
			}
		}
		if root == "" {
			root = r.Default[runtime.GOOS]
			if root == "" {
				root = r.Default["*"]
			}
		}
		if strings.HasPrefix(root, "~/") {
			root = filepath.Join(home, root[2:])
		}
		if !filepath.IsAbs(root) {
			return empty, fmt.Errorf("default agent root is not absolute")
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return empty, err
		}
	}
	var selected *agent.RuntimeProfile
	for _, p := range profiles {
		if p.Agent != id || endpoint == "" || p.Endpoint != endpoint {
			continue
		}
		if profile != "" && p.ID == profile || profile == "" && p.Root == root {
			if selected != nil {
				return empty, fmt.Errorf("ambiguous local account profile")
			}
			copy := p
			selected = &copy
		}
	}
	if selected != nil {
		canonical, err := filepath.EvalSymlinks(selected.Root)
		if err != nil || !filepath.IsAbs(selected.Root) || canonical != filepath.Clean(selected.Root) {
			return empty, fmt.Errorf("registered profile root changed; register it again")
		}
		return *selected, nil
	}
	if profile != "" {
		return empty, fmt.Errorf("unknown local account profile")
	}
	return agent.RuntimeProfile{Agent: id, Endpoint: endpoint, Root: root}, nil
}

// A failed/offline destination refresh cannot erase an earlier verified observation.
// The source receipt digest and operation must still match; return and undo revoke it.
func (a *App) retainMovement(e *Entry) {
	if e.Movement == nil || !e.Movement.CheckedAt.IsZero() {
		return
	}
	var previous cachedMovement
	b, err := host.LocalFS().ReadFile(a.movementCachePath(e.Session.Key), 16<<10)
	if err != nil || json.Unmarshal(b, &previous) != nil || previous.Key != e.Session.Key || previous.Notice == nil || previous.Notice.Operation != e.Movement.Operation {
		return
	}
	side, err := host.LocalFS().ReadFile(lineage.PathFor(e.Session.Path), 16<<20)
	if err != nil || previous.Digest != digest(side) {
		return
	}
	if !previous.Notice.CheckedAt.IsZero() {
		e.Movement = previous.Notice
	}
}

func hookNoticeText(n *MovementNotice) string {
	text := n.Text
	if !n.CheckedAt.IsZero() {
		text = strings.TrimSuffix(text, " Last checked "+n.CheckedAt.UTC().Format(time.RFC3339)+".")
	}
	return "Hopsesh status: " + text + " Check the destination before continuing; continuing here may create separate work."
}

// Retained context-full originals are not the default return destination while
// their verified replacement survives. Edited or running originals stay visible.
func retiredReturn(g *lineage.Manifest, id lineage.ReplicaID, observed map[lineage.ReplicaID]movementObservation) bool {
	old := observed[id]
	if !old.valid || old.entry == nil || old.entry.Live.State != agent.Ended {
		return false
	}
	for _, h := range g.ActiveHops() {
		if h.Rollover != nil && h.Rollover.Replica == id && observed[h.To].valid && old.state.Head == h.Rollover.Cursor.Head {
			return true
		}
	}
	return false
}

func completeAgentListing(m *Machine, key agent.SessionKey) bool {
	for _, s := range m.Agents {
		if s.Agent == key.Agent && s.Install.ProfileID() == key.Profile {
			return s.Error == ""
		}
	}
	return false
}

// Endpoint identity survives renaming a host. An alias is only an unverified
// fallback for offline display, never evidence that the original is missing.
func movementMachine(inv *Inventory, r lineage.Replica) *Machine {
	for _, m := range inv.Machines {
		if m.host != nil && r.Endpoint != "" && m.host.Facts.Endpoint == r.Endpoint {
			return m
		}
	}
	return inv.Machine(r.Location)
}

func localNoticeEndpoint(ctx context.Context) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	m := &host.Machine{Local: true, Name: LocalName(), Facts: host.Facts{Home: home, OS: runtime.GOOS, Env: map[string]string{"HOPSESH_CONFIG_DIR": os.Getenv("HOPSESH_CONFIG_DIR")}}}
	defer m.Close()
	return m.ReadIdentity(ctx)
}
