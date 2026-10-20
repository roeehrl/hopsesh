package relay

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PairInput keeps sending, receiving and conversation sharing independent.
type PairInput struct {
	Identity       string   `json:"identity"`
	Fingerprint    string   `json:"fingerprint"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Roots          []string `json:"roots"`
	Receive        bool     `json:"receive"`
	Send           bool     `json:"send"`
	Bring          bool     `json:"bring"`
	Export         bool     `json:"export"`
	ExpiresSeconds int      `json:"expiresSeconds"`
}

func ValidatePair(in PairInput) (Grant, error) {
	var g Grant
	if len(in.Identity) > 4096 {
		return g, errors.New("public identity exceeds bound")
	}
	if err := json.Unmarshal([]byte(in.Identity), &g.Peer); err != nil {
		return g, errors.New("invalid public identity JSON")
	}
	if err := g.Peer.Check(); err != nil {
		return g, err
	}
	if in.Fingerprint == "" || in.Fingerprint != g.Peer.Fingerprint() {
		return g, errors.New("compare the fingerprint on the other endpoint through a trusted channel before approval")
	}
	g.Kind, g.Endpoint = in.Kind, g.Peer.Endpoint
	if g.Kind != "device" && g.Kind != "cloud-session" {
		return g, errors.New("unknown approval kind")
	}
	if g.Kind == "cloud-session" {
		if in.Receive || in.Send || in.Bring || in.Name != "" || len(in.Roots) > 0 {
			return g, errors.New("cloud sessions cannot receive device access or be registered as machines")
		}
		if in.ExpiresSeconds < 60 || in.ExpiresSeconds > 86400 {
			return g, errors.New("cloud approval must expire between one minute and 24 hours")
		}
		g.Methods = []string{"observe"}
		g.SendMethods = []string{"observe"}
		if in.Export {
			g.SendMethods = append(g.SendMethods, "export")
		}
		g.Expires = time.Now().Add(time.Duration(in.ExpiresSeconds) * time.Second).Unix()
		return g, nil
	}
	if g.Endpoint == "" {
		return g, errors.New("a device identity must be bound to its native endpoint")
	}
	if in.Name == "" || len(in.Name) > 100 || strings.ContainsAny(in.Name, "\x00\r\n") {
		return g, errors.New("enter a machine name without control characters")
	}
	g.Methods = []string{"hello", "observe"}
	g.SendMethods = []string{"hello", "observe"}
	if in.Receive || in.Export {
		if len(in.Roots) == 0 {
			return g, errors.New("choose approved local repository roots before enabling transfers")
		}
		for _, root := range in.Roots {
			if !filepath.IsAbs(root) {
				return g, errors.New("repository roots must be absolute")
			}
			real, err := filepath.EvalSymlinks(root)
			if err != nil {
				return g, err
			}
			st, err := os.Stat(real)
			if err != nil || !st.IsDir() {
				return g, errors.New("repository root must be an existing directory")
			}
			g.Roots = append(g.Roots, real)
		}
		if in.Receive {
			g.Methods = append(g.Methods, "plan", "apply")
		}
		g.Methods = append(g.Methods, "undo")
		if in.Export {
			g.Methods = append(g.Methods, "export", "ack", "preview")
		}
	}
	if in.Send {
		g.SendMethods = append(g.SendMethods, "plan", "apply", "undo")
	}
	if in.Bring {
		g.SendMethods = append(g.SendMethods, "export", "ack", "preview")
	}
	return g, nil
}
