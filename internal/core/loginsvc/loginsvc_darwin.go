package loginsvc

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s Service) plist() string {
	return filepath.Join(s.Home, "Library", "LaunchAgents", Label+".plist")
}

func xmlText(v string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(v))
	return b.String()
}

func (s Service) contents() string {
	var args strings.Builder
	for _, a := range append([]string{s.Program}, s.Args...) {
		fmt.Fprintf(&args, "\n\t\t<string>%s</string>", xmlText(a))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>%s
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, Label, args.String(), xmlText(s.Log))
}

func (s Service) domain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func (s Service) install(ctx context.Context) (Status, error) {
	if err := os.MkdirAll(filepath.Dir(s.plist()), 0o755); err != nil {
		return Status{}, err
	}
	if err := os.MkdirAll(filepath.Dir(s.Log), 0o700); err != nil {
		return Status{}, err
	}
	tmp := s.plist() + ".tmp"
	if err := os.WriteFile(tmp, []byte(s.contents()), 0o644); err != nil {
		return Status{}, err
	}
	if err := os.Rename(tmp, s.plist()); err != nil {
		return Status{}, err
	}
	// A loaded older copy is replaced; with nobody logged in there is no gui domain and
	// launchd loads the agent at the next login.
	if _, err := s.Run(ctx, "launchctl", "print", s.domain()); err != nil {
		st := s.check(ctx)
		st.NoSession = true
		return st, nil
	}
	_, _ = s.Run(ctx, "launchctl", "bootout", s.domain()+"/"+Label)
	// bootout returns before the old copy is gone; bootstrap fails until it is.
	var out []byte
	var err error
	for i := 0; i < 20; i++ {
		if out, err = s.Run(ctx, "launchctl", "bootstrap", s.domain(), s.plist()); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return s.check(ctx), ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	if err != nil {
		st := s.check(ctx)
		st.Detail = strings.TrimSpace(string(out))
		return st, nil
	}
	_, _ = s.Run(ctx, "launchctl", "kickstart", s.domain()+"/"+Label)
	return s.check(ctx), nil
}

func (s Service) uninstall(ctx context.Context) error {
	_, _ = s.Run(ctx, "launchctl", "bootout", s.domain()+"/"+Label)
	if err := os.Remove(s.plist()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s Service) check(ctx context.Context) Status {
	st := Status{}
	b, err := os.ReadFile(s.plist())
	if err != nil {
		return st
	}
	st.Registered = true
	if i := bytes.Index(b, []byte("<array>")); i >= 0 {
		if j := bytes.Index(b[i:], []byte("<string>")); j >= 0 {
			rest := b[i+j+len("<string>"):]
			if k := bytes.Index(rest, []byte("</string>")); k >= 0 {
				st.Program = string(rest[:k])
			}
		}
	}
	out, err := s.Run(ctx, "launchctl", "print", s.domain()+"/"+Label)
	if err != nil {
		if _, e := s.Run(ctx, "launchctl", "print", s.domain()); e != nil {
			st.NoSession = true
		}
		return st
	}
	st.Running = bytes.Contains(out, []byte("state = running"))
	return st
}
