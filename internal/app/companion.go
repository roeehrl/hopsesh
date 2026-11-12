package app

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// MyCNC is the phone companion hopsesh recommends: an iPhone and iPad app that controls
// the agents on a Mac running MyCNC Host. hopsesh only reads whether MyCNC Host is here
// (its presence file) and opens its links; it never installs or starts it.
const (
	CompanionName = "MyCNC"
	CompanionPage = "https://codonic.dev/apps/mycnc/"
)

// Companion is MyCNC Host on this machine, as far as hopsesh can tell.
type Companion struct {
	Name string `json:"name"`
	Page string `json:"page"`
	// Supported: MyCNC Host runs on this kind of machine (macOS).
	Supported bool `json:"supported"`
	// Installed: its presence file is here (MyCNC Host writes it at every launch and
	// removes it only when uninstalled); Running: its socket answers a ping.
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
}

// companionPresence is MyCNC Host's presence file: {version, socket, pid}.
func companionPresence() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "MyCNCHost", "presence.json")
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "MyCNC", "presence.json")
		}
		return ""
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "mycnc", "presence.json")
	}
	return filepath.Join(home, ".local", "state", "mycnc", "presence.json")
}

// DetectCompanion reads MyCNC Host's presence file and pings its socket.
func DetectCompanion() Companion {
	return detectCompanion(companionPresence(), runtime.GOOS == "darwin", pingCompanion)
}

// pingCompanion asks MyCNC Host's socket whether it runs: {"ping":1} → {"pong":1,…}.
func pingCompanion(socket string) bool {
	if socket == "" {
		return false
	}
	c, err := net.DialTimeout("unix", socket, 300*time.Millisecond)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(500 * time.Millisecond))
	if _, err := c.Write([]byte(`{"ping":1}` + "\n")); err != nil {
		return false
	}
	line, err := bufio.NewReader(io.LimitReader(c, 4<<10)).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return false
	}
	var pong struct {
		Pong int `json:"pong"`
	}
	return json.Unmarshal(line, &pong) == nil && pong.Pong == 1
}

func detectCompanion(path string, supported bool, ping func(socket string) bool) Companion {
	c := Companion{Name: CompanionName, Page: CompanionPage, Supported: supported}
	if path == "" {
		return c
	}
	f, err := os.Open(path)
	if err != nil {
		return c
	}
	defer f.Close()
	var p struct {
		Version string `json:"version"`
		Socket  string `json:"socket"`
	}
	b, err := io.ReadAll(io.LimitReader(f, 16<<10))
	if err != nil || json.Unmarshal(b, &p) != nil {
		return c
	}
	c.Installed, c.Version = true, p.Version
	c.Running = ping(p.Socket)
	return c
}

// CompanionLink is the link that opens a session in MyCNC (it only ever opens a view);
// machine is hopsesh's name for the session's machine, which MyCNC shows when it isn't
// connected to it.
func CompanionLink(endpoint, machine string, k agent.SessionKey) string {
	q := url.Values{}
	q.Set("endpoint", endpoint)
	if machine != "" {
		q.Set("machine", machine)
	}
	q.Set("agent", string(k.Agent))
	if k.Profile != "" {
		q.Set("profile", k.Profile)
	}
	q.Set("session", string(k.Session))
	return "mycnc://session?" + q.Encode()
}

// SessionLink is hopsesh's own link to a session: hopsesh://session/<endpoint>/<agent>/<session>,
// with ?profile= for an account folder other than the default.
func SessionLink(endpoint string, k agent.SessionKey) string {
	u := url.URL{Scheme: "hopsesh", Host: "session", Path: "/" + endpoint + "/" + string(k.Agent) + "/" + string(k.Session)}
	if k.Profile != "" {
		u.RawQuery = url.Values{"profile": {k.Profile}}.Encode()
	}
	return u.String()
}

// ParseSessionLink reads a hopsesh://session link.
func ParseSessionLink(raw string) (endpoint string, k agent.SessionKey, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "hopsesh" || u.Host != "session" {
		return "", agent.SessionKey{}, false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", agent.SessionKey{}, false
	}
	k = agent.SessionKey{Agent: agent.ID(parts[1]), Profile: u.Query().Get("profile"), Session: agent.SessionID(parts[2])}
	return parts[0], k, true
}
