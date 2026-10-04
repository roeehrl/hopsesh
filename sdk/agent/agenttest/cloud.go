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
	"github.com/roeehrl/hopsesh/sdk/ir"
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

// CloudOptions adapt RunCloud to a cloud whose CLI can neither start a session from
// hopsesh nor list them on its own (Claude Code's teleport needs to be told which).
type CloudOptions struct {
	// Seed adds a session to the programs' cloud, as if the user had started it there, and
	// returns its id; RunCloud then passes it in CloudQuery.Known. For a module without
	// CloudSender.
	Seed func(cloud string) agent.SessionID
	// Request is the hand-off RunCloud sends, for a driver that starts from a real checkout
	// (claude --cloud clones the current branch of Dir's repository); nil: a made-up one.
	Request func(c agent.Cloud) agent.SendRequest
	// Step runs a terminal step (a SendCloud's Sent.Run) the way the user's terminal would,
	// answering what the driver asks as the user would, and returns what it printed. For
	// a module whose SendCloud returns one; RunCloud adds FailEnv to the command's Env when
	// it plays a failure.
	Step func(c agent.Command) agent.StepOutput
	// Work plays the cloud's agent on the session SendCloud started, before RunCloud fetches
	// it: a cloud that has nothing to bring until its agent worked (a patch) needs it; nil
	// fetches the session as it was sent.
	Work func(cloud string, id agent.SessionID)
}

// unreadableKnown is an id no vendor issues: a listing told about it reports it in
// CloudListing.Errors (or ignores it) and still lists the rest.
const unreadableKnown agent.SessionID = "?not-a-session (conformance)"

// RunCloud checks a module's clouds against the contract, with programs standing in for
// its drivers (they answer the binaries' version arguments too). Every module with
// Spec.Clouds runs it; a module that declares clouds as data only passes the Spec checks.
// For each cloud and each capability the module implements, it starts a session (or uses
// a seeded one, or the first listed), lists it, follows up, fetches and archives it, then
// checks that the vendor's refusals map onto ErrSignedOut, ErrNotEligible and
// ErrRepoUnsupported and that a listing survives an unreadable record. A module that
// adopts what its driver writes reports nothing before the driver ran; a module that
// reads links reads back its own; a module that tests its cloud refuses a login the cloud
// cannot use. Throughout, the drivers run only through the confined Host, with no
// credential in an argument or a variable.
func RunCloud(t *testing.T, m agent.Module, programs Programs) {
	t.Helper()
	RunCloudWith(t, m, programs, CloudOptions{})
}

