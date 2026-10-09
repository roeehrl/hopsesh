// Package runtimecases defines executable runtime qualification inputs shared
// by hsmatrix and the native E2E harness. This native slice does not claim the
// full provider/network-policy model in the 0.5 design.
package runtimecases

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
)

const Model = "runtime-native"

type Row struct {
	N           int    `json:"n"`
	Host        string `json:"runtimeHost"`
	Transport   string `json:"transport"`
	Provider    string `json:"providerGeneration"`
	Integration string `json:"integrationLevel"`
	Network     string `json:"networkPolicy"`
	Scope       string `json:"accountScope"`
	Topology    string `json:"branchTopology"`
	Failure     string `json:"failurePoint"`
}

func (r Row) Values() []string {
	return []string{r.Host, r.Transport, r.Provider, r.Integration, r.Network, r.Scope, r.Topology, r.Failure}
}

func (r Row) String() string {
	return fmt.Sprintf("%03d/%s/%s/%s/%s/%s/%s/%s", r.N, r.Host, r.Transport, r.Network, r.Integration, r.Scope, r.Topology, r.Failure)
}

var Factors = []string{"runtimeHost", "transport", "providerGeneration", "integrationLevel", "networkPolicy", "accountScope", "branchTopology", "failurePoint"}

// Domains describe only values the executable native harness currently varies.
// The fixed local provider remains visible so it cannot be mistaken for real
// cloud-provider qualification.
var Domains = [][]string{
	{"one-shot", "desktop", "headless"},
	{"ssh", "relay-websocket", "relay-https"},
	{"local"},
	{"observed", "exportable"},
	{"unrestricted", "websocket-blocked", "post-blocked", "untrusted-ca"},
	{"approved", "receive-disabled"},
	{"linear", "fork"},
	{"none", "owner-restart", "grant-revoked", "sender-restart-after-apply", "receiver-restart-after-apply"},
}

func (r Row) Validate() error {
	for i, value := range r.Values() {
		found := false
		for _, supported := range Domains[i] {
			found = found || supported == value
		}
		if !found {
			return fmt.Errorf("unsupported %s value %q", Factors[i], value)
		}
	}
	if r.Host == "one-shot" && r.Transport != "ssh" {
		return fmt.Errorf("a one-shot sender has no relay runtime owner")
	}
	if (r.Transport == "relay-https") != (r.Network != "unrestricted") {
		return fmt.Errorf("HTTPS policy rows must exercise blocked upgrades, blocked POST or untrusted TLS")
	}
	if r.Integration == "observed" && r.Transport == "ssh" {
		return fmt.Errorf("session-scoped observation-only grants belong to the relay; SSH login authority is separate")
	}
	if r.Failure == "grant-revoked" && r.Transport == "ssh" {
		return fmt.Errorf("relay grant revocation does not revoke independent SSH login authority")
	}
	if strings.HasSuffix(r.Failure, "-restart-after-apply") && (r.Transport == "ssh" || r.Integration != "exportable" || r.Scope != "approved" || r.Network == "post-blocked" || r.Network == "untrusted-ca") {
		return fmt.Errorf("post-apply runtime recovery requires an approved, deliverable native relay transfer")
	}
	return nil
}

func All() []Row {
	var rows []Row
	var walk func([]string)
	walk = func(v []string) {
		if len(v) == len(Domains) {
			r := Row{Host: v[0], Transport: v[1], Provider: v[2], Integration: v[3], Network: v[4], Scope: v[5], Topology: v[6], Failure: v[7]}
			if r.Validate() == nil {
				r.N = len(rows) + 1
				rows = append(rows, r)
			}
			return
		}
		for _, value := range Domains[len(v)] {
			walk(append(v, value))
		}
	}
	walk(nil)
	return rows
}

type Coverage struct {
	Model                string     `json:"model"`
	Constraints          []string   `json:"constraints"`
	Factors              []string   `json:"factors"`
	Domains              [][]string `json:"domains"`
	Strength             int        `json:"strength"`
	Seed                 int64      `json:"seed"`
	GeneratedRows        int        `json:"generatedRows"`
	SelectedRows         int        `json:"selectedRows"`
	ValidInteractions    int        `json:"validInteractions"`
	SelectedInteractions int        `json:"selectedInteractions"`
	CompleteSelection    bool       `json:"completeSelection"`
}

