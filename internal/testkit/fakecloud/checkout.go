package fakecloud

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// DemoCheckout is a checkout of github.com/example/demo for a module's tests, whose remote
// is a local bare repository standing in for GitHub (git's global config, which the test
// points GIT_CONFIG_GLOBAL at, redirects the URL), with main pushed. Git's identity is a
// made-up one, and FAKE_CLOUD_FAIL is cleared.
func DemoCheckout(tb testing.TB) string {
	tb.Helper()
	root := tb.TempDir()
	gitConfig := filepath.Join(root, "gitconfig")
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": gitConfig, "GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "Sam Doe", "GIT_AUTHOR_EMAIL": "sam@example.com",
		"GIT_COMMITTER_NAME": "Sam Doe", "GIT_COMMITTER_EMAIL": "sam@example.com", "FAKE_CLOUD_FAIL": ""} {
		tb.Setenv(k, v)
	}
	o, err := NewOrigin(root, "https://github.com/example/demo.git")
	if err != nil {
		tb.Fatal(err)
	}
	if err := o.Redirect(gitConfig); err != nil {
		tb.Fatal(err)
	}
	repo := filepath.Join(root, "demo")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "remote", "add", "origin", o.URL},
		{"-C", repo, "commit", "-q", "--allow-empty", "-m", "init"}, {"-C", repo, "push", "-q", "origin", "main"},
		{"-C", repo, "push", "-q", "origin", "main:refs/heads/hopsesh/handoff/20261004-conform"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			tb.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return repo
}
