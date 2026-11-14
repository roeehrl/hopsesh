package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
)

// RuntimeProfile is a pinned, agent-specific state root, independent of account labels.
// Binding describes the observed login; it is not a credential or proof of ownership.
type RuntimeProfile struct {
	IdentitySource string    `json:"identitySource,omitempty"`
	CheckedAt      time.Time `json:"checkedAt,omitempty"`
	Machine        string    `json:"machine,omitempty"`
	ID             string    `json:"id"`
	Endpoint       string    `json:"endpoint"`
	Agent          ID        `json:"agent"`
	Name           string    `json:"name"`
	Tags           []string  `json:"tags"`
	Root           string    `json:"root"`
	Default        bool      `json:"default"`
	Managed        bool      `json:"managed"`
	Generation     int       `json:"generation"`
	Binding        string    `json:"binding,omitempty"`
	Account        *Account  `json:"account,omitempty"`
	Error          string    `json:"error,omitempty"`
	// Command is the shell command hopsesh added on its machine to run the agent in this
	// folder ("claude-work"; "" none).
	Command string `json:"command,omitempty"`
	// Port is the port the agent's background service listens on for this folder, when
	// hopsesh gave it one (OpenCode: each account folder's own, so its service can run
	// beside the default one); 0 none.
	Port int `json:"port,omitempty"`
}

func (in Install) ProfileID() string {
	if in.Profile == nil {
		return ""
	}
	return in.Profile.ID
}
func (in Install) BindingID() string {
	if in.Profile == nil {
		return ""
	}
	return in.Profile.Binding
}

// ProfileSpec is the module's declaration of vendor-supported account isolation.
// Only module code supplies it; peer payloads never control launch policy.
type ProfileSpec struct {
	// RootEnv are variables an account's commands run with, each set to the account
	// folder.
	RootEnv []string `json:"rootEnv,omitempty"`
	// RootVars are variables set to folders inside the account folder, for an agent with
	// several roots (OpenCode: XDG_DATA_HOME=<folder>/data, OPENCODE_CONFIG_DIR=
	// <folder>/config).
	RootVars []ProfileVar `json:"rootVars,omitempty"`
	Unset    []string     `json:"unset,omitempty"`
	Login    []string     `json:"login,omitempty"`
	// LoginElsewhere signs in from another machine's browser: the agent prints a link
	// and a code instead of opening a browser where it runs (empty: Login).
	LoginElsewhere []string          `json:"loginElsewhere,omitempty"`
	InitialFiles   map[string]string `json:"initialFiles,omitempty"`
	// Inherit are settings a new folder copies from the agent's default folders, by file:
	// relative to the main root, or to another root when it starts with one
	// ("{config}/opencode.json"). Top-level keys of a TOML file (Codex's
	// cli_auth_credentials_store, so a new folder keeps its sign-in where the default one
	// does), or dot paths into a .json or .jsonc file (InheritJSON). Only the listed
	// settings are copied, never one that holds a credential, into the same root of the
	// new folder (ProfileLocate), which must be inside it.
	Inherit map[string][]string `json:"inherit,omitempty"`
	// InheritsSignIn: a new folder keeps its sign-in where the default folder does, so
	// the user interfaces say so (Codex). Inherit may carry other settings too.
	InheritsSignIn bool `json:"inheritsSignIn,omitempty"`
}

// ProfileVar is a variable an account folder sets to a folder inside it.
type ProfileVar struct {
	Env string `json:"env"`
	Sub string `json:"sub,omitempty"` // slash-separated; "" is the account folder itself
}

// Vars are every variable an account folder sets.
func (p *ProfileSpec) Vars() []string {
	if p == nil {
		return nil
	}
	out := append([]string(nil), p.RootEnv...)
	for _, v := range p.RootVars {
		if !slices.Contains(out, v.Env) {
			out = append(out, v.Env)
		}
	}
	return out
}

// EnvFor is the variables ("NAME=value") that pin the agent to the account folder root,
// with pa the rules of root's machine.
func (p *ProfileSpec) EnvFor(root string, pa Path) []string {
	if p == nil {
		return nil
	}
	var env []string
	for _, key := range p.RootEnv {
		env = append(env, key+"="+root)
	}
	for _, v := range p.RootVars {
		dir := root
		if v.Sub != "" {
			dir = pa.Join(root, v.Sub)
		}
		env = append(env, v.Env+"="+dir)
	}
	return env
}

// ProfileLocate is where a root of the Spec is for the account folder root: Locate with
// the folder's variables over the machine's (getenv), before Expand.
func (s Spec) ProfileLocate(name, root, goos string, getenv func(string) string) string {
	pinned := map[string]string{}
	for _, e := range s.Accounts.EnvFor(root, PathFor(goos)) {
		k, v, _ := strings.Cut(e, "=")
		pinned[k] = v
	}
	for _, r := range s.Roots {
		if r.Name == name {
			return r.Locate(goos, func(k string) string {
				if v, ok := pinned[k]; ok {
					return v
				}
				return getenv(k)
			})
		}
	}
	return ""
}

