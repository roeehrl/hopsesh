// Package config holds hopsesh's user configuration and state locations.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"
)

// Host is one machine hopsesh knows about.
type Host struct {
	Name        string `toml:"name"`        // label shown to the user, e.g. "studio"
	Destination string `toml:"destination"` // what to pass to ssh: alias, user@host or host
	Via         string `toml:"via"`         // tailscale | ssh-config | manual (or a "+" combination)
	Allowed     bool   `toml:"allowed"`     // the user consented to hopsesh connecting
	OS          string `toml:"os,omitempty"`
	// TailscaleName is the machine's MagicDNS name, used when Destination's own host
	// name does not resolve (e.g. an alias pointing at a .local name on another network).
	TailscaleName string `toml:"tailscale_name,omitempty"`
	Helper        bool   `toml:"helper,omitempty"` // the user allowed deploying the remote helper
	// HelperSHA256 pins the helper binary hopsesh uploaded; a helper that no longer
	// matches is not run.
	HelperSHA256 string `toml:"helper_sha256,omitempty"`
}

// Config is the user's configuration file.
type Config struct {
	ReposDir   string `toml:"repos_dir"`   // where clones go; default ~/git
	Layout     string `toml:"layout"`      // flat | ghq
	LivePolicy string `toml:"live_policy"` // handoff | fork
	RemoteCtl  bool   `toml:"remote_control"`
	// UpdateCheck is "on" or "off" once the person has answered whether the app may
	// look for new releases once a day ("" = not asked yet).
	UpdateCheck string `toml:"update_check,omitempty"`
	// Round trips. MarkMoved and SyncCode default to on (nil); PushSource to off.
	MarkMoved  *bool `toml:"mark_moved,omitempty"`  // title the copy left behind "↪ moved to …"
	SyncCode   *bool `toml:"sync_code,omitempty"`   // fetch and fast-forward the checkout here
	PushSource bool  `toml:"push_source,omitempty"` // push unpushed commits on the source first
	// SkillPrompt remembers the answer to "let Claude Code use hopsesh?": "" (not asked),
	// "declined", or the skill revision last offered.
	SkillPrompt string `toml:"skill_prompt,omitempty"`
	Hosts       []Host `toml:"hosts"`
}

// Defaults returns the configuration used when no file exists.
func Defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{ReposDir: filepath.Join(home, "git"), Layout: "flat", LivePolicy: "handoff"}
}

// Dir is the configuration directory: $XDG_CONFIG_HOME/hopsesh or ~/.config/hopsesh on
// macOS and Linux, %APPDATA%\hopsesh on Windows.
func Dir() string {
	if d := os.Getenv("HOPSESH_CONFIG_DIR"); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("APPDATA"); d != "" {
			return filepath.Join(d, "hopsesh")
		}
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "hopsesh")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "hopsesh")
}

// StateDir holds the audit log, undo journal, staging area and hopsesh's own known_hosts:
// $XDG_STATE_HOME/hopsesh or ~/.local/state/hopsesh; %LOCALAPPDATA%\hopsesh on Windows.
func StateDir() string {
	if d := os.Getenv("HOPSESH_STATE_DIR"); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "hopsesh")
		}
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "hopsesh")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "hopsesh")
}

// Path is the configuration file.
func Path() string { return filepath.Join(Dir(), "config.toml") }

// Load reads the configuration, filling defaults for missing values.
func Load() (Config, error) {
	c := Defaults()
	if _, err := toml.DecodeFile(Path(), &c); err != nil && !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	d := Defaults()
	if c.ReposDir == "" {
		c.ReposDir = d.ReposDir
	}
	if c.Layout == "" {
		c.Layout = d.Layout
	}
	if c.LivePolicy == "" {
		c.LivePolicy = d.LivePolicy
	}
	return c, nil
}

// Save writes the configuration atomically with user-only permissions.
func Save(c Config) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, Path())
}

// FindHost returns the configured host with this name.
func (c *Config) FindHost(name string) *Host {
	for i := range c.Hosts {
		if c.Hosts[i].Name == name {
			return &c.Hosts[i]
		}
	}
	return nil
}

// UpsertHost adds or updates a host by name, keeping consent unless explicitly changed.
func (c *Config) UpsertHost(h Host) {
	if old := c.FindHost(h.Name); old != nil {
		allowed, helper := old.Allowed, old.Helper
		*old = h
		old.Allowed = old.Allowed || allowed
		old.Helper = old.Helper || helper
		return
	}
	c.Hosts = append(c.Hosts, h)
}

// MarkMovedOn reports whether copies left behind are marked (default on).
func (c Config) MarkMovedOn() bool { return c.MarkMoved == nil || *c.MarkMoved }

// SyncCodeOn reports whether the checkout is brought to the session's commit (default on).
func (c Config) SyncCodeOn() bool { return c.SyncCode == nil || *c.SyncCode }
