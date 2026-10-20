package cloudintegration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/relay"
)

const startupRecordPrefix = ".hopsesh/cloud-bootstrap-"

type StartupChange struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

// RepositorySetup exposes only file names and public startup instructions.
// Existing settings may contain credentials and never cross the GUI bridge.
type RepositorySetup struct {
	Repository string `json:"repository"`
	Bootstrap
	Changes []StartupChange `json:"changes"`
	files   map[string][]byte
	before  map[string][]byte
	record  string
}

type startupOwnership struct {
	Schema   int               `json:"schema"`
	Provider string            `json:"provider"`
	Version  string            `json:"version"`
	Files    map[string]string `json:"files"`
	Hook     string            `json:"hook,omitempty"`
	Prompt   string            `json:"promptHook,omitempty"`
	Guidance string            `json:"guidance,omitempty"`
}

func startupHash(body []byte) string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }

func startupHookHash(body []byte) (string, error) {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return startupHash(canonical), nil
}

func readStartup(root *os.Root, name string) ([]byte, error) {
	// Root pins the repository against parent renames and prevents escape. Also
	// refuse links inside that repository, devices and directories as files.
	parts := strings.Split(filepath.ToSlash(name), "/")
	for i := range parts {
		p := filepath.FromSlash(strings.Join(parts[:i+1], "/"))
		st, err := root.Lstat(p)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if st.Mode()&os.ModeSymlink != 0 || i < len(parts)-1 && !st.IsDir() {
			return nil, errors.New("cloud startup paths must not traverse links or non-directories")
		}
		if i == len(parts)-1 && !st.Mode().IsRegular() {
			return nil, errors.New("cloud startup target must be a regular file")
		}
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 131073))
	if err != nil {
		return nil, err
	}
	if len(b) > 131072 {
		return nil, errors.New("cloud startup settings exceed 128 KiB")
	}
	return b, nil
}

func PlanRepository(provider, version, origin, repository string) (*RepositorySetup, error) {
	return PlanRepositoryWith(provider, version, origin, repository, Options{})
}

func PlanRepositoryWith(provider, version, origin, repository string, o Options) (*RepositorySetup, error) {
	p, err := PlanWith(provider, version, origin, o)
	if err != nil {
		return nil, err
	}
	if provider != "claude-hosted" && provider != "codex-current" {
		return nil, errors.New("repository startup installation is not supported on this execution surface")
	}
	if err = canonicalDirectory(repository); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(repository)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	plan := &RepositorySetup{Repository: repository, Bootstrap: p, Changes: []StartupChange{}, files: map[string][]byte{}, before: map[string][]byte{}}
	recordPath := startupRecordPrefix + provider + ".json"
	plan.record = recordPath
	recordBody, err := readStartup(root, recordPath)
	if err != nil {
		return nil, err
	}
	record := startupOwnership{Schema: 1, Files: map[string]string{}}
	if recordBody != nil {
		if json.Unmarshal(recordBody, &record) != nil || record.Schema != 1 || record.Files == nil {
			return nil, errors.New("cloud startup ownership record is invalid")
		}
		if record.Provider != provider {
			return nil, errors.New("cloud startup ownership record belongs to another provider")
		}
	}
	plan.before[recordPath] = recordBody
	plan.files[".hopsesh/cloud-install-"+provider+".sh"] = []byte(p.InstallScript)
	if provider == "claude-hosted" {
		plan.files[".hopsesh/cloud-session-start.sh"] = []byte(p.ClaudeHookScript)
		plan.files[".hopsesh/cloud-session-connect.sh"] = []byte(p.ClaudePromptScript)
	} else {
		plan.files[".hopsesh/codex-start.md"] = []byte(p.StartSkill + "\n")
	}
	for name, body := range plan.files {
		before, err := readStartup(root, name)
		if err != nil {
			return nil, err
		}
		if before != nil && !bytes.Equal(before, body) && record.Files[name] != startupHash(before) {
			return nil, fmt.Errorf("cloud startup file %s was modified or is not owned by hopsesh; inspect it before installing", name)
		}
		plan.before[name] = before
	}
	if provider == "codex-current" {
		if err = plan.codexGuidance(root, &record); err != nil {
			return nil, err
		}
	}
	if provider == "claude-hosted" {
		name := ".claude/settings.json"
		before, err := readStartup(root, name)
		if err != nil {
			return nil, err
		}
		settings := map[string]json.RawMessage{}
		if before != nil && (json.Unmarshal(before, &settings) != nil || settings == nil) {
			return nil, errors.New("repository Claude settings are not a valid JSON object")
		}
		hooks := map[string]json.RawMessage{}
		if raw, ok := settings["hooks"]; ok && (json.Unmarshal(raw, &hooks) != nil || hooks == nil) {
			return nil, errors.New("repository Claude hooks settings are not a valid JSON object")
		}
		if record.Hook, err = mergeHookEvent(hooks, "SessionStart", p.ClaudeHookEntry, record.Hook); err != nil {
			return nil, err
		}
		if record.Prompt, err = mergeHookEvent(hooks, "UserPromptSubmit", p.ClaudePromptEntry, record.Prompt); err != nil {
			return nil, err
		}
		settings["hooks"], err = json.Marshal(hooks)
		if err != nil {
			return nil, err
		}
		body, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return nil, err
		}
		plan.files[name] = append(body, '\n')
		plan.before[name] = before
	}
	record.Schema, record.Provider, record.Version = 1, provider, version
	for name, body := range plan.files {
		if name != ".claude/settings.json" && name != "AGENTS.md" {
			record.Files[name] = startupHash(body)
		}
	}
	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, err
	}
	plan.files[recordPath] = append(body, '\n')
	names := []string{}
	for name := range plan.files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		action := "create"
		if bytes.Equal(plan.before[name], plan.files[name]) {
			action = "unchanged"
		} else if plan.before[name] != nil {
			action = "update"
		}
		plan.Changes = append(plan.Changes, StartupChange{Path: name, Action: action})
	}
	return plan, nil
}

