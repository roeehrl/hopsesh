package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// Errors classified from ssh's own output (ssh exits 255 for every connection problem).
var (
	ErrHostKeyUnknown = errors.New("the machine's SSH host key is not trusted yet")
	ErrHostKeyChanged = errors.New("the machine's SSH host key CHANGED; refusing to connect")
	ErrAuth           = errors.New("SSH login was refused (no usable key, or the key needs a passphrase not loaded in your agent)")
	ErrUnreachable    = errors.New("the machine is unreachable")
	ErrNoSSH          = errors.New("no ssh client found on this machine")
	ErrWrongPassword  = errors.New("the machine did not accept the password")
	// ErrNoPasswordPrompt: login failed without the machine ever asking for a password
	// (password login is probably turned off there).
	ErrNoPasswordPrompt = errors.New("the machine refused the login without asking for a password (is password login turned off there?)")
)

// TailscaleCheckError means Tailscale SSH wants the user to re-authenticate in a browser.
type TailscaleCheckError struct{ URL string }

func (e *TailscaleCheckError) Error() string {
	return "Tailscale SSH needs you to approve this connection in a browser: " + e.URL
}

// Conn runs commands on one machine through the system ssh client.
type Conn struct {
	Dest     string // alias, user@host or host
	StateDir string // hopsesh's known_hosts and control sockets live here
	Log      *audit.Log
	Timeout  time.Duration
	// Fallbacks are alternative host names (e.g. the machine's Tailscale name) tried with
	// -o HostName=... when the destination's own host name does not resolve. The alias's
	// user, keys and other settings still apply.
	Fallbacks  []string
	override   string
	sshBinary  string
	controlDir string
	lnMu       sync.Mutex // held while probing, so parallel commands wait for the answer
	lnChecked  map[string][]gatedTarget

	// Password, when set, makes this a password-login machine: ssh asks for the password
	// through hopsesh (see askpass.go) and one connection is kept open and reused.
	Password   PasswordFunc
	pwMu       sync.Mutex
	pwAskMu    sync.Mutex
	pwToken    string
	pwSock     string
	pwRelease  func()
	askpassExe string
	pw         string
	pwRetry    bool
	pwAsked    bool
	pwErr      error
}

// NewConn prepares a connection (nothing is opened until the first command).
func NewConn(dest, stateDir string, log *audit.Log) (*Conn, error) {
	bin, err := exec.LookPath("ssh")
	if err != nil {
		return nil, ErrNoSSH
	}
	c := &Conn{Dest: dest, StateDir: stateDir, Log: log, Timeout: 30 * time.Second, sshBinary: bin}
	if runtime.GOOS != "windows" { // Windows OpenSSH has no ControlMaster
		// Short path: Unix socket paths are limited to ~104 bytes.
		// Unix socket paths are limited to ~104 bytes and macOS's TMPDIR is long, so use /tmp.
		c.controlDir = filepath.Join("/tmp", fmt.Sprintf("hopsesh-%d", os.Getuid()))
		_ = os.MkdirAll(c.controlDir, 0o700)
	}
	return c, nil
}

// Override returns the host name that replaced the destination's own (a fallback that
// worked), or "".
func (c *Conn) Override() string { return c.override }

// Prefer sets a host name to use from the start (a fallback that worked last time).
func (c *Conn) Prefer(name string) { c.override = name }

// KnownHostsFile is hopsesh's own known_hosts (keys the user trusted through hopsesh).
// The user's ~/.ssh/known_hosts is consulted too but never modified.
func (c *Conn) KnownHostsFile() string { return filepath.Join(c.StateDir, "known_hosts") }

func (c *Conn) baseArgs() []string {
	home, _ := os.UserHomeDir()
	userKH := filepath.Join(home, ".ssh", "known_hosts")
	batch := []string{"-o", "BatchMode=yes"}
	persist := "60"
	if c.Password != nil {
		// Ask for the password (through hopsesh), once per connection attempt, and keep the
		// authenticated connection open longer so later commands reuse it.
		batch = []string{"-o", "BatchMode=no", "-o", "NumberOfPasswordPrompts=1",
			"-o", "PreferredAuthentications=publickey,keyboard-interactive,password"}
		persist = "600"
	}
	args := append(batch,
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile="+quoteList(userKH, c.KnownHostsFile()),
		"-o", "ForwardAgent=no",
		"-o", "ForwardX11=no",
		"-o", "ClearAllForwardings=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "LogLevel=ERROR",
	)
	if c.controlDir != "" {
		args = append(args, "-o", "ControlMaster=auto", "-o", "ControlPath="+filepath.Join(c.controlDir, "%C"), "-o", "ControlPersist="+persist)
	}
	if c.override != "" {
		args = append(args, "-o", "HostName="+c.override)
	}
	return args
}

