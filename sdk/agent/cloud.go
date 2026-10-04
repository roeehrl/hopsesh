package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Sessions live at a location: a machine, or a vendor's cloud. Cloud access is a set of
// optional capabilities a module implements through its agent's own CLI, on the machine
// running hopsesh (where the vendor login is). A module never opens a socket for it and
// never reads a credential: the vendor's binary does the talking.

// LocationKind says what a location is.
type LocationKind string

const (
	AtMachine LocationKind = "machine" // this machine, one reached over SSH, or a peer's snapshot
	AtCloud   LocationKind = "cloud"   // a vendor's cloud, reached through its CLI
)

// Location is where a session lives.
type Location struct {
	Kind LocationKind `json:"kind"`
	// Name is the machine's name, or the cloud's ("codex-cloud"). Cloud names match
	// CloudNameSyntax, so a mark title can carry them.
	Name string `json:"name"`
}

// MachineLocation is a machine's location.
func MachineLocation(name string) Location { return Location{Kind: AtMachine, Name: name} }

// CloudLocation is a cloud's location.
func CloudLocation(name string) Location { return Location{Kind: AtCloud, Name: name} }

// IsCloud reports whether the location is a cloud.
func (l Location) IsCloud() bool { return l.Kind == AtCloud }

func (l Location) String() string { return l.Name }

// CloudNameSyntax is what a cloud's name looks like: lower case letters, digits and
// dashes, no spaces (mark titles end the location at a space).
var CloudNameSyntax = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// Fidelity is how much of a conversation survives one direction between a cloud and a
// machine. Each cloud declares it per direction, so the core can promise it before any
// module code runs.
type Fidelity string

const (
	FidNative Fidelity = "native" // the vendor's own copy of the conversation (Claude Code's teleport)
	FidText   Fidelity = "text"   // messages, perhaps the plan and shell output; no native tool calls
	FidCode   Fidelity = "code"   // the code and the task's title and summary; no conversation
	FidBrief  Fidelity = "brief"  // a briefing in the first prompt (every upload, as of 2026)
	FidNone   Fidelity = "none"   // nothing goes this way
)

// CodeWay is how code travels between a machine and a cloud.
type CodeWay string

const (
	ViaBranch       CodeWay = "branch"        // a pushed branch the cloud clones, or pushes back
	ViaBundle       CodeWay = "bundle"        // the driver uploads the repository itself (Claude Code's git bundle)
	ViaStartingDiff CodeWay = "starting-diff" // a patch sent with the task (Codex's CODEX_STARTING_DIFF)
	ViaPR           CodeWay = "pr"            // a pull request the cloud opened
	ViaDiff         CodeWay = "diff"          // the driver prints or applies a unified diff
)

// Need is something a cloud requires, checked by the core while planning. The user
// interfaces have a sentence for each.
type Need string

const (
	NeedSubscriptionLogin Need = "subscription-login" // a claude.ai or ChatGPT login, not an API key
	NeedGitHub            Need = "github"             // a repository host the cloud can clone and push
	NeedPushedBranch      Need = "pushed-branch"      // the cloud starts from a branch on the remote
	NeedCleanTree         Need = "clean-tree"         // the driver refuses uncommitted changes (teleport)
	NeedSameAccount       Need = "same-account"       // only the account that owns the session can fetch it
	NeedEnvironment       Need = "environment"        // a vendor environment id (codex cloud exec --env)
	NeedTerminal          Need = "terminal"           // the driver step is interactive
)

