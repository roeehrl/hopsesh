package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/devtools/schemalite"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

// A slot is a kind of payload another project may contribute for one agent:
// testbundle/<agent>/<agent version>/<dir>/[<event>/]<name>.json.
type slot struct {
	agent  agent.ID
	dir    string
	kind   string // the manifest's file kind
	schema string // in testbundle/schemas
	// event is the field that names the folder a file sits in (hooks/<event>/), or "".
	event string
	// more checks what the schema cannot, such as that hopsesh reads the file as meant.
	more func(doc []byte) error
}

var slots = []slot{
	{agent: "claude", dir: "hooks", kind: "hook", schema: "claude-hook.schema.json", event: "hook_event_name"},
	{agent: "claude", dir: "registry", kind: "registry", schema: "claude-registry.schema.json", more: claudeReads},
	{agent: "codex", dir: "hooks", kind: "hook", schema: "codex-hook.schema.json", event: "hook_event_name"},
	{agent: "codex", dir: "notify", kind: "notify", schema: "codex-notify.schema.json"},
}

// entry is one file of the bundle that comes from the repository.
type entry struct {
	src          string // on disk
	path         string // in the bundle
	kind         string
	agent        string
	agentVersion string
	schema       string // schemas/<file>, when validated against one
}

var (
	agentVersion = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}$`)
	eventName    = regexp.MustCompile(`^[A-Z][A-Za-z]{2,40}$`)
	fileName     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}\.json$`)
)

// maxPayload bounds a contributed file: a payload, not a transcript.
const maxPayload = 256 << 10

// check validates the contributions under testbundle/ and the agents' own fixtures, and
// returns the bundle's files that come from the repository, sorted by their path.
func check(root string) ([]entry, []string) {
	var out []entry
	var errs []string
	bad := func(p, f string, a ...any) { errs = append(errs, p+": "+fmt.Sprintf(f, a...)) }
	tb := filepath.Join(root, "testbundle")

	schemas := map[string]*schemalite.Schema{}
	ss, err := os.ReadDir(filepath.Join(tb, "schemas"))
	if err != nil {
		return nil, []string{err.Error()}
	}
	for _, e := range ss {
		p := "testbundle/schemas/" + e.Name()
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".schema.json") {
			bad(p, "the schemas folder holds only <name>.schema.json files")
			continue
		}
		s, err := schemalite.Load(filepath.Join(tb, "schemas", e.Name()))
		if err != nil {
			bad(p, "%v", err)
			continue
		}
		schemas[e.Name()] = s
		out = append(out, entry{src: filepath.Join(tb, "schemas", e.Name()), path: "schemas/" + e.Name(), kind: "schema"})
	}
	for _, s := range slots {
		if schemas[s.schema] == nil {
			bad("testbundle/schemas/"+s.schema, "missing (slot %s/%s)", s.agent, s.dir)
		}
	}
	out = append(out, entry{src: filepath.Join(tb, "README.md"), path: "README.md", kind: "doc"})

	modules := map[agent.ID]bool{}
	for _, m := range all.Registry().All() {
		modules[m.Spec().ID] = true
	}
	// The contributions: testbundle/<agent>/<version>/<slot>/[<event>/]<name>.json.
	top, err := os.ReadDir(tb)
	if err != nil {
		return nil, []string{err.Error()}
	}
	for _, a := range top {
		name := a.Name()
		if name == "README.md" || name == "schemas" {
			continue
		}
		if !a.IsDir() || !modules[agent.ID(name)] {
			bad("testbundle/"+name, "not an agent module's id (or README.md, schemas)")
			continue
		}
		vs, _ := os.ReadDir(filepath.Join(tb, name))
		for _, v := range vs {
			vp := "testbundle/" + name + "/" + v.Name()
			if !v.IsDir() || !agentVersion.MatchString(v.Name()) {
				bad(vp, "not an agent version folder (\"2.1.284\")")
				continue
			}
			ds, _ := os.ReadDir(filepath.Join(tb, name, v.Name()))
			for _, d := range ds {
				dp := vp + "/" + d.Name()
				i := slices.IndexFunc(slots, func(s slot) bool { return string(s.agent) == name && s.dir == d.Name() })
				if !d.IsDir() || i < 0 {
					bad(dp, "not a contribution folder for %s (%s)", name, slotNames(agent.ID(name)))
					continue
				}
				sl := slots[i]
				walkSlot(filepath.Join(tb, name, v.Name(), d.Name()), dp, sl, func(src, rel, event string) {
					p := dp + "/" + rel
					e := entry{src: src, path: name + "/" + v.Name() + "/" + sl.dir + "/" + rel, kind: sl.kind,
						agent: name, agentVersion: v.Name(), schema: "schemas/" + sl.schema}
					for _, msg := range checkPayload(src, schemas[sl.schema], sl, event) {
						bad(p, "%s", msg)
					}
					out = append(out, e)
				}, bad)
			}
		}
	}

	// The agents' own fixtures, as the module tests use them: <agent>/<version>/home/.
	reg := schemas["claude-registry.schema.json"]
	for _, m := range all.Registry().All() {
		id := string(m.Spec().ID)
		dir := filepath.Join(root, "agents", id, "testdata")
		vs, err := os.ReadDir(dir)
		if err != nil {
			continue // a cloud-only module has no fixtures
		}
		for _, v := range vs {
			if !v.IsDir() {
				continue
			}
			base := filepath.Join(dir, v.Name())
			_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				rel, _ := filepath.Rel(base, p)
				rel = filepath.ToSlash(rel)
				e := entry{src: p, path: id + "/" + v.Name() + "/home/" + rel, kind: "fixture", agent: id, agentVersion: v.Name()}
				where := "agents/" + id + "/testdata/" + v.Name() + "/" + rel
				b, err := os.ReadFile(p)
				if err != nil {
					bad(where, "%v", err)
					return nil
				}
				switch {
				case id == "claude" && path.Dir(rel) == "sessions" && strings.HasSuffix(rel, ".json") && reg != nil:
					e.schema = "schemas/claude-registry.schema.json"
					for _, msg := range reg.Validate(b) {
						bad(where, "%s", msg)
					}
				case strings.HasSuffix(rel, ".json"):
					if !json.Valid(b) {
						bad(where, "not JSON")
					}
				case strings.HasSuffix(rel, ".jsonl"):
					sc := bufio.NewScanner(bytes.NewReader(b))
					sc.Buffer(nil, 16<<20)
					for n := 1; sc.Scan(); n++ {
						if l := bytes.TrimSpace(sc.Bytes()); len(l) > 0 && !json.Valid(l) {
							bad(where, "line %d is not JSON", n)
						}
					}
				}
				out = append(out, e)
				return nil
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	for i := 1; i < len(out); i++ {
		if out[i].path == out[i-1].path {
			bad(out[i].path, "two files have this path in the bundle")
		}
	}
	return out, errs
}

// walkSlot calls f for each file of a slot folder, with its path below the folder and
// the event folder it sits in (for a slot by event).
func walkSlot(dir, at string, sl slot, f func(src, rel, event string), bad func(p, f string, a ...any)) {
	files := func(d, rel, event string) {
		es, _ := os.ReadDir(d)
		for _, e := range es {
			p := at + "/" + rel + e.Name()
			if e.IsDir() || !fileName.MatchString(e.Name()) {
				bad(p, "a contribution is a <name>.json file (lower case letters, digits and dashes)")
				continue
			}
			f(filepath.Join(d, e.Name()), rel+e.Name(), event)
		}
	}
	if sl.event == "" {
		files(dir, "", "")
		return
	}
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if !e.IsDir() || !eventName.MatchString(e.Name()) {
			bad(at+"/"+e.Name(), "%s/ holds one folder per event, named as %s says (\"Notification\")", sl.dir, sl.event)
			continue
		}
		files(filepath.Join(dir, e.Name()), e.Name()+"/", e.Name())
	}
}

