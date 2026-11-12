package loginsvc

import (
	"context"
	"fmt"
	"strings"
)

const task = `hopsesh\serve`

func (s Service) install(ctx context.Context) (Status, error) {
	line := `"` + s.Program + `"`
	for _, a := range s.Args {
		line += " " + a
	}
	// /IT: only while the user is signed in, in their own session (where their
	// credentials and apps are).
	if out, err := s.Run(ctx, "schtasks", "/Create", "/F", "/SC", "ONLOGON", "/IT", "/RL", "LIMITED", "/TN", task, "/TR", line); err != nil {
		return Status{}, fmt.Errorf("schtasks /Create: %s", strings.TrimSpace(string(out)))
	}
	if _, err := s.Run(ctx, "schtasks", "/Run", "/TN", task); err != nil {
		st := s.check(ctx)
		st.NoSession = true
		return st, nil
	}
	return s.check(ctx), nil
}

func (s Service) uninstall(ctx context.Context) error {
	_, _ = s.Run(ctx, "schtasks", "/End", "/TN", task)
	if out, err := s.Run(ctx, "schtasks", "/Delete", "/F", "/TN", task); err != nil && !strings.Contains(string(out), "cannot find") {
		return fmt.Errorf("schtasks /Delete: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (s Service) check(ctx context.Context) Status {
	out, err := s.Run(ctx, "schtasks", "/Query", "/TN", task, "/V", "/FO", "LIST")
	if err != nil {
		return Status{}
	}
	st := Status{Registered: true}
	for _, l := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Status":
			st.Running = strings.TrimSpace(v) == "Running"
		case "Task To Run":
			v = strings.TrimSpace(v)
			if rest, ok := strings.CutPrefix(v, `"`); ok {
				st.Program, _, _ = strings.Cut(rest, `"`)
			} else {
				st.Program, _, _ = strings.Cut(v, " ")
			}
		}
	}
	return st
}
