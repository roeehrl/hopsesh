package cloudintegration

import (
	"bytes"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/relay"
)

func TestCloudCheckpointConsumerRefusesAlteredIdentityScopeAndFidelity(t *testing.T) {
	now := time.Now().UTC()
	id, err := relay.GenerateIdentity("cloud/claude-hosted/0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	observation := Observation{Provider: "claude-hosted", Session: "actual-session", Incarnation: "0123456789abcdef0123456789abcdef", Workspace: "/workspace/repo", ObservedAt: now, LeaseExpires: now.Add(time.Hour), TranscriptAvailable: true, ExportAllowed: true}
	original, err := sealedExport(observation, []byte("{\"sessionId\":\"actual-session\",\"content\":\"complete\"}\n{\"unfinished\":"))
	if err != nil {
		t.Fatal(err)
	}
	if err = original.Check(id.Public, now); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*Export)
	}{
		{"other incarnation", func(e *Export) { e.Incarnation = "ffffffffffffffffffffffffffffffff" }},
		{"other provider", func(e *Export) { e.Provider = "codex-current" }},
		{"relative workspace", func(e *Export) { e.Workspace = "../repo" }},
		{"expired lease", func(e *Export) { e.LeaseExpires = now.Add(-time.Second) }},
		{"stale observation", func(e *Export) { e.ObservedAt = now.Add(-6 * time.Minute) }},
		{"changed data", func(e *Export) { e.Data = append(bytes.Clone(e.Data), 'x') }},
		{"record count", func(e *Export) { e.Checkpoint.Records++ }},
		{"omitted tail", func(e *Export) { e.Checkpoint.OmittedTail++ }},
		{"export boundary", func(e *Export) { e.Checkpoint.ExportedBytes++ }},
		{"read boundary", func(e *Export) { e.Bytes++ }},
		{"unapproved export", func(e *Export) { e.ExportAllowed = false }},
		{"claimed complete session", func(e *Export) { e.Checkpoint.Kind = "finished-session" }},
		{"old checkpoint", func(e *Export) { e.Checkpoint.Created = now.Add(-time.Second) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			altered := original
			test.change(&altered)
			if err := altered.Check(id.Public, now); err == nil {
				t.Fatal("altered cloud checkpoint accepted")
			}
		})
	}
}

func TestCurrentCloudSurfaceCannotAdvertiseUnqualifiedNativeExport(t *testing.T) {
	now := time.Now().UTC()
	for _, provider := range []string{"codex-current", "work-cloud"} {
		id, err := relay.GenerateIdentity("cloud/" + provider + "/0123456789abcdef0123456789abcdef")
		if err != nil {
			t.Fatal(err)
		}
		observation := Observation{Provider: provider, Session: "actual-task", Incarnation: "0123456789abcdef0123456789abcdef", Workspace: "/workspace/repo", ObservedAt: now, LeaseExpires: now.Add(time.Hour)}
		if err = observation.Check(id.Public, now); err != nil {
			t.Fatal(err)
		}
		observation.TranscriptAvailable = true
		observation.ExportAllowed = true
		if err = observation.Check(id.Public, now); err == nil {
			t.Fatal("unqualified transcript capability accepted")
		}
	}
}

func TestCloudWorkspaceMetadataAcrossOperatingSystems(t *testing.T) {
	for _, test := range []struct {
		path string
		want bool
	}{
		{"/workspace/repo", true}, {"/", true},
		{`C:\Users\runner\repo`, true}, {`D:\`, true}, {"C:/Users/runner/repo", true},
		{`\\server\share\repo`, true}, {`\\server\share`, true},
		{`\\server\share\`, true}, {`\\server\share\repo\`, false},
		{"", false}, {"relative/repo", false}, {"/repo/../other", false},
		{`C:repo`, false}, {`\repo`, false}, {`C:\repo\..\other`, false},
		{`C:\repo\\other`, false}, {`\\server`, false}, {`\\server\share\..`, false},
		{`\\?\C:\repo`, false}, {`\\.\pipe\repo`, false}, {"/repo\nother", false},
	} {
		t.Run(test.path, func(t *testing.T) {
			if got := canonicalWorkspaceMetadata(test.path); got != test.want {
				t.Fatalf("workspace validity = %v, want %v", got, test.want)
			}
		})
	}
}
