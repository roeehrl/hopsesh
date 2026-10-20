package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
)

// RemoteFS is a remote filesystem over SFTP, carried by the system ssh client.
type RemoteFS struct {
	client    *sftp.Client
	cancel    context.CancelFunc
	cmd       *exec.Cmd
	stdin     io.Closer
	stdout    io.Closer
	closeOnce sync.Once
	closeErr  error
	// Windows servers expose drive paths as /C:/Users/...; ToSFTP converts native paths.
	windows bool
}

// OpenSFTP starts the SFTP subsystem through ssh and returns a filesystem. The session
// lives until Close (not until ctx ends), so it can outlive the scan that opened it.
func (c *Conn) OpenSFTP(ctx context.Context, windows bool) (*RemoteFS, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Only successful negotiation transfers lifetime ownership to RemoteFS.
	// A stalled SSH subsystem must still obey both caller and connection limits.
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	startup, stopStartup := context.WithTimeout(ctx, timeout)
	defer stopStartup()
	lifetime, cancel := context.WithCancel(context.Background())
	cmd := c.sftpCommand(lifetime)
	cmd.WaitDelay = 100 * time.Millisecond
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		return nil, err
	}
	var stderr sshDiagnostic
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	// Close our pipe ends too: killing ssh alone cannot release an inherited
	// stdout held by a descendant. Join the callback before inspecting the result.
	canceled := make(chan struct{})
	stopCancel := context.AfterFunc(startup, func() {
		cancel()
		_ = stdout.Close()
		_ = stdin.Close()
		close(canceled)
	})
	client, err := sftp.NewClientPipe(stdout, stdin, sftp.UseConcurrentReads(true), sftp.MaxConcurrentRequestsPerFile(16))
	if !stopCancel() {
		<-canceled
	}
	if startup.Err() != nil {
		err = startup.Err()
	}
	if err != nil {
		cancel()
		_ = stdout.Close()
		_ = stdin.Close()
		if client != nil {
			_ = client.Close()
		}
		_ = cmd.Wait()
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("SFTP startup: %w", err)
		}
		return nil, classify(err, stderr.String())
	}
	return &RemoteFS{client: client, cancel: cancel, cmd: cmd, stdin: stdin, stdout: stdout, windows: windows}, nil
}

// Client is the SFTP client, for callers that need operations RemoteFS does not wrap.
func (r *RemoteFS) Client() *sftp.Client { return r.client }

// Close ends the SFTP session.
func (r *RemoteFS) Close() error {
	r.closeOnce.Do(func() {
		// Close the transport before waiting for the SFTP reader; a server may
		// ignore stdin EOF. Reap the owned process on every successful session.
		r.cancel()
		_ = r.stdout.Close()
		r.closeErr = r.stdin.Close()
		_ = r.client.Close()
		_ = r.cmd.Wait()
		if errors.Is(r.closeErr, os.ErrClosed) {
			r.closeErr = nil
		}
	})
	return r.closeErr
}

// ToSFTP converts a native remote path (C:\Users\x on Windows) to SFTP form (/C:/Users/x).
func (r *RemoteFS) ToSFTP(p string) string {
	if !r.windows {
		return p
	}
	p = strings.ReplaceAll(p, `\`, "/")
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + p
	}
	return p
}

// WriteFile uploads data atomically (temp name, then rename); used to deploy the helper.
func (r *RemoteFS) WriteFile(name string, data io.Reader, mode os.FileMode) error {
	p := r.ToSFTP(name)
	_ = r.client.MkdirAll(path.Dir(p))
	tmp := p + ".part"
	f, err := r.client.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	_ = r.client.Chmod(tmp, mode)
	_ = r.client.Remove(p)
	return r.client.PosixRename(tmp, p)
}
