package transport

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// When ssh-keyscan returns no keys (Windows' does that for some OpenSSH servers), the key
// comes from one ssh connection with a throwaway known_hosts: what ssh accepted into it.
func TestHostKeyViaSSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in ssh is a shell script")
	}
	dir := t.TempDir()
	key := "AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"
	fake := filepath.Join(dir, "ssh")
	// Like ssh with StrictHostKeyChecking=accept-new: write the key, then fail the login.
	script := "#!/bin/sh\nfor a; do case \"$a\" in UserKnownHostsFile=*) f=\"${a#UserKnownHostsFile=}\";; esac; done\n" +
		"echo \"[studio]:2222 ssh-ed25519 " + key + "\" > \"$f\"\necho 'Permission denied' >&2\nexit 255\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Conn{Dest: "studio", sshBinary: fake}
	keys := parseHostKeys(c.hostKeyViaSSH(t.Context()), "[studio]:2222")
	if len(keys) != 1 || keys[0].Type != "ssh-ed25519" || keys[0].Line != "[studio]:2222 ssh-ed25519 "+key {
		t.Fatalf("keys: %+v", keys)
	}
	if got := parseHostKeys([]byte("# studio:2222 SSH-2.0-OpenSSH_9.6p1 Ubuntu\n"), "studio"); len(got) != 0 {
		t.Fatalf("a banner is not a key: %+v", got)
	}
}
