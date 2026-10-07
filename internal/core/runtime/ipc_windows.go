//go:build windows

package runtime

import (
	"context"
	"errors"
	"github.com/Microsoft/go-winio"
	"github.com/roeehrl/hopsesh/internal/localstate"
	"golang.org/x/sys/windows"
	"net"
)

func address(dir, id string) string { return `\\.\pipe\hopsesh-runtime-` + id }
func currentSID() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid, nil
}
func privateDirectory(dir string) error { return localstate.PrivateDirectory(dir) }
func listen(n Namespace) (net.Listener, error) {
	sid, err := currentSID()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(n.Address, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid.String() + ")", InputBufferSize: 65536, OutputBufferSize: 65536})
}
func sameUser(c net.Conn) error { return nil } // Listener DACL and FILE_PIPE_REJECT_REMOTE_CLIENTS enforce current-user local access.
func dial(ctx context.Context, n Namespace) (net.Conn, error) {
	c, err := winio.DialPipeContext(ctx, n.Address)
	if err != nil {
		return nil, err
	}
	// Verify the server's OS token before sending any request or reading a snapshot.
	f, ok := c.(interface{ Fd() uintptr })
	if !ok {
		c.Close()
		return nil, errors.New("pipe handle unavailable")
	}
	var pid uint32
	err = windows.GetNamedPipeServerProcessId(windows.Handle(f.Fd()), &pid)
	if err == nil {
		var h windows.Handle
		h, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if err == nil {
			var token windows.Token
			err = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token)
			windows.CloseHandle(h)
			if err == nil {
				u, e := token.GetTokenUser()
				token.Close()
				sid, se := currentSID()
				err = errors.Join(e, se)
				if err == nil && !windows.EqualSid(u.User.Sid, sid) {
					err = errors.New("runtime server belongs to another user")
				}
			}
		}
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}