func (p *RepositorySetup) Apply(ctx context.Context) error {
	if p == nil || len(p.files) == 0 {
		return errors.New("cloud startup plan is missing")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(p.Repository)
	if err != nil {
		return err
	}
	defer root.Close()
	// Recheck every reviewed input before writing anything. A changed file needs
	// a new preview; never overwrite a concurrent settings edit from a stale plan.
	for name, before := range p.before {
		current, err := readStartup(root, name)
		if err != nil {
			return err
		}
		if (current == nil) != (before == nil) || !bytes.Equal(current, before) {
			return errors.New("repository startup files changed since review; prepare a new plan")
		}
	}
	names := []string{}
	for name := range p.files {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		rank := func(name string) int {
			if name == p.record {
				return 2
			}
			if name == ".claude/settings.json" || name == "AGENTS.md" {
				return 1
			}
			return 0
		}
		a, b := rank(names[i]), rank(names[j])
		if a != b {
			return a < b
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		if bytes.Equal(p.files[name], p.before[name]) {
			continue
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = root.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return err
		}
		current, err := readStartup(root, name)
		if err != nil {
			return err
		}
		if (current == nil) != (p.before[name] == nil) || !bytes.Equal(current, p.before[name]) {
			return errors.New("repository startup changed during installation; integration was not completed")
		}
		id, err := relay.NewOperationID()
		if err != nil {
			return err
		}
		staged := filepath.Join(filepath.Dir(name), ".hopsesh-startup-"+id)
		file, err := root.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = file.Write(p.files[name])
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = root.Rename(staged, name)
		}
		if err != nil {
			_ = root.Remove(staged)
			return errors.New("cloud startup installation was not completed; inspect the files before retrying")
		}
	}
	return nil
}

// mergeHookEvent installs hopsesh's entry for one hook event, keeping every other
// entry. An existing hopsesh entry is replaced only if it is unchanged from what
// hopsesh last installed; a hand-edited one is left for the person to inspect.
func mergeHookEvent(hooks map[string]json.RawMessage, event string, entry json.RawMessage, recorded string) (string, error) {
	entries := []json.RawMessage{}
	if raw, ok := hooks[event]; ok && (json.Unmarshal(raw, &entries) != nil || entries == nil) {
		return "", fmt.Errorf("repository %s hooks are not an array", event)
	}
	type commands struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	var desired commands
	if err := json.Unmarshal(entry, &desired); err != nil || len(desired.Hooks) != 1 {
		return "", errors.New("invalid generated cloud startup hook")
	}
	desiredHash, err := startupHookHash(entry)
	if err != nil {
		return "", err
	}
	merged := []json.RawMessage{}
	installed := false
	for _, existing := range entries {
		var candidate commands
		if json.Unmarshal(existing, &candidate) != nil {
			return "", fmt.Errorf("invalid Claude %s hook entry", event)
		}
		ours := false
		for _, hook := range candidate.Hooks {
			if hook.Command == desired.Hooks[0].Command {
				ours = true
			}
		}
		if !ours {
			merged = append(merged, existing)
			continue
		}
		existingHash, err := startupHookHash(existing)
		if err != nil {
			return "", err
		}
		if existingHash != desiredHash && recorded != existingHash {
			return "", errors.New("hopsesh cloud hook was modified; inspect it before installing")
		}
		if !installed {
			merged = append(merged, entry)
			installed = true
		}
	}
	if !installed {
		merged = append(merged, entry)
	}
	if hooks[event], err = json.Marshal(merged); err != nil {
		return "", err
	}
	return desiredHash, nil
}
