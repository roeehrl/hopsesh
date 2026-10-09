package config

import "encoding/json"

// Clone gives a background operation its own settings. Config contains only
// JSON-compatible data. Decode into an empty value: decoding into a shallow copy
// reuses its maps, slices and pointers, racing with other readers of those values.
// Callers must hold their configuration lock while capturing the copy.
func (c Config) Clone() Config {
	var out Config
	b, _ := json.Marshal(c)
	_ = json.Unmarshal(b, &out)
	return out
}
