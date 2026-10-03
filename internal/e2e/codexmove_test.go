package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// seedCodex copies the Codex fixtures into a location, pointing their paths at its repo.
func seedCodex(t *testing.T, l location) agent.Install {
	t.Helper()
	in := codexInstall(l)
	fix := "../../agents/codex/testdata/0.153.2"
	err := filepath.Walk(fix, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || filepath.Base(p) == "auth.json" {
			return err
		}
		rel, _ := filepath.Rel(fix, p)
		b, _ := os.ReadFile(p)
		b = []byte(strings.ReplaceAll(string(b), "/home/u/git/demo", jsonText(l.repo)))
		dst := filepath.Join(in.Root("home"), rel)
		os.MkdirAll(filepath.Dir(dst), 0o700)
		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// A Codex session moved to a machine signed in to another account loses its encrypted
// reasoning and compaction (bound to the account that produced them), and keeps the
// conversation; under the same account it arrives whole.
func TestCodexMoveAcrossAccounts(t *testing.T) {
	root := t.TempDir()
	box, here := newLocation(t, "box", root), newLocation(t, "here", root)
	boxIn := seedCodex(t, box)
	hereIn := codexInstall(here)
	os.MkdirAll(filepath.Join(hereIn.Root("home"), "sessions"), 0o700)
	cx := codex.New()
	var s agent.Summary
	for _, x := range listAgent(t, box, cx, boxIn) {
		if x.Key.Session == "01a0fe1c-0000-7000-8000-000000000001" {
			s = x
		}
	}
	if s.Path == "" {
		t.Fatal("fixture thread not listed")
	}
	original, _ := os.ReadFile(s.Path)
	if !strings.Contains(string(original), "encrypted_content") {
		t.Fatal("the fixture should carry encrypted content")
	}
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		src, dst  string
		encrypted bool
	}{
		{"same account", "chatgpt:aa", "chatgpt:aa", true},
		{"another account", "chatgpt:aa", "chatgpt:bb", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := move.Input{Source: move.Side{Machine: box.m, Module: cx, Install: boxIn, Account: &agent.Account{Key: tc.src}}, Session: s,
				Target: move.Side{Machine: here.m, Module: cx, Install: hereIn, Account: &agent.Account{Key: tc.dst}}}
			p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
			if err != nil || len(p.Blockers) > 0 {
				t.Fatalf("%v %v", err, p.Blockers)
			}
			res, err := move.Apply(ctx, p, in, move.Env{StateDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			var got agent.Summary
			for _, x := range listAgent(t, here, cx, hereIn) {
				if x.Key == s.Key {
					got = x
				}
			}
			b, _ := os.ReadFile(got.Path)
			if strings.Contains(string(b), "encrypted_content") != tc.encrypted {
				t.Fatalf("encrypted content kept=%v, want %v", !tc.encrypted, tc.encrypted)
			}
			if !strings.Contains(string(b), "codeword") {
				t.Fatal("the conversation must stay")
			}
			if !tc.encrypted && res.Rewrite.DroppedRecords == 0 {
				t.Fatal("records should be reported as dropped")
			}
			os.Remove(got.Path)
		})
	}
}
