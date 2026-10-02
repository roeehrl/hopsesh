package transport

import (
	"context"
	"io"
	"os"
	"path"
	"strings"

	"github.com/pkg/sftp"

	"github.com/roeehrl/hopsesh/internal/core/fsys"
)

// RemoteFS is a remote filesystem over SFTP, carried by the system ssh client.
type RemoteFS struct {
	fsys.Slash
	client *sftp.Client
	cancel context.CancelFunc
	// Windows servers expose drive paths as /C:/Users/...; ToSFTP converts native paths.
	windows bool
}

// OpenSFTP starts the SFTP subsystem through ssh and returns a filesystem. The session
// lives until Close (not until ctx ends), so it can outlive the scan that opened it.
func (c *Conn) OpenSFTP(ctx context.Context, windows bool) (*RemoteFS, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := c.sftpCommand(ctx)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	client, err := sftp.NewClientPipe(stdout, stdin, sftp.UseConcurrentReads(true), sftp.MaxConcurrentRequestsPerFile(16))
	if err != nil {
		cancel()
		_ = cmd.Wait()
		return nil, classify(err, stderr.String())
	}
	return &RemoteFS{client: client, cancel: cancel, windows: windows}, nil
}

// Close ends the SFTP session.
func (r *RemoteFS) Close() error {
	err := r.client.Close()
	r.cancel()
	return err
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

func (r *RemoteFS) ReadDir(dir string) ([]os.FileInfo, error) { return r.client.ReadDir(r.ToSFTP(dir)) }
func (r *RemoteFS) Stat(name string) (os.FileInfo, error)     { return r.client.Stat(r.ToSFTP(name)) }
func (r *RemoteFS) Open(name string) (fsys.File, error)       { return r.client.Open(r.ToSFTP(name)) }
func (r *RemoteFS) Join(elem ...string) string                { return path.Join(elem...) }

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