func slotNames(id agent.ID) string {
	var n []string
	for _, s := range slots {
		if s.agent == id {
			n = append(n, s.dir)
		}
	}
	if len(n) == 0 {
		return "none yet"
	}
	return strings.Join(n, ", ")
}

// checkPayload validates one contributed file: its size, its schema, its event folder,
// what hopsesh makes of it, and that it holds only made-up data.
func checkPayload(src string, s *schemalite.Schema, sl slot, event string) []string {
	b, err := os.ReadFile(src)
	if err != nil {
		return []string{err.Error()}
	}
	if len(b) > maxPayload {
		return []string{fmt.Sprintf("larger than %d KiB", maxPayload>>10)}
	}
	var errs []string
	if s != nil {
		errs = append(errs, s.Validate(b)...)
	}
	if len(errs) > 0 {
		return errs
	}
	var doc map[string]any
	_ = json.Unmarshal(b, &doc)
	if sl.event != "" && doc[sl.event] != event {
		errs = append(errs, fmt.Sprintf("%s is %v, but the file is in %s/%s/", sl.event, doc[sl.event], sl.dir, event))
	}
	if sl.more != nil {
		if err := sl.more(b); err != nil {
			errs = append(errs, err.Error())
		}
	}
	return append(errs, scrub(b)...)
}

// claudeReads checks that the Claude Code module reads a registry entry as its contributor
// meant: the session is open, with the process id, and "waiting for <waitingFor>" as its
// status when that is set.
func claudeReads(b []byte) error {
	var le struct {
		PID        int    `json:"pid"`
		SessionID  string `json:"sessionId"`
		Status     string `json:"status"`
		WaitingFor string `json:"waitingFor"`
	}
	if err := json.Unmarshal(b, &le); err != nil {
		return err
	}
	m := claude.New()
	h := agenttest.NewFakeHost("/home/alice")
	h.Put(fmt.Sprintf("/home/alice/.claude/sessions/%d.json", le.PID), b, time.Unix(1_790_000_000, 0))
	h.AddBinary("claude", "2.1.284 (Claude Code)")
	h.PIDs[le.PID] = true
	ctx := context.Background()
	in, err := m.Detect(ctx, h)
	if err != nil {
		return fmt.Errorf("hopsesh: %w", err)
	}
	sid := agent.SessionID(le.SessionID)
	live, err := m.Live(ctx, agent.Confine(h, m.Spec(), in), in, []agent.SessionID{sid})
	if err != nil {
		return fmt.Errorf("hopsesh reads it with an error: %w", err)
	}
	got := live[sid]
	want := le.Status
	if le.WaitingFor != "" {
		want = "waiting for " + le.WaitingFor
	}
	if got.State != agent.Live || got.PID != le.PID || got.Status != want {
		return fmt.Errorf("hopsesh reads it as %s, pid %d, status %q; want open, pid %d, status %q", got.State, got.PID, got.Status, le.PID, want)
	}
	return nil
}
