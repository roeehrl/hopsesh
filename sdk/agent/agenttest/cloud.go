package agenttest

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Programs answer a module's binaries in a FakeHost (FakeHost.Programs).
type Programs = map[string]func(argv []string, o agent.RunOptions) agent.Result

// FailEnv is the variable RunCloud sets in a run's RunOptions.Env to make the stand-in
// driver fail like the vendor's: "signed-out", "not-eligible", "repo-mismatch" (a send),
// or "bad-record" (one unreadable record in a listing). The programs given to RunCloud
// must honour it.
const FailEnv = "FAKE_CLOUD_FAIL"

// tokenLike matches strings that look like credentials: no driver argument or variable a
// module passes may carry one (the vendor's CLI holds the login).
var tokenLike = regexp.MustCompile(`sk-ant-|sk-(?:proj|svcacct|admin)-|\bgh[pousr]_[A-Za-z0-9]{20}|github_pat_|\beyJ[A-Za-z0-9_-]{10,}\.|(?i)\bbearer\s`)

// credentialVar matches variable names that hold credentials.
var credentialVar = regexp.MustCompile(`(?i)(API_KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL)`)

// RunCloud checks a module's clouds against the contract, with programs standing in for
// its drivers (they answer the binaries' version arguments too). Every module with
// Spec.Clouds runs it; a module that declares clouds as data only passes the Spec checks.
// For each cloud and each capability the module implements, it starts a session (or uses
// the first listed), lists it, follows up, fetches and archives it, then checks that the
// vendor's refusals map onto ErrSignedOut, ErrNotEligible and ErrRepoUnsupported and that a
// listing survives an unreadable record. Throughout, the drivers run only through the
// confined Host, with no credential in an argument or a variable.
func RunCloud(t *testing.T, m agent.Module, programs Programs) {
	t.Helper()
	ctx := context.Background()
	spec := m.Spec()

	t.Run("spec", func(t *testing.T) {
		if len(spec.Clouds) == 0 {
			t.Fatal("RunCloud is for modules with Spec.Clouds")
		}
		if err := agent.CheckClouds(m); err != nil {
			t.Fatal(err)
		}
		if spec.Name == "" || spec.Vendor == "" || len(spec.Binaries) == 0 || len(spec.Tested) == 0 {
			t.Fatal("a Spec needs a name, a vendor, binaries and tested versions")
		}
		for _, c := range spec.Clouds {
			for _, mk := range []agent.Mark{{Kind: agent.MarkMoved, Location: c.Name}, {Kind: agent.MarkContinued, AgentName: spec.Name, Location: c.Name}} {
				got, title, ok := agent.ParseMarkTitle(agent.MarkTitle(mk, "a title"))
				if !ok || got.Location != c.Name || title != "a title" {
					t.Errorf("cloud %s does not survive a mark title: %+v %q", c.Name, got, title)
				}
			}
			if c.Up == agent.FidNative {
				t.Errorf("cloud %s: no vendor cloud takes a native conversation up; declare FidBrief", c.Name)
			}
			if len(c.Hosts) == 0 {
				t.Errorf("cloud %s names no repository host", c.Name)
			}
			if c.Watch.Surface == "" || len(c.Watch.Docs) == 0 {
				t.Errorf("cloud %s: Watch says what hopsesh uses there and which docs to watch", c.Name)
			}
		}
	})

	lister, okL := m.(agent.CloudLister)
	sender, okS := m.(agent.CloudSender)
	fetcher, okF := m.(agent.CloudFetcher)
	follower, okW := m.(agent.CloudFollower)
	archiver, okA := m.(agent.CloudArchiver)
	if !okL && !okS && !okF && !okW && !okA {
		return
	}

	var mu sync.Mutex
	var calls []string
	setup := func(t *testing.T, fail string) (agent.Host, agent.Install) {
		fh := NewFakeHost("/home/alice")
		for name, prog := range programs {
			fh.Programs[name] = func(argv []string, o agent.RunOptions) agent.Result {
				mu.Lock()
				calls = append(calls, strings.Join(argv, " ")+" | "+strings.Join(o.Env, " "))
				mu.Unlock()
				for _, kv := range o.Env {
					k, v, _ := strings.Cut(kv, "=")
					if tokenLike.MatchString(v) || credentialVar.MatchString(k) {
						t.Errorf("%s runs with a credential-like variable %s", argv[0], k)
					}
				}
				if fail != "" {
					o.Env = append(append([]string(nil), o.Env...), FailEnv+"="+fail)
				}
				return prog(argv, o)
			}
		}
		for _, b := range spec.Binaries {
			prog, ok := programs[b.Name]
			if !ok {
				t.Fatalf("no program stands in for %s", b.Name)
			}
			line := strings.SplitN(string(prog(append([]string{b.Name}, b.VersionArgs...), agent.RunOptions{}).Stdout), "\n", 2)[0]
			fh.AddBinary(b.Name, line)
		}
		in, err := m.Detect(ctx, fh)
		if err != nil {
			t.Fatalf("Detect: %v", err)
		}
		if in.Agent == "" {
			in.Agent = spec.ID
		}
		return agent.Confine(fh, spec, in), in
	}

	for _, c := range spec.Clouds {
		t.Run(c.Name, func(t *testing.T) {
			h, in := setup(t, "")
			var id agent.SessionID
			if okS {
				cs, err := sender.SendCloud(ctx, h, in, sendRequest(c))
				if err != nil {
					t.Fatalf("SendCloud: %v", err)
				}
				checkSession(t, spec, c, cs)
				id = cs.Key.Session
			}
			if okL {
				var known []agent.SessionID
				if id != "" {
					known = append(known, id)
				}
				l, err := lister.ListCloud(ctx, h, in, agent.CloudQuery{Cloud: c.Name, Known: known})
				if err != nil {
					t.Fatalf("ListCloud: %v", err)
				}
				found := false
				for _, s := range l.Sessions {
					checkSession(t, spec, c, s)
					found = found || s.Key.Session == id
				}
				if id != "" && !found {
					t.Errorf("the session SendCloud started (%s) is not listed", id)
				}
				if id == "" && len(l.Sessions) > 0 {
					id = l.Sessions[0].Key.Session
				}
			}
			if id == "" {
				t.Fatal("RunCloud needs a session: implement CloudSender, or have the programs list one")
			}
			if okW {
				cs, err := follower.FollowUp(ctx, h, in, id, agent.NotePrefix+"a follow-up from the conformance kit")
				if err != nil {
					t.Fatalf("FollowUp: %v", err)
				}
				if cs.Key.Session != id {
					t.Errorf("FollowUp answered for %s, not %s", cs.Key.Session, id)
				}
			}
			if okF {
				code := agent.ViaBranch
				if len(c.CodeDown) > 0 {
					code = c.CodeDown[0]
				}
				f, err := fetcher.FetchCloud(ctx, h, in, id, agent.FetchTarget{Dir: "/home/alice/git/demo-fetch", Code: code})
				if err != nil {
					t.Fatalf("FetchCloud: %v", err)
				}
				checkFetched(t, spec, c, f)
			}
			if okA {
				if err := archiver.Archive(ctx, h, in, id); err != nil {
					t.Fatalf("Archive: %v", err)
				}
				if okL {
					l, err := lister.ListCloud(ctx, h, in, agent.CloudQuery{Cloud: c.Name, Known: []agent.SessionID{id}})
					if err != nil {
						t.Fatalf("ListCloud after Archive: %v", err)
					}
					for _, s := range l.Sessions {
						if s.Key.Session == id && s.State != agent.CloudArchived {
							t.Errorf("an archived session is listed as %s", s.State)
						}
					}
				}
			}
		})

		t.Run(c.Name+"/refusals", func(t *testing.T) {
			for fail, want := range map[string]error{"signed-out": agent.ErrSignedOut, "not-eligible": agent.ErrNotEligible} {
				h, in := setup(t, fail)
				if okL {
					if _, err := lister.ListCloud(ctx, h, in, agent.CloudQuery{Cloud: c.Name}); !errors.Is(err, want) {
						t.Errorf("ListCloud while %s: want %v, got %v", fail, want, err)
					}
				}
				if okS {
					if _, err := sender.SendCloud(ctx, h, in, sendRequest(c)); !errors.Is(err, want) {
						t.Errorf("SendCloud while %s: want %v, got %v", fail, want, err)
					}
				}
			}
			if okS {
				h, in := setup(t, "repo-mismatch")
				if _, err := sender.SendCloud(ctx, h, in, sendRequest(c)); !errors.Is(err, agent.ErrRepoUnsupported) {
					t.Errorf("SendCloud to a repository the cloud cannot clone: want ErrRepoUnsupported, got %v", err)
				}
			}
			if okL {
				h, in := setup(t, "")
				if okS {
					if _, err := sender.SendCloud(ctx, h, in, sendRequest(c)); err != nil {
						t.Fatal(err)
					}
				}
				h, in = setup(t, "bad-record")
				l, err := lister.ListCloud(ctx, h, in, agent.CloudQuery{Cloud: c.Name})
				if err != nil {
					t.Fatalf("one unreadable record must not fail a listing: %v", err)
				}
				if len(l.Errors) == 0 {
					t.Error("an unreadable record goes in CloudListing.Errors")
				}
			}
		})
	}

	t.Run("drivers", func(t *testing.T) {
		bins := map[string]bool{}
		for _, b := range spec.Binaries {
			bins["/bin/"+b.Name] = true
		}
		mu.Lock()
		defer mu.Unlock()
		for _, call := range calls {
			argv, _, _ := strings.Cut(call, " | ")
			name, _, _ := strings.Cut(argv, " ")
			if !bins[name] && !bins["/bin/"+name] {
				t.Errorf("ran %s, which is not one of the Spec's binaries", name)
			}
			if tokenLike.MatchString(argv) {
				t.Errorf("a driver argument looks like a credential: %s", argv)
			}
		}
	})
}

