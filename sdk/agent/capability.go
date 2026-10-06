package agent

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Capability names something a module can do beyond the required interface.
type Capability string

const (
	CapLive          Capability = "live"           // LiveDetector
	CapStop          Capability = "stop"           // Stopper
	CapMark          Capability = "mark"           // Marker: the agent's own "moved" mark
	CapAccount       Capability = "account"        // AccountProber
	CapSanitize      Capability = "sanitize"       // Sanitizer: same-agent move to another account
	CapPostInstall   Capability = "post-install"   // PostInstaller
	CapRead          Capability = "read"           // Reader: a conversion source
	CapWrite         Capability = "write"          // Writer: a conversion target
	CapNativeReplay  Capability = "native-replay"  // Writer renders tool calls natively (Profile.NativeReplay)
	CapIntegrate     Capability = "integrate"      // Integrator: skill and approval rules
	CapFork          Capability = "fork"           // Resume honours ResumeOptions.Fork (Spec.Features)
	CapRemoteControl Capability = "remote-control" // Resume honours ResumeOptions.RemoteControl (Spec.Features)
	CapApp           Capability = "app"            // Resume honours ResumeOptions.App (Spec.Features)
	CapNotify        Capability = "notify"         // Notifier: the resumed session tells the old one
	CapImport        Capability = "import"         // Importer: the agent converts another agent's sessions itself
	CapPreview       Capability = "preview"        // Previewer: the end of a conversation, for the app
	CapRename        Capability = "rename"         // Renamer: a new title in the agent's own data
)

// LiveDetector reports which sessions are open right now.
type LiveDetector interface {
	Live(ctx context.Context, h Host, in Install, ids []SessionID) (map[SessionID]LiveInfo, error)
}

// Stopper asks an open session to end, waiting up to grace.
type Stopper interface {
	Stop(ctx context.Context, h Host, in Install, s Summary, grace time.Duration) error
}

// Marker records, in the agent's own data, that a copy was left behind ("↪ moved to …"),
// so the agent's own session list shows it. The core's lineage manifest records it in
// any case.
type Marker interface {
	Mark(ctx context.Context, h Host, in Install, s Summary, m Mark) error
}

// AccountProber identifies the login the agent uses on a machine, without reading
// credentials.
type AccountProber interface {
	Account(ctx context.Context, h Host, in Install) (Account, error)
}

// Sanitizer is the extra policy for a same-agent move to a machine signed in to another
// account (content bound to the source account is removed).
type Sanitizer interface {
	Sanitize() RewritePolicy
}

// PostInstaller runs after a session was installed (for example to make the agent index
// it).
type PostInstaller interface {
	AfterInstall(ctx context.Context, h Host, in Install, key SessionKey, p Placement) error
}

// Reader lifts a session into the conversation IR, from a cursor (incremental reads for
// round trips).
type Reader interface {
	Read(ctx context.Context, h Host, in Install, s Summary, from ir.Cursor) (ir.Segment, error)
}

// Importer is the agent's own importer of another agent's sessions: it converts a native
// session file itself, an alternative to the core's conversion through Writer.
type Importer interface {
	CanImport(from ID) bool
	// Import converts the session file at path (on h's machine) into a new session of this
	// agent and returns its id. If validation fails after creation, return both its
	// id and an error so the core can adopt the file for undo without launching it.
	Import(ctx context.Context, h Host, in Install, from ID, path, cwd, title string) (SessionID, error)
}

// Writer writes IR into the agent's native format: a new session, or an append to one it
// can extend.
type Writer interface {
	Profile(in Install) ir.Profile
	Write(ctx context.Context, h Host, in Install, req ir.WriteRequest) (ir.WriteResult, error)
}

// Notifier lets a resumed session tell the copy left behind that the work moved on. The
// text is added to the new session's first message; the agent itself delivers it.
type Notifier interface {
	NotifyInstruction(oldName, oldLocation, newName string, fork bool) string
}

// Integrator says where the shared hopsesh skill goes for this agent, and how the agent is
// told which hopsesh commands may run without asking.
type Integrator interface {
	Integration(h Host, in Install) Integration
}

// Integration is an agent's integration points.
type Integration struct {
	SkillDir string    // folder that holds skills ("~/.claude/skills"); the skill goes in SkillDir/hopsesh
	Rules    *RuleFile // nil when the agent has no rules the core can manage
}

// Rules are hopsesh command prefixes (after the binary) by approval.
type Rules struct {
	Bin   string     // the command as the agent runs it ("hopsesh")
	Allow [][]string // run without asking: {"ls"}, {"show"}
	Ask   [][]string // ask first: {"pull"}, {"undo"}
}

