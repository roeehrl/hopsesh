package inventory

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
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/transport"
)

// The optional remote helper is a copy of hopsesh on the other machine that lists its
// sessions in one SSH command (`hopsesh agent --json`) instead of many SFTP reads. It is
// deployed only when you ask, pinned by SHA-256, listens on nothing, and is removable.
// Moving a session still reads files over SFTP. macOS and Linux machines only.

// HelperRel is where the helper lives, relative to the remote home directory.
const HelperRel = ".local/share/hopsesh/bin/hopsesh"

// AgentSchema is the agent output format this build understands.
const AgentSchema = "hopsesh.agent/v1"

// ErrHelperMismatch means the helper on the machine is not the one hopsesh uploaded.
var ErrHelperMismatch = errors.New("the helper on that machine does not match the one hopsesh installed (not run)")

// HelperPath is the helper's absolute path on a machine.
func HelperPath(f *transport.Facts) string { return path.Join(f.Home, HelperRel) }

// goArch maps `uname -m` to Go's architecture names.
func goArch(unameM string) string {
	switch strings.ToLower(unameM) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return strings.ToLower(unameM)
}

// HelperBinary picks the local hopsesh binary to upload: explicit, or this program when it
// can run there (same OS and architecture, or a universal macOS build to a Mac).
func HelperBinary(explicit string, f *transport.Facts) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if f.OS == "windows" {
		return "", errors.New("the helper supports macOS and Linux machines; Windows machines are scanned without it")
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if strings.Contains(exe, ".app/Contents/MacOS/") { // the app: use the CLI inside the bundle
		exe = filepath.Join(filepath.Dir(exe), "..", "Resources", "bin", "hopsesh")
	}
	if f.OS != runtime.GOOS || (goArch(f.Arch) != runtime.GOARCH && f.OS != "darwin") {
		return "", fmt.Errorf("this hopsesh is for %s/%s and the machine is %s/%s; download hopsesh for that machine and pass --binary",
			runtime.GOOS, runtime.GOARCH, f.OS, goArch(f.Arch))
	}
	return exe, nil
}

func fileSHA256(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// runPinned is the remote script that checks the helper's hash before running it.
const runPinned = `f=$1; want=$2; shift 2
if command -v sha256sum >/dev/null 2>&1; then h=$(sha256sum "$f" | cut -c1-64)
else h=$(shasum -a 256 "$f" | cut -c1-64); fi
[ "$h" = "$want" ] || { echo "hopsesh: helper hash mismatch" >&2; exit 97; }
exec "$f" "$@"`

// InstallHelper uploads bin to the machine, checks it runs and matches, and returns its
// SHA-256 for the config.
func InstallHelper(ctx context.Context, conn *transport.Conn, f *transport.Facts, bin string) (string, error) {
	if f.OS == "windows" {
		return "", errors.New("the helper supports macOS and Linux machines")
	}
	sum, err := fileSHA256(bin)
	if err != nil {
		return "", err
	}
	src, err := os.Open(bin)
	if err != nil {
		return "", err
	}
	defer src.Close()
	rfs, err := conn.OpenSFTP(ctx, false)
	if err != nil {
		return "", fmt.Errorf("SFTP: %w", err)
	}
	defer rfs.Close()
	dst := HelperPath(f)
	if err := rfs.WriteFile(dst, src, 0o755); err != nil {
		return "", fmt.Errorf("upload to %s: %w", dst, err)
	}
	out, err := conn.RunSh(ctx, runPinned, dst, sum, "version")
	if err != nil {
		_, _ = conn.RunSh(ctx, `rm -f "$1"`, dst)
		var re *transport.RemoteError
		if errors.As(err, &re) && re.Code == 97 {
			return "", errors.New("the uploaded helper's hash does not match (upload damaged?); removed it")
		}
		return "", fmt.Errorf("the helper does not run there (wrong architecture?); removed it: %w", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(out)), "hopsesh ") {
		return "", fmt.Errorf("unexpected helper output: %q", firstLineOf(out))
	}
	return sum, nil
}

// RemoveHelper deletes the helper from the machine.
func RemoveHelper(ctx context.Context, conn *transport.Conn, f *transport.Facts) error {
	_, err := conn.RunSh(ctx, `rm -f "$1" && d=$(dirname "$1") && rmdir "$d" "$(dirname "$d")" 2>/dev/null; exit 0`, HelperPath(f))
	return err
}

// runHelper lists the machine's sessions through the pinned helper.
func runHelper(ctx context.Context, conn *transport.Conn, f *transport.Facts, sum string) ([]Session, error) {
	out, err := conn.RunSh(ctx, runPinned, HelperPath(f), sum, "agent", "--json")
	if err != nil {
		var re *transport.RemoteError
		if errors.As(err, &re) && re.Code == 97 {
			return nil, ErrHelperMismatch
		}
		return nil, err
	}
	var resp struct {
		Schema  string  `json:"schema"`
		Machine Machine `json:"machine"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &resp); err != nil {
		return nil, fmt.Errorf("helper output: %w", err)
	}
	if resp.Schema != AgentSchema {
		return nil, fmt.Errorf("helper speaks %q, this hopsesh expects %q (reinstall the helper)", resp.Schema, AgentSchema)
	}
	return resp.Machine.Sessions, nil
}

func firstLineOf(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
