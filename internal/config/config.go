// Package config holds hopsesh's user configuration and state locations.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/BurntSushi/toml"
)

// Schema is the configuration format. Files in another format are refused, never read:
// hopsesh keeps no code for older formats.
const Schema = 4

// ErrOldConfig means the configuration file was written by an older hopsesh.
var ErrOldConfig = errors.New("the configuration was written by an older hopsesh")

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
	// Auth is "password" for a machine that logs in with a password (asked for, never
	// stored in this file); "" means keys or the SSH agent.
	Auth string `toml:"auth,omitempty"`
	// Keychain: remember this machine's password in the macOS Keychain.
	Keychain bool `toml:"keychain,omitempty"`
}

// Agent is per-agent configuration, by module id.
type Agent struct {
	// Disabled leaves the agent out of scans and moves.
	Disabled bool `toml:"disabled,omitempty"`
	// RemoteControl turns the agent's own remote control on for continued sessions.
	RemoteControl bool `toml:"remote_control,omitempty"`
	// Import has the agent's own importer convert sessions continued in it, by default.
	Import bool `toml:"import,omitempty"`
}

// Cloud is per-cloud configuration, by the cloud's name ("codex-cloud"). Like a machine,
// a cloud is left alone until the user allows it.
type Cloud struct {
	// Allowed: the user consented to hopsesh running the agent's cloud commands.
	Allowed bool `toml:"allowed"`
	// Code is how code goes up: "branch" (a pushed handoff branch, the default) or "bundle"
	// (the agent uploads the repository itself, where it can).
	Code string `toml:"code,omitempty"`
	// HistoryFile commits the conversation as .hopsesh/handoff.md on the handoff branch
	// (off by default: it puts conversation text on the repository's host).
	HistoryFile bool `toml:"history_file,omitempty"`
	// Untracked are untracked files (globs) carried up by default; credential-like files
	// are never carried, whatever this says.
	Untracked []string `toml:"untracked,omitempty"`
	// BranchPrefix starts the handoff branches' names (default "hopsesh/handoff/").
	BranchPrefix string `toml:"branch_prefix,omitempty"`
	// DeleteBranch is when a handoff branch is deleted: "never", "after-merge" (the
	// default) or "on-undo".
	DeleteBranch string `toml:"delete_branch,omitempty"`
	// RenameVendorBranches brings a cloud's own branches (claude/…) home under
	// hopsesh/from/<cloud>/… (default on).
	RenameVendorBranches *bool `toml:"rename_vendor_branches,omitempty"`
	// Environments are the vendor's environment ids by repository identity
	// ("github.com/acme/api" = "env_…"), for clouds that need one.
	Environments map[string]string `toml:"environments,omitempty"`
}

// Defaults of a cloud's settings.
const (
	CloudCodeBranch     = "branch"
	CloudCodeBundle     = "bundle"
	DefaultBranchPrefix = "hopsesh/handoff/"
	DeleteNever         = "never"
	DeleteAfterMerge    = "after-merge"
	DeleteOnUndo        = "on-undo"
)

// Peer is how this machine works with hopsesh on other machines.
type Peer struct {
	// Receive lets hopsesh on another machine send sessions here (off by default).
	Receive bool `toml:"receive,omitempty"`
}

// Config is the user's configuration file.
type Config struct {
	Schema   int    `toml:"schema"`
	ReposDir string `toml:"repos_dir"` // where clones go; default ~/git
	Layout   string `toml:"layout"`    // flat | ghq
	// UpdateCheck is "on" or "off" once the person has answered whether the app may
	// look for new releases once a day ("" = not asked yet).
	UpdateCheck string `toml:"update_check,omitempty"`
	// Round trips. MarkMoved and SyncCode default to on (nil); PushSource to off.
	MarkMoved  *bool `toml:"mark_moved,omitempty"`  // mark the copy left behind
	SyncCode   *bool `toml:"sync_code,omitempty"`   // fetch and fast-forward the checkout here
	PushSource bool  `toml:"push_source,omitempty"` // push unpushed commits on the source first
	// SkillPrompt remembers the answer to "let your agents use hopsesh?": "" (not asked),
	// "declined", or the skill revision last offered.
	SkillPrompt string `toml:"skill_prompt,omitempty"`
	// CLIPrompt is "declined" once the user said not now to linking the command-line tool
	// from the app.
	CLIPrompt string `toml:"cli_prompt,omitempty"`
	// AppIcons shows an agent's installed desktop app icon in the app (default on).
	AppIcons *bool            `toml:"app_icons,omitempty"`
	Agents   map[string]Agent `toml:"agents,omitempty"`
	// Clouds are the vendor clouds the user allowed or set up, by name.
	Clouds map[string]Cloud `toml:"clouds,omitempty"`
	Peer   Peer             `toml:"peer"`
	Hosts  []Host           `toml:"hosts"`
}

