package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// NamespaceArgs reads only Hopsesh's explicit namespace flags. Native application
// launch flags remain owned by the desktop shell.
func NamespaceArgs(args []string) error {
	for i := 0; i < len(args); i++ {
		flag, value, equals := strings.Cut(args[i], "=")
		env := ""
		switch flag {
		case "--config-dir":
			env = "HOPSESH_CONFIG_DIR"
		case "--state-dir":
			env = "HOPSESH_STATE_DIR"
		default:
			continue
		}
		if !equals {
			i++
			if i >= len(args) {
				return errors.New(flag + " needs a path")
			}
			value = args[i]
		}
		if value == "" {
			return errors.New(flag + " cannot be empty")
		}
		abs, err := filepath.Abs(value)
		if err != nil {
			return err
		}
		if err = os.Setenv(env, abs); err != nil {
			return err
		}
	}
	return nil
}
