package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/inventory"
)

// app holds per-invocation state shared by commands.
type app struct {
	out     io.Writer
	cfg     config.Config
	log     *audit.Log
	jsonOut bool
	yes     bool
	in      io.Reader
}

func newApp(cmd *cobra.Command) (*app, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", config.Path(), err)
	}
	log, _ := audit.Open(filepath.Join(config.StateDir(), "log"))
	a := &app{out: cmd.OutOrStdout(), cfg: cfg, log: log, in: cmd.InOrStdin()}
	a.jsonOut, _ = cmd.Flags().GetBool("json")
	a.yes, _ = cmd.Flags().GetBool("yes")
	return a, nil
}

func (a *app) printf(format string, args ...any) { fmt.Fprintf(a.out, format, args...) }

func (a *app) emitJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// interactive reports whether we can ask the user questions.
func (a *app) interactive() bool {
	f, ok := a.in.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// confirm asks a yes/no question; --yes answers yes, non-interactive answers no.
func (a *app) confirm(q string) bool {
	if a.yes {
		return true
	}
	if !a.interactive() {
		return false
	}
	a.printf("%s [y/N] ", q)
	line, _ := bufio.NewReader(a.in).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func (a *app) scanner() *inventory.Scanner {
	return &inventory.Scanner{StateDir: config.StateDir(), Log: a.log}
}

func (a *app) localRoots() []string {
	home, _ := os.UserHomeDir()
	return append([]string{a.cfg.ReposDir}, repos.DefaultRoots(home)...)
}

func ctxTimeout(minutes int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(minutes)*time.Minute)
}

// splitRef parses "<host>:<query>". A bare query (no known host prefix) returns host "".
func (a *app) splitRef(ref string) (host, query string) {
	if h, q, ok := strings.Cut(ref, ":"); ok {
		if h == "local" || h == "." || h == inventory.LocalHostName() || a.cfg.FindHost(h) != nil {
			return h, q
		}
	}
	return "", ref
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Local().Format("2 Jan")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func statusOf(s *inventory.Session) string {
	if s.Live != nil {
		st := s.Live.Status
		if st == "" {
			st = "running"
		}
		return "live " + st
	}
	return "ended"
}

// branchInfo describes where a session sits in its repository: the branch, whether it is
// a worktree (and whose), and what the main checkout has checked out.
func branchInfo(g *repos.GitState) string {
	if g == nil || !g.IsRepo {
		return ""
	}
	b := g.Branch
	if b == "" {
		b = "detached@" + g.Head
	}
	var parts []string
	switch {
	case g.ClaudeWorktree:
		parts = append(parts, b+" (Claude worktree)")
	case g.LinkedWorktree:
		parts = append(parts, b+" (worktree)")
	default:
		parts = append(parts, b)
	}
	if g.LinkedWorktree && g.MainBranch != "" {
		parts = append(parts, "main folder on "+g.MainBranch)
	}
	if !g.LinkedWorktree {
		if other := g.BranchCheckedOutElsewhere(); other != "" {
			parts = append(parts, b+" also in worktree "+other)
		}
	}
	if n := len(g.Worktrees); n > 1 {
		parts = append(parts, fmt.Sprintf("%d worktrees", n))
	}
	up, dirty := g.LeftBehind()
	if up > 0 {
		parts = append(parts, fmt.Sprintf("+%d unpushed", up))
	}
	if dirty > 0 {
		parts = append(parts, fmt.Sprintf("%d dirty", dirty))
	}
	return strings.Join(parts, " · ")
}
