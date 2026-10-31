package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/clientapi"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/version"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// ServeClient answers a program on this machine with hopsesh's client API on r and w
// (hopsesh client --stdio, or a client connection to hopsesh serve) until r ends.
func (a *App) ServeClient(ctx context.Context, r io.Reader, w io.Writer) error {
	s := &clientSession{a: a, plans: map[string]*clientPlan{}, watches: map[string]context.CancelFunc{}}
	defer s.close()
	return clientapi.Serve(ctx, r, w, s.handle)
}

// clientSession is one client connection: its plans and watches end with it.
type clientSession struct {
	a       *App
	mu      sync.Mutex
	plans   map[string]*clientPlan
	watches map[string]context.CancelFunc
}

// clientPlan is a plan made on the connection, waiting for apply or cancel. Its inventory
// stays open (its machines' connections) until then.
type clientPlan struct {
	id    string
	inv   *Inventory
	e     Entry
	plan  *move.Plan
	in    move.Input
	push  *Push
	owner *OwnerRecord
	busy  bool
}

func (p *clientPlan) close() {
	if p.push != nil {
		p.push.Close()
	}
	if p.inv != nil {
		p.inv.Close()
	}
}

func (s *clientSession) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, stop := range s.watches {
		stop()
	}
	for _, p := range s.plans {
		p.close()
	}
}

func (s *clientSession) handle(ctx context.Context, c *clientapi.Conn, id json.RawMessage, method string, params json.RawMessage) (any, error) {
	read := func(v any) error {
		if len(params) == 0 {
			return nil
		}
		if err := json.Unmarshal(params, v); err != nil {
			return clientapi.Errorf(clientapi.CodeBadRequest, "%s: %v", method, err)
		}
		return nil
	}
	switch method {
	case clientapi.MethodHello:
		return s.hello(ctx), nil
	case clientapi.MethodCatalog:
		var p clientapi.CatalogParams
		if err := read(&p); err != nil {
			return nil, err
		}
		return s.a.ClientCatalog(ctx, p), nil
	case clientapi.MethodCatalogWatch:
		var p clientapi.WatchParams
		if err := read(&p); err != nil {
			return nil, err
		}
		return s.watch(ctx, c, id, p.CatalogParams), nil
	case clientapi.MethodCatalogUnwatch:
		var p clientapi.Unwatch
		if err := read(&p); err != nil {
			return nil, err
		}
		return struct{}{}, s.unwatch(p.Watch)
	case clientapi.MethodMachines:
		var p clientapi.CatalogParams
		if err := read(&p); err != nil {
			return nil, err
		}
		return s.a.ClientCatalog(ctx, p).Machines, nil
	case clientapi.MethodPlan:
		var p clientapi.PlanParams
		if err := read(&p); err != nil {
			return nil, err
		}
		return s.plan(ctx, p)
	case clientapi.MethodApply:
		var p clientapi.ApplyParams
		if err := read(&p); err != nil {
			return nil, err
		}
		return s.apply(ctx, c, id, p)
	case clientapi.MethodCancel:
		var p clientapi.ApplyParams
		if err := read(&p); err != nil {
			return nil, err
		}
		return struct{}{}, s.cancel(p.Plan)
	case clientapi.MethodUndo:
		var p clientapi.UndoParams
		if err := read(&p); err != nil {
			return nil, err
		}
		return s.undo(ctx, p)
	case clientapi.MethodTerminalOpen:
		var p clientapi.TerminalParams
		if err := read(&p); err != nil {
			return nil, err
		}
		return s.terminal(ctx, p)
	}
	return nil, clientapi.Errorf(clientapi.CodeUnknown, "this hopsesh does not know %q", method)
}