func quoteList(paths ...string) string {
	// ssh splits this option on whitespace; paths with spaces are quoted.
	var parts []string
	for _, p := range paths {
		if strings.ContainsAny(p, " \t") {
			p = `"` + p + `"`
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}

// Run executes a remote command line (interpreted by the remote user's shell). If the
// destination's host name cannot be resolved, each fallback name is tried once and the
// first that works is kept for the rest of the connection.
func (c *Conn) Run(ctx context.Context, remoteCmd string) ([]byte, error) {
	out, err := c.runRouted(ctx, remoteCmd)
	if c.Password == nil || !errors.Is(err, ErrAuth) {
		return out, err
	}
	if !c.passwordAsked() {
		return out, ErrNoPasswordPrompt
	}
	// A refused password: forget it and ask again (twice), then give up. Declining to
	// answer after a refusal still means the password was wrong.
	for tries := 0; ; tries++ {
		if c.passwordCancelled() {
			if tries == 0 {
				return out, ErrPasswordCancelled
			}
			return out, ErrWrongPassword
		}
		c.forgetPassword()
		if tries == 2 {
			return out, ErrWrongPassword
		}
		out, err = c.runRouted(ctx, remoteCmd)
		if !errors.Is(err, ErrAuth) {
			return out, err
		}
	}
}

// runRouted runs a command, trying the machine's other names when its own is unreachable.
func (c *Conn) runRouted(ctx context.Context, remoteCmd string) ([]byte, error) {
	out, err := c.run(ctx, remoteCmd)
	if err != nil && c.override != "" && retryable(err) {
		c.override = "" // a remembered fallback stopped working: try the destination itself
		out, err = c.run(ctx, remoteCmd)
	}
	if err == nil || c.override != "" || len(c.Fallbacks) == 0 || !retryable(err) {
		return out, err
	}
	for _, fb := range c.Fallbacks {
		c.override = fb
		if out2, err2 := c.run(ctx, remoteCmd); !retryable(err2) {
			return out2, err2
		}
	}
	c.override = ""
	return out, err
}

// retryable reports whether another route to the same machine is worth trying: the name
// did not resolve, or macOS local network privacy blocked it (a Tailscale route is not
// subject to that).
func retryable(err error) bool {
	var ln *LocalNetworkError
	return unresolvable(err) || errors.As(err, &ln)
}

func unresolvable(err error) bool {
	return err != nil && errors.Is(err, ErrUnreachable) && strings.Contains(strings.ToLower(err.Error()), "could not resolve")
}

func (c *Conn) run(ctx context.Context, remoteCmd string) ([]byte, error) {
	// Before the command's own timeout: the person may take a while to answer the
	// macOS local network prompt.
	gated := c.localNetworkPreflight(ctx)
	env, err := c.passwordEnv(ctx)
	if err != nil {
		return nil, err
	}
	if c.Timeout > 0 {
		timeout := c.Timeout
		if c.Password != nil && c.cachedPassword() == "" {
			timeout += 5 * time.Minute // the person is typing the password
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	args := append(c.baseArgs(), c.Dest, "--", remoteCmd)
	cmd := proc.CommandContext(ctx, c.sshBinary, args...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err = cmd.Run()
	// CommandContext may return ExitError for the process it killed (exit 1 on
	// Windows). That is a local cancellation, never a remote command status.
	if err != nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	c.Log.Write(audit.Entry{Action: "ssh.exec", Host: c.Dest, Detail: map[string]any{
		"command": firstLine(remoteCmd), "ms": time.Since(start).Milliseconds(), "ok": err == nil}})
	if err != nil {
		cerr := classify(err, stderr.String())
		c.recheckLocalNetwork(ctx, cerr, gated)
		return stdout.Bytes(), c.explainLocalNetwork(cerr, gated)
	}
	return stdout.Bytes(), nil
}

// RunSh runs a POSIX sh script with arguments on a Unix-like machine, quoting everything.
func (c *Conn) RunSh(ctx context.Context, script string, args ...string) ([]byte, error) {
	var b strings.Builder
	b.WriteString("sh -c ")
	b.WriteString(ShQuote(script))
	b.WriteString(" hopsesh")
	for _, a := range args {
		b.WriteByte(' ')
		b.WriteString(ShQuote(a))
	}
	return c.Run(ctx, b.String())
}

// sftpCommand returns the command that starts the SFTP subsystem over ssh. It uses its
// own compressed connection: transcripts are JSON and compress ~10x, which matters on
// slow uplinks (a laptop on a phone hotspot).
func (c *Conn) sftpCommand(ctx context.Context) *exec.Cmd {
	// ssh keeps the FIRST value of a repeated option, so these go before baseArgs.
	args := append([]string{"-o", "Compression=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none"}, c.baseArgs()...)
	args = append(args, "-s", c.Dest, "sftp")
	cmd := proc.CommandContext(ctx, c.sshBinary, args...)
	if env, err := c.passwordEnv(ctx); err == nil && env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd
}

// Pipe is a remote command whose standard input and output are connected to hopsesh
// (hopsesh peer).
type Pipe struct {
	In     io.WriteCloser
	Out    io.Reader
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stderr *strings.Builder
}

// Stderr is what the remote command wrote to standard error so far.
func (p *Pipe) Stderr() string { return p.stderr.String() }

// Close ends the command.
func (p *Pipe) Close() error {
	_ = p.In.Close()
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		p.cancel()
		return err
	case <-time.After(5 * time.Second):
		p.cancel()
		return <-done
	}
}

// StartPipe runs a remote command line with its standard input and output connected, on
// its own compressed connection (like SFTP), until Close. The command lives until Close,
// not until ctx ends.
func (c *Conn) StartPipe(ctx context.Context, remoteCmd string) (*Pipe, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	env, err := c.passwordEnv(ctx)
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithCancel(context.Background())
	args := append([]string{"-o", "Compression=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none"}, c.baseArgs()...)
	args = append(args, c.Dest, "--", remoteCmd)
	cmd := proc.CommandContext(pctx, c.sshBinary, args...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	p := &Pipe{In: in, Out: out, cmd: cmd, cancel: cancel, stderr: &strings.Builder{}}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	c.Log.Write(audit.Entry{Action: "ssh.pipe", Host: c.Dest, Detail: map[string]any{"command": firstLine(remoteCmd)}})
	return p, nil
}

// Close ends the shared control connection, if any.
func (c *Conn) Close() {
	defer func() {
		c.pwMu.Lock()
		if c.pwRelease != nil {
			c.pwRelease()
			c.pwRelease, c.pwToken = nil, ""
		}
		c.pwMu.Unlock()
		c.forgetPassword()
	}()
	if c.controlDir == "" {
		return
	}
	args := append(c.baseArgs(), "-O", "exit", c.Dest)
	// A best-effort local control-socket cleanup must not hold a scan (or app
	// shutdown) hostage when ssh hangs reading config or contacting its master.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := proc.CommandContext(ctx, c.sshBinary, args...)
	cmd.WaitDelay = 100 * time.Millisecond
	_ = cmd.Run()
}

// tsCheckURL finds the Tailscale SSH check link ssh prints: at the start of the output or
// after whitespace, never inside another URL.
var tsCheckURL = regexp.MustCompile(`(?:^|\s)(https://login\.tailscale\.com/\S+)`)

func classify(err error, stderr string) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() != 255 {
		return &RemoteError{Code: ee.ExitCode(), Stderr: strings.TrimSpace(stderr)}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w (timed out): %w", ErrUnreachable, err)
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "remote host identification has changed"):
		return ErrHostKeyChanged
	case strings.Contains(low, "host key verification failed"), strings.Contains(low, "no ") && strings.Contains(low, "host key is known"):
		return ErrHostKeyUnknown
	case tsCheckURL.MatchString(stderr):
		return &TailscaleCheckError{URL: tsCheckURL.FindStringSubmatch(stderr)[1]}
	case strings.Contains(low, "permission denied"), strings.Contains(low, "too many authentication failures"):
		return ErrAuth
	case strings.Contains(low, "could not resolve hostname"), strings.Contains(low, "connection timed out"),
		strings.Contains(low, "no route to host"), strings.Contains(low, "connection refused"),
		strings.Contains(low, "network is unreachable"), strings.Contains(low, "operation timed out"):
		return fmt.Errorf("%w: %s", ErrUnreachable, strings.TrimSpace(stderr))
	}
	return fmt.Errorf("ssh: %v: %s", err, strings.TrimSpace(stderr))
}

// RemoteError is a remote command that ran but failed.
type RemoteError struct {
	Code   int
	Stderr string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("remote command failed (exit %d): %s", e.Code, e.Stderr)
}

// ShQuote quotes a string for a POSIX shell.
func ShQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// PSQuote quotes a string for PowerShell.
func PSQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// GitSSHCommand is a GIT_SSH_COMMAND that connects the way this Conn does (batch mode,
// strict host keys against the same known_hosts files, any remembered host name).
func (c *Conn) GitSSHCommand() string {
	parts := []string{ShQuote(c.sshBinary)}
	for _, a := range c.baseArgs() {
		parts = append(parts, ShQuote(a))
	}
	return strings.Join(parts, " ")
}

// GitURL is an scp-style git URL for a repository path on this machine.
func (c *Conn) GitURL(path string) string { return c.Dest + ":" + path }
