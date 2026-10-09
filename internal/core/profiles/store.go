// Package profiles stores local registrations of vendor-owned account roots. It never
// reads, copies, or refreshes credentials. Remote registrations contain public metadata.
package profiles

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

type Store struct{ Dir string }
type disk struct {
	Version  int                    `json:"version"`
	Profiles []agent.RuntimeProfile `json:"profiles"`
}

func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func RootID(endpoint string, a agent.ID, root string) string {
	b, _ := json.Marshal([]string{endpoint, string(a), root})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}
func Tags(values []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.Join(strings.Fields(v), " ")
		if v == "" {
			continue
		}
		if len(v) > 80 || strings.ContainsFunc(v, unicode.IsControl) {
			return nil, fmt.Errorf("tags must be at most 80 bytes without control characters")
		}
		k := strings.ToLower(v)
		if !seen[k] {
			out = append(out, v)
			seen[k] = true
		}
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return out, nil
}
func (s Store) read() (disk, error) {
	d := disk{Version: 1, Profiles: []agent.RuntimeProfile{}}
	b, err := os.ReadFile(filepath.Join(s.Dir, "accounts.json"))
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal(b, &d); err != nil {
		return d, err
	}
	if d.Version != 1 {
		return d, fmt.Errorf("unsupported accounts format %d", d.Version)
	}
	return d, nil
}
func (s Store) List() ([]agent.RuntimeProfile, error) { d, err := s.read(); return d.Profiles, err }

