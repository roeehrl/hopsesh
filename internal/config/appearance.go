package config

import "fmt"

// AppearanceMode fills in the default without pinning System to today's OS color.
func (c Config) AppearanceMode() string {
	if c.Appearance == "" {
		return "system"
	}
	return c.Appearance
}

func CheckAppearance(mode string) error {
	switch mode {
	case "", "system", "light", "dark":
		return nil
	default:
		return fmt.Errorf("appearance is %q: use system, light or dark", mode)
	}
}
