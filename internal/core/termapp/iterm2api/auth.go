package iterm2api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// Errors from Connect and Probe. Every one of them means "use the AppleScript path": the
// API is optional and its absence is never shown to the user as a failure.
var (
	// ErrUnavailable: the API server is not listening (iTerm2 is not running, the user has
	// not enabled the API, or this is not macOS).
	ErrUnavailable = errors.New("iterm2api: iTerm2's API is not available")
	// ErrNotAuthorized: the API is on but refused hopsesh, or the user declined the macOS
	// Automation prompt for hopsesh to control iTerm2.
	ErrNotAuthorized = errors.New("iterm2api: iTerm2's API did not authorize hopsesh")
	// ErrTooOld: this iTerm2 cannot hand out API credentials to other apps, or does not
	// speak this protocol.
	ErrTooOld = errors.New("iterm2api: this iTerm2 is too old for its API")
)

// Credentials is the cookie and key iTerm2 issues for one connection. Its value never
// appears in formatted output, so it cannot reach a log or an error by accident.
type Credentials struct {
	cookie, key string
}

// NewCredentials makes Credentials from a cookie and key (for tests and the fake server).
func NewCredentials(cookie, key string) Credentials { return Credentials{cookie, key} }

func (c Credentials) empty() bool { return c.cookie == "" }

// String hides the secret.
func (Credentials) String() string { return "iterm2api.Credentials{…}" }

// GoString hides the secret.
func (Credentials) GoString() string { return "iterm2api.Credentials{…}" }

// Format hides the secret for every verb.
func (c Credentials) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(c.String())) }

// CredentialSource asks iTerm2 for a fresh cookie and key on behalf of appName.
type CredentialSource func(ctx context.Context, appName string) (Credentials, error)

// The environment variables iTerm2 sets for programs it launches as API scripts.
const (
	envCookie = "ITERM2_COOKIE"
	envKey    = "ITERM2_KEY"
)

// takeEnvCredentials returns the cookie and key from the environment, if iTerm2 put them
// there, and removes them so no child process can inherit them (they are single-use
// anyway).
func takeEnvCredentials() Credentials {
	c := Credentials{cookie: os.Getenv(envCookie), key: os.Getenv(envKey)}
	if c.cookie != "" || c.key != "" {
		_ = os.Unsetenv(envCookie)
		_ = os.Unsetenv(envKey)
	}
	return c
}

// ScrubEnv returns env (in os.Environ form) without iTerm2's API credentials. Every child
// hopsesh starts from a context that might hold them should get its environment through
// this.
func ScrubEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(k, envCookie) || strings.EqualFold(k, envKey) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

var appNameOK = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$`)

// cookieScript asks a running iTerm2 for a cookie and key. It never launches iTerm2: the
// running check comes first, and the client asks only after a handshake showed the API
// listening.
func cookieScript(appName string) string {
	return `if application "iTerm2" is running then
	tell application "iTerm2" to request cookie and key for app named "` + appName + `"
else
	error "iTerm2 is not running" number -600
end if
`
}

// osascript runs an AppleScript given on standard input (so nothing secret or quoted is on
// a command line) and returns its standard output and error. A variable so tests can
// replace it; no test runs the real one.
var osascript = func(ctx context.Context, script string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-")
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = ScrubEnv(os.Environ())
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

var appleScriptErrNum = regexp.MustCompile(`\((-?\d+)\)\s*$`)

// AppleScriptCredentials asks iTerm2 for a cookie and key through AppleScript. The first
// call shows macOS's "hopsesh wants to control iTerm2" prompt; a declined prompt maps to
// ErrNotAuthorized.
func AppleScriptCredentials(ctx context.Context, appName string) (Credentials, error) {
	if runtime.GOOS != "darwin" {
		return Credentials{}, ErrUnavailable
	}
	if !appNameOK.MatchString(appName) {
		return Credentials{}, fmt.Errorf("iterm2api: app name %q is not allowed", appName)
	}
	stdout, stderr, err := osascript(ctx, cookieScript(appName))
	return parseCookieOutput(stdout, stderr, err)
}

// parseCookieOutput maps osascript's result to credentials or one of the package errors.
// Errors carry the AppleScript error number only, never the output.
func parseCookieOutput(stdout, stderr []byte, runErr error) (Credentials, error) {
	if runErr != nil {
		num := ""
		if m := appleScriptErrNum.FindSubmatch(bytes.TrimSpace(stderr)); m != nil {
			num = string(m[1])
		}
		switch num {
		case "-1743", "-1744": // errAEEventNotPermitted, errAEEventWouldRequireUserConsent
			return Credentials{}, fmt.Errorf("%w (macOS Automation permission %s)", ErrNotAuthorized, num)
		case "-2740", "-2741": // the dictionary has no such command: iTerm2 before 3.3.9
			return Credentials{}, fmt.Errorf("%w (AppleScript %s)", ErrTooOld, num)
		case "-600", "-609", "-10810": // not running, connection invalid, launch failure
			return Credentials{}, fmt.Errorf("%w (AppleScript %s)", ErrUnavailable, num)
		case "":
			return Credentials{}, fmt.Errorf("%w (osascript failed)", ErrUnavailable)
		}
		return Credentials{}, fmt.Errorf("%w (AppleScript error %s)", ErrNotAuthorized, num)
	}
	cookie, key, ok := strings.Cut(strings.TrimSpace(string(stdout)), " ")
	if !ok || cookie == "" || key == "" || strings.ContainsAny(cookie+key, " \r\n\t") {
		return Credentials{}, fmt.Errorf("%w (unexpected reply to the cookie request)", ErrNotAuthorized)
	}
	return Credentials{cookie: cookie, key: key}, nil
}
