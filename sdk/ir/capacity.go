package ir

import (
	"encoding/json"
	"fmt"
)

// FallbackWindow deliberately does not assume the largest model available to an account.
const FallbackWindow = 64_000

// Capacity describes a static, conservative check, not a successful model turn.
type Capacity struct {
	ConfigHash string `json:"configHash,omitempty"`
	Model      string `json:"model,omitempty"`
	Window     int    `json:"window"`
	Source     string `json:"source"`
	Existing   int    `json:"existing"`
	Unknown    bool   `json:"unknown,omitempty"`
}

func (c Capacity) EffectiveWindow() int {
	if c.Window <= 0 {
		return FallbackWindow
	}
	return c.Window
}

// Allowance reserves 30% for instructions, tools and the next response. A single
// transfer uses at most 30%. Counts are UTF-8 byte upper estimates, not a tokenizer.
func (c Capacity) Allowance() int {
	w := c.EffectiveWindow()
	if c.Unknown {
		return 0
	}
	return max(0, min(w*3/10, w*7/10-c.Existing))
}

// ItemCost includes every model-visible tool field and message framing. UTF-8 bytes
// intentionally overestimate byte-based tokenizers instead of relying on bytes/4.
func ItemCost(it Item) int {
	n := len(it.Text) + 32
	if it.Tool != nil {
		b, _ := json.Marshal(it.Tool)
		n += len(b)
	}
	return n
}
func ItemsCost(items []Item) int {
	n := 0
	for _, it := range items {
		n += ItemCost(it)
	}
	return n
}

func (c Capacity) Check(items []Item) error {
	if c.Unknown {
		return fmt.Errorf("active context cannot be measured safely; create a bounded continuation instead")
	}
	if used := ItemsCost(items); used > c.Allowance() {
		return fmt.Errorf("context capacity: incoming upper estimate %d exceeds allowance %d (existing %d, window %d; %s); create a bounded continuation", used, c.Allowance(), c.Existing, c.EffectiveWindow(), c.Source)
	}
	return nil
}
