package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// AppTarget chooses a per-user install. Linux distributions currently publish the
// headless CLI only; an unshipped GUI archive must never be advertised as available.
func AppTarget() (Target, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Target{}, err
	}
	switch runtime.GOOS {
	case "darwin":
		return Target{Kind: KindMacApp, Path: filepath.Join(home, "Applications", "hopsesh.app")}, nil
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			return Target{}, errors.New("LOCALAPPDATA is unavailable")
		}
		return Target{Kind: KindWindowsApp, Path: filepath.Join(base, "Programs", "hopsesh")}, nil
	default:
		return Target{}, errors.New("this platform has no published GUI package; install the CLI and use hopsesh runtime start for headless operation")
	}
}

// InstallApp installs a missing app from the same signed manifest used by updates.
// Existing bundles are refused: their own updater preserves signer and rollback rules.
func InstallApp(ctx context.Context, rel *Release, target Target) error {
	if PublicKey == "" {
		return ErrNoKey
	}
	if rel == nil {
		return errors.New("missing release")
	}
	if !filepath.IsAbs(target.Path) {
		return errors.New("app destination must be absolute")
	}
	if target.Kind != KindMacApp && target.Kind != KindWindowsApp {
		return errors.New("not an app target")
	}
	if target.Kind == KindMacApp && runtime.GOOS != "darwin" || target.Kind == KindWindowsApp && runtime.GOOS != "windows" {
		return errors.New("app package does not match this OS")
	}
	if _, err := os.Lstat(target.Path); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return errors.New("an app already exists at the destination; use its updater")
	}
	data, err := download(ctx, rel, AssetName(target, rel.Version))
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(target.Path), 0755); err != nil {
		return err
	}
	switch target.Kind {
	case KindMacApp:
		return installMacApp(ctx, target.Path, data, false)
	case KindWindowsApp:
		stage, err := os.MkdirTemp(filepath.Dir(target.Path), ".hopsesh-install-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(stage)
		if err = installWindowsApp(stage, data, ""); err != nil {
			return err
		}
		// Publish the complete directory once, so a failed extraction cannot expose a
		// half-installed app or overwrite any independently existing destination.
		if _, err = os.Lstat(target.Path); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("app destination appeared during installation")
		}
		if err = os.Rename(stage, target.Path); err != nil {
			return err
		}
		setInstalledVersion(rel.Version)
		return nil
	}
	return errors.New("unsupported app target")
}
