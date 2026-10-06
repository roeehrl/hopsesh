package config

import "fmt"

// Desktop controls application presence, independently of terminal preferences.
// Empty Mode preserves the behavior of an existing installation.
type Desktop struct {
	Mode      string `toml:"mode,omitempty" json:"mode"`   // app | tray | both
	Close     string `toml:"close,omitempty" json:"close"` // keep | quit; empty preserves legacy behavior
	Attention *bool  `toml:"attention,omitempty" json:"attention"`
	Previews  *bool  `toml:"previews,omitempty" json:"previews"`
}

func (d Desktop) Placement() string {
	if d.Mode == "" {
		return "app"
	}
	return d.Mode
}
func (d Desktop) AttentionOn() bool { return d.Attention == nil || *d.Attention }
func (d Desktop) PreviewsOn() bool  { return d.Previews == nil || *d.Previews }
func (d Desktop) Check() error {
	switch d.Mode {
	case "", "app", "tray", "both":
	default:
		return fmt.Errorf("desktop.mode must be app, tray or both")
	}
	switch d.Close {
	case "", "keep", "quit":
	default:
		return fmt.Errorf("desktop.close must be keep or quit")
	}
	return nil
}