func (s *clientSession) hello(ctx context.Context) clientapi.HelloResult {
	endpoint, _ := s.a.LocalAccountEndpoint(ctx)
	if endpoint == "" {
		// hopsesh never ran here: give this machine its identity now, as its first scan
		// would, so a client can rely on it from hello on.
		m := s.a.localMachine(ctx)
		if err := m.CommitIdentity(ctx); err == nil {
			endpoint, _ = m.ReadIdentity(ctx)
		}
		m.Close()
	}
	features := []string{clientapi.FeatureCatalogWatch, clientapi.FeaturePush, clientapi.FeatureOwners}
	if s.a.Login != nil {
		features = append(features, clientapi.FeatureTerminal)
	}
	return clientapi.HelloResult{API: clientapi.API, MinAPI: clientapi.MinAPI, Version: version.Version, Endpoint: endpoint,
		Machine: LocalName(), OS: runtime.GOOS, Login: s.a.Login != nil, Features: features}
}

// ----- the catalog -----

// ClientCatalog is the client API's catalog: the sessions and machines a scan finds.
func (a *App) ClientCatalog(ctx context.Context, p clientapi.CatalogParams) clientapi.Catalog {
	if p.Cached {
		if inv := a.CachedInventory(); inv != nil {
			return a.catalogOf(inv, false)
		}
	}
	inv := a.Scan(ctx, scanFor(p))
	defer inv.Close()
	return a.catalogOf(inv, true)
}

func scanFor(p clientapi.CatalogParams) ScanOptions {
	o := ScanOptions{SkipGit: true, Hosts: p.Machines}
	if p.Local {
		o.Hosts = []string{LocalName()}
	}
	return o
}

func (a *App) catalogOf(inv *Inventory, complete bool) clientapi.Catalog {
	owners := a.ExternalOwners()
	out := clientapi.Catalog{Sessions: []clientapi.Session{}, Machines: []clientapi.Machine{}, Attention: []clientapi.Attention{}, Complete: complete && !inv.Discovering, At: time.Now().UTC()}
	endpoints := map[string]string{}
	for _, m := range inv.Machines {
		cm := clientMachine(m)
		endpoints[m.Name] = cm.Endpoint
		out.Machines = append(out.Machines, cm)
		if cm.Status != StatusOK && cm.Status != "" {
			out.Attention = append(out.Attention, clientapi.Attention{ID: "machine:" + m.Name, Kind: clientapi.AttentionMachine, Machine: m.Name,
				Title: m.Name, Text: nonEmptyStr(m.Error, m.Status), Fix: m.Hint})
		}
	}
	for _, e := range inv.Entries {
		s := clientSessionOf(e, endpoints[e.Machine], OwnerOf(owners, e))
		out.Sessions = append(out.Sessions, s)
		if s.MovedTo == nil {
			continue
		}
		to := s.MovedTo.Machine
		switch {
		case s.Live.State == string(agent.Live):
			out.Attention = append(out.Attention, clientapi.Attention{ID: clientapi.AttentionOldCopyOpen + ":" + s.Ref, Kind: clientapi.AttentionOldCopyOpen,
				Machine: e.Machine, Session: s.Ref, Title: s.Title,
				Text: "The copy on " + e.Machine + " is open, but this session moved to " + to + ".",
				Fix:  "Close it on " + e.Machine + " and continue on " + to + "."})
		case e.Movement != nil && e.Movement.Status == "diverged":
			out.Attention = append(out.Attention, clientapi.Attention{ID: clientapi.AttentionOldCopyUsed + ":" + s.Ref, Kind: clientapi.AttentionOldCopyUsed,
				Machine: e.Machine, Session: s.Ref, Title: s.Title,
				Text: "The copy on " + e.Machine + " was used after the session moved to " + to + ".",
				Fix:  "Open it in hopsesh to compare the two copies."})
		}
	}
	slices.SortStableFunc(out.Sessions, func(x, y clientapi.Session) int { return y.Activity.Compare(x.Activity) })
	return out
}

