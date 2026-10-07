package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/BurntSushi/toml"
	"reflect"
	"strings"
	"time"
)

// Runtime controls the shared process, independently of desktop placement.
type Runtime struct {
	Mode             string `toml:"mode,omitempty" json:"mode"`
	ReconcileSeconds int    `toml:"reconcile_seconds,omitempty" json:"reconcileSeconds"`
}

func (r Runtime) Check() error {
	if r.Mode != "" && r.Mode != "headless" && r.Mode != "desktop" {
		return errors.New("runtime.mode must be headless or desktop")
	}
	if r.ReconcileSeconds != 0 && (r.ReconcileSeconds < 5 || r.ReconcileSeconds > 86400) {
		return errors.New("runtime.reconcile_seconds must be 5 to 86400")
	}
	return nil
}
func (r Runtime) Interval() time.Duration {
	if r.ReconcileSeconds == 0 {
		return time.Minute
	}
	return time.Duration(r.ReconcileSeconds) * time.Second
}

type Settings struct {
	Revision string         `json:"revision"`
	Values   map[string]any `json:"values"`
}

func ReadSettings() (Settings, error) {
	c, err := Load()
	if err != nil {
		return Settings{}, err
	}
	var b bytes.Buffer
	if err = toml.NewEncoder(&b).Encode(c); err != nil {
		return Settings{}, err
	}
	var values map[string]any
	_, err = toml.NewDecoder(&b).Decode(&values)
	return Settings{Revision: c.Revision(), Values: values}, err
}

// SetSetting uses TOML field names, strict JSON types and the same validation/CAS
// as every other writer. Schema and paths for runtime-only state are not editable.
func SetSetting(key string, value json.RawMessage, expected *string) (Settings, error) {
	c, err := Load()
	if err != nil {
		return Settings{}, err
	}
	if expected != nil && *expected != c.Revision() {
		return Settings{}, ErrConflict
	}
	if key == "schema" || key == "" {
		return Settings{}, errors.New("setting is not editable")
	}
	v := reflect.ValueOf(&c).Elem()
	parts := strings.Split(key, ".")
	for _, part := range parts {
		if v.Kind() != reflect.Struct {
			return Settings{}, fmt.Errorf("unknown setting %q", key)
		}
		typ := v.Type()
		found := false
		for i := 0; i < v.NumField(); i++ {
			tag := strings.Split(typ.Field(i).Tag.Get("toml"), ",")[0]
			if tag != "" && tag == part {
				v = v.Field(i)
				found = true
				break
			}
		}
		if !found {
			return Settings{}, fmt.Errorf("unknown setting %q", key)
		}
	}
	if !v.CanAddr() || !v.CanSet() {
		return Settings{}, errors.New("setting is not editable")
	}
	if err = json.Unmarshal(value, v.Addr().Interface()); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", key, err)
	}
	if err = Save(&c); err != nil {
		return Settings{}, err
	}
	return ReadSettings()
}

func SettingValue(s Settings, key string) (any, error) {
	var value any = s.Values
	for _, p := range strings.Split(key, ".") {
		m, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unknown setting %q", key)
		}
		value, ok = m[p]
		if !ok {
			return nil, fmt.Errorf("unknown or unset setting %q", key)
		}
	}
	return value, nil
}

// Relay enables the independently approved private connection; it grants no peer permissions.
type Relay struct {
	Enabled bool `toml:"enabled,omitempty" json:"enabled"`
}
