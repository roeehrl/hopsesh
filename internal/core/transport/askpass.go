package transport

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Password login. hopsesh prefers keys, but a machine can be marked as using a password.
// ssh then asks for it through SSH_ASKPASS, which points back at hopsesh itself
// (ssh runs "$SSH_ASKPASS <prompt>"; IsAskpass recognizes that call). That helper asks the running hopsesh process over a
// private Unix socket (in a 0700 folder, guarded by a per-connection token) and prints the
// password for ssh. The password never appears on a command line, in an environment
// variable or in a file; hopsesh keeps it in memory for the session only, unless the user
// chose to remember it in the macOS Keychain.

// PasswordFunc returns the SSH password for a destination, asking the person if needed.
// retry is true when the previous password was refused.
type PasswordFunc func(ctx context.Context, dest string, retry bool) (string, error)

// ErrPasswordCancelled means the person declined to enter a password.
var ErrPasswordCancelled = errors.New("no password was entered")

// IsAskpass reports whether this process was started by ssh as the askpass helper of a
// running hopsesh: ssh passes only the prompt, so the environment hopsesh gave ssh is the
// signal.
func IsAskpass() bool {
	return os.Getenv("HOPSESH_ASKPASS_TOKEN") != "" && os.Getenv("HOPSESH_ASKPASS_SOCK") != "" && len(os.Args) == 2
}

type askpassReq struct {
	Token  string `json:"token"`
	Prompt string `json:"prompt"`
}

type askpassResp struct {
	Answer string `json:"answer,omitempty"`
	OK     bool   `json:"ok"`
}

type askpassServer struct {
	path     string
	mu       sync.Mutex
	handlers map[string]func(prompt string) (string, bool)
}

var (
	srvOnce sync.Once
	srv     *askpassServer
	srvErr  error
)

func askpassDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "hopsesh-askpass")
	}
	return filepath.Join("/tmp", fmt.Sprintf("hopsesh-%d", os.Getuid()))
}

func startAskpass() (*askpassServer, error) {
	srvOnce.Do(func() {
		dir := askpassDir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			srvErr = err
			return
		}
		_ = os.Chmod(dir, 0o700)
		removeStaleSockets(dir)
		path := filepath.Join(dir, fmt.Sprintf("askpass-%d.sock", os.Getpid()))
		_ = os.Remove(path)
		ln, err := net.Listen("unix", path)
		if err != nil {
			srvErr = err
			return
		}
		_ = os.Chmod(path, 0o600)
		srv = &askpassServer{path: path, handlers: map[string]func(string) (string, bool){}}
		go srv.serve(ln)
	})
	return srv, srvErr
}

// removeStaleSockets deletes the sockets of hopsesh processes that have exited.
func removeStaleSockets(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	socks, _ := filepath.Glob(filepath.Join(dir, "askpass-*.sock"))
	for _, f := range socks {
		var pid int
		if _, err := fmt.Sscanf(filepath.Base(f), "askpass-%d.sock", &pid); err != nil || pid == os.Getpid() {
			continue
		}
		if p, err := os.FindProcess(pid); err == nil && p.Signal(syscall.Signal(0)) == nil {
			continue
		}
		_ = os.Remove(f)
	}
}

func (s *askpassServer) serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *askpassServer) handle(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Minute)) // the person may take a while
	var req askpassReq
	if err := json.NewDecoder(bufio.NewReader(c)).Decode(&req); err != nil {
		return
	}
	s.mu.Lock()
	h := s.handlers[req.Token]
	s.mu.Unlock()
	resp := askpassResp{}
	if h != nil {
		resp.Answer, resp.OK = h(req.Prompt)
	}
	_ = json.NewEncoder(c).Encode(resp)
}

func (s *askpassServer) register(h func(string) (string, bool)) (string, func()) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.handlers[tok] = h
	s.mu.Unlock()
	return tok, func() {
		s.mu.Lock()
		delete(s.handlers, tok)
		s.mu.Unlock()
	}
}

// IsPasswordPrompt reports whether an ssh askpass prompt asks for the account password
// (not a key passphrase or a host-key question, which hopsesh never answers). git also
// uses SSH_ASKPASS for HTTPS credentials ("Password for 'https://…': "); those are
// refused too, so the machine's password can only ever go to ssh.
func IsPasswordPrompt(prompt string) bool {
	p := strings.ToLower(prompt)
	if strings.Contains(p, "passphrase") || strings.Contains(p, "://") || strings.HasPrefix(p, "password for ") || strings.HasPrefix(p, "username") {
		return false
	}
	return strings.Contains(p, "password")
}