func clientMachine(m *Machine) clientapi.Machine {
	cm := clientapi.Machine{Name: m.Name, Local: m.Local, OS: m.OS, Status: m.Status, Problem: m.Error, Fix: m.Hint,
		Hopsesh: m.Hopsesh, Checked: m.CheckedAt}
	if m.host != nil {
		cm.Endpoint = m.host.Facts.Endpoint
	}
	switch m.Presence {
	case PresenceLogin:
		cm.Presence = "login"
	case "":
	default:
		cm.Presence = "ssh"
	}
	if m.Hopsesh == "" && !m.Local {
		cm.Presence = "none"
	}
	for _, s := range m.Agents {
		if s.Error == "" {
			cm.Agents = append(cm.Agents, string(s.Agent))
		}
	}
	return cm
}

// clientRef names a session in the client API: "<machine>:<agent>[@profile]/<session>".
func clientRef(machine string, k agent.SessionKey) string {
	return machine + ":" + k.String()
}

func clientKey(k agent.SessionKey) clientapi.SessionKey {
	return clientapi.SessionKey{Agent: string(k.Agent), Profile: k.Profile, Session: string(k.Session)}
}

func clientSessionOf(e Entry, endpoint string, owner *OwnerRecord) clientapi.Session {
	s := clientapi.Session{Ref: clientRef(e.Machine, e.Session.Key), Key: clientKey(e.Session.Key), Machine: e.Machine,
		Cloud: e.Location.IsCloud(), AgentName: e.AgentName, Title: titleOrPrompt(e.Session), Dir: e.Session.CWD,
		Branch: e.Session.GitBranch, Activity: e.Session.LastActivity, Cached: e.Cached}
	if !s.Cloud {
		s.Endpoint = endpoint
	}
	if e.Git != nil && e.Git.Branch != "" {
		s.Branch = e.Git.Branch
	}
	s.Live = clientapi.Live{State: string(e.Live.State), Status: e.Live.Status, App: e.Live.App,
		RemoteControl: e.Live.RemoteControl, RemoteURL: e.Live.RemoteURL}
	if s.Live.State == "" {
		s.Live.State = string(agent.Unknown)
	}
	for _, p := range e.Live.Procs {
		s.Live.PIDs = append(s.Live.PIDs, p.PID)
	}
	if len(s.Live.PIDs) == 0 && e.Live.PID > 0 {
		s.Live.PIDs = []int{e.Live.PID}
	}
	if g := e.Lineage; g != nil {
		if _, id, ok := g.Find(e.Session.Key, e.Machine); ok {
			s.Replica = string(id)
		}
	}
	if ar := e.Arrival; ar != nil {
		s.Hop = &clientapi.Hop{Operation: ar.Operation, Kind: ar.Kind, Machine: ar.Machine, Agent: string(ar.Agent), At: ar.At}
	}
	if d := e.Departure; d != nil {
		s.MovedTo = &clientapi.Hop{Operation: d.Operation, Kind: "moved", Machine: d.Machine, Agent: string(d.Agent), At: d.At}
		if d.Cloud != "" {
			s.MovedTo.Machine = d.Cloud
		}
	}
	if owner != nil {
		s.Owner = &clientapi.Owner{Name: owner.Owner, PID: owner.PID, Stop: owner.Stop}
	}
	return s
}

func titleOrPrompt(s agent.Summary) string {
	if s.Title != "" {
		return s.Title
	}
	return s.LastPrompt
}

// How often a watch reads again: this machine at most every watchLocalEvery when its
// files change, and every watchLocalPoll regardless (on macOS a directory watch sees new
// files, not writes to existing ones, and agents write their sessions as they work; a
// repeated read is cheap, from the summary cache); the other machines every
// watchRemoteEvery.
const (
	watchLocalEvery  = 2 * time.Second
	watchLocalPoll   = 3 * time.Second
	watchRemoteEvery = time.Minute
)

