//go:build !darwin && !windows

package secrets

const storeName = "a password store"

func available() bool           { return false }
func set(string, string) error  { return ErrUnavailable }
func get(string) (string, bool) { return "", false }
func del(string) error          { return nil }
