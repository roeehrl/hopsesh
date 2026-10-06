package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// diffSchemas compares recursively, matching Keep against each basename so existing
// manifests also work with versioned schemas. Preserve relative paths to avoid v1/v2
// collisions. Missing files are compared with an empty file, not a diff error message.
func diffSchemas(tested, latest, out, keep string) error {
	pattern, err := regexp.CompilePOSIX(keep)
	if err != nil {
		return err
	}
	paths := map[string]bool{}
	for _, root := range []string{tested, latest} {
		count := 0
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return fmt.Errorf("schema is not a regular file: %s", path)
			}
			if !strings.HasSuffix(path, ".json") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err == nil {
				paths[rel] = true
				count++
			}
			return err
		}); err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("no JSON schemas generated in %s", root)
		}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	var changed strings.Builder
	for _, name := range names {
		oldPath, newPath := filepath.Join(tested, name), filepath.Join(latest, name)
		old, oldErr := os.ReadFile(oldPath)
		newData, newErr := os.ReadFile(newPath)
		for _, err := range []error{oldErr, newErr} {
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if oldErr == nil && newErr == nil && bytes.Equal(old, newData) {
			continue
		}
		rel := filepath.ToSlash(name)
		fmt.Fprintln(&changed, rel)
		if !pattern.MatchString(filepath.Base(name)) {
			continue
		}
		if errors.Is(oldErr, os.ErrNotExist) {
			oldPath = os.DevNull
		}
		if errors.Is(newErr, os.ErrNotExist) {
			newPath = os.DevNull
		}
		result, err := exec.Command("diff", "-u", "-L", rel+", tested", "-L", rel+", latest", oldPath, newPath).CombinedOutput()
		var exit *exec.ExitError
		if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
			return fmt.Errorf("diff %s: %w: %s", rel, err, result)
		}
		path := filepath.Join(out, name+".diff")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, result, 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(out, "changed-files.txt"), []byte(changed.String()), 0o644)
}
