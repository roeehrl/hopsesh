package cli

import "errors"

// Claude cloud sessions run on Linux; the prompt hook never runs on Windows.
func startDetachedServe(string) error {
	return errors.New("the cloud connector runs only in Linux or macOS cloud sessions")
}