// Defaults returns the configuration used when no file exists.
func Defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{Schema: Schema, ReposDir: filepath.Join(home, "git"), Layout: "flat"}
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

// StateDir holds journals, lineage caches, the audit log, staging and hopsesh's own
// known_hosts: $XDG_STATE_HOME/hopsesh or ~/.local/state/hopsesh; %LOCALAPPDATA%\hopsesh
// on Windows.
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

// Load reads the configuration, filling defaults for missing values. A file in another
// format is refused with ErrOldConfig.
func Load() (Config, error) {
	c := Defaults()
	b, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	var probe struct {
		Schema int `toml:"schema"`
	}
	if _, err := toml.NewDecoder(bytes.NewReader(b)).Decode(&probe); err != nil || probe.Schema != Schema {
		return Defaults(), fmt.Errorf("%w: %s (move it aside; hopsesh starts fresh and you add your machines again)", ErrOldConfig, Path())
	}
	if _, err := toml.NewDecoder(bytes.NewReader(b)).Decode(&c); err != nil {
		return Defaults(), err
	}
	if err := c.Check(); err != nil {
		return Defaults(), fmt.Errorf("%s: %w", Path(), err)
	}
	d := Defaults()
	if c.ReposDir == "" {
		c.ReposDir = d.ReposDir
	}
	if c.Layout == "" {
		c.Layout = d.Layout
	}
	return c, nil
}

// Save writes the configuration atomically with user-only permissions.
func Save(c Config) error {
	c.Schema = Schema
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

// SetAside renames an older configuration file out of the way (to config.toml.old-<time>)
// so hopsesh can start fresh; it returns the new name.
func SetAside() (string, error) {
	dst := Path() + ".old-" + time.Now().Format("20060102-150405")
	if err := os.Rename(Path(), dst); err != nil {
		return "", err
	}
	return dst, nil
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
		allowed := old.Allowed
		*old = h
		old.Allowed = old.Allowed || allowed
		return
	}
	c.Hosts = append(c.Hosts, h)
}

// AgentEnabled reports whether an agent module is in use.
func (c Config) AgentEnabled(id string) bool { return !c.Agents[id].Disabled }

// MarkMovedOn reports whether copies left behind are marked (default on).
func (c Config) MarkMovedOn() bool { return c.MarkMoved == nil || *c.MarkMoved }

// AppIconsOn reports whether installed desktop apps' icons picture the agents (default on).
func (c Config) AppIconsOn() bool { return c.AppIcons == nil || *c.AppIcons }

// SyncCodeOn reports whether the checkout is brought to the session's commit (default on).
func (c Config) SyncCodeOn() bool { return c.SyncCode == nil || *c.SyncCode }

// CloudAllowed reports whether the user allowed a cloud.
func (c Config) CloudAllowed(name string) bool { return c.Clouds[name].Allowed }

// CloudSettings returns a cloud's settings with the defaults filled in.
func (c Config) CloudSettings(name string) Cloud {
	cl := c.Clouds[name]
	if cl.Code == "" {
		cl.Code = CloudCodeBranch
	}
	if cl.BranchPrefix == "" {
		cl.BranchPrefix = DefaultBranchPrefix
	}
	if cl.DeleteBranch == "" {
		cl.DeleteBranch = DeleteAfterMerge
	}
	if cl.RenameVendorBranches == nil {
		on := true
		cl.RenameVendorBranches = &on
	}
	return cl
}

// SetCloudAllowed records consent for a cloud, keeping its other settings.
func (c *Config) SetCloudAllowed(name string, allowed bool) {
	if c.Clouds == nil {
		c.Clouds = map[string]Cloud{}
	}
	cl := c.Clouds[name]
	cl.Allowed = allowed
	c.Clouds[name] = cl
}

// Check reports settings hopsesh cannot act on.
func (c Config) Check() error {
	for name, cl := range c.Clouds {
		switch cl.Code {
		case "", CloudCodeBranch, CloudCodeBundle:
		default:
			return fmt.Errorf("clouds.%s.code is %q: use %q or %q", name, cl.Code, CloudCodeBranch, CloudCodeBundle)
		}
		switch cl.DeleteBranch {
		case "", DeleteNever, DeleteAfterMerge, DeleteOnUndo:
		default:
			return fmt.Errorf("clouds.%s.delete_branch is %q: use %q, %q or %q", name, cl.DeleteBranch, DeleteNever, DeleteAfterMerge, DeleteOnUndo)
		}
	}
	return nil
}

// UsesPassword reports whether the machine logs in with a password.
func (h Host) UsesPassword() bool { return h.Auth == "password" }
