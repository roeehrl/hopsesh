package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"

	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/core/profiles"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Account folders hopsesh makes live in ~/.hopsesh/accounts/<agent>/<name> on their
// machine: in plain sight, and apart from hopsesh's state (resetting hopsesh keeps them).

// AccountsDir is where hopsesh makes account folders under a home folder.
func AccountsDir(home string) string { return filepath.Join(home, ".hopsesh", "accounts") }

// folderName is a folder (and command) name for an account name: lower case letters,
// digits and dashes.
func folderName(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// AddAccountOptions are the choices of a new account folder.
type AddAccountOptions struct {
	// Command adds <agent>-<name> (claude-work) to the shell on that machine.
	Command bool `json:"command"`
}

// AddAccount makes a new account folder for an agent on a machine (this one, or one with
// hopsesh running in its login session), ready to sign in. Its sign-in is not touched
// here: AccountSignIn runs it.
func (a *App) AddAccount(ctx context.Context, machine string, id agent.ID, name string, tags []string, o AddAccountOptions) (agent.RuntimeProfile, error) {
	if machine == "" || machine == LocalName() {
		return a.addLocalAccount(ctx, id, name, tags, o)
	}
	c, _, done, err := a.loginPeer(ctx, machine)
	if err != nil {
		return agent.RuntimeProfile{}, err
	}
	defer done()
	var p agent.RuntimeProfile
	if err := c.Call(ctx, peer.MethodAccountAdd, peer.AccountAdd{Agent: id, Name: name, Command: o.Command}, &p); err != nil {
		return p, err
	}
	p.Machine, p.Tags = machine, tags
	return a.accountStore().Register(p)
}

func (a *App) addLocalAccount(ctx context.Context, id agent.ID, name string, tags []string, o AddAccountOptions) (agent.RuntimeProfile, error) {
	name = strings.TrimSpace(name)
	slug := folderName(name)
	if slug == "" || len(name) > 120 || strings.ContainsFunc(name, unicode.IsControl) {
		return agent.RuntimeProfile{}, errors.New("use an account name of 1–120 bytes with a letter or digit")
	}
	tags, err := profiles.Tags(tags)
	if err != nil {
		return agent.RuntimeProfile{}, err
	}
	mod, ok := a.Module(id)
	if !ok || mod.Spec().Accounts == nil {
		return agent.RuntimeProfile{}, errors.New("account folders are for Claude Code and Codex")
	}
	m, err := a.accountMachine(ctx, "")
	if err != nil {
		return agent.RuntimeProfile{}, err
	}
	defer m.Close()
	endpoint, err := m.PrepareIdentity(ctx)
	if err == nil {
		err = m.CommitIdentity(ctx)
	}
	if err != nil {
		return agent.RuntimeProfile{}, err
	}
	root := filepath.Join(AccountsDir(m.Facts.Home), string(id), slug)
	if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 {
		return agent.RuntimeProfile{}, fmt.Errorf("%s exists already; pick another name", root)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return agent.RuntimeProfile{}, err
	}
	spec := mod.Spec().Accounts
	for file, contents := range spec.InitialFiles {
		if err := os.WriteFile(filepath.Join(root, file), []byte(contents), 0o600); err != nil {
			return agent.RuntimeProfile{}, err
		}
	}
	if h, err := m.For(ctx, mod.Spec(), agent.Install{}, nil); err == nil {
		if base, err := mod.Detect(ctx, h); err == nil {
			if err := inherit(base.Root("home"), root, spec.Inherit); err != nil {
				return agent.RuntimeProfile{}, err
			}
		}
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	p := agent.RuntimeProfile{Machine: m.Name, ID: profiles.ID(), Endpoint: endpoint, Agent: id, Name: name, Tags: tags, Root: root, Managed: true}
	if o.Command {
		cmd, err := addShellCommand(m.Facts.Home, string(id)+"-"+slug, mod, root)
		if err != nil {
			return p, err
		}
		p.Command = cmd
	}
	return a.accountStore().Register(p)
}

// inherit copies top-level TOML settings (by file) from the default folder to a new one.
func inherit(from, to string, keys map[string][]string) error {
	for file, names := range keys {
		var have map[string]any
		if _, err := toml.DecodeFile(filepath.Join(from, file), &have); err != nil {
			continue // no such file there, or not one hopsesh reads: the agent's own default
		}
		pick := map[string]any{}
		for _, n := range names {
			if v, ok := have[n]; ok {
				pick[n] = v
			}
		}
		if len(pick) == 0 {
			continue
		}
		var b bytes.Buffer
		if err := toml.NewEncoder(&b).Encode(pick); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(to, file), b.Bytes(), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// commandMark is in every shell command hopsesh adds, so it only ever replaces or removes
// its own.
const commandMark = "# Added by hopsesh for an account folder"

// addShellCommand writes a command that runs the agent in root (~/.local/bin/<name>),
// unless a command of that name hopsesh did not add is there.
func addShellCommand(home, name string, mod agent.Module, root string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", errors.New("hopsesh adds account commands on macOS and Linux; on Windows set " + strings.Join(mod.Spec().Accounts.RootEnv, " and ") + " yourself")
	}
	path := filepath.Join(home, ".local", "bin", name)
	if b, err := os.ReadFile(path); err == nil && !bytes.Contains(b, []byte(commandMark)) {
		return "", fmt.Errorf("%s exists and is not hopsesh's; the folder was made without a command", path)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	spec := mod.Spec()
	var b strings.Builder
	fmt.Fprintf(&b, "#!/bin/sh\n%s (%s). Remove it with hopsesh accounts forget.\n", commandMark, root)
	for _, v := range spec.Accounts.Unset {
		fmt.Fprintf(&b, "unset %s\n", v)
	}
	for _, v := range spec.Accounts.RootEnv {
		fmt.Fprintf(&b, "export %s=%s\n", v, shQuote(root))
	}
	fmt.Fprintf(&b, "exec %s \"$@\"\n", spec.Binaries[0].Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		return "", err
	}
	return name, nil
}

// removeShellCommand removes a command hopsesh added (and only such a one).
func removeShellCommand(home, name string) {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return
	}
	path := filepath.Join(home, ".local", "bin", name)
	if b, err := os.ReadFile(path); err == nil && bytes.Contains(b, []byte(commandMark)) {
		_ = os.Remove(path)
	}
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// FolderMove is one account folder hopsesh keeps in its state folder, and where it goes.
type FolderMove struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Agent agent.ID `json:"agent"`
	From  string   `json:"from"`
	To    string   `json:"to"`
}

// FoldersToMove are the account folders on this machine still in hopsesh's state folder,
// where resetting hopsesh would delete them.
func (a *App) FoldersToMove() ([]FolderMove, error) {
	ps, err := a.Accounts()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	endpoint, _ := a.LocalAccountEndpoint(context.Background())
	state := filepath.Clean(a.StateDir) + string(filepath.Separator)
	var out []FolderMove
	taken := map[string]bool{}
	for _, p := range ps {
		if !p.Managed || p.Endpoint != endpoint || !strings.HasPrefix(filepath.Clean(p.Root)+string(filepath.Separator), state) {
			continue
		}
		base := folderName(p.Name)
		if base == "" {
			base = "account"
		}
		to := filepath.Join(AccountsDir(home), string(p.Agent), base)
		for i := 2; taken[to] || exists(to); i++ {
			to = filepath.Join(AccountsDir(home), string(p.Agent), fmt.Sprintf("%s-%d", base, i))
		}
		taken[to] = true
		out = append(out, FolderMove{ID: p.ID, Name: p.Name, Agent: p.Agent, From: p.Root, To: to})
	}
	return out, nil
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// MoveFolders moves the account folders FoldersToMove names, after the user confirmed.
// Their sessions and sign-ins move with them; their ids stay.
func (a *App) MoveFolders() ([]FolderMove, error) {
	moves, err := a.FoldersToMove()
	if err != nil {
		return nil, err
	}
	var done []FolderMove
	for _, mv := range moves {
		if err := os.MkdirAll(filepath.Dir(mv.To), 0o700); err != nil {
			return done, err
		}
		if err := os.Rename(mv.From, mv.To); err != nil {
			return done, fmt.Errorf("moving %s: %w", mv.Name, err)
		}
		to := mv.To
		if real, err := filepath.EvalSymlinks(to); err == nil {
			to = real
		}
		if err := a.accountStore().Relocate(mv.ID, mv.From, to); err != nil {
			_ = os.Rename(mv.To, mv.From)
			return done, err
		}
		mv.To = to
		done = append(done, mv)
	}
	return done, nil
}
