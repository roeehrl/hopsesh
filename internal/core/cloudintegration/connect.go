package cloudintegration

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
)

// A connect code carries the owner's one-use invitation into a Claude cloud session
// through the person's own message. The repository's UserPromptSubmit hook claims it
// and blocks the message, so neither the code nor the invitation reaches the model.
// The agent never runs the claim, the connector or an export.
const connectPrefix = "hopsesh-connect 1 "

// StatusPrompt asks the hook for this session's identity without contacting anyone.
const StatusPrompt = "hopsesh status"

var fingerprintPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// UserPrompt is the documented Claude command-hook input for a submitted prompt.
type UserPrompt struct {
	Session    string `json:"session_id"`
	Workspace  string `json:"cwd"`
	Transcript string `json:"transcript_path"`
	Event      string `json:"hook_event_name"`
	Prompt     string `json:"prompt"`
}

// ReadUserPrompt reads one UserPromptSubmit input. Prompts too large to be a
// status request or connect code are reported as not ours, never as an error:
// the hook must not interfere with ordinary messages.
func ReadUserPrompt(r io.Reader, remote string) (UserPrompt, bool, error) {
	var in UserPrompt
	if remote != "true" {
		return in, false, nil
	}
	b, err := io.ReadAll(io.LimitReader(r, 65537))
	if err != nil {
		return in, false, err
	}
	if len(b) > 65536 {
		return in, false, nil
	}
	if err = json.Unmarshal(b, &in); err != nil {
		return in, false, errors.New("invalid UserPromptSubmit input")
	}
	if in.Event != "UserPromptSubmit" {
		return in, false, errors.New("expected UserPromptSubmit hook input")
	}
	p := strings.TrimSpace(in.Prompt)
	return in, p == StatusPrompt || strings.HasPrefix(p, connectPrefix), nil
}

// IsStatus reports whether the prompt is exactly the status request.
func (p UserPrompt) IsStatus() bool { return strings.TrimSpace(p.Prompt) == StatusPrompt }

// FormatConnectCode encodes an owner fingerprint and a private invitation file.
func FormatConnectCode(fingerprint string, invitation []byte) (string, error) {
	if !fingerprintPattern.MatchString(fingerprint) {
		return "", errors.New("owner fingerprint is invalid")
	}
	if len(invitation) == 0 || len(invitation) > 8192 || !json.Valid(invitation) {
		return "", errors.New("cloud invitation is invalid or exceeds limit")
	}
	return connectPrefix + fingerprint + " " + base64.RawURLEncoding.EncodeToString(invitation), nil
}

// ParseConnectCode returns the owner fingerprint and invitation from a connect code.
func ParseConnectCode(prompt string) (string, []byte, error) {
	p := strings.TrimSpace(prompt)
	if !strings.HasPrefix(p, connectPrefix) {
		return "", nil, errors.New("not a hopsesh connect code")
	}
	fields := strings.Fields(strings.TrimPrefix(p, connectPrefix))
	if len(fields) != 2 || !fingerprintPattern.MatchString(fields[0]) {
		return "", nil, errors.New("connect code is incomplete; copy the whole line again")
	}
	invitation, err := base64.RawURLEncoding.DecodeString(fields[1])
	if err != nil || len(invitation) == 0 || len(invitation) > 8192 || !json.Valid(invitation) {
		return "", nil, errors.New("connect code is damaged; copy the whole line again")
	}
	return fields[0], invitation, nil
}
