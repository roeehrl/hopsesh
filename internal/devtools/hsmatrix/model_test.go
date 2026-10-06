package main

import "testing"

// Pairwise rows cover every pair of values that some valid combination has, and only valid
// combinations; the same seed gives the same rows.
func TestPairwiseCoversEveryValidPair(t *testing.T) {
	rows := pairwise(1)
	key := func(i, j int, a, b string) string { return string(rune('0'+i)) + string(rune('0'+j)) + a + "|" + b }
	values := func(r Row) []string {
		return []string{r.Op, r.From + ">" + r.To, r.Content, r.Repo, r.Naming, r.Location}
	}
	have := map[string]bool{}
	for _, r := range rows {
		v := values(r)
		if !valid(v) {
			t.Fatalf("invalid row %s", r)
		}
		for i := 0; i < len(v); i++ {
			for j := i + 1; j < len(v); j++ {
				have[key(i, j, v[i], v[j])] = true
			}
		}
	}
	for _, c := range allCombos() {
		for i := 0; i < len(c); i++ {
			for j := i + 1; j < len(c); j++ {
				if !have[key(i, j, c[i], c[j])] {
					t.Fatalf("pair %s=%s, %s=%s is in no row", dims[i].name, c[i], dims[j].name, c[j])
				}
			}
		}
	}
	again := pairwise(1)
	for i := range rows {
		if rows[i] != again[i] {
			t.Fatal("the same seed must give the same rows")
		}
	}
}

// Fetch rows start in a cloud and come into Claude Code or Codex here (a Claude Code cloud
// session from Claude Code, a Codex cloud task from Codex); hand-off rows go there from
// Claude Code or Codex here, the cloud's own agent taking it; round trips come home in that
// agent; no other row involves a cloud. The pull-request tier stays small.
func TestCloudRows(t *testing.T) {
	rows := pairwise(1)
	n := map[string]int{}
	for _, r := range rows {
		own := map[string]string{"claude-cloud": "claude", "codex-cloud": "codex"}[r.Location]
		if cloudOp(r.Op) != (r.Location != "machine") || r.Op == "fetch" && r.From != own || r.Op == "handoff" && r.To != own ||
			r.Op == "cloud-roundtrip" && r.To != own || r.Location == "claude-cloud" && r.Op == "cloud-roundtrip" && r.From != "claude" ||
			r.Op == "cloud-hop" && (r.From != own || r.To == own) {
			t.Fatalf("row %s", r)
		}
		n[r.Op+"@"+r.Location]++
	}
	for _, k := range []string{"fetch@claude-cloud", "handoff@claude-cloud", "cloud-roundtrip@claude-cloud", "fetch@codex-cloud", "handoff@codex-cloud", "cloud-roundtrip@codex-cloud",
		"cloud-hop@claude-cloud", "cloud-hop@codex-cloud"} {
		if n[k] < 1 {
			t.Fatalf("no %s row: %v", k, n)
		}
	}
	if n["handoff@claude-cloud"]+n["handoff@codex-cloud"] < 4 || len(rows) > 100 {
		t.Fatalf("%v of %d rows", n, len(rows))
	}
}

// Movement operations must exercise every native agent pairing in PR coverage;
// triples and OS-pair jobs use the same model and receipt assertions.
func TestMovementRows(t *testing.T) {
	rows := pairwise(1)
	for _, op := range []string{"roundtrip", "quiet-roundtrip", "fork"} {
		for _, agents := range []string{"claude>claude", "codex>codex", "claude>codex", "codex>claude"} {
			found := false
			for _, r := range rows {
				found = found || r.Op == op && r.From+">"+r.To == agents && r.Location == "machine"
			}
			if !found {
				t.Errorf("no %s %s row", op, agents)
			}
		}
	}
}
