package runtimecases

import (
	"reflect"
	"testing"
)

func TestGeneratedRuntimeRowsCoverEveryValidPairAndTriple(t *testing.T) {
	for _, strength := range []int{2, 3} {
		rows, coverage, err := Select(strength, 19, "", "")
		if err != nil || !coverage.CompleteSelection || coverage.ValidInteractions == 0 {
			t.Fatal(coverage, err)
		}
		for _, candidate := range All() {
			want := candidate.Values()
			for i := range want {
				for j := i + 1; j < len(want); j++ {
					for k := j + 1; k < len(want)+1; k++ {
						if strength == 3 && k == len(want) {
							continue
						}
						found := false
						for _, row := range rows {
							have := row.Values()
							found = found || have[i] == want[i] && have[j] == want[j] && (strength == 2 || have[k] == want[k])
						}
						if !found {
							t.Fatalf("t=%d omitted valid tuple %d,%d,%d from %+v", strength, i, j, k, candidate)
						}
						if strength == 2 {
							break
						}
					}
				}
			}
		}
		again, _, err := Select(strength, 19, "", "")
		if err != nil || !reflect.DeepEqual(rows, again) {
			t.Fatal("same seed changed execution rows", err)
		}
	}
}

func TestNativeModelDoesNotClaimUnsupportedProviderOrNetworkCoverage(t *testing.T) {
	for _, row := range All() {
		if err := row.Validate(); err != nil || row.Provider != "local" || row.Host == "one-shot" && row.Transport != "ssh" || (row.Transport == "relay-https") != (row.Network != "unrestricted") || (row.Integration == "observed" || row.Failure == "grant-revoked") && row.Transport == "ssh" {
			t.Fatal("native slice fabricated unsupported coverage", row, err)
		}
	}
	row := All()[0]
	row.Provider = "claude-hosted"
	if row.Validate() == nil {
		t.Fatal("unimplemented provider variation was accepted")
	}
}

func TestNativeSelectionRejectsEmptyAndMalformedInputs(t *testing.T) {
	for _, c := range []struct {
		t           int
		only, shard string
	}{{0, "", ""}, {4, "", ""}, {2, "99999", ""}, {2, "1,", ""}, {2, "", "1/2oops"}, {2, "", "99/99"}} {
		if _, _, err := Select(c.t, 1, c.only, c.shard); err == nil {
			t.Fatal("invalid selection accepted", c)
		}
	}
	full, _, _ := Select(2, 1, "", "")
	seen := map[int]bool{}
	for _, shard := range []string{"1/2", "2/2"} {
		rows, meta, err := Select(2, 1, "", shard)
		if err != nil || meta.CompleteSelection || meta.SelectedRows != len(rows) {
			t.Fatal("partial selection misreported", meta, err)
		}
		for _, row := range rows {
			if seen[row.N] {
				t.Fatal("shards duplicate rows")
			}
			seen[row.N] = true
		}
	}
	if len(seen) != len(full) {
		t.Fatal("shards omit rows")
	}
}

func TestReportRequiresEveryExactRowToExecute(t *testing.T) {
	rows := All()[:2]
	good := []Result{{Row: rows[0], Status: "passed"}, {Row: rows[1], Status: "passed"}}
	if err := VerifyResults(rows, good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]Result{nil, good[:1], {good[0], good[0]}, {{Row: rows[0], Status: "skipped"}, good[1]}, {{Row: rows[0], Status: "failed"}, good[1]}} {
		if VerifyResults(rows, bad) == nil {
			t.Fatal("missing, duplicate or unexecuted row qualified", bad)
		}
	}
	changed := append([]Result(nil), good...)
	changed[0].Row.Host = "cloud"
	if VerifyResults(rows, changed) == nil {
		t.Fatal("substituted factors qualified requested row")
	}
}
