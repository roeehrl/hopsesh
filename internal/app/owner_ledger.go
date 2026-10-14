package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The owner ledger is hopsesh's own record of the accounts a session ran under on this
// machine, written by its prompt hook: it covers what the agent does not record (Codex
// resumes, sessions without Remote Control, older versions). One small file per session,
// under its folder: the same session ID in two folders is two sessions.
type ledgerFile struct {
	Runs []agent.OwnerRun `json:"runs"`
}

func (a *App) ledgerPath(id agent.ID, root, session string) string {
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return filepath.Join(a.StateDir, "owners", string(id), hex.EncodeToString(sum[:8]), session+".json")
}

func validLedgerName(s string) bool {
	return s != "" && len(s) <= 128 && !strings.ContainsAny(s, `/\.`) && !strings.ContainsFunc(s, unicode.IsControl)
}

// RecordOwner notes the account the session's root is signed in to now, as the agent
// starts or takes a prompt. It never fails the hook.
func (a *App) RecordOwner(ctx context.Context, id agent.ID, profile, session string) {
	if !validLedgerName(session) {
		return
	}
	mod, ok := a.Module(id)
	if !ok {
		return
	}
	li, ok := mod.(agent.LocalIdentifier)
	if !ok {
		return
	}
	scope, err := a.noticeScope(ctx, id, profile)
	if err != nil || scope.Root == "" {
		return
	}
	native := false
	if scope.ID == "" || scope.Default {
		native = true
		for _, r := range mod.Spec().Roots {
			for _, env := range r.Env {
				if os.Getenv(env) != "" {
					native = false
				}
			}
		}
	}
	run, ok := li.LocalIdentity(scope.Root, native)
	if !ok {
		// A sign-in hopsesh cannot read here (Codex's keyring): the account last checked
		// for the folder ran it.
		if scope.Account == nil || !scope.Account.LoggedIn || scope.Account.Key == "" {
			return
		}
		run = agent.OwnerRun{Key: scope.Account.Key, Person: scope.Account.Person}
	}
	path := a.ledgerPath(id, scope.Root, session)
	var f ledgerFile
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	next := agent.AddOwner(append([]agent.OwnerRun(nil), f.Runs...), run)
	if len(next) == len(f.Runs) && len(f.Runs) > 0 {
		return
	}
	f.Runs = next
	b, err := json.Marshal(f)
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

// withLedger adds the runs hopsesh recorded for a session on this machine to the ones the
// agent recorded.
func (a *App) withLedger(local bool, root string, s agent.Summary) agent.Summary {
	if !local || root == "" || !validLedgerName(string(s.Key.Session)) {
		return s
	}
	b, err := host.LocalFS().ReadFile(a.ledgerPath(s.Key.Agent, root, string(s.Key.Session)), 64<<10)
	if err != nil {
		return s
	}
	var f ledgerFile
	if json.Unmarshal(b, &f) != nil {
		return s
	}
	// The ledger starts when hopsesh first saw the session run; runs the agent recorded
	// already are not repeated.
	runs := append([]agent.OwnerRun(nil), s.Owners...)
	for _, r := range f.Runs {
		if !slices.ContainsFunc(runs, func(x agent.OwnerRun) bool { return sameRun(x, r) }) || len(runs) > 0 && sameRun(runs[len(runs)-1], r) {
			runs = agent.AddOwner(runs, r)
		}
	}
	s.Owners = runs
	return s
}

func sameRun(a, b agent.OwnerRun) bool {
	return a.Key == b.Key && (a.Person == "" || b.Person == "" || a.Person == b.Person)
}
