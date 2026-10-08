package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/proc"
	"golang.org/x/sys/unix"
)

// installMacApp replaces the app bundle with the hopsesh.app in a release disk image. The
// new app is copied next to the old one (same volume), must be signed by the same team and
// pass Gatekeeper (notarized), and then takes the old one's place in one rename.
func installMacApp(ctx context.Context, bundle string, dmg []byte, replace bool) error {
	parent := filepath.Dir(bundle)
	work, err := os.MkdirTemp(parent, ".hopsesh-update-")
	if err != nil {
		return fmt.Errorf("cannot write to %s (%w); download the new app from the release page instead", parent, err)
	}
	defer os.RemoveAll(work)
	image := filepath.Join(work, "hopsesh.dmg")
	if err := os.WriteFile(image, dmg, 0o600); err != nil {
		return err
	}
	mnt := filepath.Join(work, "mnt")
	if err := os.Mkdir(mnt, 0o700); err != nil {
		return err
	}
	if err := attachImage(ctx, image, mnt); err != nil {
		return err
	}
	defer detachImage(mnt)
	src := filepath.Join(mnt, "hopsesh.app")
	if _, err := os.Stat(src); err != nil {
		return errors.New("the disk image has no hopsesh.app; not installing")
	}
	next := filepath.Join(work, "hopsesh.app")
	if out, err := proc.CommandContext(ctx, "ditto", src, next).CombinedOutput(); err != nil {
		return fmt.Errorf("cannot copy the new app: %s", strings.TrimSpace(string(out)))
	}
	if err := sameSigner(bundle, next); err != nil {
		return err
	}
	// The manifest authenticates the publisher; first installs also require an
	// actual Developer ID signature and Gatekeeper acceptance.
	if teamID(next) == "" {
		return errors.New("new app has no Developer ID team signature")
	}
	if out, err := proc.CommandContext(ctx, "codesign", "--verify", "--deep", "--strict", next).CombinedOutput(); err != nil {
		return fmt.Errorf("new app signature rejected: %s", strings.TrimSpace(string(out)))
	}
	if out, err := proc.CommandContext(ctx, "spctl", "--assess", "--type", "execute", next).CombinedOutput(); err != nil {
		return fmt.Errorf("macOS does not accept the new app (%s); not installing", strings.TrimSpace(string(out)))
	}
	return publishMacApp(next, bundle, replace)
}

func publishMacApp(next, bundle string, replace bool) error {
	if !replace {
		// The destination can appear after the missing-app review or while the
		// signed image is being downloaded/verified. A final exclusive rename
		// refuses even an empty directory or dangling link without touching it.
		if err := unix.RenameatxNp(unix.AT_FDCWD, next, unix.AT_FDCWD, bundle, unix.RENAME_EXCL); err != nil {
			return fmt.Errorf("cannot publish new app without replacing an existing destination: %w", err)
		}
		return nil
	}
	if _, err := os.Lstat(bundle); errors.Is(err, os.ErrNotExist) {
		return os.Rename(next, bundle)
	} else if err != nil {
		return err
	}

	old := filepath.Join(filepath.Dir(next), "old.app")
	if err := os.Rename(bundle, old); err != nil {
		return fmt.Errorf("cannot replace %s (%w); download the new app from the release page instead", bundle, err)
	}
	if err := os.Rename(next, bundle); err != nil {
		_ = os.Rename(old, bundle)
		return err
	}
	return nil
}

func setInstalledVersion(string) {}

// attachImage mounts a disk image read-only at mnt, hidden from Finder. macOS 27 deprecates
// hdiutil attach for diskutil image attach, which older systems lack.
func attachImage(ctx context.Context, image, mnt string) error {
	if proc.CommandContext(ctx, "diskutil", "image", "attach", "--readOnly", "--nobrowse", "--mountPoint", mnt, image).Run() == nil {
		return nil
	}
	if out, err := proc.CommandContext(ctx, "hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mnt, image).CombinedOutput(); err != nil {
		return fmt.Errorf("cannot open the disk image: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// detachImage ejects what attachImage mounted.
func detachImage(mnt string) {
	if proc.Command("diskutil", "eject", mnt).Run() != nil {
		_ = proc.Command("hdiutil", "detach", "-force", mnt).Run()
	}
}