// profilePath is the path rules for an account folder: its install's system, else the
// folder's own form.
func profilePath(goos, root string) Path {
	if goos == "" && (WindowsPath{}).IsAbs(root) {
		return WindowsPath{}
	}
	return PathFor(goos)
}

func (in Install) ProfileEnv() []string {
	if in.Profile == nil || in.Accounts == nil {
		return nil
	}
	// Some vendors distinguish an explicit root from their native config lookup,
	// even when both point at the same directory. Only detection metadata and an
	// exact default-profile match authorize that lookup; aliases stay pinned.
	if in.NativeDefaultRoot != "" && in.Profile.Default && in.Profile.Root == in.NativeDefaultRoot {
		return nil
	}
	// The default account of an agent with several roots is its own folders: its Root
	// is the main root, not a folder holding them, so RootVars would point inside it.
	// Only RootEnv, which names the main root itself, is pinned.
	if in.defaultOfSeveralRoots() {
		var env []string
		for _, key := range in.Accounts.RootEnv {
			env = append(env, key+"="+in.Profile.Root)
		}
		return env
	}
	return in.Accounts.EnvFor(in.Profile.Root, profilePath(in.OS, in.Profile.Root))
}

// defaultOfSeveralRoots reports a default account of an agent whose account folders hold
// its roots (RootVars): the machine's own variables find its folders, as Detect did.
func (in Install) defaultOfSeveralRoots() bool {
	return in.Profile != nil && in.Profile.Default && in.Accounts != nil && len(in.Accounts.RootVars) > 0
}

// ScopeCommand applies the profile last. Caller-supplied roots and inherited account
// overrides cannot redirect a managed launch into a sibling profile.
func (in Install) ScopeCommand(c Command) Command {
	if in.Profile == nil {
		return c
	}
	if in.Accounts == nil {
		return c
	}
	unset := append(append([]string{}, in.Accounts.Unset...), in.Accounts.Vars()...)
	if in.defaultOfSeveralRoots() {
		// The machine's RootVars chose the default folders and stay; a caller's may not.
		unset = append(append([]string{}, in.Accounts.Unset...), in.Accounts.RootEnv...)
		c.Env = dropEnv(c.Env, in.Accounts.Vars(), in.OS)
	}
	filtered := []string{}
	for _, e := range c.Env {
		k, _, _ := strings.Cut(e, "=")
		blocked := false
		for _, u := range unset {
			if k == u || in.OS == "windows" && strings.EqualFold(k, u) {
				blocked = true
				break
			}
		}
		if !blocked {
			filtered = append(filtered, e)
		}
	}
	c.Env = append(filtered, in.ProfileEnv()...)
	c.Unset = append(append([]string{}, c.Unset...), unset...)
	if in.Binary != "" && len(c.Argv) > 0 && c.Argv[0] == string(in.Agent) {
		c.Argv = append([]string{}, c.Argv...)
		c.Argv[0] = in.Binary
	}
	return c
}

func dropEnv(env, keys []string, goos string) []string {
	out := []string{}
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		if !slices.ContainsFunc(keys, func(u string) bool { return k == u || goos == "windows" && strings.EqualFold(k, u) }) {
			out = append(out, e)
		}
	}
	return out
}

// InheritJSON picks settings out of a JSON (or JSONC, with comments) settings file for a
// new account folder: each field a dot path to one plain value ("model", "tui.theme"),
// kept at the same place, as plain JSON. Keys that name a credential (apiKey, token,
// password, headers, env, environment, …) are never copied: a field that is one is an
// error. A field whose value is an object or an array, or a string that looks like it
// carries a credential (a URL with a password or key, a known token form), is left out,
// since a credential can hide inside it under any name. It returns nil when none of the
// fields is copied. src is the default folder's file, which may hold credentials; only
// the picked values leave the call.
func InheritJSON(src []byte, fields []string) ([]byte, error) {
	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(stripJSONC(src)))
	d.UseNumber()
	if err := d.Decode(&doc); err != nil || doc == nil {
		return nil, errors.New("settings must be a JSON object")
	}
	out := map[string]any{}
	for _, f := range fields {
		parts := strings.Split(f, ".")
		for _, p := range parts {
			if p == "" || credentialKey(p) {
				return nil, fmt.Errorf("%q is not a setting hopsesh copies", f)
			}
		}
		v, ok := jsonAt(doc, parts)
		if !ok || !plainSetting(v) {
			continue
		}
		jsonPut(out, parts, v)
	}
	if len(out) == 0 {
		return nil, nil
	}
	b, err := json.MarshalIndent(out, "", "  ")
	return append(b, '\n'), err
}

