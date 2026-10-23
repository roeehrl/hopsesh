package codex

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// accountKey fingerprints a ChatGPT account id ("" for none): the same for a login and
// for the creator a session records.
func accountKey(id string) string {
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("codex-account:" + id))
	return "chatgpt-account:" + hex.EncodeToString(sum[:8])
}

// personKey fingerprints a ChatGPT user id ("" for none).
func personKey(id string) string {
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("codex-user:" + id))
	return hex.EncodeToString(sum[:8])
}

type identity struct{ Account, User string }

// identityOf reads the account and user ids from this machine's auth.json, keeping
// nothing else: not the tokens, not the rest of the ID token. It must agree with the email
// Codex reported.
func identityOf(h agent.Host, in agent.Install, email string) (identity, bool) {
	r, ok := h.FS().(agent.IdentityFS)
	if !ok || in.Root("home") == "" {
		return identity{}, false
	}
	got, err := r.ReadIdentity(h.Path().Join(in.Root("home"), "auth.json"), pickIdentity)
	if err != nil || !strings.EqualFold(got["email"], strings.TrimSpace(email)) || got["account"] == "" {
		return identity{}, false
	}
	return identity{Account: got["account"], User: got["user"]}, true
}

// pickIdentity takes tokens.account_id and, from the ID token's payload, the user id and
// email. The ID token is only decoded, never verified or used.
func pickIdentity(b []byte) (map[string]string, error) {
	var f struct {
		Tokens *struct {
			Account string `json:"account_id"`
			IDToken string `json:"id_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(b, &f); err != nil || f.Tokens == nil {
		return nil, errors.New("no ChatGPT login")
	}
	parts := strings.Split(f.Tokens.IDToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("no ID token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, err
	}
	var c struct {
		Email   string `json:"email"`
		Profile struct {
			Email string `json:"email"`
		} `json:"https://api.openai.com/profile"`
		Auth struct {
			Account string `json:"chatgpt_account_id"`
			User    string `json:"chatgpt_user_id"`
			UserID  string `json:"user_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	out := map[string]string{"account": f.Tokens.Account, "user": c.Auth.User, "email": c.Email}
	if out["account"] == "" {
		out["account"] = c.Auth.Account
	}
	if out["user"] == "" {
		out["user"] = c.Auth.UserID
	}
	if out["email"] == "" {
		out["email"] = c.Profile.Email
	}
	return out, nil
}

// LocalIdentity reads the account and user ids from the root's auth.json (only those).
func (m *Module) LocalIdentity(root string, _ bool) (agent.OwnerRun, bool) {
	b, err := os.ReadFile(filepath.Join(root, "auth.json"))
	if err != nil {
		return agent.OwnerRun{}, false
	}
	defer clear(b)
	id, err := pickIdentity(b)
	if err != nil || id["account"] == "" {
		return agent.OwnerRun{}, false
	}
	return agent.OwnerRun{Key: accountKey(id["account"]), Person: personKey(id["user"]), Source: "hopsesh"}, true
}
