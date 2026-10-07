package cloudintegration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

// SessionStart is the documented Claude command-hook input. It is descriptive
// input, never permission to enroll a device or read another session.
type SessionStart struct {
	Session    string `json:"session_id"`
	Workspace  string `json:"cwd"`
	Transcript string `json:"transcript_path"`
	Event      string `json:"hook_event_name"`
	Source     string `json:"source"`
}

func ReadSessionStart(r io.Reader, remote string) (SessionStart, error) {
	var in SessionStart
	if remote != "true" {
		return in, errors.New("claude cloud hook is disabled outside a cloud session")
	}
	b, err := io.ReadAll(io.LimitReader(r, 65537))
	if err != nil {
		return in, err
	}
	if len(b) > 65536 {
		return in, errors.New("SessionStart input exceeds limit")
	}
	if err = json.Unmarshal(b, &in); err != nil {
		return in, errors.New("invalid SessionStart input")
	}
	if in.Event != "SessionStart" {
		return in, errors.New("expected SessionStart hook input")
	}
	switch in.Source {
	case "startup", "resume", "clear", "compact", "fork":
	default:
		return in, errors.New("unsupported session startup source")
	}
	return in, nil
}

type Scope struct {
	Provider         string `json:"provider"`
	Session          string `json:"session"`
	Workspace        string `json:"workspace"`
	NativeRoot       string `json:"nativeRoot,omitempty"`
	Transcript       string `json:"transcript,omitempty"`
	ExportTranscript bool   `json:"exportTranscript"`
}

// Incarnation is produced only at actual task startup, never during setup. A
// restored filesystem does not authorize old keys for a new invocation.
type Incarnation struct {
	Scope
	ID        string               `json:"incarnation"`
	Directory string               `json:"directory"`
	Public    relay.PublicIdentity `json:"public"`
	Created   time.Time            `json:"created"`
	Expires   time.Time            `json:"expires"`
}

var sessionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (s Scope) check() error {
	switch s.Provider {
	case "claude-hosted", "codex-current", "codex-legacy", "work-cloud":
	default:
		return errors.New("unknown cloud execution surface")
	}
	if !sessionName.MatchString(s.Session) {
		return errors.New("a bound native session ID is required")
	}
	if err := canonicalDirectory(s.Workspace); err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	if s.Transcript == "" {
		if s.ExportTranscript {
			return errors.New("native transcript export is unavailable without a verified transcript path")
		}
		return nil
	}
	if s.Provider != "claude-hosted" && s.Provider != "codex-legacy" {
		return errors.New("native transcript visibility is not qualified on this cloud execution surface")
	}
	if err := canonicalDirectory(s.NativeRoot); err != nil {
		return fmt.Errorf("native transcript root: %w", err)
	}
	if !filepath.IsAbs(s.Transcript) || filepath.Clean(s.Transcript) != s.Transcript {
		return errors.New("transcript path must be canonical and absolute")
	}
	rel, err := filepath.Rel(s.NativeRoot, s.Transcript)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("transcript is outside the approved native root")
	}
	name := filepath.Base(s.Transcript)
	if s.Provider == "claude-hosted" && name != s.Session+".jsonl" || s.Provider == "codex-legacy" && !strings.HasSuffix(name, "-"+s.Session+".jsonl") {
		return errors.New("native transcript filename does not match this session")
	}
	parent := filepath.Dir(s.Transcript)
	if err := canonicalDirectory(parent); err != nil {
		return errors.New("transcript parent is not an existing canonical directory")
	}
	st, err := os.Lstat(s.Transcript)
	if os.IsNotExist(err) {
		return nil
	} // startup may precede the first native write
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("native transcript must be a regular file, never a symlink")
	}
	return nil
}

func canonicalDirectory(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return errors.New("directory must be canonical and absolute")
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return err
	}
	if real != p {
		return errors.New("directory must not resolve through a symlink")
	}
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return errors.New("expected an existing directory")
	}
	return nil
}

