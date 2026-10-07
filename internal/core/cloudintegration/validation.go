package cloudintegration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/relay"
)

// Check binds public observation to the independently approved incarnation.
// Labels and paths returned by a connector never select another endpoint.
func (o Observation) Check(peer relay.PublicIdentity, now time.Time) error {
	if err := peer.Check(); err != nil {
		return err
	}
	parts := strings.Split(peer.Endpoint, "/")
	if len(parts) != 3 || parts[0] != "cloud" || parts[1] != o.Provider || parts[2] != o.Incarnation || len(o.Incarnation) != 32 || strings.Trim(o.Incarnation, "0123456789abcdef") != "" || !sessionName.MatchString(o.Session) {
		return errors.New("cloud observation does not match the approved incarnation")
	}
	switch o.Provider {
	case "claude-hosted", "codex-current", "codex-legacy", "work-cloud":
	default:
		return errors.New("unsupported cloud execution surface")
	}
	if len(o.Workspace) > 4096 || !path.IsAbs(o.Workspace) || path.Clean(o.Workspace) != o.Workspace || strings.ContainsAny(o.Workspace, "\x00\r\n") {
		return errors.New("invalid cloud workspace metadata")
	}
	if o.ObservedAt.IsZero() || o.ObservedAt.After(now.Add(time.Minute)) || o.ObservedAt.Before(now.Add(-5*time.Minute)) || !o.LeaseExpires.After(now) || o.LeaseExpires.After(now.Add(relay.MaxLifetime+time.Minute)) {
		return errors.New("cloud observation is stale or exceeds its lease")
	}
	if o.Bytes < 0 || o.Bytes > relay.MaxObjectBytes/2 || (!o.TranscriptAvailable && (o.Bytes != 0 || !o.LastWrite.IsZero())) {
		return errors.New("cloud transcript metadata exceeds its scope")
	}
	if (o.Provider == "codex-current" || o.Provider == "work-cloud") && (o.TranscriptAvailable || o.ExportAllowed) {
		return errors.New("native transcript visibility is unavailable on this cloud surface")
	}
	return nil
}

// Check authenticates the typed checkpoint's byte boundary and digest before a
// module sees its native data. It does not imply a finished provider session.
func (e Export) Check(peer relay.PublicIdentity, now time.Time) error {
	if err := e.Observation.Check(peer, now); err != nil {
		return err
	}
	if !e.TranscriptAvailable || !e.ExportAllowed || e.Format != "native-jsonl" || len(e.Data) == 0 || len(e.Data) > relay.MaxObjectBytes/2 {
		return errors.New("cloud checkpoint is not an allowed native transcript export")
	}
	sum := sha256.Sum256(e.Data)
	if e.SHA256 != hex.EncodeToString(sum[:]) {
		return errors.New("cloud checkpoint digest does not match its data")
	}
	data, sealed, err := sealCheckpoint(e.Data)
	if err != nil || !bytes.Equal(data, e.Data) {
		return errors.New("cloud checkpoint does not contain only complete native records")
	}
	c := e.Checkpoint
	if c.Kind != "complete-record-prefix" || c.Records != sealed.Records || c.ExportedBytes != int64(len(e.Data)) || c.ReadBytes != e.Bytes || c.OmittedTail < 0 || c.ReadBytes != c.ExportedBytes+c.OmittedTail || c.Created.Before(e.ObservedAt) || c.Created.After(now.Add(time.Minute)) {
		return errors.New("cloud checkpoint boundary metadata does not match its data")
	}
	return nil
}
