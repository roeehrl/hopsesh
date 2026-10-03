//go:build !darwin && !windows

package update

import (
	"context"
	"errors"
)

func installMacApp(context.Context, string, []byte) error { return errors.New("not macOS") }

func setInstalledVersion(string) {}
