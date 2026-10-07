package gui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/appicon"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/presence"
	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The inspector's calls: the end of a session's conversation (Preview, never during a
// scan), renaming a session, where the sessions on this machine are open (Presence, polled
// by the window between scans), and the ⋯ menu's Reveal and Copy resume command.

// PreviewDTO is the end of a session's conversation for the inspector. Text is the agents'
// own words: the window shows it as text only.
type PreviewDTO struct {
	Items []PreviewItemDTO `json:"items"`
	More  bool             `json:"more"`            // earlier messages exist
	First *PreviewItemDTO  `json:"first,omitempty"` // the first real prompt
	// Note says why there is no preview ("Preview not available: laptop is slow to
	// answer"); "" when there is one.
	Note string `json:"note,omitempty"`
}

// PreviewItemDTO is one message, one turn's tool calls in words, or a compaction.
type PreviewItemDTO struct {
	Role string `json:"role"` // user | agent | tools | compacted
	Text string `json:"text"`
	Time string `json:"time,omitempty"` // RFC 3339
}

// previewCache keeps recent previews by session file, size and time (at most 50).
type previewCache struct {
	mu    sync.Mutex
	order []string
	items map[string]PreviewDTO
}

const previewCacheSize = 50

func (c *previewCache) get(k string) (PreviewDTO, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.items[k]
	if ok {
		c.touch(k)
	}
	return d, ok
}

func (c *previewCache) put(k string, d PreviewDTO) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]PreviewDTO{}
	}
	c.items[k] = d
	c.touch(k)
	for len(c.order) > previewCacheSize {
		delete(c.items, c.order[0])
		c.order = c.order[1:]
	}
}