// sendRequest is a handoff as the core would prepare it.
func sendRequest(c agent.Cloud) agent.SendRequest {
	r := agent.SendRequest{Cloud: c.Name, Dir: "/home/alice/git/demo", Repo: "github.com/example/demo", Branch: "hopsesh/handoff/20261004-conform",
		Base: "0123456789abcdef0123456789abcdef01234567", Brief: agent.NotePrefix + "This task continues a session (conformance).", Title: "conformance"}
	for _, n := range c.Needs {
		if n == agent.NeedEnvironment {
			r.Env = "env_conformance"
		}
	}
	return r
}

func checkSession(t *testing.T, spec agent.Spec, c agent.Cloud, s agent.CloudSession) {
	t.Helper()
	if s.Key.Agent != spec.ID || s.Key.Session == "" || s.Cloud != c.Name {
		t.Errorf("incomplete cloud session: %+v", s)
	}
	switch s.State {
	case agent.CloudRunning, agent.CloudIdle, agent.CloudDone, agent.CloudFailed, agent.CloudArchived, agent.CloudUnknown:
	default:
		t.Errorf("cloud session %s: state %q", s.Key.Session, s.State)
	}
	if s.Updated.After(time.Now().Add(time.Hour)) {
		t.Errorf("cloud session %s was updated in the future: %v", s.Key.Session, s.Updated)
	}
}

func checkFetched(t *testing.T, spec agent.Spec, c agent.Cloud, f agent.Fetched) {
	t.Helper()
	if f.Run == nil && f.Adopt == nil && f.Segment == nil && f.Code.Way == "" {
		t.Error("FetchCloud brought nothing and asked nothing of the user")
	}
	if f.Run != nil {
		ok := false
		for _, b := range spec.Binaries {
			ok = ok || len(f.Run.Argv) > 0 && f.Run.Argv[0] == b.Name
		}
		if !ok {
			t.Errorf("Fetched.Run must run one of the Spec's binaries: %v", f.Run.Argv)
		}
	}
	if a := f.Adopt; a != nil {
		root := false
		for _, r := range spec.Roots {
			root = root || r.Name == a.Root
		}
		if !root || a.Session == "" || strings.HasPrefix(a.Dir, "/") || strings.Contains("/"+a.Dir+"/", "/../") {
			t.Errorf("Fetched.Adopt must name a session in a folder inside one of the module's roots: %+v", a)
		}
	}
	if f.Segment != nil && c.Down == agent.FidCode {
		t.Errorf("cloud %s declares code only coming down but fetched a conversation", c.Name)
	}
}
