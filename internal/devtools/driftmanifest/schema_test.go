package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecursiveAccountSchemaDiff(t *testing.T) {
	if _, err := exec.LookPath("diff"); err != nil {
		t.Skip("requires diff (mandatory in drift workflow)")
	}
	old, latest, out := t.TempDir(), t.TempDir(), t.TempDir()
	write := func(root, path, data string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(old, "v2/GetAccountParams.json", `{"refreshToken":false}`)
	write(latest, "v2/GetAccountParams.json", `{"refreshToken":true}`)
	write(old, "v1/GetAccountParams.json", `{"version":1}`)
	write(latest, "v1/GetAccountParams.json", `{"version":2}`)
	write(old, "v2/GetAccountResponse.json", `{"email":"alice@example.com"}`)
	write(latest, "v2/LoginAccountResponse.json", `{"accountId":"new-field"}`)
	write(old, "ThreadResumeParams.json", `{"threadId":"same"}`)
	write(latest, "ThreadResumeParams.json", `{"threadId":"same"}`)
	write(latest, "Unrelated.json", `{}`)
	if err := diffSchemas(old, latest, out, agentWatch["codex"].Watch.Schema.Keep); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"v2/GetAccountParams.json.diff":     `+{"refreshToken":true}`,
		"v1/GetAccountParams.json.diff":     `+{"version":2}`,
		"v2/GetAccountResponse.json.diff":   `-{"email":"alice@example.com"}`,
		"v2/LoginAccountResponse.json.diff": `+{"accountId":"new-field"}`,
	} {
		b, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(path)))
		if err != nil || !strings.Contains(string(b), content) {
			t.Errorf("%s: %v: %s", path, err, b)
		}
	}
	for _, absent := range []string{"ThreadResumeParams.json.diff", "Unrelated.json.diff"} {
		if _, err := os.Stat(filepath.Join(out, absent)); !os.IsNotExist(err) {
			t.Errorf("unexpected diff %s: %v", absent, err)
		}
	}
	changed, err := os.ReadFile(filepath.Join(out, "changed-files.txt"))
	if err != nil || !strings.Contains(string(changed), "Unrelated.json") || len(strings.Fields(string(changed))) != 5 {
		t.Fatalf("changed-file inventory: %v: %s", err, changed)
	}
	if err := diffSchemas(old, t.TempDir(), t.TempDir(), ".*"); err == nil {
		t.Fatal("empty schema generation was treated as successful coverage")
	}
	if err := diffSchemas(old, latest, t.TempDir(), "("); err == nil {
		t.Fatal("invalid schema filter accepted")
	}
	if err := diffSchemas(old, filepath.Join(t.TempDir(), "absent"), t.TempDir(), ".*"); err == nil {
		t.Fatal("missing schema directory accepted")
	}
}
