package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/testkit"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// jsonOut is a --json output the skill tells agents to read: its real output on a demo
// home when the command runs without a cloud or another machine, and always the shape of
// the Go values it prints (top-level key → type).
type jsonOut struct {
	args  []string // run on the demo home; nil: the shape only (clouds, other machines)
	shape map[string]reflect.Type
}

const demoRef = "claude/0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a01"

var (
	planType   = map[string]reflect.Type{"": reflect.TypeFor[move.Plan]()}
	resultType = reflect.TypeFor[move.Result]()
	jsonOuts   = map[string]jsonOut{
		"ls": {[]string{"ls", "--json"}, map[string]reflect.Type{"machines": reflect.TypeFor[[]app.Machine](),
			"groups": reflect.TypeFor[[]app.Group](), "clouds": reflect.TypeFor[[]app.Cloud]()}},
		"plan":           {[]string{"plan", demoRef, "--json"}, planType},
		"plan --in":      {[]string{"plan", demoRef, "--in", "codex", "--json"}, planType},
		"pull":           {[]string{"pull", demoRef, "--in", "codex", "--json", "--yes"}, map[string]reflect.Type{"plan": planType[""], "result": resultType, "command": reflect.TypeFor[string]()}},
		"plan --to":      {nil, planType}, // a hand-off plan: a cloud
		"plan <cloud>":   {nil, planType}, // bringing back, or a hop: a cloud
		"handoff --yes":  {nil, map[string]reflect.Type{"plan": planType[""], "result": resultType, "handoff": reflect.TypeFor[move.HandoffResult](), "error": reflect.TypeFor[string]()}},
		"pull <cloud>":   {nil, map[string]reflect.Type{"plan": planType[""], "result": resultType, "brought": reflect.TypeFor[app.Brought]()}},
		"clouds cleanup": {nil, map[string]reflect.Type{"branches": reflect.TypeFor[[]app.BranchCandidate]()}},
		"cd --dry-run": {nil, map[string]reflect.Type{"entry": reflect.TypeFor[app.Entry](), "plan": reflect.TypeFor[agent.DirPlan](),
			"open": reflect.TypeFor[bool](), "inSession": reflect.TypeFor[string]()}},
	}
)