// watch answers with the catalog as it is now, then sends catalog.changed with the whole
// catalog each time it changes, until unwatched or the connection ends.
func (s *clientSession) watch(ctx context.Context, c *clientapi.Conn, id json.RawMessage, p clientapi.CatalogParams) clientapi.Catalog {
	wctx, stop := context.WithCancel(ctx)
	s.mu.Lock()
	if old, ok := s.watches[string(id)]; ok {
		old()
	}
	s.watches[string(id)] = stop
	s.mu.Unlock()
	cur := s.a.ClientCatalog(wctx, p)
	go s.a.watchCatalog(wctx, cur, p, func(cat clientapi.Catalog) {
		_ = c.Event(id, clientapi.EventCatalogChanged, cat)
	})
	return cur
}

func (s *clientSession) unwatch(id json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stop, ok := s.watches[string(bytes.TrimSpace(id))]
	if !ok {
		return clientapi.Errorf(clientapi.CodeNotFound, "no catalog.watch with that id on this connection")
	}
	stop()
	delete(s.watches, string(bytes.TrimSpace(id)))
	return nil
}

func (a *App) watchCatalog(ctx context.Context, cur clientapi.Catalog, p clientapi.CatalogParams, send func(clientapi.Catalog)) {
	last := catalogSum(cur)
	local := make(chan struct{}, 1)
	go a.WatchSessions(ctx, func() {
		select {
		case local <- struct{}{}:
		default:
		}
	}, WatchOptions{})
	remote := time.NewTicker(watchRemoteEvery)
	defer remote.Stop()
	poll := time.NewTicker(watchLocalPoll)
	defer poll.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-poll.C:
				select {
				case local <- struct{}{}:
				default:
				}
			}
		}
	}()
	lastLocal := time.Now() // a watch's first event comes after its answer
	publish := func(next clientapi.Catalog) {
		cur = next
		if sum := catalogSum(next); !bytes.Equal(sum, last) {
			last = sum
			send(next)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-local:
			if wait := watchLocalEvery - time.Since(lastLocal); wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
			lastLocal = time.Now()
			if p.Local {
				publish(a.ClientCatalog(ctx, p))
				continue
			}
			lp := p
			lp.Local, lp.Cached = true, false
			publish(mergeLocal(cur, a.ClientCatalog(ctx, lp)))
		case <-remote.C:
			if !p.Local {
				fresh := p
				fresh.Cached = false
				publish(a.ClientCatalog(ctx, fresh))
			}
		}
	}
}

// mergeLocal replaces this machine's sessions and machine in cat with those of local.
func mergeLocal(cat, local clientapi.Catalog) clientapi.Catalog {
	here := LocalName()
	out := clientapi.Catalog{Complete: cat.Complete, At: local.At, Attention: []clientapi.Attention{}}
	for _, x := range cat.Attention {
		if x.Machine != here {
			out.Attention = append(out.Attention, x)
		}
	}
	out.Attention = append(out.Attention, local.Attention...)
	for _, s := range cat.Sessions {
		if s.Machine != here {
			out.Sessions = append(out.Sessions, s)
		}
	}
	out.Sessions = append(out.Sessions, local.Sessions...)
	slices.SortStableFunc(out.Sessions, func(x, y clientapi.Session) int { return y.Activity.Compare(x.Activity) })
	for _, m := range cat.Machines {
		if !m.Local {
			out.Machines = append(out.Machines, m)
		}
	}
	for _, m := range local.Machines {
		if m.Local {
			out.Machines = append(out.Machines, m)
		}
	}
	return out
}

// catalogSum is a catalog's content without its time, to tell whether it changed.
func catalogSum(c clientapi.Catalog) []byte {
	c.At = time.Time{}
	for i := range c.Machines {
		c.Machines[i].Checked = time.Time{}
	}
	b, _ := json.Marshal(c)
	return b
}

// ----- moving -----