// Cloud is one vendor cloud a module reaches, declared as data in Spec.Clouds: what the
// core may promise, and what the drift check watches.
type Cloud struct {
	Name   string // the location name: "codex-cloud"
	Title  string // for people: "Codex cloud"
	Driver string // the Spec binary that holds the login and runs the cloud verbs: "codex"
	// Tested lists the driver versions the cloud verbs were tested with, as version
	// prefixes (Spec.Tested's form).
	Tested []string
	// Hosts are the repository hosts the cloud can clone from and push to: "github.com".
	Hosts []string
	Up    Fidelity // a conversation going up: FidBrief everywhere today
	Down  Fidelity // coming down to the same agent here: FidNative, FidCode, FidText
	// CodeUp and CodeDown are how code travels each way, preferred first.
	CodeUp   []CodeWay
	CodeDown []CodeWay
	Needs    []Need
	// VendorPrefix starts the names of the branches the cloud's agent pushes ("claude/").
	// Brought home, such a branch is renamed under hopsesh/from/<cloud>/ (unless the user
	// turned that off).
	VendorPrefix string
	// Problems are upstream issues that explain a fetch's known failures, by outcome
	// ("partial", "empty"), for the user interfaces to point to.
	Problems map[string]string
	// Unset are environment variables the driver must not inherit: a marker that makes it
	// save nothing (a nested session's), or an API key that would take the place of the
	// subscription login. The core removes them from every run of the cloud capabilities
	// and from a command the user runs (Fetched.Run).
	Unset []string
	// Noun is what the vendor calls one of its sessions, for people ("task"); "" is
	// "session".
	Noun string
	// Limits are parts of the vendor's cloud the driver cannot reach, in short sentences
	// the user interfaces show beside the cloud (the hand-off menu, its card, `hopsesh clouds
	// test`), so a missing path is said, never faked.
	Limits []string
	// Summary: a cloud whose Down is FidCode still brings the task's own words back (its
	// title and what came of it, in Fetched.Segment), which the core writes as a local
	// session beside the code.
	Summary bool
	// EnvHint says how the user makes an environment, for a cloud with NeedEnvironment
	// ("open `codex cloud` once to create one").
	EnvHint string
	// BriefBranch: the driver cannot choose the branch a new session starts from (the cloud
	// clones the repository's default branch, or one hopsesh cannot name), so the core's
	// briefing asks the cloud agent to check out the handoff branch first, and the plan says
	// so.
	BriefBranch bool
	// NoFollowUp says, for people, why hopsesh sends this cloud's sessions no follow-up
	// and where the user can ("Claude Code has no command that sends one without its own
	// terminal session; open the session on claude.ai to write to it"). The user interfaces
	// show it in place of a follow-up. "" for a cloud whose module is a CloudFollower, or
	// where nothing needs saying.
	NoFollowUp string
	Watch      Watch
}

// Watch is what the weekly upstream-drift check watches for a cloud (read by
// internal/devtools/driftmanifest): the single place a module says which vendor
// surfaces its cloud code relies on.
type Watch struct {
	// Surface is what hopsesh uses there, in plain words, for the drift review.
	Surface string
	// Docs are Markdown pages and llms.txt indexes, hashed and diffed week to week.
	Docs []string
	// Feeds are changelogs and release feeds.
	Feeds []Feed
	// Grep is an extended regular expression for the feeds' new entries and the docs
	// diffs.
	Grep string
	// Help are commands that need no login, whose output is diffed between the tested and
	// the latest driver: {"codex", "cloud", "exec", "--help"}.
	Help [][]string
	// Relies are the flags and subcommands in that help the cloud code uses; one that
	// disappears is a break.
	Relies []string
	// Issues are upstream issues and pull requests: "anthropics/claude-code#95873".
	Issues []string
	// Searches are GitHub issue searches, limited to issues opened since the last run.
	Searches []string
	// Code is part of an upstream repository whose commits and canaries are watched.
	Code *WatchCode
}

// FeedKind is how the drift check reads a feed.
type FeedKind string

const (
	FeedMarkdown FeedKind = "markdown" // a CHANGELOG.md, cut at the tested version
	FeedRSS      FeedKind = "feed"     // RSS or Atom: the items since the last run
	FeedReleases FeedKind = "releases" // a GitHub repository's releases since Tag+tested
)

// Feed is one changelog or release feed.
type Feed struct {
	Kind FeedKind
	URL  string // markdown and feed
	Repo string // releases: "openai/codex"
	Tag  string // releases: the tag's prefix before the version ("rust-v")
}

// WatchCode is part of an upstream repository: the commits on Paths since the last run,
// and how many files hold each canary (strings the cloud code depends on, such as
// "CODEX_STARTING_DIFF").
type WatchCode struct {
	Repo     string
	Paths    []string
	Canaries []string
}