// RuleFile is where an agent keeps approval rules.
type RuleFile struct {
	Path string
	// Owned: hopsesh writes the whole file (Render). Otherwise it is the agent's shared
	// settings file, edited with Merge and checked with Has.
	Owned  bool
	Render func(r Rules) []byte
	Merge  func(existing []byte, r Rules) ([]byte, error)
	Has    func(existing []byte, r Rules) bool
	Remove func(existing []byte, r Rules) ([]byte, error)
}

// Capabilities lists what a module can do, from the interfaces it implements and the
// features its Spec declares.
func Capabilities(m Module) []Capability {
	var out []Capability
	add := func(ok bool, c Capability) {
		if ok {
			out = append(out, c)
		}
	}
	_, live := m.(LiveDetector)
	_, stop := m.(Stopper)
	_, mark := m.(Marker)
	_, acct := m.(AccountProber)
	_, san := m.(Sanitizer)
	_, post := m.(PostInstaller)
	_, read := m.(Reader)
	w, write := m.(Writer)
	_, integ := m.(Integrator)
	_, notify := m.(Notifier)
	_, imp := m.(Importer)
	_, preview := m.(Previewer)
	_, rename := m.(Renamer)
	_, clist := m.(CloudLister)
	_, csend := m.(CloudSender)
	_, cfetch := m.(CloudFetcher)
	_, cfollow := m.(CloudFollower)
	_, carchive := m.(CloudArchiver)
	add(live, CapLive)
	add(stop, CapStop)
	add(mark, CapMark)
	add(acct, CapAccount)
	add(san, CapSanitize)
	add(post, CapPostInstall)
	add(read, CapRead)
	add(write, CapWrite)
	add(write && w.Profile(Install{}).NativeReplay, CapNativeReplay)
	add(integ, CapIntegrate)
	add(notify, CapNotify)
	add(imp, CapImport)
	add(preview, CapPreview)
	add(rename, CapRename)
	add(clist, CapCloudList)
	add(csend, CapCloudSend)
	add(cfetch, CapCloudFetch)
	add(cfollow, CapCloudFollow)
	add(carchive, CapCloudArchive)
	out = append(out, m.Spec().Features...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Has reports whether a module has a capability.
func Has(m Module, c Capability) bool {
	for _, x := range Capabilities(m) {
		if x == c {
			return true
		}
	}
	return false
}

// IsExperimental reports whether a module marks a capability experimental.
func IsExperimental(m Module, c Capability) bool {
	for _, x := range m.Spec().Experimental {
		if x == c {
			return true
		}
	}
	return false
}

// DefaultInstall resolves a Spec's roots and first binary from a machine's facts.
func DefaultInstall(s Spec, h Host) Install {
	f := h.Facts()
	in := Install{Accounts: s.Accounts, Agent: s.ID, Roots: map[string]string{}}
	for _, r := range s.Roots {
		p := ""
		for _, e := range r.Env {
			if v := f.Env[e]; v != "" {
				p = v
				break
			}
		}
		if p == "" {
			p = r.Default[f.OS]
			if p == "" {
				p = r.Default["*"]
			}
		}
		in.Roots[r.Name] = Expand(p, f.Home, in.Roots, h.Path())
	}
	if len(s.Binaries) > 0 {
		if b, ok := f.Binaries[s.Binaries[0].Name]; ok {
			in.Binary, in.Version = b.Path, VersionOf(b.Version)
		}
	}
	if len(s.Roots) > 0 {
		if fi, err := h.FS().Stat(in.Roots[s.Roots[0].Name]); err == nil && fi.IsDir() {
			in.Present = true
		}
	}
	return in
}

// Expand replaces a leading "~" with home and "{name}" with resolved roots.
func Expand(p, home string, roots map[string]string, pa Path) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		p = pa.Join(home, p[2:])
	}
	for n, v := range roots {
		p = strings.ReplaceAll(p, "{"+n+"}", v)
	}
	return pa.Clean(p)
}

// VersionOf extracts a dotted version ("2.1.284") from a --version line.
func VersionOf(line string) string {
	for _, f := range strings.Fields(line) {
		f = strings.TrimPrefix(f, "v")
		if len(f) > 0 && f[0] >= '0' && f[0] <= '9' && strings.Contains(f, ".") {
			return strings.TrimRight(f, ",;)")
		}
	}
	return ""
}

// TestedWith reports whether a version matches one of the Spec's tested version prefixes.
func (s Spec) TestedWith(version string) bool {
	for _, t := range s.Tested {
		if version == t || strings.HasPrefix(version, t+".") {
			return true
		}
	}
	return false
}

// AppChecker validates an installation-specific desktop resume before it is offered or run.
type AppChecker interface {
	CheckApp(Install, SessionKey, ResumeOptions) error
}