// findClientRef is the one session a ref names in inv.
func findClientRef(inv *Inventory, ref string) (Entry, error) {
	machine, rest, ok := strings.Cut(ref, ":")
	if !ok || machine == "" {
		return Entry{}, clientapi.Errorf(clientapi.CodeBadRequest, "a session ref is <machine>:<agent>/<session>, from the catalog")
	}
	k, err := agent.ParseKey(rest)
	if err != nil {
		return Entry{}, clientapi.Errorf(clientapi.CodeBadRequest, "session ref %q: %v", ref, err)
	}
	var found []Entry
	for _, e := range inv.Entries {
		if e.Machine == machine && e.Session.Key == k {
			found = append(found, e)
		}
	}
	switch len(found) {
	case 0:
		if m := inv.Machine(machine); m != nil && m.Status != StatusOK {
			e := clientapi.Errorf(clientapi.CodeNotFound, "%s can't be read now: %s", machine, nonEmptyStr(m.Error, m.Status))
			e.Fix = m.Hint
			return Entry{}, e
		}
		return Entry{}, clientapi.Errorf(clientapi.CodeNotFound, "no session %s on %s", k, machine)
	case 1:
		return found[0], nil
	}
	return Entry{}, clientapi.Errorf(clientapi.CodeAmbiguous, "%d copies of %s on %s", len(found), k, machine)
}

func (s *clientSession) plan(ctx context.Context, p clientapi.PlanParams) (*clientapi.Plan, error) {
	machine, _, _ := strings.Cut(p.Session, ":")
	to := p.To
	if to == LocalName() {
		to = ""
	}
	if to != "" && machine != LocalName() {
		return nil, clientapi.Errorf(clientapi.CodeUnsupported, "only a session on this machine can be sent to another; ask hopsesh on %s", machine)
	}
	hosts := []string{machine}
	if machine != LocalName() {
		hosts = append(hosts, LocalName())
	}
	inv := s.a.Scan(ctx, ScanOptions{Hosts: hosts, GitFor: func(e Entry) bool { return clientRef(e.Machine, e.Session.Key) == p.Session }})
	cp := &clientPlan{inv: inv}
	ok := false
	defer func() {
		if !ok {
			cp.close()
		}
	}()
	e, err := findClientRef(inv, p.Session)
	if err != nil {
		return nil, err
	}
	if e.Cached {
		return nil, clientapi.Errorf(clientapi.CodeStale, "%s could not be read just now; try again", machine)
	}
	if s.a.OldCopy(e) {
		e := clientapi.Errorf(clientapi.CodeBlocked, "%s is the copy a move left behind; the session goes on where it moved", titleOrPrompt(e.Session))
		e.Fix = "continue the copy it moved to"
		return nil, e
	}
	cp.e = e
	opts := s.a.DefaultOptions()
	opts.Fork, opts.TargetDir = p.Fork, p.Dir
	if to == "" {
		cp.plan, cp.in, err = s.a.Plan(ctx, inv, e, agent.ID(p.Agent), opts)
	} else {
		h := s.a.Cfg.FindHost(to)
		if h == nil {
			return nil, clientapi.Errorf(clientapi.CodeNotFound, "hopsesh knows no machine %s", to)
		}
		cp.push, err = s.a.StartPush(ctx, inv, e, *h, agent.ID(p.Agent), opts)
		if cp.push != nil {
			cp.plan = cp.push.Plan
		}
	}
	if err != nil {
		return nil, err
	}
	if machine == LocalName() {
		cp.owner = OwnerOf(s.a.ExternalOwners(), e)
	}
	cp.id = rand.Text()[:16]
	s.mu.Lock()
	s.plans[cp.id] = cp
	s.mu.Unlock()
	ok = true
	return clientPlanOf(cp), nil
}

