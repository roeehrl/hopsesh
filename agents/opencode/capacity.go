package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// ContextCapacity takes the model from the session's newest reply (for a new session,
// from the newest reply of any) and its window from the models.dev catalog OpenCode
// caches in its database. OpenCode's config file holds provider keys, so it is never read
// (decision D6): a model the catalog does not know (a local provider) gets the fallback
// window. A session's usage is its newest reply's tokens, plus the bytes of what follows;
// after a compaction newer than that reply, the bytes from the compaction on. Where the
// database cannot be read (none yet, another machine's closed WAL, a schema hopsesh does
// not know), an existing session's usage is unknown.
func (m *Module) ContextCapacity(ctx context.Context, h agent.Host, in agent.Install, s *agent.Summary) (ir.Capacity, error) {
	c := ir.Capacity{Window: ir.FallbackWindow, Source: "conservative fallback; OpenCode's model window unknown"}
	r, ok, err := openSQL(ctx, h, in)
	if ok && err == nil {
		err = r.capacity(ctx, s, &c)
	}
	if !ok || errors.Is(err, agent.ErrNotInstalled) || errors.Is(err, agent.ErrUnsupported) {
		fallback := ir.Capacity{Window: ir.FallbackWindow, Source: c.Source, Unknown: s != nil}
		return fallback, nil
	}
	return c, err
}

func (r sqlReader) capacity(ctx context.Context, s *agent.Summary, c *ir.Capacity) error {
	var model modelRef
	if s != nil {
		sid := string(s.Key.Session)
		rows, err := r.sq.Query(ctx, r.db, lastAssistantQuery, sid)
		if err != nil {
			return err
		}
		from := int64(0) // usage counts the bytes of messages after this seq
		if len(rows.Rows) > 0 {
			row := rows.Rows[0]
			_ = json.Unmarshal([]byte(row.String("model")), &model)
			var t tokens
			if json.Unmarshal([]byte(row.String("tokens")), &t) == nil {
				c.Existing = int(t.Input + t.Output + t.Reasoning + t.Cache.Read + t.Cache.Write)
			}
			from = row.Int("seq")
		}
		comp, err := r.sq.Query(ctx, r.db, lastCompactionQuery, sid)
		if err != nil {
			return err
		}
		if len(comp.Rows) > 0 && !comp.Rows[0].Null("seq") && comp.Rows[0].Int("seq") > from {
			// The compaction replaced what came before: its summary and what follows.
			c.Existing, from = 0, comp.Rows[0].Int("seq")-1
		}
		after, err := r.sq.Query(ctx, r.db, afterQuery, sid, from)
		if err != nil {
			return err
		}
		if len(after.Rows) > 0 {
			c.Existing += int(after.Rows[0].Int("n"))
		}
	} else {
		rows, err := r.sq.Query(ctx, r.db, anyAssistantQuery)
		if err != nil {
			return err
		}
		if len(rows.Rows) > 0 {
			_ = json.Unmarshal([]byte(rows.Rows[0].String("model")), &model)
		}
	}
	c.Model = model.String()
	digest, window, err := r.catalogWindow(ctx, model)
	if err != nil {
		return err
	}
	c.ConfigHash = fmt.Sprintf("%x", sha256.Sum256([]byte(c.Model+"\x00"+digest)))
	if window > 0 {
		c.Window, c.Source = window, "models.dev catalog cached by OpenCode"
	}
	return nil
}

// catalogWindow is a model's usable context in the cached models.dev catalog (its input
// limit, else its context), with the catalog's digest; 0 when the catalog or the model is
// not there.
func (r sqlReader) catalogWindow(ctx context.Context, model modelRef) (string, int, error) {
	rows, err := r.sq.Query(ctx, r.db, catalogQuery)
	if err != nil || len(rows.Rows) == 0 {
		return "", 0, err
	}
	var cached struct {
		Digest string `json:"digest"`
		Body   string `json:"body"`
	}
	if json.Unmarshal([]byte(rows.Rows[0].String("value")), &cached) != nil {
		return "", 0, nil
	}
	if model.ID == "" {
		return cached.Digest, 0, nil
	}
	var catalog map[string]struct {
		Models map[string]struct {
			Limit struct {
				Context int `json:"context"`
				Input   int `json:"input"`
			} `json:"limit"`
		} `json:"models"`
	}
	if json.Unmarshal([]byte(cached.Body), &catalog) != nil {
		return cached.Digest, 0, nil
	}
	mm, ok := catalog[model.ProviderID].Models[model.ID]
	if !ok {
		return cached.Digest, 0, nil
	}
	if mm.Limit.Input > 0 {
		return cached.Digest, mm.Limit.Input, nil
	}
	return cached.Digest, mm.Limit.Context, nil
}