func Begin(ctx context.Context, parent string, scope Scope, ttl time.Duration) (Incarnation, error) {
	var s Incarnation
	if err := scope.check(); err != nil {
		return s, err
	}
	if ttl < time.Minute || ttl > relay.MaxLifetime {
		return s, errors.New("cloud connector lease must be between one minute and 24 hours")
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		return s, err
	}
	if err := localstate.PrivateDirectory(parent); err != nil {
		return s, err
	}
	if err := canonicalDirectory(parent); err != nil {
		return s, err
	}
	lock, err := localstate.Lock(ctx, filepath.Join(parent, "incarnations.lock"))
	if err != nil {
		return s, err
	}
	defer lock.Close()
	id, err := relay.NewOperationID()
	if err != nil {
		return s, err
	}
	dir := filepath.Join(parent, id)
	if err = os.Mkdir(dir, 0700); err != nil {
		return s, err
	}
	good := false
	defer func() {
		if !good {
			_ = os.RemoveAll(dir)
		}
	}()
	identity, err := (relay.Store{Directory: dir}).Identity(ctx, "cloud/"+scope.Provider+"/"+id)
	if err != nil {
		return s, err
	}
	now := time.Now().UTC()
	s = Incarnation{Scope: scope, ID: id, Directory: dir, Public: identity.Public, Created: now, Expires: now.Add(ttl)}
	body, err := json.Marshal(s)
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "session.json"), body, 0600)
	}
	if err != nil {
		return Incarnation{}, err
	}
	// A restart supersedes this session's previous lease locally. Forks use a
	// different slot, so starting a fork never invalidates its original.
	f, err := os.CreateTemp(parent, ".active-*")
	if err != nil {
		return Incarnation{}, err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.WriteString(s.ID)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return Incarnation{}, err
	}
	if closeErr != nil {
		return Incarnation{}, closeErr
	}
	if err = os.Rename(f.Name(), s.activePath()); err != nil {
		return Incarnation{}, err
	}
	good = true
	return s, nil
}

func Load(ctx context.Context, dir string) (Incarnation, error) {
	var s Incarnation
	if err := canonicalDirectory(dir); err != nil {
		return s, err
	}
	if err := localstate.PrivateDirectory(dir); err != nil {
		return s, err
	}
	b, err := localstate.ReadPrivateFile(filepath.Join(dir, "session.json"), 65536)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if s.Directory != dir || s.ID != filepath.Base(dir) || len(s.ID) != 32 || s.Public.Endpoint != "cloud/"+s.Provider+"/"+s.ID || s.Expires.Sub(s.Created) > relay.MaxLifetime || !s.Expires.After(time.Now()) || s.Created.After(time.Now().Add(time.Minute)) {
		return s, errors.New("cloud session incarnation is invalid or expired; start a fresh connector")
	}
	if err = s.check(); err != nil {
		return s, err
	}
	if err = s.current(); err != nil {
		return s, err
	}
	if _, err = localstate.ReadPrivateFile(filepath.Join(dir, "identity.json"), 8192); err != nil {
		return s, err
	}
	i, err := (relay.Store{Directory: dir}).Identity(ctx)
	if err == nil && i.Public.Fingerprint() != s.Public.Fingerprint() {
		err = errors.New("cloud session identity changed")
	}
	return s, err
}

func (s Incarnation) activePath() string {
	sum := sha256.Sum256([]byte(s.Provider + "\x00" + s.Session + "\x00" + s.Workspace))
	return filepath.Join(filepath.Dir(s.Directory), "active-"+hex.EncodeToString(sum[:]))
}
func (s Incarnation) current() error {
	b, err := localstate.ReadPrivateFile(s.activePath(), 32)
	if err != nil {
		return err
	}
	if string(b) != s.ID {
		return errors.New("cloud session connector was superseded by a new incarnation")
	}
	return nil
}

