package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/BurntSushi/toml"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// ContextCapacity reads only configuration and transcript data, never auth files.
// Unknown models use a small fallback; explicit configuration can only lower the cap.
func (m *Module) ContextCapacity(ctx context.Context, h agent.Host, in agent.Install, s *agent.Summary) (ir.Capacity, error) {
	c := ir.Capacity{Window: ir.FallbackWindow, Source: "conservative fallback; effective model window unverified"}
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
			if v > 0 && v < c.Window {
				c.Window = v
				c.Source = "destination config.toml (bounded by conservative fallback)"
			}
		}
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
