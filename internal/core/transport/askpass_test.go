package transport

import (
	"context"
	"testing"
)

func TestAskpassBridge(t *testing.T) {
	asked := 0
	c := &Conn{Dest: "me@box", Password: func(ctx context.Context, dest string, retry bool) (string, error) {
		asked++
		if dest != "me@box" {
			t.Errorf("dest %q", dest)
		}
		return "s3cret pass", nil
	}}
	env, err := c.passwordEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	get := func(k string) string {
		for _, e := range env {
			if len(e) > len(k) && e[:len(k)+1] == k+"=" {
				return e[len(k)+1:]
			}
		}
		return ""
	}
	sock, tok := get("HOPSESH_ASKPASS_SOCK"), get("HOPSESH_ASKPASS_TOKEN")
	if get("SSH_ASKPASS_REQUIRE") != "force" || sock == "" || tok == "" {
		t.Fatalf("env: %v", env)
	}
	for _, e := range env {
		if e == "s3cret pass" || len(e) > 0 && containsStr(e, "s3cret") {
			t.Fatal("the password must never be in the environment")
		}
	}
	if a, ok := askpassQuery(sock, tok, "(me@box) Password:"); !ok || a != "s3cret pass" {
		t.Fatalf("password prompt: %q %v", a, ok)
	}
	if _, ok := askpassQuery(sock, tok, "me@box's password: "); !ok || asked != 1 {
		t.Fatalf("second prompt should reuse the answer (asked %d)", asked)
	}
	if _, ok := askpassQuery(sock, tok, "Enter passphrase for key '/x/id_ed25519':"); ok {
		t.Fatal("key passphrase prompts are never answered")
	}
	if _, ok := askpassQuery(sock, "wrong-token", "Password:"); ok {
		t.Fatal("an unknown token gets nothing")
	}
	c.Close()
	if _, ok := askpassQuery(sock, tok, "Password:"); ok {
		t.Fatal("after Close the token is gone")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestIsPasswordPrompt(t *testing.T) {
	for prompt, want := range map[string]bool{
		"me@10.0.0.5's password: ":                                true,
		"(me@studio.local) Password: ":                            true,
		"Enter passphrase for key '/x/id_ed25519': ":              false,
		"Password for 'https://github.com': ":                     false,
		"Username for 'https://github.com': ":                     false,
		"Are you sure you want to continue connecting (yes/no)? ": false,
	} {
		if got := IsPasswordPrompt(prompt); got != want {
			t.Errorf("IsPasswordPrompt(%q) = %v, want %v", prompt, got, want)
		}
	}
}