type Observation struct {
	Provider            string    `json:"provider"`
	Session             string    `json:"session"`
	Incarnation         string    `json:"incarnation"`
	Workspace           string    `json:"workspace"`
	ObservedAt          time.Time `json:"observedAt"`
	LeaseExpires        time.Time `json:"leaseExpires"`
	TranscriptAvailable bool      `json:"transcriptAvailable"`
	ExportAllowed       bool      `json:"exportAllowed"`
	LastWrite           time.Time `json:"lastWrite,omitempty"`
	Bytes               int64     `json:"bytes"`
}

// Handler has no shell, inventory, receive, undo, code-export or settings route.
// Both the immutable local scope and the independently pinned peer grant apply.
func (s Incarnation) Handler(ctx context.Context, grant relay.Grant, operation, method string, params json.RawMessage) (any, error) {
	if !s.Expires.After(time.Now()) || !grant.Allows(method, time.Now()) {
		return nil, relay.ErrRevoked
	}
	if grant.Kind != "cloud-session" {
		return nil, errors.New("cloud connector requires a session-scoped grant")
	}
	if err := s.current(); err != nil {
		return nil, err
	}
	if method != "observe" && method != "export" {
		return nil, errors.New("cloud connector method is not allowed")
	}
	if len(params) > 0 && string(params) != "null" && string(params) != "{}" {
		return nil, errors.New("cloud connector requests cannot widen the bound session")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	obs := Observation{Provider: s.Provider, Session: s.Session, Incarnation: s.ID, Workspace: s.Workspace, ObservedAt: time.Now().UTC(), LeaseExpires: s.Expires, ExportAllowed: s.ExportTranscript}
	if s.Transcript != "" {
		st, err := os.Lstat(s.Transcript)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			obs.TranscriptAvailable = true
			obs.LastWrite = st.ModTime().UTC()
			obs.Bytes = st.Size()
		}
	}
	if method == "observe" {
		return obs, nil
	}
	if !s.ExportTranscript {
		return nil, errors.New("native transcript export was not opted in for this session")
	}
	if !obs.TranscriptAvailable {
		return nil, errors.New("native transcript is not available yet")
	}
	// Native file permissions are vendor-owned. Explicit export still requires
	// an owned regular opened object and a bounded read without following links.
	b, err := localstate.ReadOwnedFile(s.Transcript, relay.MaxObjectBytes/2)
	if err != nil {
		return nil, err
	}
	return sealedExport(obs, b)
}

// Run uses the same reviewed transport and durable request processor as device
// delivery, but exclusively the session-scoped Handler above. Lease expiration
// always stops the listener; cached setup cannot silently refresh it.
func (s Incarnation) Run(ctx context.Context) error {
	if !s.Expires.After(time.Now()) {
		return relay.ErrRevoked
	}
	if err := s.current(); err != nil {
		return err
	}
	l, err := localstate.TryLock(filepath.Join(s.Directory, "connector.lock"))
	if err != nil {
		return err
	}
	defer l.Close()
	store := relay.Store{Directory: s.Directory}
	identity, err := store.Identity(ctx)
	if err != nil {
		return err
	}
	if identity.Public.ID != s.Public.ID {
		return errors.New("cloud connector keys changed")
	}
	c, err := store.Connection(ctx)
	if err != nil {
		return err
	}
	if c.Device != identity.Public.ID || c.Expires > s.Expires.Unix() {
		return errors.New("routing credential exceeds this connector's identity or lease")
	}
	httpClient, err := c.HTTPClient()
	if err != nil {
		return err
	}
	if httpClient != nil {
		defer httpClient.CloseIdleConnections()
	}
	ctx, cancel := context.WithDeadline(ctx, s.Expires)
	defer cancel()
	service := relay.Service{Transport: relay.Transport{Base: c.URL, Token: c.Token, Space: c.Space, HTTP: httpClient}, Processor: relay.Processor{Identity: identity, Space: c.Space, Store: store, Handle: s.Handler, Recover: s.Handler}, Notify: func(error) {
		if s.current() != nil {
			cancel()
		}
	}}
	return service.Run(ctx)
}