// Select generates stable pair/triple coverage before applying row/shard
// filters. IDs identify full model combinations and never change with a seed.
func Select(strength int, seed int64, only, shard string) ([]Row, Coverage, error) {
	meta := Coverage{Model: Model, Factors: Factors, Domains: Domains, Strength: strength, Seed: seed,
		Constraints: []string{"one-shot senders use SSH because no local relay owner is running", "relay-https varies blocked upgrades, denied POST and untrusted TLS; other transports use unrestricted networking", "all rows use unverified portable native account boundaries; scope varies peer receiving consent", "observation-only and grant revocation apply to the relay, not SSH login authority", "post-apply restart/retry requires an exportable approved relay transfer without network denial", "provider generation is local; cloud/provider capability coverage is separate"}}
	if strength != 2 && strength != 3 {
		return nil, meta, fmt.Errorf("strength must be 2 or 3")
	}
	all := All()
	wanted := interactions(all, strength)
	uncovered := interactions(all, strength)
	rnd := rand.New(rand.NewSource(seed))
	rnd.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	var rows []Row
	for len(uncovered) > 0 {
		best, score := -1, 0
		for i, row := range all {
			n := 0
			for key := range interactions([]Row{row}, strength) {
				if uncovered[key] {
					n++
				}
			}
			if n > score {
				best, score = i, n
			}
		}
		if best == -1 {
			return nil, meta, fmt.Errorf("generator could not cover %d valid interactions", len(uncovered))
		}
		row := all[best]
		rows = append(rows, row)
		for key := range interactions([]Row{row}, strength) {
			delete(uncovered, key)
		}
		all = append(all[:best], all[best+1:]...)
	}
	meta.GeneratedRows, meta.ValidInteractions = len(rows), len(wanted)
	if only != "" {
		keep := map[int]bool{}
		for _, s := range strings.Split(only, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || n < 1 {
				return nil, meta, fmt.Errorf("invalid row selector %q", s)
			}
			keep[n] = false
		}
		var selected []Row
		for _, r := range rows {
			if _, ok := keep[r.N]; ok {
				selected, keep[r.N] = append(selected, r), true
			}
		}
		for n, found := range keep {
			if !found {
				return nil, meta, fmt.Errorf("row %d is not in this generated selection", n)
			}
		}
		rows = selected
	}
	if shard != "" {
		left, right, ok := strings.Cut(shard, "/")
		k, errK := strconv.Atoi(left)
		n, errN := strconv.Atoi(right)
		if !ok || errK != nil || errN != nil || k < 1 || n < k {
			return nil, meta, fmt.Errorf("invalid shard %q; expected k/n", shard)
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
		return nil, meta, fmt.Errorf("empty matrix selection")
	}
	meta.SelectedRows = len(rows)
	meta.SelectedInteractions = len(interactions(rows, strength))
	meta.CompleteSelection = meta.SelectedInteractions == meta.ValidInteractions
	return rows, meta, nil
}

func interactions(rows []Row, strength int) map[string]bool {
	set := map[string]bool{}
	for _, row := range rows {
		v := row.Values()
		for i := 0; i < len(v); i++ {
			for j := i + 1; j < len(v); j++ {
				key := fmt.Sprintf("%d=%s|%d=%s", i, v[i], j, v[j])
				if strength == 2 {
					set[key] = true
				} else {
					for k := j + 1; k < len(v); k++ {
						set[fmt.Sprintf("%s|%d=%s", key, k, v[k])] = true
					}
				}
			}
		}
	}
	return set
}

type Result struct {
	Row     Row     `json:"row"`
	Status  string  `json:"status"`
	Seconds float64 `json:"seconds"`
}

// VerifyResults prevents missing, duplicated, skipped or substituted cases
// from qualifying a generated selection. A passing test process alone is not
// evidence that the requested rows ran.
func VerifyResults(rows []Row, results []Result) error {
	if len(rows) == 0 || len(results) != len(rows) {
		return fmt.Errorf("expected %d nonempty results, received %d", len(rows), len(results))
	}
	seen := map[int]bool{}
	for i, row := range rows {
		r := results[i]
		if r.Row != row || seen[row.N] || r.Status != "passed" {
			return fmt.Errorf("row %d did not execute successfully with the requested factors", row.N)
		}
		seen[row.N] = true
	}
	return nil
}
