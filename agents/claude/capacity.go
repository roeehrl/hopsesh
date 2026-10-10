package claude

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// ContextCapacity follows only the resumed branch. Native usage measures the last
// completed prompt; later records use a conservative byte upper estimate. A
// compaction summary resets that baseline. Unknown models keep the small fallback.
func (m *Module) ContextCapacity(ctx context.Context, h agent.Host, in agent.Install, s *agent.Summary) (ir.Capacity, error) {
	c := ir.Capacity{Window: ir.FallbackWindow, Source: "conservative fallback; effective Claude model window unverified"}
	var cfg struct {
		Model   string            `json:"model"`
		Compact int               `json:"autoCompactWindow"`
		Env     map[string]string `json:"env"`
		Models  map[string]struct {
			Compact int `json:"autoCompactWindow"`
		} `json:"modelSettings"`
	}
	// Hash every consulted settings scope so a plan cannot survive a changed
	// project limit. Reads use the receiving machine, including over SSH.
	paths := []string{h.Path().Join(in.Root(home), "settings.json")}
	if s != nil && s.CWD != "" {
		paths = append(paths, h.Path().Join(s.CWD, ".claude", "settings.json"), h.Path().Join(s.CWD, ".claude", "settings.local.json"))
	}
	cfg.Env = map[string]string{}
	rawConfig := []string{}
	for _, path := range paths {
		raw, e := h.FS().ReadFile(path, 1<<20)
		if errors.Is(e, fs.ErrNotExist) {
			rawConfig = append(rawConfig, "")
			continue
		}
		if e != nil {
			return c, e
		}
		part := cfg
		part.Env = nil
		part.Models = nil
		if e = json.Unmarshal(raw, &part); e != nil {
			return c, e
		}
		cfg.Model, cfg.Compact = part.Model, part.Compact
		for k, v := range part.Env {
			cfg.Env[k] = v
		}
		if part.Models != nil {
			cfg.Models = part.Models
		}
		rawConfig = append(rawConfig, string(raw))
	}
	env := func(key string) string {
		if v, ok := cfg.Env[key]; ok {
			return v
		}
		return h.Facts().Env[key]
	}
	config, _ := json.Marshal([]any{rawConfig, env("ANTHROPIC_MODEL"), env("CLAUDE_CODE_DISABLE_1M_CONTEXT"), env("CLAUDE_CODE_AUTO_COMPACT_WINDOW")})
	c.ConfigHash = fmt.Sprintf("%x", sha256.Sum256(config))
	c.Model = cfg.Model
	if v := env("ANTHROPIC_MODEL"); v != "" {
		c.Model = v
	}
	if s != nil {
		f, err := h.FS().Open(s.Path)
		if err != nil {
			return c, err
		}
		defer f.Close()
		recs, _, err := readRecords(&ir.BoundedReader{Context: ctx, Reader: f})
		if err != nil {
			return c, err
		}
		nativeModel := ""
		for _, i := range activeBranch(recs) {
			r := recs[i]
			if r.IsCompactSummary {
				c.Existing = 0
			}
			if r.Message == nil {
				continue
			}
			msg := r.Message
			if msg.Model != "" && msg.Model != "<synthetic>" {
				nativeModel = msg.Model
			}
			u := msg.Usage
			if r.Type == "assistant" && u != nil && u.Input >= 0 && u.CacheRead >= 0 && u.CacheCreate >= 0 && u.Output >= 0 && u.Input+u.CacheRead+u.CacheCreate > 0 {
				// Streaming blocks can repeat the same usage. Replacement, not addition,
				// prevents both cached input and repeated assistant blocks being counted twice.
				c.Existing = u.Input + u.CacheRead + u.CacheCreate + u.Output
				c.Source = "native Claude request usage plus subsequent record byte upper estimates"
			} else {
				c.Existing += len(msg.Content) + 32
			}
		}
		if c.Model == "" {
			c.Model = nativeModel
		}
	}
	// Only exact model IDs with a documented default 1M window qualify. An alias,
	// old opt-in [1m] variant, or custom gateway never grants a larger cap.
	switch c.Model {
	case "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-opus-5-5", "claude-sonnet-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5", "claude-fable-5-1":
		c.Window = 1_000_000
		c.Source += "; documented default 1M model window"
	}
	if v := env("CLAUDE_CODE_DISABLE_1M_CONTEXT"); v == "1" || v == "true" {
		c.Window = min(c.Window, 200_000)
	}
	compact := cfg.Compact
	if model, ok := cfg.Models[c.Model]; ok && model.Compact > 0 {
		compact = model.Compact
	}
	if v := env("CLAUDE_CODE_AUTO_COMPACT_WINDOW"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			c.Unknown = true
		} else {
			compact = n
		}
	}
	if compact > 0 {
		c.Window = min(c.Window, compact)
	}
	return c, nil
}