// CloudState is a cloud session's state, in the vendor's terms mapped to these.
type CloudState string

const (
	CloudRunning  CloudState = "running"
	CloudIdle     CloudState = "idle" // waiting for the user (a Claude Code cloud session stays open)
	CloudDone     CloudState = "done"
	CloudFailed   CloudState = "failed"
	CloudArchived CloudState = "archived"
	CloudUnknown  CloudState = "unknown"
)

// CloudSession is one session in a vendor's cloud.
type CloudSession struct {
	// Key's Agent is the module; Session is the vendor's id ("session_01…", "task_e_…").
	Key   SessionKey `json:"key"`
	Cloud string     `json:"cloud"` // the cloud's name
	URL   string     `json:"url,omitempty"`
	Title string     `json:"title,omitempty"`
	// Repo is the repository's identity: host/owner/repo.
	Repo     string     `json:"repo,omitempty"`
	Branch   string     `json:"branch,omitempty"`
	Base     string     `json:"base,omitempty"` // the commit the cloud started from, when known
	PR       string     `json:"pr,omitempty"`
	State    CloudState `json:"state"`
	Updated  time.Time  `json:"updated"`
	Attempts int        `json:"attempts,omitempty"`
	// Mirror: a local session mirrored to the vendor (Remote Control), not a cloud run.
	Mirror bool `json:"mirror,omitempty"`
	// Local is the session a Mirror mirrors, on the machine running hopsesh.
	Local SessionID `json:"local,omitempty"`
	// Account is the module's fingerprint of the account that owns the session (Account.Key's
	// form), when the module knows it.
	Account string `json:"account,omitempty"`
	// Env and EnvLabel are the vendor's environment the session runs in (its id, and its
	// name for people), for clouds that need one.
	Env      string `json:"env,omitempty"`
	EnvLabel string `json:"envLabel,omitempty"`
	// Changes sums up the code the session changed, as the vendor reports it
	// ("+12 −3 · 2 files").
	Changes string `json:"changes,omitempty"`
}

// SessionNoun is what a cloud calls its sessions ("session" unless it says).
func (c Cloud) SessionNoun() string {
	if c.Noun == "" {
		return "session"
	}
	return c.Noun
}

// CloudLink is the vendor's cloud copy of a session that runs on a machine (Claude Code's
// Remote Control keeps one on claude.ai while it is connected).
type CloudLink struct {
	Cloud string    `json:"cloud"`
	ID    SessionID `json:"id"`
	URL   string    `json:"url,omitempty"`
	// Account is the module's fingerprint of the account that owns it, when recorded.
	Account string `json:"account,omitempty"`
}

// Cloud capabilities. Each is carried out by running the cloud's Driver through
// Host.Exec on the machine running hopsesh.
const (
	CapCloudList    Capability = "cloud-list"    // CloudLister
	CapCloudSend    Capability = "cloud-send"    // CloudSender
	CapCloudFetch   Capability = "cloud-fetch"   // CloudFetcher
	CapCloudFollow  Capability = "cloud-follow"  // CloudFollower
	CapCloudArchive Capability = "cloud-archive" // CloudArchiver
)

// CloudLister lists a cloud's sessions (codex cloud list --json, gh agent-task list).
type CloudLister interface {
	ListCloud(ctx context.Context, h Host, in Install, q CloudQuery) (CloudListing, error)
}

// CloudQuery narrows a cloud listing.
type CloudQuery struct {
	Cloud string
	Repo  string // only this repository's sessions ("" for all)
	// Known are ids hopsesh recorded for this cloud (lineage, marks): refresh them even
	// when the vendor's own listing leaves them out.
	Known []SessionID
	// Local are the module's sessions on this machine from the same scan, so a module can
	// find mirrors among them (Summary.Mirror) without listing again; nil: it lists them
	// itself when it needs them.
	Local []Summary
	Limit int
	// Env lists only the sessions of one of the vendor's environments ("" for all).
	Env string
}

// CloudListing is the result of ListCloud. One unreadable session goes in Errors.
type CloudListing struct {
	Sessions []CloudSession
	// Partial: the vendor cannot list everything (Claude Code has no list command), so
	// these are the sessions the module could find; the vendor's own picker has the rest.
	Partial bool
	Errors  []SessionError
}

