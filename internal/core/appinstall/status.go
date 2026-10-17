// Package appinstall detects the installed desktop shell without running it.
package appinstall

import (
	"encoding/xml"
	"errors"
	"github.com/roeehrl/hopsesh/internal/update"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Status struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	CLI       string `json:"cli,omitempty"`
	Version   string `json:"version,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func Inspect(path, platform string) Status {
	s := Status{Path: path}
	var program string
	switch platform {
	case "darwin":
		program = filepath.Join(path, "Contents", "MacOS", "hopsesh-app")
		s.CLI = filepath.Join(path, "Contents", "Resources", "bin", "hopsesh")
		f, err := os.Open(filepath.Join(path, "Contents", "Info.plist"))
		if err != nil {
			s.Reason = err.Error()
			return s
		}
		defer f.Close()
		decoder := xml.NewDecoder(io.LimitReader(f, 1<<20))
		key, id := "", ""
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				s.Reason = "invalid app metadata"
				return s
			}
			start, ok := token.(xml.StartElement)
			if !ok {
				continue
			}
			switch start.Name.Local {
			case "key":
				if err = decoder.DecodeElement(&key, &start); err != nil {
					s.Reason = "invalid app metadata"
					return s
				}
			case "string":
				var value string
				if err = decoder.DecodeElement(&value, &start); err != nil {
					s.Reason = "invalid app metadata"
					return s
				}
				switch key {
				case "CFBundleIdentifier":
					id = value
				case "CFBundleShortVersionString":
					s.Version = value
				}
				key = ""
			}
		}
		if id != "io.github.roeehrl.hopsesh" {
			s.Reason = "bundle identity does not match Hopsesh"
			return s
		}
	case "windows":
		program = filepath.Join(path, "hopsesh-app.exe")
		s.CLI = filepath.Join(path, "hopsesh.exe")
	default:
		s.Reason = "no published desktop package for this platform"
		return s
	}
	for _, p := range []string{program, s.CLI} {
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() {
			s.Reason = "app package is incomplete"
			return s
		}
	}
	s.Installed = true
	return s
}
func Detect() Status {
	target, err := update.AppTarget()
	if err != nil {
		return Status{Reason: err.Error()}
	}
	candidates := []string{target.Path}
	if runtime.GOOS == "darwin" {
		candidates = append([]string{"/Applications/hopsesh.app"}, candidates...)
	}
	exe, _ := os.Executable()
	if resolved, e := filepath.EvalSymlinks(exe); e == nil {
		exe = resolved
	}
	switch runtime.GOOS {
	case "darwin":
		if i := strings.Index(exe, ".app/Contents/"); i >= 0 {
			candidates = append([]string{exe[:i+4]}, candidates...)
		}
	case "windows":
		candidates = append([]string{filepath.Dir(exe)}, candidates...)
	}
	for _, path := range candidates {
		s := Inspect(path, runtime.GOOS)
		if s.Installed {
			return s
		}
		if _, err := os.Lstat(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return s
		}
	}
	return Status{Path: target.Path, Reason: "Hopsesh desktop app is not installed"}
}
