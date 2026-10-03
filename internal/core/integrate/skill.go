package integrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// The hopsesh skill is one set of files (SKILL.md and its references) written to every
// agent's skills folder. An install record (.hopsesh-install.json, beside SKILL.md) keeps
// the hash of every file hopsesh wrote there, so each copy is "current" (exactly this
// build's files), "stale" (an older hopsesh's files, untouched: safe to replace),
// "modified" (edited by the user: never overwritten without force), "foreign" (a skill
// called hopsesh that hopsesh did not write), "broken" or absent.

// SkillName is the skill's name and folder.
const SkillName = "hopsesh"

const recordFile = ".hopsesh-install.json"

// States of a skill copy.
const (
	Absent   = "absent"
	Current  = "current"
	Stale    = "stale"
	Modified = "modified"
	Foreign  = "foreign"
	Broken   = "broken"
)

// Record is the install record.
type Record struct {
	Version string            `json:"hopseshVersion"`
	Files   map[string]string `json:"files"` // name → sha256
}

// SkillStatus is one copy of the skill.
type SkillStatus struct {
	State   string   `json:"state"`
	Dir     string   `json:"dir"`
	Version string   `json:"installedVersion,omitempty"`
	Changed []string `json:"changedFiles,omitempty"` // files the user edited
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// CheckSkill compares the skill in dir with the files this build would write.
func CheckSkill(dir string, files map[string][]byte) SkillStatus {
	st := SkillStatus{State: Absent, Dir: dir}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return st
	}
	b, err := os.ReadFile(filepath.Join(dir, recordFile))
	var rec Record
	if err != nil || json.Unmarshal(b, &rec) != nil {
		st.State = Foreign
		return st
	}
	st.Version = rec.Version
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		st.State = Broken
		return st
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
		return st
	}
	st.State = Current
	if len(files) != len(rec.Files) {
		st.State = Stale
	}
	for name, b := range files {
		if rec.Files[name] != sum(b) {
			st.State = Stale
		}
	}
	return st
}

// ErrModified means a copy of the skill was edited by the user.
var ErrModified = errors.New("the hopsesh skill was edited after hopsesh installed it")

// ErrForeign means a skill called hopsesh exists that hopsesh did not install.
var ErrForeign = errors.New("a skill called hopsesh already exists and was not installed by hopsesh")

// InstallSkill writes the files to dir. An edited copy, or one hopsesh did not write, is
// replaced only with force (the old folder is kept as hopsesh.bak-N).
func InstallSkill(dir, version string, files map[string][]byte, force bool) (SkillStatus, error) {
	st := CheckSkill(dir, files)
	switch st.State {
	case Current:
		return st, nil
	case Modified:
		if !force {
			return st, fmt.Errorf("%w: %s", ErrModified, dir)
		}
	case Foreign:
		if !force {
			return st, fmt.Errorf("%w: %s", ErrForeign, dir)
		}
	}
	if st.State == Modified || st.State == Foreign {
		if err := backup(dir); err != nil {
			return st, err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return st, err
	}
	rec := Record{Version: version, Files: map[string]string{}}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return names[j] == "SKILL.md" }) // SKILL.md last: agents watch it
	for _, n := range names {
		if err := writeAtomic(filepath.Join(dir, n), files[n], 0o644); err != nil {
			return st, err
		}
		rec.Files[n] = sum(files[n])
	}
	b, _ := json.MarshalIndent(rec, "", "  ")
	if err := writeAtomic(filepath.Join(dir, recordFile), append(b, '\n'), 0o644); err != nil {
		return st, err
	}
	return CheckSkill(dir, files), nil
}

// RemoveSkill deletes a copy hopsesh owns unchanged, or any copy with force.
func RemoveSkill(dir string, files map[string][]byte, force bool) error {
	st := CheckSkill(dir, files)
	switch st.State {
	case Absent:
		return nil
	case Modified, Foreign:
		if !force {
			return fmt.Errorf("not removing %s: %s (use --force)", dir, map[string]string{Modified: "it was edited after hopsesh installed it", Foreign: "hopsesh did not install it"}[st.State])
		}
	}
	return os.RemoveAll(dir)
}

// WriteOwned writes a file hopsesh owns outright (an agent's rules file).
func WriteOwned(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeAtomic(path, b, 0o600)
}

// MergeShared rewrites a shared settings file through merge, keeping a backup of what it
// replaced (path.hopsesh-backup).
func MergeShared(path string, merge func([]byte) ([]byte, error)) error {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b, err := merge(old)
	if err != nil {
		return err
	}
	if old != nil {
		if err := os.WriteFile(path+".hopsesh-backup", old, 0o600); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeAtomic(path, b, 0o600)
}

func writeAtomic(path string, b []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, perm); err != nil {
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
