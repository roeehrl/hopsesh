package main

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

// The scenario model: every row is one combination of these values. Pairwise generation
// (every pair of values in some row) keeps PR runs short; -all runs every combination.

// Row is one scenario.
type Row struct {
	N       int    `json:"n"`
	Op      string `json:"op"`      // what hopsesh does
	From    string `json:"from"`    // the source session's agent: claude | codex
	To      string `json:"to"`      // the agent it ends up in
	Content string `json:"content"` // the conversation's language, or large
	Repo    string `json:"repo"`    // the source checkout's state
	Naming  string `json:"naming"`  // how the other machine is named: address | alias
	// Location is the cloud a row involves, or machine: a fetch starts there (repo
	// unpushed: the cloud session never pushed its work), a hand-off goes there from this
	// machine, a cloud round trip goes there and comes back, and a cloud hop starts there
	// and goes on to the other cloud through this machine.
	Location string `json:"location"`
}

func (r Row) String() string {
	return fmt.Sprintf("#%d %s %s→%s %s %s %s %s", r.N, r.Op, r.From, r.To, r.Content, r.Repo, r.Naming, r.Location)
}

var dims = []struct {
	name   string
	values []string
}{
	{"op", []string{"move", "continue", "push", "roundtrip", "conflict", "undo-used", "skill", "fetch", "handoff", "cloud-roundtrip", "cloud-hop"}},
	{"agents", []string{"claude>claude", "codex>codex", "claude>codex", "codex>claude"}},
	{"content", []string{"ascii", "zh", "ja", "ar", "el", "large"}},
	{"repo", []string{"clean", "unpushed", "uncommitted", "worktree", "none"}},
	{"naming", []string{"address", "alias"}},
	{"location", []string{"machine", "claude-cloud", "codex-cloud"}},
}

// valid rules out combinations that cannot happen.
func valid(v []string) bool {
	op, agents, repo, naming, location := v[0], v[1], v[3], v[4], v[5]
	same := agents == "claude>claude" || agents == "codex>codex"
	if cloudOp(op) != (location != "machine") {
		return false // only a fetch, a hand-off and a cloud round trip involve a cloud
	}
	if op == "cloud-hop" {
		// A cloud session goes on to the other vendor's cloud through this machine: Claude
		// Code cloud → Codex cloud (claude>codex) or Codex cloud → Claude Code cloud
		// (codex>claude), from a session that pushed its work.
		return naming == "address" && repo == "clean" &&
			(location == "claude-cloud" && agents == "claude>codex" || location == "codex-cloud" && agents == "codex>claude")
	}
	if location == "codex-cloud" {
		return validCodex(op, agents, repo, naming)
	}
	switch op {
	case "handoff":
		// A Claude Code or Codex session here goes to Claude Code cloud (always a Claude
		// Code session there), in each state of its checkout (none: refused).
		return (agents == "claude>claude" || agents == "codex>claude") && naming == "address" && (repo != "worktree" || agents == "claude>claude")
	case "cloud-roundtrip":
		// There and back: handed off, worked on in the cloud, brought home in Claude Code.
		return agents == "claude>claude" && (repo == "clean" || repo == "uncommitted") && naming == "address"
	case "fetch":
		// A Claude Code cloud session comes here in Claude Code, or on into Codex; its
		// branch was pushed (clean) or never (unpushed). No other machine takes part.
		return (agents == "claude>claude" || agents == "claude>codex") && (repo == "clean" || repo == "unpushed") && naming == "address"
	case "continue":
		if same {
			return false
		}
	case "move", "conflict", "undo-used":
		if !same {
			return false
		}
	case "skill":
		// Not about a session: one canonical shape.
		return agents == "claude>claude" && v[2] == "ascii" && repo == "clean"
	}
	if repo == "worktree" && !strings.HasPrefix(agents, "claude>") {
		return false // agent worktrees are Claude Code's
	}
	return true
}

