package codex

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// auth.json yields only the account id, user id and email; tokens never come back.
func TestIdentityKeepsOnlyIDs(t *testing.T) {
	claims, _ := json.Marshal(map[string]any{"email": "me@example.com", "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-1", "chatgpt_user_id": "user-1", "chatgpt_plan_type": "pro"}})
	idToken := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
	b, _ := json.Marshal(map[string]any{"OPENAI_API_KEY": nil, "tokens": map[string]any{"id_token": idToken, "access_token": "SECRET-ACCESS", "refresh_token": "SECRET-REFRESH", "account_id": "acct-1"}})
	got, err := pickIdentity(b)
	if err != nil || got["account"] != "acct-1" || got["user"] != "user-1" || got["email"] != "me@example.com" || len(got) != 3 {
		t.Fatalf("%v %v", got, err)
	}
	for _, v := range got {
		if strings.Contains(v, "SECRET") {
			t.Fatal("a token leaked")
		}
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "auth.json"), b, 0o600)
	run, ok := New().LocalIdentity(root, true)
	if !ok || run.Key != accountKey("acct-1") || run.Person != personKey("user-1") {
		t.Fatalf("%+v %v", run, ok)
	}
	if _, err := pickIdentity([]byte(`{"OPENAI_API_KEY":"sk-x"}`)); err == nil {
		t.Fatal("an API-key login has no ChatGPT identity")
	}
}