// AskpassMain is what "hopsesh <prompt>" runs when IsAskpass: it asks the hopsesh process that
// started ssh and prints the answer. It returns the exit code.
func AskpassMain(args []string) int {
	sock, tok := os.Getenv("HOPSESH_ASKPASS_SOCK"), os.Getenv("HOPSESH_ASKPASS_TOKEN")
	if sock == "" || tok == "" {
		fmt.Fprintln(os.Stderr, "hopsesh: askpass helper used outside hopsesh")
		return 1
	}
	answer, ok := askpassQuery(sock, tok, strings.Join(args, " "))
	if !ok {
		return 1
	}
	fmt.Println(answer)
	return 0
}

func askpassQuery(sock, tok, prompt string) (string, bool) {
	c, err := net.Dial("unix", sock)
	if err != nil {
		return "", false
	}
	defer c.Close()
	if err := json.NewEncoder(c).Encode(askpassReq{Token: tok, Prompt: prompt}); err != nil {
		return "", false
	}
	var resp askpassResp
	if err := json.NewDecoder(bufio.NewReader(c)).Decode(&resp); err != nil || !resp.OK {
		return "", false
	}
	return resp.Answer, true
}

// passwordEnv returns the environment that makes ssh ask this Conn for the password.
func (c *Conn) passwordEnv(ctx context.Context) ([]string, error) {
	if c.Password == nil {
		return nil, nil
	}
	c.pwMu.Lock()
	defer c.pwMu.Unlock()
	if c.pwToken == "" {
		s, err := startAskpass()
		if err != nil {
			return nil, fmt.Errorf("cannot ask for the password: %w", err)
		}
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		c.askpassExe = exe
		c.pwToken, c.pwRelease = s.register(func(prompt string) (string, bool) {
			if !IsPasswordPrompt(prompt) {
				return "", false
			}
			return c.answer(ctx)
		})
		c.pwSock = s.path
	}
	return []string{
		"SSH_ASKPASS=" + c.askpassExe,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=" + nonEmptyEnv("DISPLAY", ":0"),
		"HOPSESH_ASKPASS_SOCK=" + c.pwSock,
		"HOPSESH_ASKPASS_TOKEN=" + c.pwToken,
	}, nil
}

// answer returns the cached password, or asks the PasswordFunc once for this connection.
func (c *Conn) answer(ctx context.Context) (string, bool) {
	c.pwAskMu.Lock()
	defer c.pwAskMu.Unlock()
	c.pwAsked = true
	if c.pw != "" {
		return c.pw, true
	}
	pw, err := c.Password(ctx, c.Dest, c.pwRetry)
	if err != nil || pw == "" {
		if err == nil {
			err = ErrPasswordCancelled
		}
		c.pwErr = err
		return "", false
	}
	c.pw, c.pwErr = pw, nil
	return pw, true
}

// GitSSHEnv is the environment git needs to reach this machine with GitSSHCommand
// (the askpass settings when the machine uses a password).
func (c *Conn) GitSSHEnv(ctx context.Context) []string {
	env, _ := c.passwordEnv(ctx)
	if env != nil {
		// git must not ask anything itself (it would use the same helper).
		env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=")
	}
	return env
}

func nonEmptyEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (c *Conn) cachedPassword() string {
	c.pwAskMu.Lock()
	defer c.pwAskMu.Unlock()
	return c.pw
}

// forgetPassword drops the cached password; the next prompt is a retry.
func (c *Conn) forgetPassword() {
	c.pwAskMu.Lock()
	defer c.pwAskMu.Unlock()
	if c.pw != "" {
		c.pwRetry = true
	}
	c.pw = ""
}

func (c *Conn) passwordCancelled() bool {
	c.pwAskMu.Lock()
	defer c.pwAskMu.Unlock()
	return errors.Is(c.pwErr, ErrPasswordCancelled)
}

func (c *Conn) passwordAsked() bool {
	c.pwAskMu.Lock()
	defer c.pwAskMu.Unlock()
	return c.pwAsked
}
