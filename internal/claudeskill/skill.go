// Package claudeskill installs, checks and removes the hopsesh skill for Claude Code: a
// personal skill at <claude config dir>/skills/hopsesh that teaches Claude to list and move
// sessions with the hopsesh CLI (plan first, move only after the user says yes).
//
// The files are rendered from templates in this binary. An install record
// (.hopsesh-install.json, beside SKILL.md) keeps the hash of every file hopsesh wrote, so
// a skill is "current" (matches this build), "stale" (written by an older hopsesh and not
// edited since: safe to replace), "modified" (the user edited it: never overwritten without
// force), "foreign" (a skill called hopsesh that hopsesh did not write), "broken" or absent.
package claudeskill

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// Name is the skill's name and folder.
const Name = "hopsesh"

// Format changes when the skill's structure changes in a way older hopsesh versions
// would not understand.
const Format = "1"

const recordFile = ".hopsesh-install.json"

//go:embed files/*.tmpl
var templates embed.FS

// States of an installed skill.
const (
	Absent   = "absent"
	Current  = "current"
	Stale    = "stale"
	Modified = "modified"
	Foreign  = "foreign"
	Broken   = "broken"
)

// Params fill the templates.
type Params struct {
	Bin     string // how Claude should run hopsesh: "hopsesh" or an absolute path
	Version string
	Format  string
}

// Render returns the skill's files (name → content) for these parameters.
func Render(p Params) (map[string][]byte, error) {
	if p.Format == "" {
		p.Format = Format
	}
	if p.Bin == "" {
		p.Bin = "hopsesh"
	}
	out := map[string][]byte{}
	for _, name := range []string{"SKILL.md", "reference.md"} {
		src, err := templates.ReadFile("files/" + name + ".tmpl")
		if err != nil {
			return nil, err
		}
		t, err := template.New(name).Parse(string(src))
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		if err := t.Execute(&b, p); err != nil {
			return nil, err
		}
		out[name] = bytes.ReplaceAll(b.Bytes(), []byte("\r\n"), []byte("\n"))
	}
	return out, nil
}

// Record is the install record.
type Record struct {
	Format  string            `json:"format"`
	Version string            `json:"hopseshVersion"`
	Bin     string            `json:"bin"`
	Files   map[string]string `json:"files"` // name → sha256
}

// Status describes the skill on disk.
type Status struct {
	State     string   `json:"state"`
	Dir       string   `json:"dir"`
	Version   string   `json:"installedVersion,omitempty"`
	Bin       string   `json:"bin,omitempty"`
	Changed   []string `json:"changedFiles,omitempty"` // files the user edited (modified)
	NewSkills bool     `json:"newSkillsFolder,omitempty"`
}

// Dir is the skill folder under a Claude Code config dir.
func Dir(configDir string) string { return filepath.Join(configDir, "skills", Name) }

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func readRecord(dir string) (*Record, error) {
	b, err := os.ReadFile(filepath.Join(dir, recordFile))
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Check reports the state of the skill compared with what this build would write.
func Check(configDir string, want Params) (Status, error) {
	dir := Dir(configDir)
	st := Status{State: Absent, Dir: dir}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	rec, err := readRecord(dir)
	if err != nil {
		st.State = Foreign
		return st, nil
	}
	st.Version, st.Bin = rec.Version, rec.Bin
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		st.State = Broken
		return st, nil
	}
	for name, h := range rec.Files {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || sum(b) != h {
			st.Changed = append(st.Changed, name)
		}
	}
	sort.Strings(st.Changed)
	if len(st.Changed) > 0 {
		st.State = Modified
		return st, nil
	}
	files, err := Render(want)
	if err != nil {
		return st, err
	}
	st.State = Current
	for name, b := range files {
		if rec.Files[name] != sum(b) {
			st.State = Stale
		}
	}
	if len(files) != len(rec.Files) {
		st.State = Stale
	}
	return st, nil
}

// ErrModified means the skill was edited by the user; Install needs force to replace it.
var ErrModified = errors.New("the hopsesh skill was edited after hopsesh installed it")

// ErrForeign means a skill called hopsesh exists that hopsesh did not install.
var ErrForeign = errors.New("a skill called hopsesh already exists and was not installed by hopsesh")

