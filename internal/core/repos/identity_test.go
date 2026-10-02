package repos

import "testing"

func TestIdentity(t *testing.T) {
	same := []string{
		"git@github.com:Owner/Repo.git",
		"https://github.com/owner/repo",
		"https://user:tok@github.com/owner/repo.git/",
		"ssh://git@github.com:22/owner/repo.git",
		"github.com:owner/repo",
	}
	for _, r := range same {
		if got := Identity(r); got != "github.com/owner/repo" {
			t.Errorf("Identity(%q) = %q", r, got)
		}
	}
	for r, want := range map[string]string{
		"git@gitlab.example.com:group/sub/proj.git": "gitlab.example.com/group/sub/proj",
		"/Users/me/repo.git":                        "",
		`C:\repos\x`:                                "",
		"file:///srv/git/x.git":                     "",
		"":                                          "",
	} {
		if got := Identity(r); got != want {
			t.Errorf("Identity(%q) = %q, want %q", r, got, want)
		}
	}
	if Name("github.com/owner/repo") != "repo" {
		t.Error("Name")
	}
}
