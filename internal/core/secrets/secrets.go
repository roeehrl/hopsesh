// Package secrets keeps machine passwords in the system's password store when the user
// asks for it: the macOS Keychain or Windows Credential Manager. A password never appears
// in a program's arguments. Other systems have no store here: passwords are asked for
// each session.
package secrets

import (
	"errors"
	"strings"
)

// ErrUnavailable means there is no password store on this system.
var ErrUnavailable = errors.New("remembering passwords needs the macOS Keychain or Windows Credential Manager")

// Available reports whether passwords can be remembered on this system.
func Available() bool { return available() }

// Set stores (or replaces) the password for an account (e.g. "studio me@10.0.0.5").
func Set(account, password string) error {
	if !Available() {
		return ErrUnavailable
	}
	if strings.ContainsAny(password, "\n\r") {
		return errors.New("the password contains a line break")
	}
	return set(account, password)
}

// Get returns the stored password for an account.
func Get(account string) (string, bool) {
	if !Available() {
		return "", false
	}
	return get(account)
}

// Delete removes the stored password for an account (no error when there is none).
func Delete(account string) error {
	if !Available() {
		return nil
	}
	return del(account)
}

// StoreName names this system's password store for people: "the Keychain".
func StoreName() string { return storeName }

// Account is the store's account name for a machine.
func Account(name, destination string) string { return name + " " + destination }