func (c *previewCache) touch(k string) {
	for i, x := range c.order {
		if x == k {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, k)
}

var previews previewCache

// previewTimeout bounds a preview from another machine.
const previewTimeout = 3 * time.Second

// Preview returns the end of a session's conversation: its last n messages (4 when n is
// 0), with a line for each turn's tool calls. A machine that is slow to answer gets a
// note, not an error.
func (a *App) Preview(machine, key string, n int) (*PreviewDTO, error) {
	if n <= 0 {
		n = 4
	}
	n = min(n, 200)
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	inv := a.inv
	on := a.core.Cfg.PreviewsOn()
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !on {
		return &PreviewDTO{Items: []PreviewItemDTO{}, Note: "Conversation previews are off (Settings → General)."}, nil
	}
	local := false
	if m := inv.Machine(machine); m != nil {
		local = m.Local
	}
	// Inventory timestamps describe conversation activity, not the current file version.
	// Only reuse a preview when we can validate its size and mtime. Remote reads remain
	// bounded by the module and timeout until the host API exposes a version token here.
	ck := ""
	if local && !e.Location.IsCloud() {
		if fi, err := os.Stat(filepath.FromSlash(e.Session.Path)); err == nil {
			ck = fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%d", machine, key, e.Session.Path, fi.Size(), fi.ModTime().UnixNano(), n)
		}
	}
	if ck != "" {
		if d, ok := previews.get(ck); ok {
			return &d, nil
		}
	}
	limit := 30 * time.Second
	if !local {
		limit = previewTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	p, err := core.Preview(ctx, inv, e, n)
	switch {
	case errors.Is(err, app.ErrNoPreview):
		return &PreviewDTO{Items: []PreviewItemDTO{}, Note: fmt.Sprintf("%s's conversations can't be previewed yet.", e.AgentName)}, nil
	case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
		return &PreviewDTO{Items: []PreviewItemDTO{}, Note: fmt.Sprintf("Preview not available: %s is slow to answer.", machine)}, nil
	case err != nil:
		return &PreviewDTO{Items: []PreviewItemDTO{}, Note: "Preview not available: " + err.Error()}, nil
	}
	a.mu.Lock()
	on = a.core.Cfg.PreviewsOn()
	a.mu.Unlock()
	if !on {
		return &PreviewDTO{Items: []PreviewItemDTO{}, Note: "Conversation previews are off (Settings → General)."}, nil
	}
	d := previewDTO(p, e.AgentName)
	if ck != "" {
		previews.put(ck, d)
	}
	return &d, nil
}

func previewDTO(p agent.Preview, agentName string) PreviewDTO {
	d := PreviewDTO{Items: []PreviewItemDTO{}, More: p.More}
	for _, it := range p.Items {
		d.Items = append(d.Items, previewItem(it))
	}
	if p.First != nil {
		f := previewItem(*p.First)
		d.First = &f
	}
	return d
}

func previewItem(it agent.PreviewItem) PreviewItemDTO {
	d := PreviewItemDTO{Role: string(it.Role), Text: it.Text}
	if !it.Time.IsZero() {
		d.Time = it.Time.UTC().Format(time.RFC3339)
	}
	if it.Role == agent.PreviewTools {
		byKind := map[string]int{}
		for k, n := range it.Tools {
			byKind[string(k)] += n
		}
		d.Text = toolWords(byKind)
	}
	return d
}

// toolWords says what one turn's tool calls did: "Ran 4 commands · edited 3 files · read
// 6".
func toolWords(tools map[string]int) string {
	n := func(k string) int { return tools[k] }
	plural := func(c int, one, many string) string {
		if c == 1 {
			return one
		}
		return fmt.Sprintf(many, c)
	}
	var out []string
	if c := n("execute"); c > 0 {
		out = append(out, plural(c, "Ran 1 command", "Ran %d commands"))
	}
	if c := n("edit") + n("delete") + n("move"); c > 0 {
		out = append(out, plural(c, "edited 1 file", "edited %d files"))
	}
	if c := n("read"); c > 0 {
		out = append(out, plural(c, "read 1 file", "read %d files"))
	}
	if c := n("search"); c > 0 {
		out = append(out, plural(c, "searched once", "searched %d times"))
	}
	if c := n("fetch"); c > 0 {
		out = append(out, plural(c, "fetched 1 page", "fetched %d pages"))
	}
	if c := n("subagent"); c > 0 {
		out = append(out, plural(c, "ran 1 subagent", "ran %d subagents"))
	}
	rest := 0
	for k, c := range tools {
		switch k {
		case "execute", "edit", "delete", "move", "read", "search", "fetch", "subagent":
		default:
			rest += c
		}
	}
	if rest > 0 {
		out = append(out, plural(rest, "1 other tool call", "%d other tool calls"))
	}
	if len(out) == 0 {
		return ""
	}
	out[0] = strings.ToUpper(out[0][:1]) + out[0][1:]
	return strings.Join(out, " · ")
}

// Rename gives a session a new title in its agent's own data (Claude Code's custom title,
// Codex's thread name), as their own rename does; Activity undoes it.
func (a *App) Rename(machine, key, title string) error {
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	inv := a.inv
	a.mu.Unlock()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = core.Rename(ctx, inv, e, title)
	return err
}

// PlaceDTO is one place a session on this machine is open in, besides the hopsesh
// Terminal window's tabs (the window knows those): a terminal app, the agent's desktop
// app, an editor, tmux or ssh.
type PlaceDTO struct {
	Kind    string `json:"kind"`          // presence.Kind
	App     string `json:"app,omitempty"` // its name for people ("VS Code")
	Count   int    `json:"count"`
	Waiting bool   `json:"waiting,omitempty"`
}

// LiveDTO is how a session on this machine runs now.
type LiveDTO struct {
	Live   bool       `json:"live"`
	Status string     `json:"status"` // the entry's state words, as in a scan
	Needs  bool       `json:"needs"`
	App    string     `json:"app,omitempty"`
	Name   string     `json:"name,omitempty"` // the name the running agent gives it
	Places []PlaceDTO `json:"places"`
}

// PresenceDTO is where this machine's sessions are open now, by machine + "\x00" + key.
// Other machines' sessions are read with their scans.
type PresenceDTO struct {
	Entries map[string]LiveDTO `json:"entries"`
}

// Presence reads where this machine's sessions are open now: the agents' own registries
// and locks, and one snapshot of the process table (each process's first known ancestor:
// iTerm2, Terminal, an editor, tmux, ssh). It never talks to a terminal app (that happens
// only when the user asks to show a session).
func (a *App) Presence() (*PresenceDTO, error) {
	out := &PresenceDTO{Entries: map[string]LiveDTO{}}
	a.mu.Lock()
	inv := a.inv
	a.mu.Unlock()
	if inv == nil {
		return out, nil
	}
	core := a.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	live := core.LiveHere(ctx, inv)
	table, _ := presence.Snapshot(ctx)
	self := os.Getpid()
	here := inv.Local()
	if here == nil {
		return out, nil
	}
	for _, e := range inv.Entries {
		if e.Machine != here.Name || e.Location.IsCloud() {
			continue
		}
		li, ok := live[e.Session.Key]
		if !ok {
			continue
		}
		e.Live = li
		d := LiveDTO{Live: li.State == agent.Live, Status: statusWords(core, e), Needs: li.State == agent.Live && strings.HasPrefix(li.Status, "waiting"),
			Name: li.Name, Places: placesOf(li, table, self)}
		if d.Live && li.App {
			d.App = appName(core, e)
		}
		out.Entries[e.Machine+"\x00"+e.Session.Key.String()] = d
	}
	return out, nil
}

// appName is the name of the agent's desktop app ("Claude").
func appName(core *app.App, e app.Entry) string {
	name := e.AgentName
	if m, ok := core.Module(e.Agent); ok {
		name = nonEmptyStr(appicon.Name(m.Spec().Icon.Apps), name)
	}
	return name
}

// placesOf is where a live session's processes run (besides hopsesh's own tabs): each
// process by its first known ancestor, counted by place.
func placesOf(li agent.LiveInfo, t presence.Table, self int) []PlaceDTO {
	out := []PlaceDTO{}
	if li.State != agent.Live {
		return out
	}
	procs := li.Procs
	if len(procs) == 0 && li.PID > 0 {
		procs = []agent.LiveProc{{PID: li.PID, App: li.App, Waiting: strings.HasPrefix(li.Status, "waiting")}}
	}
	add := func(kind, appLabel string, waiting bool) {
		for i := range out {
			if out[i].Kind == kind && out[i].App == appLabel {
				out[i].Count++
				out[i].Waiting = out[i].Waiting || waiting
				return
			}
		}
		out = append(out, PlaceDTO{Kind: kind, App: appLabel, Count: 1, Waiting: waiting})
	}
	if len(procs) == 0 {
		if li.App {
			add(string(presence.KindClaudeApp), "", strings.HasPrefix(li.Status, "waiting"))
		} else {
			add(string(presence.KindUnknown), "", strings.HasPrefix(li.Status, "waiting"))
		}
		return out
	}

	// Count each process once even when a detector supplies duplicate observations.
	unique := make([]agent.LiveProc, 0, len(procs))
	seen := map[int]int{}
	for _, p := range procs {
		if i, ok := seen[p.PID]; ok {
			unique[i].Waiting = unique[i].Waiting || p.Waiting
			unique[i].App = unique[i].App || p.App
		} else {
			seen[p.PID] = len(unique)
			unique = append(unique, p)
		}
	}
	for _, p := range unique {
		if p.App {
			add(string(presence.KindClaudeApp), "", p.Waiting)
			continue
		}
		h := presence.Host{Kind: presence.KindUnknown}
		if t != nil {
			h = t.Classify(p.PID, self)
		}
		if h.Kind == presence.KindHopsesh {
			continue // a tab of the hopsesh Terminal window: the window counts its tabs
		}
		add(string(h.Kind), h.App, p.Waiting)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

// RevealEntry shows a session's file in Finder (Explorer on Windows).
func (a *App) RevealEntry(machine, key string) error {
	a.mu.Lock()
	e, err := a.find(machine, key)
	local := false
	if err == nil {
		m := a.inv.Machine(machine)
		local = m != nil && m.Local
	}
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if !local || e.Session.Path == "" {
		return errors.New("only a session on this machine can be revealed")
	}
	p := filepath.FromSlash(e.Session.Path)
	if revealHook != nil {
		return revealHook(p)
	}
	switch runtime.GOOS {
	case "darwin":
		return proc.Command("open", "-R", p).Run()
	case "windows":
		return proc.Command("explorer", "/select,"+p).Start()
	}
	return proc.Command("xdg-open", filepath.Dir(p)).Start()
}

var revealHook func(path string) error

// SetRevealHook sends every path RevealEntry would show to f instead (tests).
func SetRevealHook(f func(path string) error) { revealHook = f }

// ResumeCommand is the command that resumes a session on this machine, for the user's
// shell (copied from the ⋯ menu).
func (a *App) ResumeCommand(machine, key string) (string, error) {
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	inv := a.inv
	a.mu.Unlock()
	if err != nil {
		return "", err
	}
	c, err := core.Resume(inv, e, agent.ResumeOptions{})
	if err != nil {
		return "", err
	}
	return launch.Shell(c, "", launch.DefaultShell()), nil
}

// ShowPlace brings forward an editor a session runs in (VS Code, Cursor): hopsesh can't
// pick the terminal inside it, so the app comes to the front as it is.
func (a *App) ShowPlace(name string) error {
	app, ok := ideApps[name]
	if !ok {
		return errors.New("hopsesh cannot show that app")
	}
	if appHook != nil {
		return appHook(name, agent.Command{})
	}
	if runtime.GOOS != "darwin" {
		return errors.New("hopsesh can't bring that app forward here")
	}
	return proc.Command("open", "-a", app).Run()
}

// ideApps are the editors ShowPlace can bring forward, by the name presence gives them, as
// macOS knows the app.
var ideApps = map[string]string{"VS Code": "Visual Studio Code", "Cursor": "Cursor", "Windsurf": "Windsurf", "Zed": "Zed"}
