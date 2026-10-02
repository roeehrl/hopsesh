package inventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/transport"
)

// KeyLogin is what SetupKeyLogin did.
type KeyLogin struct {
	PublicKey string `json:"publicKey"` // the local key file whose public half was installed
	Created   bool   `json:"created"`   // the key was made for this
	Added     bool   `json:"added"`     // false: the machine already had it
}

// installKey appends a public key to ~/.ssh/authorized_keys unless it is there already,
// keeping the permissions sshd insists on.
const installKey = `set -e
umask 077
mkdir -p "$HOME/.ssh"
chmod 700 "$HOME/.ssh"
f="$HOME/.ssh/authorized_keys"
touch "$f"
chmod 600 "$f"
if grep -qxF "$1" "$f"; then echo present; exit 0; fi
if [ -n "$(tail -c 1 "$f")" ]; then printf '\n' >> "$f"; fi
printf '%s\n' "$1" >> "$f"
echo added`

// ErrNoLocalKey means this machine has no SSH key that ssh would offer to the machine;
// SetupKeyLogin can create one (createKey).
var ErrNoLocalKey = errors.New("this machine has no SSH key that ssh would use for it")

// SetupKeyLogin moves a password machine to key login: it logs in once with the password,
// adds this machine's public key to the account's authorized_keys, and checks that a
// login without a password now works. It does not change the configuration; the caller
// switches the machine to key login when this succeeds.
func SetupKeyLogin(ctx context.Context, h config.Host, pw transport.PasswordFunc, createKey bool, stateDir string, log *audit.Log) (*KeyLogin, error) {
	pubPath, created, err := localPublicKey(ctx, h.Destination, createKey)
	if err != nil {
		return nil, err
	}
	pub, err := os.ReadFile(pubPath)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(string(pub))
	if strings.ContainsAny(key, "\r\n") || !strings.HasPrefix(key, "ssh-") && !strings.HasPrefix(key, "ecdsa-") && !strings.HasPrefix(key, "sk-") {
		return nil, fmt.Errorf("%s does not look like an SSH public key", pubPath)
	}
	res := &KeyLogin{PublicKey: pubPath, Created: created}

	conn, err := transport.NewConn(h.Destination, stateDir, log)
	if err != nil {
		return nil, err
	}
	if h.TailscaleName != "" && h.TailscaleName != h.Destination {
		conn.Fallbacks = []string{h.TailscaleName}
	}
	conn.Password = pw
	facts, err := conn.Probe(ctx)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if facts.OS == "windows" {
		conn.Close()
		return nil, errors.New("setting up key login on Windows is not automated (administrators use C:\\ProgramData\\ssh\\administrators_authorized_keys); add the key by hand, then switch the machine to key login")
	}
	out, err := conn.RunSh(ctx, installKey, key)
	conn.Close()
	if err != nil {
		return nil, fmt.Errorf("adding the key: %w", err)
	}
	res.Added = strings.TrimSpace(string(out)) == "added"
	if log != nil {
		log.Write(audit.Entry{Action: "hosts.setup-key", Host: h.Name, Detail: map[string]any{"key": filepath.Base(pubPath), "added": res.Added}})
	}

	// The proof: a fresh connection with no password at all.
	check, err := transport.NewConn(h.Destination, stateDir, log)
	if err != nil {
		return res, err
	}
	defer check.Close()
	if h.TailscaleName != "" && h.TailscaleName != h.Destination {
		check.Fallbacks = []string{h.TailscaleName}
	}
	if _, err := check.Probe(ctx); err != nil {
		return res, fmt.Errorf("the key was added, but logging in without a password still fails (%v); the machine may not allow key login, or ssh offers a different key", err)
	}
	return res, nil
}

// localPublicKey returns the public half of the first key ssh would offer to dest (its
// IdentityFile settings, as `ssh -G` reports them), or, with create, makes
// ~/.ssh/id_ed25519 (no passphrase, as ssh-keygen's defaults) when there is none.
func localPublicKey(ctx context.Context, dest string, create bool) (string, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	var candidates []string
	if out, err := exec.CommandContext(ctx, "ssh", "-G", dest).Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if f, ok := strings.CutPrefix(line, "identityfile "); ok {
				f = strings.TrimSpace(f)
				if rest, ok := strings.CutPrefix(f, "~/"); ok {
					f = filepath.Join(home, rest)
				}
				candidates = append(candidates, f)
			}
		}
	}
	dir := filepath.Join(home, ".ssh")
	if len(candidates) == 0 {
		for _, n := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			candidates = append(candidates, filepath.Join(dir, n))
		}
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if _, err := os.Stat(p + ".pub"); err == nil {
			return p + ".pub", false, nil
		}
	}
	p := filepath.Join(dir, "id_ed25519")
	if _, err := os.Stat(p); err == nil {
		return "", false, errors.New("~/.ssh/id_ed25519 has no .pub file next to it; recreate it with: ssh-keygen -y -f ~/.ssh/id_ed25519 > ~/.ssh/id_ed25519.pub")
	}
	if !create {
		return "", false, ErrNoLocalKey
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false, err
	}
	host, _ := os.Hostname()
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "hopsesh@"+host, "-f", p)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", false, fmt.Errorf("creating an SSH key: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return p + ".pub", true, nil
}