// CloudSender starts a cloud session from a briefing and code the core prepared
// (codex cloud exec, gh agent-task create), or says what the user runs to start it where
// the driver needs a terminal (claude --cloud "<briefing>").
type CloudSender interface {
	SendCloud(ctx context.Context, h Host, in Install, r SendRequest) (Sent, error)
}

// Sent is what SendCloud did: the session it started, or, when the driver starts one only
// in a terminal, the command for it.
type Sent struct {
	// Session is the session started (empty when Run is set).
	Session CloudSession
	// Run is a command for the user's terminal (claude --cloud "<briefing>" in
	// SendRequest.Dir). The core runs it where the user sees it and answers what it asks
	// (Claude Code's question whether the folder is trusted); hopsesh never types into it.
	// It watches only what the command prints and its exit code, and the module reads the
	// session from them (CloudStepReader, which it then implements).
	Run *Command
}

// StepOutput is what a terminal step (Sent.Run) printed, as hopsesh saw it go by: plain
// text with the terminal's control sequences taken out, at most its last 64 KB; the
// terminal's width (a line that wide may go on in the next one, where the program broke
// it); and the exit code (-1 when it is unknown).
type StepOutput struct {
	Text  string
	Width int
	Code  int
}

// CloudStepReader reads the session a terminal step started (a SendCloud's Sent.Run) from
// what it printed, or maps its refusal onto the cloud sentinels. A step that printed no
// session and no refusal returns an error that says what was seen.
type CloudStepReader interface {
	ReadStep(cloud string, out StepOutput) (CloudSession, error)
}

// SendRequest is a cloud session to start.
type SendRequest struct {
	Cloud string
	// Dir is a worktree the core made, on Branch (Claude Code clones "your current
	// branch").
	Dir    string
	Repo   string // the repository's identity
	Branch string // already pushed by the core
	Base   string
	// Brief is the first prompt, rendered and redacted by the core; it begins with
	// NotePrefix.
	Brief string
	Diff  []byte // only with ViaStartingDiff
	Env   string // the vendor's environment id, when the cloud has NeedEnvironment
	// Attempts asks for that many attempts at once (best-of-N), where the cloud runs them; 0
	// is the cloud's default.
	Attempts int
	Title    string
	// Code is how the code goes up: ViaBranch (Branch is pushed; the default) or ViaBundle
	// (the driver uploads the repository from Dir itself, and nothing is pushed).
	Code CodeWay
}

// CloudFetcher brings a cloud session to this machine (Claude Code's teleport, codex
// cloud apply, jules remote pull).
type CloudFetcher interface {
	FetchCloud(ctx context.Context, h Host, in Install, id SessionID, t FetchTarget) (Fetched, error)
}

// FetchTarget is where a fetched session lands.
type FetchTarget struct {
	// Dir is a worktree the core made on the cloud's branch, so the user's own checkout
	// stays as it is.
	Dir  string
	Code CodeWay
}

// Fetched is what a fetch brought, or what the user must run to bring it.
type Fetched struct {
	Code CodeResult
	// Run is a command the user runs in a terminal when the driver needs one
	// (claude --teleport <id>).
	Run *Command
	// Adopt is where the driver writes a native session, so the core can journal it.
	Adopt *Adopt
	// Segment is the conversation in the IR when no native copy exists (text-only clouds).
	Segment *ir.Segment
	// Expected is how many messages the vendor says it restored, when it says, so the core
	// can tell a partial copy (Claude Code 2.1.289's teleport says nothing).
	Expected int
	// Loss names what stays in the cloud: "tool calls stay in the cloud".
	Loss []string
	// Note is what the user must do in Run's terminal for the copy to be saved ("Claude
	// Code saves its copy only after you send a message in it"), shown with the command.
	Note string
}

// CodeResult is the code a fetch brought.
type CodeResult struct {
	Way    CodeWay `json:"way,omitempty"`
	Branch string  `json:"branch,omitempty"` // the cloud's branch (ViaBranch, ViaPR)
	PR     string  `json:"pr,omitempty"`
	// Diff is a unified diff the core applies and commits on a hopsesh branch (ViaDiff),
	// unless the driver applied it in FetchTarget.Dir itself (Applied).
	Diff    []byte `json:"-"`
	Applied bool   `json:"applied,omitempty"`
}