// skillJSON is every JSON path the skill names, with the output it is read from and its
// full path there. A dotted path in the skill missing here fails; so does an entry the
// skill no longer names. optional: absent from the demo's real output (omitempty, or a
// state the demo does not reach), so its type is checked instead.
var skillJSON = map[string][]struct {
	out, path string
	optional  bool
}{
	// ls --json
	"machines":   {{"ls", "machines", false}},
	"status":     {{"ls", "machines.status", false}},
	"hint":       {{"ls", "machines.hint", true}},
	"groups":     {{"ls", "groups", false}},
	"entry":      {{"ls", "groups.items.entry", false}},
	"machine":    {{"ls", "groups.items.entry.machine", false}},
	"agent":      {{"ls", "groups.items.entry.agent", false}, {"plan", "agent", false}},
	"session":    {{"ls", "groups.items.entry.session", false}, {"handoff --yes", "handoff.session", false}},
	"key":        {{"ls", "groups.items.entry.session.key", false}, {"plan", "key", false}},
	"title":      {{"ls", "groups.items.entry.session.title", false}, {"plan", "title", false}},
	"cwd":        {{"ls", "groups.items.entry.session.cwd", false}},
	"lastActive": {{"ls", "groups.items.entry.session.lastActive", false}},
	"lastPrompt": {{"ls", "groups.items.entry.session.lastPrompt", false}},
	"live":       {{"ls", "groups.items.entry.live", false}},
	"git":        {{"ls", "groups.items.entry.git", false}},
	"departure":  {{"ls", "groups.items.entry.departure", true}},
	"copies":     {{"ls", "groups.items.copies", true}},
	// plan --json
	"kind":                    {{"plan", "kind", false}},
	"source":                  {{"plan", "source", false}},
	"target.cwd":              {{"plan", "target.cwd", false}},
	"repo":                    {{"plan", "repo", false}},
	"repo.action":             {{"plan", "repo.action", false}},
	"sync":                    {{"plan", "sync", true}},
	"conflict":                {{"plan", "conflict", true}},
	"warnings":                {{"plan", "warnings", false}, {"pull", "result.warnings", true}},
	"blockers":                {{"plan", "blockers", false}},
	"continue":                {{"plan --in", "continue", false}},
	"continue.relation":       {{"plan --in", "continue.relation", false}},
	"report":                  {{"plan --in", "continue.report", false}},
	"continue.report.summary": {{"plan --in", "continue.report.summary", false}},
	"briefing":                {{"plan --in", "continue.briefing", false}},
	// pull --json --yes
	"plan":            {{"pull", "plan", false}, {"handoff --yes", "plan", false}, {"pull <cloud>", "plan", false}},
	"result":          {{"pull", "result", false}, {"handoff --yes", "result", false}, {"pull <cloud>", "result", false}},
	"command":         {{"pull", "command", false}, {"pull <cloud>", "brought.command", false}},
	"journal":         {{"pull", "result.journal", false}},
	"result.journal":  {{"handoff --yes", "result.journal", false}},
	"plan.from":       {{"cd --dry-run", "plan.from", false}},
	"plan.to":         {{"cd --dry-run", "plan.to", false}},
	"plan.warnings":   {{"cd --dry-run", "plan.warnings", false}},
	"plan.blockers":   {{"cd --dry-run", "plan.blockers", false}},
	"syncNote":        {{"pull", "result.syncNote", true}},
	"result.syncNote": {{"pull", "result.syncNote", true}},
	"pushed":          {{"pull", "result.pushed", true}},
	"pushError":       {{"pull", "result.pushError", true}},
	// plan --to <cloud> --json
	"handoff.conversation":      {{"plan --to", "handoff.conversation", false}},
	"handoff.stays":             {{"plan --to", "handoff.stays", false}},
	"handoff.loss":              {{"plan --to", "handoff.loss", false}},
	"handoff.reuse":             {{"plan --to", "handoff.reuse", false}},
	"handoff.branch":            {{"plan --to", "handoff.branch", false}, {"handoff --yes", "handoff.branch", false}},
	"handoff.unpushed":          {{"plan --to", "handoff.unpushed", false}},
	"handoff.tracked":           {{"plan --to", "handoff.tracked", false}},
	"handoff.withheld":          {{"plan --to", "handoff.withheld", false}},
	"handoff.offered":           {{"plan --to", "handoff.offered", false}},
	"handoff.checks":            {{"plan --to", "handoff.checks", false}},
	"handoff.brief":             {{"plan --to", "handoff.brief", false}},
	"handoff.notes":             {{"plan --to", "handoff.notes", false}},
	"handoff.envNeeded":         {{"plan --to", "handoff.envNeeded", false}},
	"handoff.env":               {{"plan --to", "handoff.env", false}},
	"handoff.envNote":           {{"plan --to", "handoff.envNote", false}},
	"handoff.envs":              {{"plan --to", "handoff.envs", false}},
	"label":                     {{"plan --to", "handoff.envs.label", false}},
	"handoff.canStartingDiff":   {{"plan --to", "handoff.canStartingDiff", false}},
	"handoff.startingDiffOffer": {{"plan --to", "handoff.startingDiffOffer", false}},
	"handoff.terminal":          {{"plan --to", "handoff.terminal", false}},
	"handoff.folder":            {{"plan --to", "handoff.folder", false}},
	// handoff --json --yes
	"handoff":            {{"handoff --yes", "handoff", false}},
	"error":              {{"handoff --yes", "error", false}},
	"handoff.url":        {{"handoff --yes", "handoff.url", false}},
	"url":                {{"handoff --yes", "handoff.url", false}},
	"branch":             {{"handoff --yes", "handoff.branch", false}, {"pull <cloud>", "brought.branch", false}},
	"env":                {{"handoff --yes", "handoff.env", false}},
	"noun":               {{"handoff --yes", "handoff.noun", false}},
	"steps":              {{"handoff --yes", "handoff.steps", false}},
	"stayed":             {{"handoff --yes", "handoff.stayed", false}},
	"handoff.stayed":     {{"handoff --yes", "handoff.stayed", false}},
	"failed":             {{"handoff --yes", "handoff.failed", false}},
	"message":            {{"handoff --yes", "handoff.message", false}, {"pull <cloud>", "brought.message", false}},
	"handoff.message":    {{"handoff --yes", "handoff.message", false}},
	"manual":             {{"handoff --yes", "handoff.manual", false}},
	"pasted":             {{"handoff --yes", "handoff.pasted", false}},
	"noFollowUp":         {{"handoff --yes", "handoff.noFollowUp", false}},
	"handoff.noFollowUp": {{"handoff --yes", "handoff.noFollowUp", false}},
	// plan <cloud>:<id> [--to <cloud>] --json
	"fetch.loss":   {{"plan <cloud>", "fetch.loss", false}},
	"hop.legs":     {{"plan <cloud>", "hop.legs", false}},
	"hop.loss":     {{"plan <cloud>", "hop.loss", false}},
	"hop.then":     {{"plan <cloud>", "hop.then", false}},
	"hop.terminal": {{"plan <cloud>", "hop.terminal", false}},
	// pull <cloud>:<id> --json --yes
	"brought":  {{"pull <cloud>", "brought", false}},
	"outcome":  {{"pull <cloud>", "brought.outcome", false}},
	"worktree": {{"pull <cloud>", "brought.worktree", false}},
	"written":  {{"pull <cloud>", "brought.written", false}},
	"loss":     {{"pull <cloud>", "brought.loss", false}},
	// clouds cleanup --list --json
	"offer": {{"clouds cleanup", "branches.offer", false}},
	"why":   {{"clouds cleanup", "branches.why", false}},
}