// plainSetting reports whether v is a value InheritJSON copies: a number, a boolean, null,
// or a string that does not look like a credential.
func plainSetting(v any) bool {
	switch x := v.(type) {
	case map[string]any, []any:
		return false
	case string:
		return !credentialValue(x)
	}
	return true
}

// credentialPrefixes start tokens of common services.
var credentialPrefixes = []string{"sk-", "sk_", "pk_", "rk_", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-",
	"xoxb-", "xoxp-", "xoxa-", "xapp-", "AKIA", "ASIA", "AIza", "ya29.", "eyJ", "hf_", "gsk_", "xai-", "Bearer ", "Basic "}

// credentialValue reports whether a setting's value looks like it carries a credential:
// a known token form, or a URL with a password or a credential-named query parameter.
func credentialValue(s string) bool {
	t := strings.TrimSpace(s)
	for _, p := range credentialPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	if u, err := url.Parse(t); err == nil && u.Scheme != "" {
		if _, has := u.User.Password(); has || u.User != nil && u.User.Username() != "" && u.Host != "" {
			return true
		}
		for k := range u.Query() {
			if credentialKey(k) {
				return true
			}
		}
	}
	return false
}

// stripJSONC turns JSON with comments (// and /* */) and trailing commas, as agents
// write their settings (.jsonc), into plain JSON. Strings are kept as they are.
func stripJSONC(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(b) && b[j] != '"' {
				if b[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(b) {
				return append(out, b[i:]...)
			}
			out = append(out, b[i:j+1]...)
			i = j
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			i--
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			end := bytes.Index(b[i+2:], []byte("*/"))
			if end < 0 {
				return out
			}
			out = append(out, ' ')
			i += end + 3
		case c == ']' || c == '}':
			// Drop a comma before the closing bracket.
			k := len(out) - 1
			for k >= 0 && (out[k] == ' ' || out[k] == '\t' || out[k] == '\n' || out[k] == '\r') {
				k--
			}
			if k >= 0 && out[k] == ',' {
				out = append(out[:k], out[k+1:]...)
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}

// credentialWords are words of setting names that hold credentials or carry them.
var credentialWords = map[string]bool{"key": true, "keys": true, "apikey": true, "token": true, "tokens": true,
	"secret": true, "secrets": true, "password": true, "passwd": true, "credential": true, "credentials": true,
	"auth": true, "authorization": true, "cookie": true, "cookies": true, "bearer": true, "header": true,
	"headers": true, "env": true, "environment": true, "oauth": true, "pat": true}

// credentialEnds end words that name credentials even when run together ("openAIKey").
var credentialEnds = []string{"key", "token", "secret", "password"}

// credentialKey reports whether a setting's name has a credential word in it ("apiKey",
// "OPENAI_API_KEY", "headers"), split at case changes, digits and punctuation. It errs
// toward refusing ("hotkey").
func credentialKey(k string) bool {
	word := []rune{}
	prevLower := false
	check := func() bool {
		w := strings.ToLower(string(word))
		word = word[:0]
		if credentialWords[w] {
			return true
		}
		for _, end := range credentialEnds {
			if strings.HasSuffix(w, end) {
				return true
			}
		}
		return false
	}
	for _, r := range k {
		switch {
		case unicode.IsUpper(r):
			if prevLower && check() {
				return true
			}
			word = append(word, r)
			prevLower = false
		case unicode.IsLetter(r):
			word = append(word, r)
			prevLower = true
		default:
			if check() {
				return true
			}
			prevLower = false
		}
	}
	return check()
}

func jsonAt(v any, path []string) (any, bool) {
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[p]; !ok {
			return nil, false
		}
	}
	return v, true
}

func jsonPut(m map[string]any, path []string, v any) {
	for _, p := range path[:len(path)-1] {
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
	m[path[len(path)-1]] = v
}

// AccountPreparer readies an account folder hopsesh has just made for the agent, before
// anyone signs in to it: h runs commands with the folder's variables (in.Profile is the
// new folder's). OpenCode gives the folder its own background-service port, so its
// service can run beside the default one. It returns what to record with the folder.
type AccountPreparer interface {
	PrepareAccount(ctx context.Context, h Host, in Install, o AccountPrep) (AccountPrepared, error)
}

// AccountPrep is what hopsesh knows of the machine for PrepareAccount.
type AccountPrep struct {
	// Taken are the ports other account folders on the machine were given.
	Taken []int
	// Free reports whether nothing listens on a port of the machine now (nil: unknown).
	Free func(port int) bool
}

// AccountPrepared is what PrepareAccount set up, recorded with the folder.
type AccountPrepared struct {
	Port int // its background service's port; 0 none
}
