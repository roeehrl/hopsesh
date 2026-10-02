package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/secrets"
	"github.com/roeehrl/hopsesh/internal/core/transport"
)

// passwords answers ssh's password questions for machines that log in with a password:
// from --password-stdin, the macOS Keychain (when the machine is set to remember it),
// or a hidden prompt in the terminal. Passwords live in memory for this run only.
type passwords struct {
	mu       sync.Mutex // one prompt at a time (machines are scanned in parallel)
	cache    map[string]string
	stdin    *string
	noPrompt bool // inside the full-screen UI: never prompt (passwords were asked first)
}

var pwStdinOnce sync.Once

func (a *app) passwordFor(h config.Host) transport.PasswordFunc {
	if a.pw == nil {
		a.pw = &passwords{cache: map[string]string{}}
	}
	p := a.pw
	acct := secrets.Account(h.Name, h.Destination)
	return func(ctx context.Context, dest string, retry bool) (string, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if retry {
			delete(p.cache, dest)
			_ = secrets.Delete(acct) // a refused password must not come back from the Keychain
		}
		if pw := p.cache[dest]; pw != "" {
			return pw, nil
		}
		if a.pwStdin && !retry {
			pwStdinOnce.Do(func() {
				b, _ := io.ReadAll(io.LimitReader(a.in, 4096))
				s := strings.TrimRight(string(b), "\r\n")
				p.stdin = &s
			})
			if p.stdin != nil && *p.stdin != "" {
				p.cache[dest] = *p.stdin
				return *p.stdin, nil
			}
		}
		if h.Keychain && !retry {
			if pw, ok := secrets.Get(acct); ok && pw != "" {
				p.cache[dest] = pw
				return pw, nil
			}
		}
		if p.noPrompt || a.pwStdin {
			return "", transport.ErrPasswordCancelled
		}
		label := fmt.Sprintf("Password for %s (%s)", h.Name, dest)
		if retry {
			label = "That password was not accepted. " + label
		}
		pw, err := readSecret(label + ": ")
		if err != nil || pw == "" {
			return "", transport.ErrPasswordCancelled
		}
		p.cache[dest] = pw
		if h.Keychain {
			if err := secrets.Set(acct, pw); err != nil {
				fmt.Fprintln(os.Stderr, "  (not remembered:", err, ")")
			}
		}
		return pw, nil
	}
}

// readSecret reads a line from the terminal without echoing it. It asks only when a person
// is evidently at a terminal (standard input or error is one): a program running hopsesh
// with pipes, such as an agent, gets no prompt that could block on a terminal it shares.
func readSecret(prompt string) (string, error) {
	tty := os.Stdin
	if !term.IsTerminal(int(os.Stdin.Fd())) && !term.IsTerminal(int(os.Stderr.Fd())) {
		return "", transport.ErrPasswordCancelled
	}
	if !term.IsTerminal(int(tty.Fd())) {
		f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return "", transport.ErrPasswordCancelled
		}
		defer f.Close()
		tty = f
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		// Not a terminal after all: fall back to a plain line (still not echoed by us).
		line, rerr := bufio.NewReader(tty).ReadString('\n')
		if rerr != nil {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	return string(b), nil
}

// askPasswordsFirst asks, before the full-screen UI starts, for the passwords of allowed
// machines that need one (and are not in the Keychain).
func (a *app) askPasswordsFirst() {
	for _, h := range a.cfg.Hosts {
		if !h.Allowed || !h.UsesPassword() {
			continue
		}
		f := a.passwordFor(h)
		_, _ = f(context.Background(), h.Destination, false)
	}
	if a.pw != nil {
		a.pw.noPrompt = true
	}
}
