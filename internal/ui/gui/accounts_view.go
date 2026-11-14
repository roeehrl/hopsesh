package gui

import (
	"context"
	"hash/fnv"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// AccountsViewDTO is the Accounts screen: the accounts you use, who each tool is signed
// in as on every machine, and the folders sessions are kept in.
type AccountsViewDTO struct {
	People   []PersonDTO      `json:"people"`
	Machines []ToolRowDTO     `json:"machines"`
	Folders  []FolderDTO      `json:"folders"`
	Notes    []NoteDTO        `json:"notes"`
	Move     []app.FolderMove `json:"move"`
	Profiles []AccountDTO     `json:"profiles"`
}

// PersonDTO is one account: an email, or one only seen in sessions' records.
type PersonDTO struct {
	ID       string   `json:"id"`
	Email    string   `json:"email,omitempty"`
	Initials string   `json:"initials"`
	Hue      int      `json:"hue"`
	Labels   []string `json:"labels"`
	Where    []string `json:"where"` // "This Mac: Claude desktop, Codex (terminal and app)"
	Sessions int      `json:"sessions"`
	Machines int      `json:"machines"`
	RanOn    []string `json:"ranOn"` // the machines its sessions are on ("This machine" first)
	// Unknown: no tool hopsesh checks is signed in to it; Agent is whose account it is.
	// Number tells unknown accounts of one agent apart (1, 2 ...); it is also its initials.
	Unknown bool     `json:"unknown,omitempty"`
	Agent   agent.ID `json:"agent,omitempty"`
	Number  int      `json:"number,omitempty"`
}

// ToolRowDTO is one machine's tools and who each is signed in as.
type ToolRowDTO struct {
	Machine     string    `json:"machine"`
	Local       bool      `json:"local"`
	OS          string    `json:"os,omitempty"`
	Presence    string    `json:"presence,omitempty"`
	PresenceWhy string    `json:"presenceWhy,omitempty"`
	Hopsesh     string    `json:"hopsesh,omitempty"`
	Checked     time.Time `json:"checked,omitempty"`
	Scanned     bool      `json:"scanned"`
	Status      string    `json:"status,omitempty"`
	Cells       []CellDTO `json:"cells"`
}

// ToolDTO is a column of the grid: an agent's command-line tool or its desktop app.
type ToolDTO struct {
	ID   string `json:"id"` // CellDTO.Tool: "claude-terminal", "claude-desktop", "codex-app"
	Name string `json:"name"`
}

// accountTools are the grid's columns for one agent: its terminal, then its desktop app.
func accountTools(s agent.Spec) []ToolDTO {
	out := []ToolDTO{{ID: string(s.ID) + "-terminal", Name: s.Name + " · terminal"}}
	if d := s.Desktop; d != nil {
		f := strings.Fields(d.Tool)
		word := "app"
		if len(f) > 1 {
			word = strings.ToLower(f[len(f)-1])
		}
		out = append(out, ToolDTO{ID: string(s.ID) + "-" + word, Name: d.Tool})
	}
	return out
}

// accountModules are the enabled agents the Accounts screen shows: those on machines that
// say who they are signed in as.
func accountModules(core *app.App) []agent.Module {
	var out []agent.Module
	for _, m := range core.Modules() {
		if m.Spec().MainRoot().Name != "" && agent.Has(m, agent.CapAccount) {
			out = append(out, m)
		}
	}
	return out
}

// CellDTO is a tool on a machine: its version, then who it is signed in as in each folder.
type CellDTO struct {
	Tool      string      `json:"tool"`
	Installed bool        `json:"installed"`
	Version   string      `json:"version,omitempty"` // "Claude desktop 2.31226.1, running its own Claude Code 2.1.295"
	Note      string      `json:"note,omitempty"`
	SignIns   []SignInDTO `json:"signIns"`
}

// SignInDTO is who a tool is signed in as in one folder.
type SignInDTO struct {
	Person  string `json:"person,omitempty"` // PersonDTO.ID ("" unknown)
	Email   string `json:"email,omitempty"`
	Folder  string `json:"folder"` // "default" or the folder's name
	Path    string `json:"path"`
	Profile string `json:"profile,omitempty"`
	Status  string `json:"status"` // signed-in, signed-out, unknown, failed
	Detail  string `json:"detail,omitempty"`
}

// FolderDTO is a folder a tool keeps sessions in, on one machine.
type FolderDTO struct {
	Machine    string     `json:"machine"`
	Local      bool       `json:"local"`
	Profile    string     `json:"profile"`
	Generation int        `json:"generation"`
	Agent      agent.ID   `json:"agent"`
	AgentName  string     `json:"agentName"`
	Name       string     `json:"name"` // "default" or its own
	Path       string     `json:"path"`
	Default    bool       `json:"default"`
	Tools      string     `json:"tools"`
	Command    string     `json:"command,omitempty"`
	Port       int        `json:"port,omitempty"` // its agent's background service's own port (OpenCode); 0 none
	Counts     []CountDTO `json:"counts"`
	Unrecorded int        `json:"unrecorded"`
	Error      string     `json:"error,omitempty"`
	SignedIn   bool       `json:"signedIn"`
	CanRun     bool       `json:"canRun"` // a new session can start in it (here, or through hopsesh there)
	Tags       []string   `json:"tags"`
}

// CountDTO is how many sessions of a folder an account ran.
type CountDTO struct {
	Person string `json:"person"`
	N      int    `json:"n"`
}

// NoteDTO is something to know about a machine's sign-ins.
type NoteDTO struct {
	Machine string `json:"machine"`
	Title   string `json:"title"`
	Text    string `json:"text"`
}

// AccountsView builds the Accounts screen from the last scan and the account registry.
func (a *App) AccountsView() (*AccountsViewDTO, error) {
	profiles, err := a.Accounts()
	if err != nil {
		return nil, err
	}
	core := a.snapshot()
	moves, _ := core.FoldersToMove()
	a.mu.Lock()
	inv := a.inv
	a.mu.Unlock()
	out := &AccountsViewDTO{People: []PersonDTO{}, Machines: []ToolRowDTO{}, Folders: []FolderDTO{}, Notes: []NoteDTO{}, Move: moves, Profiles: profiles}
	if out.Move == nil {
		out.Move = []app.FolderMove{}
	}
	v := newPeople()
	mods := accountModules(core)
	// Plans are named for every agent: a disabled one's accounts still read "Claude Max".
	for _, mod := range core.Reg.All() {
		v.plans[mod.Spec().ID] = mod.Spec().Plans
	}
	// Accounts with an email: whoever any tool on any machine is signed in as.
	for _, p := range profiles {
		if acct := p.Account; acct != nil && acct.LoggedIn && acct.Email != "" {
			v.signedIn(p.Agent, acct)
		}
	}
	if inv == nil {
		inv = &app.Inventory{}
	}
	// The registry is newer than the last scan for what the user edits (names, tags).
	registry := map[string]agent.RuntimeProfile{}
	for _, p := range profiles {
		registry[p.ID] = p.RuntimeProfile
	}
	current := func(p *agent.RuntimeProfile) *agent.RuntimeProfile {
		if r, ok := registry[p.ID]; ok {
			q := *p
			q.Name, q.Tags, q.Generation, q.Command = r.Name, r.Tags, r.Generation, r.Command
			if r.CheckedAt.After(p.CheckedAt) {
				q.Account, q.Error, q.IdentitySource, q.CheckedAt = r.Account, r.Error, r.IdentitySource, r.CheckedAt
			}
			return &q
		}
		return p
	}
	for _, m := range inv.Machines {
		for _, st := range m.Agents {
			if p := st.Install.Profile; p != nil && p.Account != nil && p.Account.LoggedIn && p.Account.Email != "" {
				v.signedIn(st.Agent, p.Account)
			}
		}
	}
	home, _ := os.UserHomeDir()
	short := func(m *app.Machine, p string) string {
		h := home
		if m != nil && m.Host() != nil && m.Host().Facts.Home != "" {
			h = m.Host().Facts.Home
		}
		homes := []string{h}
		if m == nil || m.Local {
			if real, err := filepath.EvalSymlinks(h); err == nil && real != h {
				homes = append(homes, real)
			}
		}
		for _, h := range homes {
			if h != "" && (p == h || strings.HasPrefix(p, h+"/")) {
				return "~" + strings.TrimPrefix(p, h)
			}
		}
		return p
	}
	// Sessions: who ran them, counted per account and per folder.
	type folderKey struct{ machine, profile string }
	counts := map[folderKey]map[string]int{}
	unrecorded := map[folderKey]int{}
	shown := map[agent.ID]bool{}
	for _, mod := range mods {
		shown[mod.Spec().ID] = true
	}
	for _, e := range inv.Entries {
		if !shown[e.Agent] {
			continue
		}
		fk := folderKey{e.Machine, e.Session.Key.Profile}
		runs := e.Session.Owners
		if len(runs) == 0 {
			unrecorded[fk]++
			continue
		}
		seen := map[string]bool{}
		for _, r := range runs {
			id := v.ran(e.Agent, r)
			if seen[id] {
				continue
			}
			seen[id] = true
			v.counted(id, e.Machine)
			if counts[fk] == nil {
				counts[fk] = map[string]int{}
			}
			counts[fk][id]++
		}
	}
	machines := slices.Clone(inv.Machines)
	slices.SortStableFunc(machines, func(x, y *app.Machine) int {
		if x.Local != y.Local {
			if x.Local {
				return -1
			}
			return 1
		}
		return strings.Compare(x.Name, y.Name)
	})
	for _, m := range machines {
		if m.Local {
			v.local = m.Name
		}
		row := ToolRowDTO{Machine: m.Name, Local: m.Local, OS: m.OS, Presence: m.Presence, PresenceWhy: m.PresenceWhy, Hopsesh: m.Hopsesh, Checked: m.CheckedAt, Scanned: true, Status: m.Status}
		installs := map[agent.ID][]agent.Install{}
		for _, st := range m.Agents {
			installs[st.Agent] = append(installs[st.Agent], st.Install)
		}
		signIn := func(id agent.ID, in agent.Install) SignInDTO {
			s := SignInDTO{Folder: "default", Path: short(m, in.Main()), Status: "unknown", Profile: in.ProfileID()}
			if in.Profile == nil {
				return s
			}
			p := current(in.Profile)
			if !p.Default {
				s.Folder = p.Name
			}
			s.Path = short(m, p.Root)
			switch {
			case p.Account != nil && p.Account.LoggedIn:
				s.Status, s.Email = "signed-in", p.Account.Email
				s.Person = v.ofAccount(id, p.Account)
			case p.Error != "":
				s.Status, s.Detail = map[bool]string{true: "unknown", false: "failed"}[p.IdentitySource == "ssh"], p.Error
			case p.Account != nil:
				s.Status = "signed-out"
			}
			return s
		}
		base := func(ins []agent.Install) (agent.Install, bool) {
			for _, in := range ins {
				if in.Profile == nil || in.Profile.Default {
					return in, true
				}
			}
			if len(ins) > 0 {
				return ins[0], true
			}
			return agent.Install{}, false
		}
		type appCell struct {
			installed bool
			person    string // who the app is signed in as, when it has its own sign-in
		}
		apps := map[agent.ID]appCell{}
		type terminal struct {
			name string
			cell CellDTO
		}
		var terminals []terminal
		for _, mod := range mods {
			spec := mod.Spec()
			id := spec.ID
			tools := accountTools(spec)
			// The agent in the terminal: every folder.
			b, has := base(installs[id])
			cell := CellDTO{Tool: tools[0].ID, SignIns: []SignInDTO{}}
			if has && b.Binary != "" {
				cell.Installed = true
				if b.Runtime == agent.RuntimeApp {
					cell.Note = "No " + spec.Name + " CLI; the app's " + spec.Name + " " + b.Version + " is used"
				} else {
					cell.Version = spec.Name + " " + b.Version
				}
				for _, in := range installs[id] {
					cell.SignIns = append(cell.SignIns, signIn(id, in))
				}
			}
			row.Cells = append(row.Cells, cell)
			terminals = append(terminals, terminal{spec.Name, cell})
			d := spec.Desktop
			if d == nil {
				continue
			}
			desk := CellDTO{Tool: tools[1].ID, SignIns: []SignInDTO{}}
			ac := appCell{}
			if has && (strings.HasSuffix(b.Desktop, ".app") || strings.HasSuffix(strings.ToLower(b.Desktop), ".exe")) {
				desk.Installed, ac.installed = true, true
				if d.OwnSignIn {
					desk.Version = d.Product + " " + b.DesktopVersion
					// Its own sign-in, always in the default folder.
					if b.DesktopEngine != "" {
						desk.Version += ", running its own " + spec.Name + " " + b.DesktopEngine
					}
					s := SignInDTO{Folder: "default", Path: defaultRoot(spec, m), Status: "unknown"}
					if da := b.DesktopAccount; da != nil {
						s.Status = "signed-in"
						s.Person = v.ofRun(id, *da)
						s.Email = v.email(s.Person)
						ac.person = s.Person
						v.where(s.Person, m, d.Tool)
					} else if m.Presence != app.PresenceLogin && !m.Local {
						s.Detail = "its sign-in is seen by hopsesh running on " + m.Name
					} else {
						s.Detail = d.Product + " hasn't said who is signed in: open it once, signed in"
					}
					desk.SignIns = append(desk.SignIns, s)
					// Two accounts writing to one folder: the terminal's and the app's.
					for _, t := range cell.SignIns {
						if t.Folder == "default" && t.Person != "" && ac.person != "" && t.Person != ac.person {
							out.Notes = append(out.Notes, NoteDTO{Machine: m.Name, Title: machineWords(m) + ": two accounts write to " + s.Path,
								Text: "The terminal is " + nonEmpty(t.Email, "one account") + " and " + d.Product + " is " + nonEmpty(v.email(ac.person), "another") + ", and both keep sessions in " + s.Path + ". Every session shows the account that ran it; opening one continues it as the account of the tool you choose."})
						}
					}
				} else {
					// The terminal's sign-in in the default folder, shared.
					desk.Version = d.Product + " " + shortVersion(b.DesktopVersion)
					if b.DesktopEngine != "" {
						desk.Version += ", running " + spec.Name + " " + b.DesktopEngine
					}
					s := signIn(id, b)
					desk.SignIns = append(desk.SignIns, s)
					desk.Note = "same sign-in as the terminal"
					if s.Person != "" {
						v.where(s.Person, m, d.Tool)
					}
				}
			}
			apps[id] = ac
			row.Cells = append(row.Cells, desk)
		}
		// Where each account is used: the apps above, then the terminals.
		for _, t := range terminals {
			for _, s := range t.cell.SignIns {
				if s.Person != "" {
					tool := t.name + " in the terminal"
					if s.Folder != "default" {
						tool += " in the " + s.Folder + " folder"
					}
					v.where(s.Person, m, tool)
				}
			}
		}
		out.Machines = append(out.Machines, row)
		// Folders.
		canRun := m.Local || m.Presence == app.PresenceLogin
		for _, mod := range mods {
			spec := mod.Spec()
			for _, in := range installs[spec.ID] {
				if in.Profile == nil {
					continue
				}
				p := current(in.Profile)
				f := FolderDTO{Machine: m.Name, Local: m.Local, Profile: p.ID, Generation: p.Generation, Agent: in.Agent, AgentName: spec.Name,
					Name: "default", Path: short(m, p.Root), Default: p.Default, Command: p.Command, Port: p.Port, Counts: []CountDTO{}, Error: p.Error, CanRun: canRun && in.Present, Tags: p.Tags,
					SignedIn: p.Account != nil && p.Account.LoggedIn}
				if f.Tags == nil {
					f.Tags = []string{}
				}
				if !p.Default {
					f.Name = p.Name
				}
				who := ""
				if p.Account != nil && p.Account.LoggedIn {
					who = nonEmpty(p.Account.Email, "signed in")
				}
				d, ac := spec.Desktop, apps[spec.ID]
				switch {
				case d != nil && d.OwnSignIn && p.Default && ac.installed:
					f.Tools = "Terminal (" + nonEmpty(who, "not signed in") + ") and " + d.Product + " (" + nonEmpty(v.email(ac.person), "its own sign-in") + ")"
				case d != nil && !d.OwnSignIn && p.Default && ac.installed:
					f.Tools = "Terminal and " + d.Product + " share one sign-in (" + nonEmpty(who, "not signed in") + ")"
				default:
					f.Tools = "Terminal only (" + nonEmpty(who, "not signed in") + ")"
				}
				fk := folderKey{m.Name, p.ID}
				for id, n := range counts[fk] {
					f.Counts = append(f.Counts, CountDTO{Person: id, N: n})
				}
				slices.SortFunc(f.Counts, func(x, y CountDTO) int { return y.N - x.N })
				f.Unrecorded = unrecorded[fk]
				out.Folders = append(out.Folders, f)
			}
		}
	}
	out.People = v.list()
	return out, nil
}

// defaultRoot is where the agent keeps sessions when nothing says otherwise ("~/.claude"),
// as people see it on m.
func defaultRoot(s agent.Spec, m *app.Machine) string {
	r := s.MainRoot()
	os := m.OS
	if m.Host() != nil && m.Host().Facts.OS != "" {
		os = m.Host().Facts.OS
	}
	return r.DefaultFor(os)
}

func machineWords(m *app.Machine) string {
	if m.Local {
		return "This machine"
	}
	return m.Name
}

// shortVersion is a desktop app's version for people: 26.1007.30912 → 26.1007.
func shortVersion(v string) string {
	if f := strings.Split(v, "."); len(f) == 3 && len(f[2]) >= 4 {
		return f[0] + "." + f[1]
	}
	return v
}

// people gathers the accounts the screen shows: by email when known, else by the
// fingerprints sessions recorded.
type people struct {
	byID    map[string]*PersonDTO
	order   []string
	keys    map[string]string // agent \x00 key \x00 person → id (and with person "")
	persons map[string]string // agent \x00 person → id
	tools   map[string]map[string][]string
	ranOn   map[string]map[string]bool
	local   string              // this machine's name
	plans   map[agent.ID]string // agent.Spec.Plans
}

func newPeople() *people {
	return &people{byID: map[string]*PersonDTO{}, keys: map[string]string{}, persons: map[string]string{}, tools: map[string]map[string][]string{}, ranOn: map[string]map[string]bool{}, plans: map[agent.ID]string{}}
}

func (v *people) add(id, email string, unknown bool, a agent.ID) *PersonDTO {
	if p := v.byID[id]; p != nil {
		return p
	}
	h := fnv.New32a()
	h.Write([]byte(id))
	p := &PersonDTO{ID: id, Email: email, Hue: int(h.Sum32() % 360), Labels: []string{}, Where: []string{}, Unknown: unknown, Agent: a}
	p.Initials = initials(email)
	if unknown {
		p.Initials = "?"
	}
	v.byID[id] = p
	v.order = append(v.order, id)
	return p
}

// signedIn records an account a tool is signed in as.
func (v *people) signedIn(a agent.ID, acct *agent.Account) string {
	id := "email:" + strings.ToLower(acct.Email)
	p := v.add(id, acct.Email, false, "")
	if l := planLabel(v.plans[a], acct.Label); l != "" && !slices.Contains(p.Labels, l) {
		p.Labels = append(p.Labels, l)
	}
	if acct.Key != "" {
		v.keys[string(a)+"\x00"+acct.Key+"\x00"+acct.Person] = id
		if _, ok := v.keys[string(a)+"\x00"+acct.Key+"\x00"]; !ok || acct.Person == "" {
			v.keys[string(a)+"\x00"+acct.Key+"\x00"] = id
		}
	}
	if acct.Person != "" {
		v.persons[string(a)+"\x00"+acct.Person] = id
	}
	return id
}

func (v *people) ofAccount(a agent.ID, acct *agent.Account) string {
	if acct.Email != "" {
		return v.signedIn(a, acct)
	}
	return v.ofRun(a, agent.OwnerRun{Key: acct.Key, Person: acct.Person})
}

// ofRun is the account a recorded run (or desktop's sign-in) was under: by its user, then
// by its account; an unknown one is an account of its own.
func (v *people) ofRun(a agent.ID, r agent.OwnerRun) string {
	if r.Person != "" {
		if id, ok := v.persons[string(a)+"\x00"+r.Person]; ok {
			return id
		}
	}
	if id, ok := v.keys[string(a)+"\x00"+r.Key+"\x00"+r.Person]; ok && r.Key != "" {
		return id
	}
	if id, ok := v.keys[string(a)+"\x00"+r.Key+"\x00"]; ok && r.Key != "" && r.Person == "" {
		return id
	}
	id := "unknown:" + string(a) + ":" + r.Key + ":" + r.Person
	if r.Person != "" {
		id = "unknown:" + string(a) + ":person:" + r.Person
	}
	v.add(id, "", true, a)
	return id
}

func (v *people) ran(a agent.ID, r agent.OwnerRun) string { return v.ofRun(a, r) }

func (v *people) counted(id, machine string) {
	if p := v.byID[id]; p != nil {
		p.Sessions++
		if v.ranOn[id] == nil {
			v.ranOn[id] = map[string]bool{}
		}
		v.ranOn[id][machine] = true
	}
}

func (v *people) where(id string, m *app.Machine, tool string) {
	if v.tools[id] == nil {
		v.tools[id] = map[string][]string{}
	}
	if !slices.Contains(v.tools[id][m.Name], tool) {
		v.tools[id][m.Name] = append(v.tools[id][m.Name], tool)
	}
	if m.Local {
		v.tools[id]["\x00here"] = []string{m.Name}
	}
}

func (v *people) email(id string) string {
	if p := v.byID[id]; p != nil {
		return p.Email
	}
	return ""
}

func (v *people) list() []PersonDTO {
	out := []PersonDTO{}
	for _, id := range v.order {
		p := *v.byID[id]
		here := ""
		if h := v.tools[id]["\x00here"]; len(h) > 0 {
			here = h[0]
		}
		names := []string{}
		for name := range v.tools[id] {
			if name != "\x00here" {
				names = append(names, name)
			}
		}
		slices.SortFunc(names, func(x, y string) int {
			if (x == here) != (y == here) {
				if x == here {
					return -1
				}
				return 1
			}
			return strings.Compare(x, y)
		})
		for _, name := range names {
			label := name
			if name == here {
				label = "This machine"
			}
			p.Where = append(p.Where, label+": "+strings.Join(v.tools[id][name], ", "))
		}
		p.Machines = len(v.ranOn[id])
		p.RanOn = []string{}
		for name := range v.ranOn[id] {
			p.RanOn = append(p.RanOn, name)
		}
		slices.SortFunc(p.RanOn, func(x, y string) int {
			if (x == v.local) != (y == v.local) {
				if x == v.local {
					return -1
				}
				return 1
			}
			return strings.Compare(x, y)
		})
		for i, name := range p.RanOn {
			if name == v.local {
				p.RanOn[i] = "This machine"
			}
		}
		if p.Unknown && p.Sessions == 0 {
			continue
		}
		out = append(out, p)
	}
	slices.SortStableFunc(out, func(x, y PersonDTO) int {
		if x.Unknown != y.Unknown {
			if x.Unknown {
				return 1
			}
			return -1
		}
		return y.Sessions - x.Sessions
	})
	// Accounts seen only in records are told apart by number, most sessions first.
	n := map[agent.ID]int{}
	for i := range out {
		if p := &out[i]; p.Unknown {
			n[p.Agent]++
			p.Number = n[p.Agent]
			p.Initials = strconv.Itoa(p.Number)
		}
	}
	return out
}

// initials are an email's: its name's first letter, and its domain's for a domain of its
// own (roy@conduct.security → RC; me@gmail.com → M).
func initials(email string) string {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" {
		return "?"
	}
	out := strings.ToUpper(local[:1])
	label, _, _ := strings.Cut(domain, ".")
	switch strings.ToLower(label) {
	case "", "gmail", "googlemail", "outlook", "hotmail", "live", "icloud", "me", "mac", "yahoo", "proton", "protonmail", "pm", "fastmail", "hey", "aol", "gmx":
		return out
	}
	return out + strings.ToUpper(label[:1])
}

// planLabel names an account's plan for people ("Claude Max", "ChatGPT Pro"): plans names
// what a bare plan belongs to (agent.Spec.Plans).
func planLabel(plans, label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	if plans != "" && !strings.HasPrefix(strings.ToLower(label), strings.ToLower(plans)) {
		label = plans + " " + label
	}
	f := strings.Fields(label)
	for i, w := range f {
		if len(w) > 0 && w[0] >= 'a' && w[0] <= 'z' {
			f[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(f, " ")
}

// MoveAccountFolders moves account folders out of hopsesh's state folder (after asking).
func (a *App) MoveAccountFolders() ([]app.FolderMove, error) {
	moved, err := a.snapshot().MoveFolders()
	a.emit(AccountEvent, map[string]string{})
	return moved, err
}

// AddAccountFolder makes a new account folder on a machine, ready to sign in.
func (a *App) AddAccountFolder(machine, id, name string, tags []string, command bool) (agent.RuntimeProfile, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return a.snapshot().AddAccount(ctx, machine, agent.ID(id), name, tags, app.AddAccountOptions{Command: command})
}