func clientPlanOf(cp *clientPlan) *clientapi.Plan {
	p := cp.plan
	out := &clientapi.Plan{ID: cp.id, Kind: p.Kind, Title: p.Title, Agent: p.Agent, Files: len(p.Files.Files), Bytes: p.Bytes,
		Conflict: p.Conflict, Warnings: p.Warnings, Blockers: p.Blockers, NoWork: p.NoWork,
		Protected: len(p.CannotProtect) == 0 && !p.Options.Fork, Live: p.Live,
		From: clientapi.PlanSide{Machine: p.Source.Location, Key: clientKey(p.Key), Dir: p.Source.CWD, Account: p.Source.ProfileName},
		To:   clientapi.PlanSide{Machine: p.Target.Location, Key: clientKey(p.Placement.Key), Dir: p.Target.CWD, Account: p.Target.ProfileName}}
	if r := p.Repo; r.Action != "" {
		out.Repo = &clientapi.PlanRepo{Action: r.Action, Path: r.LocalPath, Branch: nonEmptyStr(r.LocalBranch, r.SourceBranch), Remote: r.Remote}
	}
	if cp.owner != nil {
		out.Owner = &clientapi.Owner{Name: cp.owner.Owner, PID: cp.owner.PID, Stop: cp.owner.Stop}
	}
	return out
}

// take marks a plan as being applied, so a second apply of it is refused.
func (s *clientSession) take(id string) (*clientPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp, ok := s.plans[id]
	switch {
	case !ok:
		return nil, clientapi.Errorf(clientapi.CodeNotFound, "no plan %s on this connection (plans end with their connection)", id)
	case cp.busy:
		return nil, clientapi.Errorf(clientapi.CodeFailed, "plan %s is being applied already", id)
	}
	cp.busy = true
	return cp, nil
}

func (s *clientSession) drop(id string) {
	s.mu.Lock()
	cp := s.plans[id]
	delete(s.plans, id)
	s.mu.Unlock()
	if cp != nil {
		cp.close()
	}
}

func (s *clientSession) cancel(id string) error {
	if _, err := s.take(id); err != nil {
		return err
	}
	s.drop(id)
	return nil
}

func (s *clientSession) apply(ctx context.Context, c *clientapi.Conn, rid json.RawMessage, p clientapi.ApplyParams) (*clientapi.Receipt, error) {
	cp, err := s.take(p.Plan)
	if err != nil {
		return nil, err
	}
	// A plan is used once: applied, or failed (plan again from what is there now).
	defer s.drop(p.Plan)
	if len(cp.plan.Blockers) > 0 {
		return nil, clientapi.Errorf(clientapi.CodeBlocked, "%s", strings.Join(cp.plan.Blockers, "; "))
	}
	if cp.owner != nil && !p.OwnerStopped {
		e := clientapi.Errorf(clientapi.CodeOwned, "%s runs this session (process %d); it must stop it first", cp.owner.Owner, cp.owner.PID)
		e.Fix = nonEmptyStr(cp.owner.Stop, "stop it in "+cp.owner.Owner)
		return nil, e
	}
	n, title := 0, ""
	step := func(next string) {
		if n > 0 {
			_ = c.Event(rid, clientapi.EventStep, clientapi.Step{Plan: cp.id, N: n, Title: title, State: "done"})
		}
		if next == "done" {
			n, title = 0, ""
			return
		}
		n++
		title = next
		_ = c.Event(rid, clientapi.EventStep, clientapi.Step{Plan: cp.id, N: n, Title: title, State: "running"})
	}
	fail := func(err error, journal string) (*clientapi.Receipt, error) {
		if n > 0 {
			_ = c.Event(rid, clientapi.EventStep, clientapi.Step{Plan: cp.id, N: n, Title: title, State: "failed"})
		}
		code := clientapi.CodeFailed
		if strings.Contains(err.Error(), "changed since planning") {
			code = clientapi.CodeStale
		}
		return nil, &clientapi.Error{Code: code, Message: err.Error(), Journal: journal}
	}
	actx, done := context.WithTimeout(ctx, 30*time.Minute)
	defer done()
	if cp.push != nil {
		step("sending the session to " + cp.plan.Target.Location)
		res, err := cp.push.Commit(actx)
		if err != nil {
			j := ""
			if res != nil {
				j = res.Journal
			}
			return fail(err, j)
		}
		step("done")
		r := receiptOf(cp, res.Result)
		r.Journal, r.Pushed = res.Journal, res.Pushed
		return r, nil
	}
	res, err := s.a.Apply(actx, cp.plan, cp.in, step)
	if err != nil {
		if errors.Is(err, move.ErrBlocked) {
			return nil, clientapi.Errorf(clientapi.CodeBlocked, "%v", err)
		}
		j := ""
		if res != nil {
			j = res.Journal
		}
		return fail(err, j)
	}
	if n > 0 {
		step("done")
	}
	return receiptOf(cp, res), nil
}

