package config

import "encoding/json"

// Clone gives a background operation its own settings while retaining the source
// revision for compare-and-swap saves. Config contains only JSON-compatible data.
// Decode into an empty value: decoding into a shallow copy reuses its maps,
// slices and pointers, and even the act of copying can race with other readers.
// Callers must hold their configuration lock while capturing the copy.
func (c Config) Clone() Config {
	var out Config
	b, _ := json.Marshal(c)
	_ = json.Unmarshal(b, &out)
	out.revision = c.revision
	return out
}
