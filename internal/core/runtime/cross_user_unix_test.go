//go:build linux || darwin

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

type crossUserProbe struct {
	Namespace Namespace `json:"namespace"`
	Secret    string    `json:"secret"`
}

type crossUserResult struct {
	UID           string `json:"uid"`
	Namespace     string `json:"namespace"`
	StatusDenied  bool   `json:"statusDenied"`
	SecretDenied  bool   `json:"secretDenied"`
	PeerRejected  bool   `json:"peerRejected"`
	ReceivedBytes int    `json:"receivedBytes"`
}

// Enabled explicitly on native CI. Uses an existing unprivileged OS account,
// creates no user/service, and never points either process at real agent data.
func TestRuntimeDifferentOSUserIsolation(t *testing.T) {
	if os.Getenv("HOPSESH_RUNTIME_CROSS_USER") != "1" {
		t.Skip("requires noninteractive sudo for disposable cross-user qualification")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("/tmp", "hopsesh-cross-user-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	// The helper must be executable by nobody; t.TempDir's ancestors are
	// deliberately private and cannot serve as this fixture's public launcher.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	binary := filepath.Join(root, "probe")
	copy, err := os.OpenFile(binary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(copy, source)
	closeErr := copy.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatal(copyErr, closeErr)
	}
	n, err := NewNamespace(filepath.Join(root, "config"), filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	n.Directory = filepath.Join(root, "private")
	socketDir := filepath.Join(root, "socket")
	n.Address = filepath.Join(socketDir, "runtime.sock")
	testHost(t, n, func(context.Context) (json.RawMessage, error) {
		return json.RawMessage(`{"sessions":["PRIVATE-CROSS-USER-FIXTURE"]}`), nil
	}, nil)
	secret := filepath.Join(n.Directory, "private-fixture")
	if err := os.WriteFile(secret, []byte("PRIVATE-CROSS-USER-FIXTURE"), 0600); err != nil {
		t.Fatal(err)
	}
	probe, _ := json.Marshal(crossUserProbe{Namespace: n, Secret: secret})
	run := func(foreign bool) crossUserResult {
		t.Helper()
		args := []string{binary, "-test.run=^TestRuntimeCrossUserHelper$", "--", "runtime-cross-user-helper"}
		if foreign {
			args = append([]string{"/usr/bin/sudo", "-n", "-u", "nobody", "--"}, args...)
		}
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=/tmp"}
		cmd.Stdin = bytes.NewReader(probe)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("cross-user helper foreign=%t: %v %s %s", foreign, err, out, stderr.Bytes())
		}
		var result crossUserResult
		if err := json.NewDecoder(bytes.NewReader(out)).Decode(&result); err != nil {
			t.Fatal("invalid cross-user helper result", err)
		}
		return result
	}
	self := run(false)
	uid := strconv.Itoa(os.Geteuid())
	if self.UID != uid || self.Namespace != n.ID || self.StatusDenied || self.SecretDenied || self.PeerRejected || self.ReceivedBytes == 0 {
		t.Fatal("same-user positive control failed", self)
	}
	// Fault injection is confined to this disposable socket. Even if its file
	// protection is accidentally widened, the server must authenticate before
	// sending its hello or any snapshot, and the client must distrust its peer.
	if err := os.Chmod(socketDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(n.Address, 0666); err != nil {
		t.Fatal(err)
	}
	foreign := run(true)
	if foreign.UID == uid || foreign.Namespace == n.ID || !foreign.StatusDenied || !foreign.SecretDenied || !foreign.PeerRejected || foreign.ReceivedBytes != 0 {
		t.Fatal("another OS user accessed or impersonated the runtime", foreign)
	}
	if err := os.Chmod(n.Address, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socketDir, 0700); err != nil {
		t.Fatal(err)
	}
	var status Status
	if err := (Client{n}).Call(ctx, "status", nil, &status); err != nil {
		t.Fatal("foreign probe damaged the owner's runtime", err)
	}
	t.Logf("verified owner uid=%s and foreign uid=%s have distinct namespaces; private files, IPC and both peer checks enforced", uid, foreign.UID)
}

func TestRuntimeCrossUserHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != "runtime-cross-user-helper" {
		t.Skip("cross-user subprocess helper")
	}
	var probe crossUserProbe
	if err := json.NewDecoder(os.Stdin).Decode(&probe); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	n, err := NewNamespace(probe.Namespace.Config, probe.Namespace.State)
	if err != nil {
		t.Fatal(err)
	}
	result := crossUserResult{UID: strconv.Itoa(os.Geteuid()), Namespace: n.ID}
	var status Status
	result.StatusDenied = (Client{probe.Namespace}).Call(ctx, "status", nil, &status) != nil
	_, err = os.ReadFile(probe.Secret)
	result.SecretDenied = err != nil
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", probe.Namespace.Address)
	if err != nil {
		t.Fatal("public test socket was unreachable; peer check was not exercised", err)
	}
	defer conn.Close()
	result.PeerRejected = sameUser(conn) != nil
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var body [32]byte
	result.ReceivedBytes, err = conn.Read(body[:])
	if result.PeerRejected && err != io.EOF {
		t.Fatal("server must close foreign peer before sending hello", err)
	}
	encoded, _ := json.Marshal(result)
	fmt.Println(string(encoded))
}
