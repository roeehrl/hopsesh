package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// RemoteControlDefaulter is an agent with its own "start Remote Control for every
// session" setting in a profile's settings file (Claude Code's remoteControlAtStartup).
type RemoteControlDefaulter interface {
	// RemoteControlDefault is the setting in in's profile; nil when this install has none.
	RemoteControlDefault(h Host, in Install) *SettingFile
}

// SettingFile is one boolean key of a shared JSON settings file. Set changes only that
// key: on writes true, off removes it (the agent's own default again). All functions are
// pure; the app owns bounded local reads and atomic writes.
type SettingFile struct {
	Path     string
	Key      string
	Evidence string // the vendor's documentation of the key
	// Reason is why this install can't use it ("" when it can).
	Reason string
}

// Get reads the key: on is true only when it is the JSON true.
func (f SettingFile) Get(b []byte) (on bool, err error) {
	doc, err := settingsDoc(b)
	if err != nil {
		return false, err
	}
	return string(bytes.TrimSpace(doc[f.Key])) == "true", nil
}

// Set turns the key on (true) or off (removed), leaving every other key as it was.
func (f SettingFile) Set(b []byte, on bool) ([]byte, error) {
	doc, err := settingsDoc(b)
	if err != nil {
		return nil, err
	}
	if was, _ := f.Get(b); was == on && (on || doc[f.Key] == nil) {
		return b, nil
	}
	if on {
		doc[f.Key] = json.RawMessage("true")
	} else {
		delete(doc, f.Key)
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	return append(out, '\n'), err
}

func settingsDoc(b []byte) (map[string]json.RawMessage, error) {
	doc := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(b)) > 0 {
		if err := json.Unmarshal(b, &doc); err != nil || doc == nil {
			return nil, fmt.Errorf("settings must be a JSON object; not changing them")
		}
	}
	return doc, nil
}
