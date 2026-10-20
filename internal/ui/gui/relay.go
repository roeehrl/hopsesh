package gui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
)

type RelayPeerDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Fingerprint string   `json:"fingerprint"`
	Kind        string   `json:"kind"`
	Roots       []string `json:"roots"`
	Methods     []string `json:"methods"`
	SendMethods []string `json:"sendMethods"`
	Expires     int64    `json:"expires"`
	Revoked     bool     `json:"revoked"`
}
type RelaySettingsDTO struct {
	ReceiveEnabled bool                       `json:"receiveEnabled"`
	Enabled        bool                       `json:"enabled"`
	Initialized    bool                       `json:"initialized"`
	Identity       *relay.PublicIdentity      `json:"identity,omitempty"`
	Enrolled       bool                       `json:"enrolled"`
	URL            string                     `json:"url,omitempty"`
	Expires        int64                      `json:"expires,omitempty"`
	Health         relay.Health               `json:"health"`
	Peers          []RelayPeerDTO             `json:"peers"`
	Admissions     []relay.AdmissionRecord    `json:"admissions"`
	Tasks          []relay.CloudTaskRecord    `json:"tasks"`
	TaskLinks      []app.CloudHandoffLinkInfo `json:"taskLinks"`
	Error          string                     `json:"error,omitempty"`
}

func (a *App) RelayCloudHandoffOptions() ([]app.CloudHandoffOption, error) {
	return a.snapshot().CloudHandoffOptions()
}

func (a *App) RelayPlanCloudHandoffLink(task, handoff string, fork bool) (app.CloudHandoffLinkPlan, error) {
	return a.snapshot().PlanCloudHandoffLink(task, handoff, fork)
}

func (a *App) RelayLinkCloudHandoff(task, handoff string, fork bool, review string) (app.CloudHandoffLinkPlan, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.snapshot().LinkCloudHandoff(ctx, task, handoff, fork, review)
}

