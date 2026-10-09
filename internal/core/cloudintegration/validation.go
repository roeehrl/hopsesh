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
	if !validStartupSource(o.Source) {
		return errors.New("invalid cloud startup reason")
	}
	// A valid historical signature is descriptive until the native issuer also
	// verifies the invitation's claim against this independently approved peer.
	if o.Task != nil {
		if o.Task.Verify(o.Task.Owner.ID) != nil || o.Task.Provider != o.Provider || o.Generation < 1 || o.Generation >= 1<<53 || len(o.Admission) != 32 || strings.Trim(o.Admission, "0123456789abcdef") != "" {
			return errors.New("invalid cloud task provenance")
		}
	} else if o.Admission != "" || o.Generation != 0 {
		return errors.New("cloud admission has no task provenance")
	}
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
	if !canonicalWorkspaceMetadata(o.Workspace) {
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

// This is a remote display value, never a local filesystem authority. Validate
// both native path forms independently of the inspecting machine's OS.
func canonicalWorkspaceMetadata(workspace string) bool {
	if workspace == "" || len(workspace) > 4096 || strings.ContainsFunc(workspace, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return false
	}
	if strings.HasPrefix(workspace, "/") {
		return path.Clean(workspace) == workspace
	}
	p := strings.ReplaceAll(workspace, `\`, "/")
	if len(p) >= 3 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) && p[1:3] == ":/" {
		return path.Clean(p[2:]) == p[2:]
	}
	if strings.HasPrefix(workspace, `\\`) {
		parts := strings.Split(p[2:], "/")
		if len(parts) == 3 && parts[2] == "" { // A UNC share root may retain its separator.
			parts = parts[:2]
		}
		if len(parts) < 2 || parts[0] == "?" || parts[0] == "." {
			return false
		}
		for _, part := range parts {
			if part == "" || part == "." || part == ".." {
				return false
			}
		}
		return true
	}
	return false
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