// Install writes (or updates) the skill. A skill the user edited, or one hopsesh did not
// write, is only replaced with force (the old folder is kept as skills/hopsesh.bak-N).
func Install(configDir string, p Params, force bool) (Status, error) {
	st, err := Check(configDir, p)
	if err != nil {
		return st, err
	}
	switch st.State {
	case Current:
		return st, nil
	case Modified:
		if !force {
			return st, ErrModified
		}
	case Foreign:
		if !force {
			return st, ErrForeign
		}
	}
	skills := filepath.Join(configDir, "skills")
	_, statErr := os.Stat(skills)
	newSkills := errors.Is(statErr, os.ErrNotExist)
	dir := Dir(configDir)
	if force && (st.State == Modified || st.State == Foreign) {
		if err := backup(dir); err != nil {
			return st, err
		}
	}
	files, err := Render(p)
	if err != nil {
		return st, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return st, err
	}
	rec := Record{Format: Format, Version: p.Version, Bin: p.Bin, Files: map[string]string{}}
	// SKILL.md last, so Claude Code's watcher never sees a half-written skill.
	order := []string{"reference.md", "SKILL.md"}
	for _, name := range order {
		if err := writeAtomic(filepath.Join(dir, name), files[name]); err != nil {
			return st, err
		}
		rec.Files[name] = sum(files[name])
	}
	b, _ := json.MarshalIndent(rec, "", "  ")
	if err := writeAtomic(filepath.Join(dir, recordFile), append(b, '\n')); err != nil {
		return st, err
	}
	st, err = Check(configDir, p)
	st.NewSkills = newSkills
	return st, err
}

// Remove deletes the skill when hopsesh owns it unchanged (current, stale or broken), or
// with force.
func Remove(configDir string, p Params, force bool) error {
	st, err := Check(configDir, p)
	if err != nil {
		return err
	}
	switch st.State {
	case Absent:
		return nil
	case Modified, Foreign:
		if !force {
			return fmt.Errorf("not removing %s: %s (use --force)", st.Dir, map[string]string{Modified: "it was edited after hopsesh installed it", Foreign: "hopsesh did not install it"}[st.State])
		}
	}
	return os.RemoveAll(st.Dir)
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func backup(dir string) error {
	for i := 1; i < 100; i++ {
		dst := fmt.Sprintf("%s.bak-%d", dir, i)
		if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
			return os.Rename(dir, dst)
		}
	}
	return errors.New("too many backups of the hopsesh skill")
}

// Rules are the permission rules hopsesh can add to Claude Code's settings: read-only
// commands allowed, moves and undo always asking (even in auto mode).
func Rules(bin string) (allow, ask []string) {
	allow = []string{"Bash(" + bin + " ls *)", "Bash(" + bin + " show *)", "Bash(" + bin + " plan *)", "Bash(" + bin + " hosts --json)", "Bash(" + bin + " doctor *)", "Bash(" + bin + " version)"}
	ask = []string{"Bash(" + bin + " pull *)", "Bash(" + bin + " undo *)", "Bash(" + bin + " import *)"}
	return
}

// AddRules merges Rules into a Claude Code settings.json (creating it if needed), keeping
// everything else and a backup of the previous file. It returns the rules it added.
func AddRules(settingsPath, bin string) ([]string, error) {
	allow, ask := Rules(bin)
	var doc map[string]any
	b, err := os.ReadFile(settingsPath)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("%s is not valid JSON; not changing it: %w", settingsPath, err)
		}
		if err := os.WriteFile(settingsPath+".hopsesh-backup", b, 0o600); err != nil {
			return nil, err
		}
	case errors.Is(err, os.ErrNotExist):
		doc = map[string]any{}
	default:
		return nil, err
	}
	perms, _ := doc["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	var added []string
	merge := func(key string, rules []string) {
		cur, _ := perms[key].([]any)
		have := map[string]bool{}
		for _, r := range cur {
			if s, ok := r.(string); ok {
				have[s] = true
			}
		}
		for _, r := range rules {
			if !have[r] {
				cur = append(cur, r)
				added = append(added, key+": "+r)
			}
		}
		perms[key] = cur
	}
	merge("allow", allow)
	merge("ask", ask)
	if len(added) == 0 {
		return nil, nil
	}
	doc["permissions"] = perms
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return nil, err
	}
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(settingsPath); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := settingsPath + ".hopsesh-tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), mode); err != nil {
		return nil, err
	}
	return added, os.Rename(tmp, settingsPath)
}

// HasRules reports whether the settings already carry every hopsesh rule.
func HasRules(settingsPath, bin string) bool {
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}
	allow, ask := Rules(bin)
	s := string(b)
	for _, r := range append(allow, ask...) {
		if !strings.Contains(s, jsonString(r)) {
			return false
		}
	}
	return true
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