func receiptOf(cp *clientPlan, res *move.Result) *clientapi.Receipt {
	p := cp.plan
	key := p.Placement.Key
	if res != nil && res.Key.Session != "" {
		key = res.Key
	}
	r := &clientapi.Receipt{Plan: cp.id, Machine: p.Target.Location, Key: clientKey(key), Ref: clientRef(p.Target.Location, key), Dir: p.Target.CWD}
	if res != nil {
		r.Journal, r.Command, r.Files, r.Bytes, r.Cloned, r.Worktree = res.Journal, res.Command, res.Files, res.Bytes, res.Cloned, res.Worktree
		r.Pushed, r.Stopped, r.Warnings = res.Pushed, res.Stopped, res.Warnings
	}
	return r
}

func (s *clientSession) undo(ctx context.Context, p clientapi.UndoParams) (*clientapi.Undone, error) {
	if p.Journal == "" {
		return nil, clientapi.Errorf(clientapi.CodeBadRequest, "undo needs the journal id an apply returned")
	}
	js, err := s.a.Journals()
	if err != nil {
		return nil, err
	}
	known := false
	for _, j := range js {
		if j.ID == p.Journal {
			known = true
			if j.Undone {
				return nil, clientapi.Errorf(clientapi.CodeNotFound, "%s was undone already", p.Journal)
			}
		}
	}
	if !known {
		return nil, clientapi.Errorf(clientapi.CodeNotFound, "no journal %s on %s", p.Journal, LocalName())
	}
	uctx, done := context.WithTimeout(ctx, 5*time.Minute)
	defer done()
	j, err := s.a.Undo(uctx, p.Journal, p.Force)
	if err != nil {
		return nil, err
	}
	return &clientapi.Undone{Journal: j.ID, Title: j.Title}, nil
}

func (s *clientSession) terminal(ctx context.Context, p clientapi.TerminalParams) (*clientapi.Terminal, error) {
	if s.a.Login == nil {
		e := clientapi.Errorf(clientapi.CodeNoLogin, "hopsesh serve is not running in the login session on %s", LocalName())
		e.Fix = "hopsesh serve install"
		return nil, e
	}
	machine, rest, _ := strings.Cut(p.Session, ":")
	if machine != LocalName() {
		return nil, clientapi.Errorf(clientapi.CodeUnsupported, "terminal.open runs a session on this machine; ask hopsesh on %s", machine)
	}
	k, err := agent.ParseKey(rest)
	if err != nil {
		return nil, clientapi.Errorf(clientapi.CodeBadRequest, "session ref %q: %v", p.Session, err)
	}
	t, err := s.a.OpenTerminal(ctx, terminalResume(k, p))
	if err != nil {
		if strings.HasPrefix(err.Error(), MovedOriginal) {
			return nil, &clientapi.Error{Code: clientapi.CodeBlocked, Message: err.Error(), Fix: "continue the copy it moved to, or ask again with anyway"}
		}
		return nil, err
	}
	return &clientapi.Terminal{ID: t.ID, Title: t.Title, Dir: t.Dir, State: t.State, Started: t.Started}, nil
}

func terminalResume(k agent.SessionKey, p clientapi.TerminalParams) peer.TerminalRequest {
	return peer.TerminalRequest{Kind: peer.TerminalResume, Agent: k.Agent, Profile: k.Profile, Session: string(k.Session),
		Cols: p.Cols, Rows: p.Rows, Anyway: p.Anyway}
}
