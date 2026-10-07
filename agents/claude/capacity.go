package claude

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// ContextCapacity follows the active branch, resetting only on an explicit readable
// compaction summary. The default model is account-dependent: do not assume 1M.
func (m *Module) ContextCapacity(ctx context.Context, h agent.Host, in agent.Install, s *agent.Summary) (ir.Capacity, error) {
	c := ir.Capacity{Window: ir.FallbackWindow, Source: "conservative fallback; effective Claude model window unverified"}
	raw, e := h.FS().ReadFile(h.Path().Join(in.Root(home), "settings.json"), 1<<20)
	if e != nil && !errors.Is(e, fs.ErrNotExist) {
		return c, e
	}
	if e == nil {
		var cfg struct {
			Model string `json:"model"`
		}
		if e = json.Unmarshal(raw, &cfg); e != nil {
			return c, e
		}
		c.Model = cfg.Model
		c.ConfigHash = fmt.Sprintf("%x", sha256.Sum256(raw))
	}
	if s == nil {
		return c, nil
	}
	f, err := h.FS().Open(s.Path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	recs, _, err := readRecords(&ir.BoundedReader{Context: ctx, Reader: f})
	if err != nil {
		return c, err
	}
	for _, i := range activeBranch(recs) {
		r := recs[i]
		if r.IsCompactSummary {
			c.Existing = 0
		}
		if r.Message != nil {
			c.Existing += len(r.Message.Content) + 32
		}
	}
	return c, nil
}
