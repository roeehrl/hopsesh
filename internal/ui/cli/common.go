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

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// run is one command's invocation.
type run struct {
	out     io.Writer
	in      io.Reader
	app     *app.App
	jsonOut bool
	yes     bool
	pw      *passwords
	pwStdin bool
}

// modules is the registry the program was built with (set by NewRoot).
var modules *registry.Registry

func newRun(cmd *cobra.Command) (*run, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	log, _ := audit.Open(filepath.Join(config.StateDir(), "log"))
	r := &run{out: cmd.OutOrStdout(), in: cmd.InOrStdin(), app: app.New(cfg, modules, config.StateDir(), log)}
	r.app.Passwords = r.passwordFor
	r.jsonOut, _ = cmd.Flags().GetBool("json")
	r.yes, _ = cmd.Flags().GetBool("yes")
	r.pwStdin, _ = cmd.Flags().GetBool("password-stdin")
	return r, nil
}

func (r *run) printf(format string, args ...any) { fmt.Fprintf(r.out, format, args...) }

func (r *run) emitJSON(v any) error {
	enc := json.NewEncoder(r.out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// interactive reports whether questions can be asked.
func (r *run) interactive() bool {
	f, ok := r.in.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// confirm asks a yes/no question; --yes answers yes, a non-interactive run answers no.
func (r *run) confirm(q string) bool {
	if r.yes {
		return true
	}
	if !r.interactive() {
		return false
	}
	r.printf("%s [y/N] ", q)
	line, _ := bufio.NewReader(r.in).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func ctxTimeout(minutes int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(minutes)*time.Minute)
}

// scan reads the machines a command needs: one host (and always this machine, which is
// where sessions arrive), or every allowed machine.
func (r *run) scan(cmd *cobra.Command, only string, skipGit bool) *app.Inventory {
	ctx, cancel := ctxTimeout(3)
	defer cancel()
	o := app.ScanOptions{SkipGit: skipGit}
	if only != "" && only != "local" && only != "." {
		o.Hosts = []string{only, app.LocalName()}
		if h := r.app.Cfg.FindHost(only); h != nil && !h.Allowed {
			r.printf("note: %s is not allowed yet; reading it once because you named it\n", only)
			h.Allowed = true
		}
	} else if only != "" {
		o.Hosts = []string{app.LocalName()}
	}
	if noLocal, _ := cmd.Flags().GetBool("no-local"); noLocal {
		o.NoLocal = true
	}
	return r.app.Scan(ctx, o)
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

func shortID(id agent.SessionID) string {
	if len(id) > 8 {
		return string(id[:8])
	}
	return string(id)
}

// branchInfo describes where a session sits in its repository.
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
	case g.AgentWorktree:
		parts = append(parts, b+" (agent worktree)")
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
	up, dirty := g.LeftBehind()
	if up > 0 {
		parts = append(parts, fmt.Sprintf("+%d unpushed", up))
	}
	if dirty > 0 {
		parts = append(parts, fmt.Sprintf("%d dirty", dirty))
	}
	return strings.Join(parts, " · ")
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}
