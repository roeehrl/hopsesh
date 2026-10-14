package loginsvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s Service) unit() string {
	return filepath.Join(s.Home, ".config", "systemd", "user", "hopsesh.service")
}

func systemdQuote(a string) string {
	if a != "" && !strings.ContainsAny(a, " \t\"'\\$%") {
		return a
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", "$$", "%", "%%")
	return `"` + r.Replace(a) + `"`
}

func (s Service) install(ctx context.Context) (Status, error) {
	if err := os.MkdirAll(filepath.Dir(s.unit()), 0o755); err != nil {
		return Status{}, err
	}
	var argv []string
	for _, a := range append([]string{s.Program}, s.Args...) {
		argv = append(argv, systemdQuote(a))
	}
	unit := fmt.Sprintf(`[Unit]
Description=hopsesh in the login session (sessions, accounts and terminals for your other machines)

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, strings.Join(argv, " "))
	tmp := s.unit() + ".tmp"
	if err := os.WriteFile(tmp, []byte(unit), 0o644); err != nil {
		return Status{}, err
	}
	if err := os.Rename(tmp, s.unit()); err != nil {
		return Status{}, err
	}
	if out, err := s.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		st := s.check(ctx)
		st.NoSession, st.Detail = true, strings.TrimSpace(string(out))
		return st, nil
	}
	if out, err := s.Run(ctx, "systemctl", "--user", "enable", "--now", "hopsesh.service"); err != nil {
		return s.check(ctx), fmt.Errorf("systemctl --user enable: %s", strings.TrimSpace(string(out)))
	}
	_, _ = s.Run(ctx, "systemctl", "--user", "restart", "hopsesh.service")
	return s.check(ctx), nil
}

func (s Service) uninstall(ctx context.Context) error {
	_, _ = s.Run(ctx, "systemctl", "--user", "disable", "--now", "hopsesh.service")
	if err := os.Remove(s.unit()); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, _ = s.Run(ctx, "systemctl", "--user", "daemon-reload")
	return nil
}

func (s Service) check(ctx context.Context) Status {
	st := Status{}
	b, err := os.ReadFile(s.unit())
	if err != nil {
		return st
	}
	st.Registered = true
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "ExecStart="); ok {
			if rest, ok := strings.CutPrefix(v, `"`); ok {
				p, _, _ := strings.Cut(rest, `"`)
				st.Program = strings.NewReplacer(`\\`, `\`, `$$`, `$`, `%%`, `%`).Replace(p)
			} else {
				st.Program, _, _ = strings.Cut(v, " ")
			}
		}
	}
	out, err := s.Run(ctx, "systemctl", "--user", "is-active", "hopsesh.service")
	st.Running = err == nil && strings.TrimSpace(string(out)) == "active"
	if err != nil && strings.Contains(string(out), "Failed to connect") {
		st.NoSession = true
	}
	return st
}
