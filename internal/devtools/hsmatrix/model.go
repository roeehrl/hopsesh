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
}

func (r Row) String() string {
	return fmt.Sprintf("#%d %s %s→%s %s %s %s", r.N, r.Op, r.From, r.To, r.Content, r.Repo, r.Naming)
}

var dims = []struct {
	name   string
	values []string
}{
	{"op", []string{"move", "continue", "push", "roundtrip", "conflict", "undo-used", "skill"}},
	{"agents", []string{"claude>claude", "codex>codex", "claude>codex", "codex>claude"}},
	{"content", []string{"ascii", "zh", "ja", "ar", "el", "large"}},
	{"repo", []string{"clean", "unpushed", "uncommitted", "worktree", "none"}},
	{"naming", []string{"address", "alias"}},
}

// valid rules out combinations that cannot happen.
func valid(v []string) bool {
	op, agents, repo := v[0], v[1], v[3]
	same := agents == "claude>claude" || agents == "codex>codex"
	switch op {
	case "continue":
		if same {
			return false
		}
	case "move", "roundtrip", "conflict", "undo-used":
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

func toRow(n int, v []string) Row {
	from, to, _ := strings.Cut(v[1], ">")
	return Row{N: n, Op: v[0], From: from, To: to, Content: v[2], Repo: v[3], Naming: v[4]}
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
func pairwise(seed int64) []Row {
	combos := allCombos()
	type pair struct{ i, j int; a, b string }
	uncovered := map[pair]bool{}
	for _, c := range combos {
		for i := 0; i < len(c); i++ {
			for j := i + 1; j < len(c); j++ {
				uncovered[pair{i, j, c[i], c[j]}] = true
			}
		}
	}
	rnd := rand.New(rand.NewSource(seed))
	rnd.Shuffle(len(combos), func(i, j int) { combos[i], combos[j] = combos[j], combos[i] })
	var rows [][]string
	for len(uncovered) > 0 {
		best, bestN := -1, 0
		for k, c := range combos {
			n := 0
			for i := 0; i < len(c); i++ {
				for j := i + 1; j < len(c); j++ {
					if uncovered[pair{i, j, c[i], c[j]}] {
						n++
					}
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
		for i := 0; i < len(c); i++ {
			for j := i + 1; j < len(c); j++ {
				delete(uncovered, pair{i, j, c[i], c[j]})
			}
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

func every() []Row {
	combos := allCombos()
	out := make([]Row, len(combos))
	for i, c := range combos {
		out[i] = toRow(i+1, c)
	}
	return out
}
