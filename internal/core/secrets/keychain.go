// Package secrets keeps machine passwords in the macOS Keychain when the user asks for
// it. The password reaches the security tool on its standard input, never in its
// arguments. Other systems have no store here: passwords are asked for each session.
package secrets

import (
	"bytes"
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

const service = "hopsesh"

// Available reports whether passwords can be remembered on this system.
func Available() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	_, err := exec.LookPath("security")
	return err == nil
}

// ErrUnavailable means there is no password store on this system.
var ErrUnavailable = errors.New("remembering passwords needs the macOS Keychain")

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// Set stores (or replaces) the password for an account (e.g. "studio me@10.0.0.5").
func Set(account, password string) error {
	if !Available() {
		return ErrUnavailable
	}
	if strings.ContainsAny(password, "\n\r") {
		return errors.New("the password contains a line break")
	}
	cmd := exec.Command("security", "-i")
	cmd.Stdin = strings.NewReader("add-generic-password -U -s " + quote(service) + " -a " + quote(account) +
		" -l " + quote("hopsesh: "+account) + " -w " + quote(password) + "\n")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return errors.New("Keychain: " + strings.TrimSpace(out.String()))
	}
	if strings.Contains(out.String(), "error") {
		return errors.New("Keychain: " + strings.TrimSpace(out.String()))
	}
	return nil
}

// Get returns the stored password for an account.
func Get(account string) (string, bool) {
	if !Available() {
		return "", false
	}
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-a", account, "-w").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(out), "\n"), true
}

// Delete removes the stored password for an account (no error when there is none).
func Delete(account string) error {
	if !Available() {
		return nil
	}
	_ = exec.Command("security", "delete-generic-password", "-s", service, "-a", account).Run()
	return nil
}

// Account is the Keychain account name for a machine.
func Account(name, destination string) string { return name + " " + destination }