// The JSON the skill tells agents to read is what hopsesh prints: the real output of the
// commands a demo home can run, and the printed Go values' shape for the rest.
func TestSkillJSONPathsExist(t *testing.T) {
	home := t.TempDir()
	for k, v := range testkit.Env(home) {
		t.Setenv(k, v)
	}
	if err := testkit.DemoHome(home); err != nil {
		t.Fatal(err)
	}
	real := map[string]map[string]bool{}
	// In order: pull last, as it writes.
	for _, name := range []string{"ls", "plan", "plan --in", "pull"} {
		var out bytes.Buffer
		cmd := NewRoot(&out, all.Registry())
		cmd.SetArgs(jsonOuts[name].args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v: %v\n%s", jsonOuts[name].args, err, out.String())
		}
		var v any
		if err := json.Unmarshal(out.Bytes(), &v); err != nil {
			t.Fatalf("%v printed no JSON: %v", jsonOuts[name].args, err)
		}
		real[name] = map[string]bool{}
		valuePaths(v, "", real[name])
	}
	shapes := map[string]map[string]bool{}
	for name, o := range jsonOuts {
		shapes[name] = map[string]bool{}
		for key, typ := range o.shape {
			typePaths(typ, key, shapes[name], map[reflect.Type]bool{})
		}
	}

	files := renderedSkill(t)
	var text strings.Builder
	for _, b := range files {
		text.Write(b)
	}
	// Dotted paths anywhere in a code span ("`handoff.url`", "`hopsesh undo <result.journal>`"),
	// file names aside; each needs an entry.
	dotted := regexp.MustCompile(`[a-z][A-Za-z]*(?:\.[a-z][A-Za-z]*)+`)
	for _, span := range regexp.MustCompile("`[^`]+`").FindAllString(text.String(), -1) {
		for _, loc := range dotted.FindAllStringIndex(span, -1) {
			p := span[loc[0]:loc[1]]
			before, after := span[loc[0]-1], span[loc[1]]
			if before == '/' || before == '.' || after == '/' || strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".json") {
				continue
			}
			if skillJSON[p] == nil {
				t.Errorf("the skill names `%s`: add it to skillJSON with the output it is read from", p)
			}
		}
	}
	var keys []string
	for k := range skillJSON {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !regexp.MustCompile(`(^|[^A-Za-z.])` + regexp.QuoteMeta(k) + `($|[^A-Za-z])`).MatchString(text.String()) {
			t.Errorf("skillJSON has %q, which the skill no longer names", k)
		}
		for _, u := range skillJSON[k] {
			o, ok := jsonOuts[u.out]
			switch {
			case !ok:
				t.Errorf("%s: no output %q", k, u.out)
			case o.args != nil && real[u.out][u.path]:
				t.Logf("%s: %s %s (real output)", k, u.out, u.path)
			case o.args != nil && !u.optional:
				t.Errorf("`%s`: %s --json printed no %s on the demo home", k, u.out, u.path)
			case shapes[u.out][u.path]:
				t.Logf("%s: %s %s (printed type)", k, u.out, u.path)
			default:
				t.Errorf("`%s`: %s --json has no %s", k, u.out, u.path)
			}
		}
	}
}

// valuePaths adds every object key path in v ("groups.items.entry"); lists are passed
// through, so a path names every element's key.
func valuePaths(v any, prefix string, into map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			p := join(prefix, k)
			into[p] = true
			valuePaths(e, p, into)
		}
	case []any:
		for _, e := range x {
			valuePaths(e, prefix, into)
		}
	}
}

// typePaths adds the JSON key paths of the values of type t, as encoding/json prints
// them: json names, embedded structs flattened, pointers and lists passed through.
func typePaths(t reflect.Type, prefix string, into map[string]bool, seen map[reflect.Type]bool) {
	if prefix != "" {
		into[prefix] = true
	}
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return
	}
	seen[t] = true
	defer delete(seen, t)
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if f.Anonymous && name == "" {
			typePaths(f.Type, prefix, into, seen)
			continue
		}
		if name == "" {
			name = f.Name
		}
		typePaths(f.Type, join(prefix, name), into, seen)
	}
}

func join(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}