// Adopt says where a driver writes a session file: under the install's root Root, in
// folder Dir (root-relative, slash-separated), a file created after Since that names
// Session, or holds the vendor's own sign that it is a copy of it.
type Adopt struct {
	Root    string
	Dir     string
	Since   time.Time
	Session SessionID
}

// CloudAdopter finds the session a driver wrote for a fetch once it is there
// (Fetched.Adopt), so the core can journal it and tell a complete copy from a partial one. A
// module whose FetchCloud returns an Adopt implements it.
type CloudAdopter interface {
	// Adopted returns the session, or ErrNotFound while the driver has not written it.
	Adopted(ctx context.Context, h Host, in Install, a Adopt) (Adoption, error)
}

// Adoption is a session a driver wrote for a fetch.
type Adoption struct {
	Session Summary
	// Remote is the cloud session it copies (the vendor's own picker chose it when the Adopt
	// named none).
	Remote SessionID
	// Restored is how many messages the copy holds; Expected, how many the vendor said it
	// restored, when it said (Stated).
	Restored int
	Expected int
	Stated   bool
	// Replies is how many of the restored messages are the agent's; First is the copy's
	// first user message, as it reads, so the core can check the copy begins where the
	// session began (the briefing hopsesh sent) when the vendor states no count.
	Replies int
	First   string
	// Title names the cloud conversation ("" : the copy's own title stands).
	Title string
}

// CloudLinker reads a cloud session's link or id as the vendor shows it ("Paste a link"),
// and makes its page's link.
type CloudLinker interface {
	ParseCloudLink(s string) (cloud string, id SessionID, ok bool)
	CloudURL(cloud string, id SessionID) string
}

// CloudTester probes a cloud through its driver without changing anything or starting a
// model turn: the login it uses, and that the driver still has what the module relies on.
// A login the cloud cannot use returns ErrSignedOut or ErrNotEligible, with the checks so
// far.
type CloudTester interface {
	TestCloud(ctx context.Context, h Host, in Install, cloud string) (CloudTest, error)
}

// CloudTest is what a probe found.
type CloudTest struct {
	Account string       `json:"account,omitempty"` // the login, for people: "claude.ai · max"
	Checks  []CloudCheck `json:"checks"`
}

// CloudCheck is one finding of a probe.
type CloudCheck struct {
	OK   bool   `json:"ok"`
	Text string `json:"text"`
}

// CloudFollower sends a follow-up message to a cloud session (fakecloud remote message;
// none of the vendors' CLIs hopsesh drives has a non-interactive one as of 2026-10).
type CloudFollower interface {
	FollowUp(ctx context.Context, h Host, in Install, id SessionID, text string) (CloudSession, error)
}

// CloudArchiver archives a cloud session (Jules, Devin, Cursor; not the Claude Code or
// Codex CLIs).
type CloudArchiver interface {
	Archive(ctx context.Context, h Host, in Install, id SessionID) error
}

// Errors a cloud capability returns, wrapped with detail; like the others, modules never
// invent other top-level kinds.
var (
	ErrSignedOut       = errors.New("the agent is not signed in with the account its cloud needs")
	ErrNotEligible     = errors.New("plan or organization policy does not allow cloud sessions")
	ErrRepoUnsupported = errors.New("the cloud cannot clone or push this repository")
	// ErrNoEnvironment: the cloud has no environment of that name or id for this account,
	// or none at all (for clouds with NeedEnvironment).
	ErrNoEnvironment = errors.New("the cloud has no such environment")
	// ErrNoSession: a terminal step ended without starting a session and without a
	// refusal hopsesh knows (the user left it, or answered no). The user interfaces offer
	// to paste the session's link, in case one started that hopsesh did not see.
	ErrNoSession = errors.New("no cloud session started")
)

// NoLocal is embedded by a cloud-only module (no data folders on any machine) for the
// methods that act on local files: it lists nothing, and the rest are unsupported.
type NoLocal struct{}

