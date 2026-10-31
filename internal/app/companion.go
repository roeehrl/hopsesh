package app

import (
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// MyCNC is the phone companion hopsesh recommends: an iPhone and iPad app that controls
// the agents on a Mac running MyCNC Host. hopsesh only reads whether MyCNC Host is here
// (its presence file) and opens its links; it never installs or starts it.
const (
	CompanionName = "MyCNC"
	CompanionPage = "https://codonic.dev/apps/mycnc"
)

// Companion is MyCNC Host on this machine, as far as hopsesh can tell.
type Companion struct {
	Name string `json:"name"`
	Page string `json:"page"`
	// Supported: MyCNC Host runs on this kind of machine (macOS).
	Supported bool `json:"supported"`
	// Installed: its presence file is here; Running: the process it names runs.
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
}

// companionPresence is MyCNC Host's presence file: {version, socket, pid}.
func companionPresence() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "MyCNC Host", "presence.json")
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

// DetectCompanion reads MyCNC Host's presence file.
func DetectCompanion() Companion {
	return detectCompanion(companionPresence(), runtime.GOOS == "darwin", host.ProcessExists)
}

func detectCompanion(path string, supported bool, alive func(int) bool) Companion {
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
		PID     int    `json:"pid"`
	}
	b, err := io.ReadAll(io.LimitReader(f, 16<<10))
	if err != nil || json.Unmarshal(b, &p) != nil {
		return c
	}
	c.Installed, c.Version = true, p.Version
	c.Running = p.PID > 0 && alive(p.PID)
	return c
}

// CompanionLink is the link that opens a session in MyCNC.
func CompanionLink(endpoint string, k agent.SessionKey) string {
	q := url.Values{}
	q.Set("endpoint", endpoint)
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
