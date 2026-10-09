package main

import (
	"fmt"
	"strconv"
	"strings"
)

// selection records which part of the generated model was actually requested.
// This is selection coverage, not a claim that those rows executed successfully.
type selection struct {
	Model                string   `json:"model"`
	Factors              []string `json:"factors"`
	Strength             int      `json:"strength"`
	Seed                 int64    `json:"seed"`
	Exhaustive           bool     `json:"exhaustive"`
	GeneratedRows        int      `json:"generatedRows"`
	SelectedRows         int      `json:"selectedRows"`
	ValidInteractions    int      `json:"validInteractions"`
	SelectedInteractions int      `json:"selectedInteractions"`
	Complete             bool     `json:"complete"`
	Only                 string   `json:"only,omitempty"`
	Shard                string   `json:"shard,omitempty"`
}

func validateStrength(strength int) error {
	if strength != 2 && strength != 3 {
		return fmt.Errorf("-t must be 2 (pairs) or 3 (triples), got %d", strength)
	}
	return nil
}

func selectedRows(strength int, seed int64, all bool, only, shard string) ([]Row, selection, error) {
	meta := selection{Model: "ssh-and-vendor-cloud", Strength: strength, Seed: seed, Exhaustive: all, Only: only, Shard: shard}
	if err := validateStrength(strength); err != nil {
		return nil, meta, err
	}
	rows := covering(strength, seed)
	if all {
		rows = every()
	}
	meta.GeneratedRows = len(rows)
	if only != "" {
		keep := map[string]bool{}
		for _, s := range strings.Split(only, ",") {
			key := strings.TrimSpace(s)
			if key == "" {
				return nil, meta, fmt.Errorf("-only contains an empty selector")
			}
			keep[key] = false
		}
		var selected []Row
		for _, row := range rows {
			matched := false
			for _, key := range []string{strconv.Itoa(row.N), row.Op} {
				if _, ok := keep[key]; ok {
					keep[key], matched = true, true
				}
			}
			if matched {
				selected = append(selected, row)
			}
		}
		for key, matched := range keep {
			if !matched {
				return nil, meta, fmt.Errorf("-only selector %q matches no generated row", key)
			}
		}
		rows = selected
	}
	if shard != "" {
		left, right, ok := strings.Cut(shard, "/")
		k, errK := strconv.Atoi(left)
		n, errN := strconv.Atoi(right)
		if !ok || errK != nil || errN != nil || n < 1 || k < 1 || k > n {
			return nil, meta, fmt.Errorf("-shard wants k/n with 1 <= k <= n, got %q", shard)
		}
		var selected []Row
		for i, row := range rows {
			if i%n == k-1 {
				selected = append(selected, row)
			}
		}
		rows = selected
	}
	if len(rows) == 0 {
		return nil, meta, fmt.Errorf("matrix selection is empty; no scenarios would execute")
	}
	meta.SelectedRows = len(rows)
	for _, dim := range dims {
		meta.Factors = append(meta.Factors, dim.name)
	}
	positions := tuples(len(dims), strength)
	wanted, selected := map[string]bool{}, map[string]bool{}
	for _, combo := range allCombos() {
		for _, pos := range positions {
			wanted[tkey(pos, combo)] = true
		}
	}
	for _, row := range rows {
		values := []string{row.Op, row.From + ">" + row.To, row.Content, row.Repo, row.Naming, row.Location}
		for _, pos := range positions {
			selected[tkey(pos, values)] = true
		}
	}
	meta.ValidInteractions, meta.SelectedInteractions = len(wanted), len(selected)
	meta.Complete = meta.ValidInteractions == meta.SelectedInteractions
	return rows, meta, nil
}
