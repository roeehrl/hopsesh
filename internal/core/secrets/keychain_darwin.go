package secrets

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// The macOS Keychain, through the security tool; the password reaches it on its standard
// input.

const (
	storeName = "the Keychain"
	service   = "hopsesh"
)

func available() bool {
	_, err := exec.LookPath("security")
	return err == nil
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func set(account, password string) error {
	cmd := proc.Command("security", "-i")
	cmd.Stdin = strings.NewReader("add-generic-password -U -s " + quote(service) + " -a " + quote(account) +
		" -l " + quote("hopsesh: "+account) + " -w " + quote(password) + "\n")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil || strings.Contains(out.String(), "error") {
		return errors.New("Keychain: " + strings.TrimSpace(out.String()))
	}
	return nil
}

func get(account string) (string, bool) {
	out, err := proc.Command("security", "find-generic-password", "-s", service, "-a", account, "-w").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(out), "\n"), true
}

func del(account string) error {
	_ = proc.Command("security", "delete-generic-password", "-s", service, "-a", account).Run()
	return nil
}
