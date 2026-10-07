package relay

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

// Public reads an existing identity without creating keys or taking ownership.
func (s Store) Public() (PublicIdentity, error) {
	b, err := localstate.ReadPrivateFile(filepath.Join(s.Directory, "identity.json"), 8192)
	if err != nil {
		return PublicIdentity{}, err
	}
	var id Identity
	if err = json.Unmarshal(b, &id); err != nil {
		return PublicIdentity{}, err
	}
	if err = id.Check(); err != nil {
		return PublicIdentity{}, err
	}
	return id.Public, nil
}

// Grants lists local approvals, never routing credentials or identity secrets.
// Reading settings must not silently initialize relay state.
func (s Store) Grants() ([]Grant, error) {
	out := []Grant{}
	dir, err := os.Open(s.Directory)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	if err = localstate.PrivateDirectory(s.Directory); err != nil {
		return nil, err
	}
	for {
		names, e := dir.Readdirnames(128)
		for _, name := range names {
			if !strings.HasPrefix(name, "peer-") || !strings.HasSuffix(name, ".json") {
				continue
			}
			if len(out) >= 4096 {
				return nil, errors.New("relay approval listing exceeds bound")
			}
			b, e := localstate.ReadPrivateFile(filepath.Join(s.Directory, name), 8192)
			if e != nil {
				return nil, e
			}
			var g Grant
			if e = json.Unmarshal(b, &g); e != nil {
				return nil, e
			}
			if name != "peer-"+g.Peer.ID+".json" {
				return nil, errors.New("relay approval binding changed")
			}
			if e = g.Peer.Check(); e != nil {
				return nil, e
			}
			out = append(out, g)
		}
		if len(names) == 0 {
			if e != nil && !errors.Is(e, io.EOF) {
				return nil, e
			}
			break
		}
		if e != nil && !errors.Is(e, io.EOF) {
			return nil, e
		}
	}
	slices.SortFunc(out, func(a, b Grant) int { return strings.Compare(a.Peer.ID, b.Peer.ID) })
	return out, nil
}

// PublicConnection excludes all enrollment credentials and local trust paths.
type PublicConnection struct {
	URL     string `json:"url"`
	Device  string `json:"device"`
	Expires int64  `json:"expires"`
}

func (s Store) PublicConnection() (PublicConnection, error) {
	b, err := localstate.ReadPrivateFile(filepath.Join(s.Directory, "connection.json"), 8192)
	if err != nil {
		return PublicConnection{}, err
	}
	var c Connection
	if err = json.Unmarshal(b, &c); err != nil {
		return PublicConnection{}, err
	}
	if _, err = (Transport{Base: c.URL, Space: c.Space, Token: c.Token}).endpoint("/v1/messages"); err != nil {
		return PublicConnection{}, err
	}
	return PublicConnection{URL: c.URL, Device: c.Device, Expires: c.Expires}, nil
}
