package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// ContextCapacity reads only configuration and transcript data, never auth files.
// Unknown models use a small fallback; explicit configuration can only lower the cap.
func (m *Module) ContextCapacity(ctx context.Context, h agent.Host, in agent.Install, s *agent.Summary) (ir.Capacity, error) {
	c := ir.Capacity{Window: ir.FallbackWindow, Source: "conservative fallback; effective model window unverified"}
	configured := 0 // the lowest window or compaction limit in config.toml
	raw, err := h.FS().ReadFile(h.Path().Join(in.Root(home), "config.toml"), 1<<20)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return c, err
	}
	if err == nil {
		c.ConfigHash = fmt.Sprintf("%x", sha256.Sum256(raw))
		var cfg struct {
			Model    string `toml:"model"`
			Window   int    `toml:"model_context_window"`
			Compact  int    `toml:"model_auto_compact_token_limit"`
			Profile  string `toml:"profile"`
			Profiles map[string]struct {
				Model   string `toml:"model"`
				Window  int    `toml:"model_context_window"`
				Compact int    `toml:"model_auto_compact_token_limit"`
			} `toml:"profiles"`
		}
		if _, err = toml.Decode(string(raw), &cfg); err != nil {
			return c, err
		}
		if p, ok := cfg.Profiles[cfg.Profile]; ok {
			if p.Model != "" {
				cfg.Model = p.Model
			}
			if p.Window > 0 {
				cfg.Window = p.Window
			}
			if p.Compact > 0 {
				cfg.Compact = p.Compact
			}
		}
		c.Model = cfg.Model
		for _, v := range []int{cfg.Window, cfg.Compact} {
			if v > 0 && (configured == 0 || v < configured) {
				configured = v
			}
		}
	}
	// Codex records the window it actually serves in its token_count events. Use the
	// session's own, or for a new session the newest rollouts'; configuration still lowers it.
	observedFrom := []string{}
	if s != nil {
		observedFrom = append(observedFrom, s.Path)
	}
	observedFrom = append(observedFrom, recentRollouts(h, in, 5)...)
	for _, p := range observedFrom {
		if w := observedWindow(h.FS(), p); w > 0 {
			c.Window, c.Source = w, "window reported by Codex in its own rollout"
			break
		}
	}
	if configured > 0 && configured < c.Window {
		c.Window, c.Source = configured, "destination config.toml (bounded by "+c.Source+")"
	}
	if s == nil {
		return c, nil
	}
	f, err := h.FS().Open(s.Path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	recs, _, err := readAnalysisLines(ctx, f)
	if err != nil {
		return c, err
	}
	if len(recs) > 0 {
		var mt meta
		_ = json.Unmarshal(recs[0].Payload, &mt)
		if _, err = paginatedNext(mt, recs); err != nil {
			return c, err
		}
	}
	for _, r := range recs {
		switch r.Type {
		case "compacted":
			if r.compactBytes == nil {
				c.Unknown = true
				continue
			}
			c.Existing = *r.compactBytes
			c.Unknown = false
		case "response_item":
			c.Existing += len(r.Payload) + 32
		}
	}
	return c, nil
}

var windowPattern = regexp.MustCompile(`"model_context_window":(\d+)`)

// observedWindow is the last model_context_window in a rollout's tail.
func observedWindow(fsys agent.FS, p string) int {
	f, err := fsys.Open(p)
	if err != nil {
		return 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0
	}
	n := min(fi.Size(), 512<<10)
	buf := make([]byte, n)
	if _, err = f.ReadAt(buf, fi.Size()-n); err != nil && !errors.Is(err, io.EOF) {
		return 0
	}
	m := windowPattern.FindAllSubmatch(buf, -1)
	if len(m) == 0 {
		return 0
	}
	w, _ := strconv.Atoi(string(m[len(m)-1][1]))
	if w < 1000 || w > 10_000_000 {
		return 0
	}
	return w
}

// recentRollouts are the newest rollout files (sessions/YYYY/MM/DD/*.jsonl).
func recentRollouts(h agent.Host, in agent.Install, limit int) []string {
	fsys, pa := h.FS(), h.Path()
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := fsys.ReadDir(dir)
		if err != nil {
			return
		}
		if depth == 3 {
			sort.Slice(entries, func(i, j int) bool { return entries[i].ModTime().After(entries[j].ModTime()) })
			for _, e := range entries {
				if len(out) < limit && !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
					out = append(out, pa.Join(dir, e.Name()))
				}
			}
			return
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
		for _, e := range entries {
			if len(out) >= limit {
				return
			}
			if e.IsDir() {
				walk(pa.Join(dir, e.Name()), depth+1)
			}
		}
	}
	walk(pa.Join(in.Root(home), "sessions"), 0)
	return out
}