// Update serializes read-modify-write across the GUI, TUI and separate CLI processes.
func (s Store) Update(fn func(*[]agent.RuntimeProfile) error) error {
	if s.Dir == "" {
		return errors.New("account state directory is not configured")
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(s.Dir, "accounts.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	until := time.Now().Add(5 * time.Second)
	for {
		if err = lockFile(lock); err == nil {
			break
		}
		if time.Now().After(until) {
			return fmt.Errorf("accounts are busy: %w", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	d, err := s.read()
	if err != nil {
		return err
	}
	if err = fn(&d.Profiles); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, "accounts-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), filepath.Join(s.Dir, "accounts.json"))
}

var profileID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

func (s Store) Register(p agent.RuntimeProfile) (agent.RuntimeProfile, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 120 || strings.ContainsFunc(p.Name, unicode.IsControl) {
		return p, errors.New("use an account name of 1–120 bytes without control characters")
	}
	if p.Endpoint == "" || p.Root == "" || p.Agent == "" {
		return p, errors.New("an account needs an endpoint, root and supported agent")
	}
	var err error
	p.Tags, err = Tags(p.Tags)
	if err != nil {
		return p, err
	}
	if p.ID == "" {
		p.ID = ID()
	}
	if !profileID.MatchString(p.ID) {
		return p, errors.New("invalid account profile ID")
	}
	p.Generation = 1
	err = s.Update(func(ps *[]agent.RuntimeProfile) error {
		for _, old := range *ps {
			if old.ID == p.ID || old.Endpoint == p.Endpoint && old.Agent == p.Agent && old.Root == p.Root {
				return errors.New("that account root is already registered")
			}
		}
		*ps = append(*ps, p)
		return nil
	})
	return p, err
}
func (s Store) Edit(id, name string, tags []string, generation int) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 || strings.ContainsFunc(name, unicode.IsControl) {
		return errors.New("use a name of 1–120 bytes without control characters")
	}
	normalized, err := Tags(tags)
	if err != nil {
		return err
	}
	return s.Update(func(ps *[]agent.RuntimeProfile) error {
		for i := range *ps {
			p := &(*ps)[i]
			if p.ID != id {
				continue
			}
			if p.Generation != generation {
				return errors.New("account changed; refresh before saving")
			}
			p.Name = name
			p.Tags = normalized
			p.Generation++
			return nil
		}
		return errors.New("account not found")
	})
}
func (s Store) Forget(id string, generation int) error {
	return s.Update(func(ps *[]agent.RuntimeProfile) error {
		for i, p := range *ps {
			if p.ID != id {
				continue
			}
			if p.Generation != generation {
				return errors.New("account changed; refresh before removing")
			}
			*ps = append((*ps)[:i], (*ps)[i+1:]...)
			return nil
		}
		return errors.New("account not found")
	})
}

// Observe records public login metadata and rotates the opaque binding after an
// observable account change. Limited metadata never upgrades to verified identity.
func (s Store) Observe(id string, account *agent.Account, problem string) (agent.RuntimeProfile, error) {
	return s.ObserveFrom(id, account, problem, "")
}

// ObserveFrom retains the origin of public metadata; failures preserve the last known login.
func (s Store) ObserveFrom(id string, account *agent.Account, problem, source string) (agent.RuntimeProfile, error) {
	var out agent.RuntimeProfile
	err := s.Update(func(ps *[]agent.RuntimeProfile) error {
		for i := range *ps {
			p := &(*ps)[i]
			if p.ID != id {
				continue
			}
			if account != nil {
				if p.Account == nil || p.Account.Observation != account.Observation || p.Account.LoggedIn != account.LoggedIn || p.Account.Provider != account.Provider {
					p.Binding = ID()
					p.Generation++
				}
				p.Account = account
			}
			p.Error = problem
			if source != "" {
				p.IdentitySource = source
			}
			p.CheckedAt = time.Now().UTC()
			out = *p
			return nil
		}
		return errors.New("account not found")
	})
	return out, err
}

// DecodeRegistrations reads a peer's known non-secret registry. Tags and account
// keys stay private; vendor-reported email and plan are public identity metadata.
func DecodeRegistrations(b []byte) ([]agent.RuntimeProfile, error) {
	var d disk
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	if d.Version != 1 {
		return nil, fmt.Errorf("unsupported accounts format %d", d.Version)
	}
	for i := range d.Profiles {
		p := &d.Profiles[i]
		p.Tags = nil
		if p.Account != nil {
			a := *p.Account
			a.Key = ""
			p.Account = &a
		}
		if p.Error != "" {
			p.Error = "Account check failed on the owner machine; last known sign-in shown"
		}
		p.Generation = 1
		p.Managed = false
		p.IdentitySource = "owner"
	}
	return d.Profiles, nil
}

// ImportObservation accepts newer metadata from the endpoint that owns the root.
// Local labels and tags stay local, and a failed or stale remote read cannot reset them.
func (s Store) ImportObservation(id string, remote agent.RuntimeProfile) error {
	return s.Update(func(ps *[]agent.RuntimeProfile) error {
		for i := range *ps {
			p := &(*ps)[i]
			if p.ID != id {
				continue
			}
			if p.Endpoint != remote.Endpoint || p.Agent != remote.Agent || p.Root != remote.Root {
				return errors.New("remote profile scope changed")
			}
			if remote.CheckedAt.After(p.CheckedAt) && remote.Account != nil {
				if p.Binding != remote.Binding {
					p.Generation++
				}
				p.Binding = remote.Binding
				p.Account = remote.Account
				p.IdentitySource = "owner"
				p.CheckedAt = remote.CheckedAt
				p.Error = remote.Error
			}
			return nil
		}
		return errors.New("profile not found")
	})
}

// SetDefault tracks the vendor's currently configured root, without changing profile
// identity. Switching that root invalidates plans which could launch the desktop app.
func (s Store) SetDefault(endpoint string, id agent.ID, selected string) error {
	return s.Update(func(ps *[]agent.RuntimeProfile) error {
		for i := range *ps {
			p := &(*ps)[i]
			if p.Endpoint != endpoint || p.Agent != id {
				continue
			}
			want := p.ID == selected
			if p.Default != want {
				p.Default = want
				p.Generation++
			}
		}
		return nil
	})
}