// RunCloudWith is RunCloud with options.
func RunCloudWith(t *testing.T, m agent.Module, programs Programs, o CloudOptions) {
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
	adopter, okD := m.(agent.CloudAdopter)
	linker, okK := m.(agent.CloudLinker)
	tester, okT := m.(agent.CloudTester)
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

	reader, okR := m.(agent.CloudStepReader)
	// send starts a session: SendCloud, then its terminal step when it returns one.
	send := func(t *testing.T, h agent.Host, in agent.Install, c agent.Cloud, r agent.SendRequest, fail string) (agent.CloudSession, error) {
		t.Helper()
		sent, err := sender.SendCloud(ctx, h, in, r)
		if err != nil || sent.Run == nil {
			return sent.Session, err
		}
		run := *sent.Run
		switch {
		case !okR:
			t.Fatal("SendCloud returns a terminal step, so the module implements CloudStepReader")
		case len(run.Argv) == 0 || run.Argv[0] != c.Driver:
			t.Fatalf("a terminal step runs the cloud's driver %s, not %v", c.Driver, run.Argv)
		case run.Dir != r.Dir:
			t.Errorf("the terminal step runs in %q, not in the request's folder %q", run.Dir, r.Dir)
		case o.Step == nil:
			t.Fatal("SendCloud returns a terminal step: give RunCloudWith a Step that runs it")
		}
		for _, a := range append(append([]string(nil), run.Argv...), run.Env...) {
			if tokenLike.MatchString(a) {
				t.Errorf("a terminal step carries something that looks like a credential: %q", a)
			}
		}
		if !strings.Contains(strings.Join(run.Argv, " "), r.Brief) {
			t.Error("the terminal step does not carry the briefing")
		}
		if fail != "" {
			run.Env = append(append([]string(nil), run.Env...), FailEnv+"="+fail)
		}
		mu.Lock()
		calls = append(calls, strings.Join(run.Argv, " ")+" | "+strings.Join(run.Env, " "))
		mu.Unlock()
		return reader.ReadStep(c.Name, o.Step(run))
	}

	for _, c := range spec.Clouds {
		t.Run(c.Name, func(t *testing.T) {
			h, in := setup(t, "")
			var id agent.SessionID
			if okS {
				cs, err := send(t, h, in, c, o.request(c), "")
				if err != nil {
					t.Fatalf("SendCloud: %v", err)
				}
				checkSession(t, spec, c, cs)
				id = cs.Key.Session
			} else if o.Seed != nil {
				id = o.Seed(c.Name)
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
			if okS && o.Work != nil {
				o.Work(c.Name, id)
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
				if f.Adopt != nil && !okD {
					t.Error("FetchCloud returns an Adopt, so the module implements CloudAdopter")
				}
				if f.Adopt != nil && okD {
					if _, err := adopter.Adopted(ctx, h, in, *f.Adopt); !errors.Is(err, agent.ErrNotFound) {
						t.Errorf("before the driver ran, Adopted reports ErrNotFound, not %v", err)
					}
				}
			}
			if okK {
				url := linker.CloudURL(c.Name, id)
				for _, s := range []string{url, string(id)} {
					if cl, got, ok := linker.ParseCloudLink(s); !ok || cl != c.Name || got != id {
						t.Errorf("ParseCloudLink(%q) = %s, %s, %v; want %s, %s", s, cl, got, ok, c.Name, id)
					}
				}
				if _, _, ok := linker.ParseCloudLink("not a link"); ok {
					t.Error("ParseCloudLink reads anything as a link")
				}
			}
			if okT {
				ct, err := tester.TestCloud(ctx, h, in, c.Name)
				if err != nil || len(ct.Checks) == 0 {
					t.Errorf("TestCloud: %+v %v", ct, err)
				}
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
					if _, err := send(t, h, in, c, o.request(c), fail); !errors.Is(err, want) {
						t.Errorf("SendCloud while %s: want %v, got %v", fail, want, err)
					}
				}
				if okT {
					if _, err := tester.TestCloud(ctx, h, in, c.Name); !errors.Is(err, want) {
						t.Errorf("TestCloud while %s: want %v, got %v", fail, want, err)
					}
				}
			}
			if okS && needs(c, agent.NeedEnvironment) {
				h, in := setup(t, "no-env")
				if _, err := send(t, h, in, c, o.request(c), "no-env"); !errors.Is(err, agent.ErrNoEnvironment) {
					t.Errorf("SendCloud to an environment the cloud does not have: want ErrNoEnvironment, got %v", err)
				}
				r := o.request(c)
				r.Env = ""
				if _, err := send(t, h, in, c, r, "no-env"); !errors.Is(err, agent.ErrNoEnvironment) {
					t.Errorf("SendCloud without an environment: want ErrNoEnvironment, got %v", err)
				}
			}
			if okS {
				h, in := setup(t, "repo-mismatch")
				if _, err := send(t, h, in, c, o.request(c), "repo-mismatch"); !errors.Is(err, agent.ErrRepoUnsupported) {
					t.Errorf("SendCloud to a repository the cloud cannot clone: want ErrRepoUnsupported, got %v", err)
				}
			}
			if okL {
				h, in := setup(t, "")
				if okS {
					if _, err := send(t, h, in, c, o.request(c), ""); err != nil {
						t.Fatal(err)
					}
				}
				h, in = setup(t, "bad-record")
				l, err := lister.ListCloud(ctx, h, in, agent.CloudQuery{Cloud: c.Name, Known: []agent.SessionID{unreadableKnown}})
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

func needs(c agent.Cloud, n agent.Need) bool {
	for _, x := range c.Needs {
		if x == n {
			return true
		}
	}
	return false
}

// request is the hand-off to send: the module's own (Request), or a made-up one.
func (o CloudOptions) request(c agent.Cloud) agent.SendRequest {
	if o.Request != nil {
		return o.Request(c)
	}
	return sendRequest(c)
}

// sendRequest is a handoff as the core would prepare it.
func sendRequest(c agent.Cloud) agent.SendRequest {
	r := agent.SendRequest{Cloud: c.Name, Dir: "/home/alice/git/demo", Repo: "github.com/example/demo", Branch: "hopsesh/handoff/20261004-conform",
		Base: "0123456789abcdef0123456789abcdef01234567", Brief: agent.NotePrefix + "This task continues a session (conformance).", Title: "conformance"}
	if needs(c, agent.NeedEnvironment) {
		r.Env = "env_conformance"
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
	if seg := f.Segment; seg != nil {
		switch c.Down {
		case agent.FidCode:
			// Code only: the task's own words (its title, a summary) may come as messages, never
			// its steps.
			for _, n := range seg.Nodes {
				if n.Kind != ir.KindMessage {
					t.Errorf("cloud %s declares code only coming down but fetched a %s", c.Name, n.Kind)
				}
			}
		case agent.FidNone, agent.FidBrief:
			t.Errorf("cloud %s declares no conversation coming down but fetched one", c.Name)
		}
	}
}