// List returns an empty listing.
func (NoLocal) List(context.Context, Host, Install) (Listing, error) { return Listing{}, nil }

// Bundle is unsupported.
func (NoLocal) Bundle(context.Context, Host, Install, Summary) (Bundle, error) {
	return Bundle{}, ErrUnsupported
}

// PlanMove is unsupported.
func (NoLocal) PlanMove(Install, Install, Summary, Bundle, Placement) (MovePlan, error) {
	return MovePlan{}, ErrUnsupported
}

// Verify is unsupported.
func (NoLocal) Verify(context.Context, Host, MovePlan, map[string]string, Placement) error {
	return ErrUnsupported
}

// Resume has no command: a cloud-only module's sessions are resumed in the vendor's cloud.
func (NoLocal) Resume(Install, SessionKey, Placement, ResumeOptions) Command { return Command{} }

// FindCloud returns the Spec's cloud with this name.
func (s Spec) FindCloud(name string) (Cloud, bool) {
	for _, c := range s.Clouds {
		if c.Name == name {
			return c, true
		}
	}
	return Cloud{}, false
}

// TestedWith reports whether a driver version matches one of the cloud's tested version
// prefixes.
func (c Cloud) TestedWith(version string) bool { return Spec{Tested: c.Tested}.TestedWith(version) }

// CheckClouds checks a module's clouds the way the registry and the conformance kit do:
// every cloud is named for a mark title, driven by one of the Spec's binaries and
// declares what it carries each way; a module that implements a cloud capability declares
// at least one cloud.
func CheckClouds(m Module) error {
	s := m.Spec()
	bins := map[string]bool{}
	for _, b := range s.Binaries {
		bins[b.Name] = true
	}
	seen := map[string]bool{}
	for _, c := range s.Clouds {
		switch {
		case !CloudNameSyntax.MatchString(c.Name):
			return fmt.Errorf("cloud name %q must match %s", c.Name, CloudNameSyntax)
		case seen[c.Name]:
			return fmt.Errorf("two clouds are named %s", c.Name)
		case c.Title == "":
			return fmt.Errorf("cloud %s needs a title", c.Name)
		case !bins[c.Driver]:
			return fmt.Errorf("cloud %s is driven by %q, which is not one of the Spec's binaries", c.Name, c.Driver)
		case !validFidelity(c.Up) || !validFidelity(c.Down):
			return fmt.Errorf("cloud %s: fidelity up %q, down %q", c.Name, c.Up, c.Down)
		case len(c.Tested) == 0:
			return fmt.Errorf("cloud %s lists no driver versions it was tested with", c.Name)
		}
		seen[c.Name] = true
		for _, w := range append(append([]CodeWay(nil), c.CodeUp...), c.CodeDown...) {
			if !validCodeWay(w) {
				return fmt.Errorf("cloud %s: code way %q", c.Name, w)
			}
		}
		for _, n := range c.Needs {
			if !validNeed(n) {
				return fmt.Errorf("cloud %s: need %q", c.Name, n)
			}
		}
	}
	if len(s.Clouds) == 0 {
		for _, c := range Capabilities(m) {
			if IsCloudCapability(c) {
				return fmt.Errorf("the module implements %s but declares no cloud in Spec.Clouds", c)
			}
		}
	}
	return nil
}

// IsCloudCapability reports whether c is one of the cloud capabilities.
func IsCloudCapability(c Capability) bool {
	switch c {
	case CapCloudList, CapCloudSend, CapCloudFetch, CapCloudFollow, CapCloudArchive:
		return true
	}
	return false
}

func validFidelity(f Fidelity) bool {
	switch f {
	case FidNative, FidText, FidCode, FidBrief, FidNone:
		return true
	}
	return false
}

func validCodeWay(w CodeWay) bool {
	switch w {
	case ViaBranch, ViaBundle, ViaStartingDiff, ViaPR, ViaDiff:
		return true
	}
	return false
}

func validNeed(n Need) bool {
	switch n {
	case NeedSubscriptionLogin, NeedGitHub, NeedPushedBranch, NeedCleanTree, NeedSameAccount, NeedEnvironment, NeedTerminal:
		return true
	}
	return false
}