func (a *App) RelaySettings() RelaySettingsDTO {
	core := a.snapshot()
	store := relay.Store{Directory: filepath.Join(core.StateDir, "relay")}
	out := RelaySettingsDTO{Enabled: core.Cfg.Relay.Enabled, ReceiveEnabled: core.Cfg.Peer.Receive, Peers: []RelayPeerDTO{}}
	if id, err := store.Public(); err == nil {
		out.Initialized = true
		out.Identity = &id
	} else if !os.IsNotExist(err) {
		out.Error = "Relay identity could not be read safely"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if out.Initialized {
		if c, err := store.PublicConnection(); err == nil && c.Device == out.Identity.ID {
			out.Enrolled = true
			out.URL = c.URL
			out.Expires = c.Expires
		}
		if n, err := localruntime.NewNamespace(config.Dir(), core.StateDir); err == nil {
			_ = (localruntime.Client{Namespace: n}).Call(ctx, "relay.status", nil, &out.Health)
		}
	}
	grants, err := store.Grants()
	if err != nil {
		out.Error = "Relay approvals could not be read safely"
		return out
	}
	for _, g := range grants {
		name := g.Peer.ID[:12]
		for _, h := range core.Cfg.Hosts {
			if h.RelayID == g.Peer.ID {
				name = h.Name
				break
			}
		}
		out.Peers = append(out.Peers, RelayPeerDTO{ID: g.Peer.ID, Name: name, Fingerprint: g.Peer.Fingerprint(), Kind: g.Kind, Roots: g.Roots, Methods: g.Methods, SendMethods: g.SendMethods, Expires: g.Expires, Revoked: g.Revoked})
	}
	if rows, err := (relay.AdmissionStore{Directory: filepath.Join(core.StateDir, "cloud-admissions")}).List(); err == nil {
		out.Admissions = rows
	} else {
		out.Error = "Cloud invitations could not be read safely"
	}
	if rows, err := (relay.AdmissionStore{Directory: filepath.Join(core.StateDir, "cloud-admissions")}).Tasks(); err == nil {
		out.Tasks = rows
	} else {
		out.Error = "Cloud task history could not be read safely"
	}
	if links, err := core.CloudHandoffLinks(); err == nil {
		out.TaskLinks = links
	} else {
		out.Error = "Saved cloud handoff associations could not be read safely"
	}
	return out
}

// RelayIssueCloudAdmission returns private file metadata only. A one-use
// secret never crosses the browser bridge or clipboard.
func (a *App) RelayIssueCloudAdmission(provider, session string, leaseSeconds int, resume string) (relay.AdmissionRecord, error) {
	core := a.snapshot()
	store := relay.Store{Directory: filepath.Join(core.StateDir, "relay")}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	public, err := store.Public()
	if err != nil {
		return relay.AdmissionRecord{}, errors.New("create and enroll this endpoint before inviting a cloud session")
	}
	owner, err := store.Identity(ctx, public.Endpoint)
	if err != nil {
		return relay.AdmissionRecord{}, err
	}
	c, err := store.Connection(ctx)
	if err != nil {
		return relay.AdmissionRecord{}, err
	}
	if leaseSeconds < 60 || leaseSeconds > 86400 {
		return relay.AdmissionRecord{}, errors.New("cloud routing lease must be between one minute and 24 hours")
	}
	return (relay.AdmissionStore{Directory: filepath.Join(core.StateDir, "cloud-admissions")}).IssueTask(ctx, owner, c, provider, session, time.Duration(leaseSeconds)*time.Second, resume)
}

func (a *App) RelayRevokeCloudAdmission(id string) error {
	core := a.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := (relay.Store{Directory: filepath.Join(core.StateDir, "relay")}).Connection(ctx)
	if err != nil {
		return err
	}
	return (relay.AdmissionStore{Directory: filepath.Join(core.StateDir, "cloud-admissions")}).Revoke(ctx, c, id)
}

func (a *App) RelayCheckCloudAdmission(id string) (relay.AdmissionStatus, error) {
	core := a.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := (relay.Store{Directory: filepath.Join(core.StateDir, "relay")}).Connection(ctx)
	if err != nil {
		return relay.AdmissionStatus{}, err
	}
	return (relay.AdmissionStore{Directory: filepath.Join(core.StateDir, "cloud-admissions")}).Check(ctx, c, id)
}
func (a *App) RelayInitialize() (relay.PublicIdentity, error) {
	core := a.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := &host.Machine{Local: true, Name: app.LocalName(), Facts: host.ObserveLocal(ctx, nil)}
	defer m.Close()
	if err := m.CommitIdentity(ctx); err != nil {
		return relay.PublicIdentity{}, err
	}
	id, err := (relay.Store{Directory: filepath.Join(core.StateDir, "relay")}).Identity(ctx, m.Facts.Endpoint)
	if err != nil {
		return relay.PublicIdentity{}, err
	}
	if id.Public.Endpoint != m.Facts.Endpoint {
		return relay.PublicIdentity{}, errors.New("relay identity belongs to another native endpoint")
	}
	return id.Public, nil
}

type RelayPairInput = relay.PairInput

func (a *App) RelayPair(in RelayPairInput) error {
	g, err := relay.ValidatePair(in)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if in.Receive && !cfg.Peer.Receive {
		return errors.New("enable receiving on this computer in Machines before granting a peer access to selected repositories")
	}
	if old := cfg.FindHost(in.Name); in.Name != "" && old != nil && old.RelayID != g.Peer.ID {
		return errors.New("machine name already belongs to another endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store := relay.Store{Directory: filepath.Join(a.core.StateDir, "relay")}
	self, err := store.Public()
	if err != nil {
		return errors.New("create this endpoint's identity before pairing")
	}
	if self.ID == g.Peer.ID {
		return errors.New("this is your own endpoint identity; use the other endpoint's public identity")
	}
	if err = store.Approve(ctx, g); err != nil {
		return err
	}
	if in.Name != "" {
		cfg.UpsertHost(config.Host{Name: in.Name, RelayID: g.Peer.ID, Via: "relay", Allowed: true})
		if err = config.Save(&cfg); err != nil {
			return errors.New("peer approved, but machine registration was not saved; reload settings and approve again")
		}
		a.core.Cfg = cfg
	}
	return nil
}
func (a *App) RelayConnect(origin, credential string) error {
	if len(credential) > 8192 {
		return errors.New("enrollment credential exceeds bound")
	}
	var c relay.Connection
	if err := json.Unmarshal([]byte(credential), &c); err != nil {
		return errors.New("invalid scoped enrollment credential JSON")
	}
	c.URL = origin
	core := a.snapshot()
	store := relay.Store{Directory: filepath.Join(core.StateDir, "relay")}
	id, err := store.Public()
	if err != nil {
		return err
	}
	if c.Device != id.ID {
		return errors.New("credential belongs to another endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = store.SetConnection(ctx, c); err != nil {
		return err
	}
	return a.RelayEnable(true)
}
func (a *App) RelayEnable(enabled bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Relay.Enabled = enabled
	if err = config.Save(&cfg); err != nil {
		return err
	}
	a.core.Cfg = cfg
	return nil
}
func (a *App) RelayRevoke(peer string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := (relay.Store{Directory: filepath.Join(a.core.StateDir, "relay")}).Revoke(ctx, peer); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return errors.New("approval revoked; machine settings could not be refreshed")
	}
	for i := range cfg.Hosts {
		if cfg.Hosts[i].RelayID == peer {
			cfg.Hosts[i].Allowed = false
		}
	}
	if err = config.Save(&cfg); err != nil {
		return errors.New("approval revoked; reload settings to refresh the machine list")
	}
	a.core.Cfg = cfg
	return nil
}