// validCodex rules the Codex cloud rows: a session here (Claude Code or Codex) goes to
// Codex cloud (always Codex there) in each state of its checkout (none: refused); a round
// trip comes home in Codex; a task comes here in Codex or on into Claude Code, done (clean)
// or still running (unpushed: refused, there is no diff yet).
func validCodex(op, agents, repo, naming string) bool {
	if naming != "address" {
		return false
	}
	switch op {
	case "handoff":
		return (agents == "claude>codex" || agents == "codex>codex") && (repo != "worktree" || agents == "claude>codex")
	case "cloud-roundtrip":
		return (agents == "claude>codex" || agents == "codex>codex") && (repo == "clean" || repo == "uncommitted")
	case "fetch":
		return (agents == "codex>codex" || agents == "codex>claude") && (repo == "clean" || repo == "unpushed")
	}
	return false
}

// cloudOp reports whether an op involves a cloud.
func cloudOp(op string) bool {
	return op == "fetch" || op == "handoff" || op == "cloud-roundtrip" || op == "cloud-hop"
}

func toRow(n int, v []string) Row {
	from, to, _ := strings.Cut(v[1], ">")
	return Row{N: n, Op: v[0], From: from, To: to, Content: v[2], Repo: v[3], Naming: v[4], Location: v[5]}
}

func allCombos() [][]string {
	var out [][]string
	var walk func(i int, cur []string)
	walk = func(i int, cur []string) {
		if i == len(dims) {
			if valid(cur) {
				out = append(out, append([]string(nil), cur...))
			}
			return
		}
		for _, v := range dims[i].values {
			walk(i+1, append(cur, v))
		}
	}
	walk(0, nil)
	return out
}

// pairwise picks rows greedily until every valid pair of values appears in one, the same
// rows for the same seed.
func pairwise(seed int64) []Row { return covering(2, seed) }

// covering picks rows greedily until every valid t-tuple of values (pairs for t=2, triples
// for t=3) appears in one; the same rows for the same seed.
func covering(t int, seed int64) []Row {
	combos := allCombos()
	pos := tuples(len(dims), t)
	uncovered := map[string]bool{}
	for _, c := range combos {
		for _, p := range pos {
			uncovered[tkey(p, c)] = true
		}
	}
	rnd := rand.New(rand.NewSource(seed))
	rnd.Shuffle(len(combos), func(i, j int) { combos[i], combos[j] = combos[j], combos[i] })
	var rows [][]string
	for len(uncovered) > 0 {
		best, bestN := -1, 0
		for k, c := range combos {
			n := 0
			for _, p := range pos {
				if uncovered[tkey(p, c)] {
					n++
				}
			}
			if n > bestN {
				best, bestN = k, n
			}
		}
		if best < 0 {
			break
		}
		c := combos[best]
		for _, p := range pos {
			delete(uncovered, tkey(p, c))
		}
		rows = append(rows, c)
		combos = append(combos[:best], combos[best+1:]...)
	}
	sort.SliceStable(rows, func(a, b int) bool { return rows[a][0] < rows[b][0] })
	out := make([]Row, len(rows))
	for i, c := range rows {
		out[i] = toRow(i+1, c)
	}
	return out
}

// tuples lists the t-sized sets of value positions in a row of n values.
func tuples(n, t int) [][]int {
	var out [][]int
	var walk func(start int, cur []int)
	walk = func(start int, cur []int) {
		if len(cur) == t {
			out = append(out, append([]int(nil), cur...))
			return
		}
		for i := start; i < n; i++ {
			walk(i+1, append(cur, i))
		}
	}
	walk(0, nil)
	return out
}

// tkey names the values of row c at positions pos.
func tkey(pos []int, c []string) string {
	var b strings.Builder
	for _, i := range pos {
		fmt.Fprintf(&b, "%d=%s|", i, c[i])
	}
	return b.String()
}

func every() []Row {
	combos := allCombos()
	out := make([]Row, len(combos))
	for i, c := range combos {
		out[i] = toRow(i+1, c)
	}
	return out
}
