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

// Fetch rows start in Claude Code's cloud and come into Claude Code or Codex here; no
// other row starts in a cloud. The pull-request tier stays small.
func TestFetchRows(t *testing.T) {
	rows := pairwise(1)
	n := 0
	for _, r := range rows {
		if (r.Op == "fetch") != (r.Location == "claude-cloud") || r.Op == "fetch" && r.From != "claude" {
			t.Fatalf("row %s", r)
		}
		if r.Op == "fetch" {
			n++
		}
	}
	if n < 2 || len(rows) > 60 {
		t.Fatalf("%d fetch rows of %d", n, len(rows))
	}
}
