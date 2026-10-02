package link

import (
	"strings"
	"testing"
)

func TestStartPromptCoversMoveAndChecks(t *testing.T) {
	p := StartPrompt(Context{
		SourceHost: "studio", SourceOS: "macOS", SourceVersion: "2.1.284", SourceCWD: "/Users/a/git/p",
		TargetHost: "laptop", TargetOS: "macOS", TargetCWD: "/Users/b/git/p/.claude/worktrees/feat",
		Branch: "feat", WorktreeNote: "A matching worktree was created here.", Unpushed: 2, Dirty: 7,
		SecretsFound: 1, Redacted: true, NotifyOld: true, OldName: "refactor auth", NewName: "refactor-auth@laptop",
	})
	for _, want := range []string{"moved here from another machine", "studio", "laptop", "2.1.284", "branch feat",
		"worktree", "2 unpushed", "7 uncommitted", "git status", "environment variables", "redacted",
		`"refactor auth"`, "SendMessage", "stop working on this task", "wait for my go-ahead"} {
		if !strings.Contains(p, want) {
			t.Errorf("start prompt lacks %q:\n%s", want, p)
		}
	}
}

func TestResumeQuoting(t *testing.T) {
	r := Resume{Dir: "/Users/b/My Repo", SessionID: "abc", Fork: true, RemoteCtl: true, Name: "x@laptop", StartPrompt: "it's moved"}
	sh := r.Shell("posix")
	if sh != `cd '/Users/b/My Repo' && claude --resume abc --fork-session --remote-control x@laptop 'it'"'"'s moved'` {
		t.Errorf("posix: %s", sh)
	}
	ps := r.Shell("powershell")
	if ps != `Set-Location '/Users/b/My Repo'; & 'claude' '--resume' 'abc' '--fork-session' '--remote-control' 'x@laptop' 'it''s moved'` {
		t.Errorf("powershell: %s", ps)
	}
}

func TestResumeWithPromptFile(t *testing.T) {
	r := Resume{Dir: "/d", SessionID: "abc", StartPrompt: "long", PromptFile: "/s/p.md"}
	if got := r.Shell("posix"); got != `cd /d && claude --resume abc "$(cat /s/p.md)"` {
		t.Errorf("posix: %s", got)
	}
	if got := r.Shell("powershell"); got != `Set-Location '/d'; & 'claude' '--resume' 'abc' (Get-Content -Raw '/s/p.md')` {
		t.Errorf("powershell: %s", got)
	}
	if a := r.Argv(); a[len(a)-1] != "long" {
		t.Error("argv must still carry the prompt itself")
	}
}

func TestAuth(t *testing.T) {
	a, err := ParseAuth([]byte(`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","orgId":"o1","subscriptionType":"max","email":"x@y"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ok, why := a.RemoteControl(); !ok {
		t.Fatalf("subscription login should allow Remote Control: %s", why)
	}
	key := &Auth{LoggedIn: true, Method: "api-key", Provider: "firstParty", OrgID: "o2"}
	if ok, _ := key.RemoteControl(); ok {
		t.Fatal("API key login must not allow Remote Control")
	}
	if ok, _ := (&Auth{LoggedIn: true, Provider: "bedrock"}).RemoteControl(); ok {
		t.Fatal("Bedrock must not allow Remote Control")
	}
	if same, known := SameAccount(a, key); !known || same {
		t.Fatal("different orgs should be known and different")
	}
	if _, known := SameAccount(a, nil); known {
		t.Fatal("missing side should be unknown")
	}
}

func TestResumeDesktop(t *testing.T) {
	r := Resume{SessionID: "abc", Desktop: true}
	got := strings.Join(r.Argv(), " ")
	if got != "claude --desktop --resume abc" {
		t.Fatalf("got %q", got)
	}
}
