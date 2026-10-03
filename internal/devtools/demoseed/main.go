//go:build !windows

// Command demoseed fills a demo machine with made-up git repositories and Claude Code
// sessions, for the recordings in demo/. Every name, path and prompt is invented.
//
//	demoseed -role studio   # the machine the sessions live on
//	demoseed -role laptop   # the machine they are moved to
//
// Git remotes point at github.com/acme/*; a global insteadOf maps them to local bare
// repositories, so cloning works without a network. The demo machines are Linux
// containers, so this tool builds only on Unix-like systems.
package main

import (
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
)

const version = "2.1.284"

var (
	home    string
	bareDir = envOr("DEMO_BARE", "/srv/git/acme") // shared by both demo machines in demo/
	now     = time.Now().UTC()
)

func main() {
	role := flag.String("role", "studio", "studio or laptop")
	flag.Parse()
	home, _ = os.UserHomeDir()
	must(os.MkdirAll(bareDir, 0o755))
	git("", "config", "--global", "user.name", "Alice Example")
	git("", "config", "--global", "user.email", "alice@example.com")
	git("", "config", "--global", "init.defaultBranch", "main")
	git("", "config", "--global", "url."+bareDir+"/.insteadOf", "https://github.com/acme/")
	git("", "config", "--global", "--add", "url."+bareDir+"/.insteadOf", "git@github.com:acme/")
	for _, r := range []string{"webapp", "api", "infra"} {
		bare(r)
	}
	switch *role {
	case "studio":
		studio()
	case "laptop":
		laptop()
	default:
		fail(fmt.Errorf("unknown role %q", *role))
	}
}

func studio() {
	web := clone("webapp", "git@github.com:acme/webapp.git", "git")
	wt := filepath.Join(web, ".claude", "worktrees", "checkout-flow")
	git(web, "worktree", "add", "-q", "-b", "feat/checkout-flow", wt)
	write(filepath.Join(wt, "src", "checkout", "applepay.ts"), "export const applePay = () => {};\n")
	git(wt, "add", "-A")
	git(wt, "commit", "-q", "-m", "checkout: Apple Pay button")

	api := clone("api", "https://github.com/acme/api.git", "git")
	write(filepath.Join(api, "internal", "ratelimit", "bucket.go"), "package ratelimit\n")
	git(api, "add", "-A")
	git(api, "commit", "-q", "-m", "ratelimit: token bucket")
	write(filepath.Join(api, "internal", "ratelimit", "bucket_test.go"), "package ratelimit\n")
	git(api, "add", "-A")
	git(api, "commit", "-q", "-m", "ratelimit: tests")
	write(filepath.Join(api, "internal", "ratelimit", "middleware.go"), "package ratelimit\n")

	infra := clone("infra", "https://github.com/acme/infra.git", "git")
	notes := filepath.Join(home, "notes")
	must(os.MkdirAll(notes, 0o755))

	session(web, "main", "Fix flaky checkout tests", 2*time.Hour, "idle",
		"the checkout e2e tests fail about one run in ten, find out why",
		"rerun the e2e suite with --repeat 20", "src/checkout/cart.spec.ts")
	session(wt, "feat/checkout-flow", "Checkout flow: Apple Pay button", 25*time.Minute, "busy",
		"add an Apple Pay button to the checkout page",
		"wire the Apple Pay button to the new payments endpoint", "src/checkout/applepay.ts")
	session(api, "main", "Rate limiter for the public API", 26*time.Hour, "",
		"we need per-key rate limiting on the public API",
		"add a token bucket per API key, with tests", "internal/ratelimit/bucket.go")
	session(infra, "main", "Terraform: move staging to arm64", 3*time.Hour, "",
		"plan moving the staging cluster to arm64 nodes",
		"plan the change for the staging cluster only", "staging/nodes.tf")
	session(notes, "", "Draft release notes for v2.3", 4*24*time.Hour, "",
		"draft release notes for v2.3 from the merged PRs",
		"make the tone less formal", "v2.3.md")
}

func laptop() {
	web := clone("webapp", "git@github.com:acme/webapp.git", "src")
	api := clone("api", "https://github.com/acme/api.git", "src")
	session(web, "main", "Dark mode color tokens", 5*time.Hour, "",
		"add dark mode tokens to the design system",
		"use the new tokens in the header too", "src/theme/tokens.ts")
	session(api, "main", "Paginate /v2/orders", 2*24*time.Hour, "",
		"add cursor pagination to /v2/orders",
		"document the cursor format in the OpenAPI spec", "api/openapi.yaml")
}

// bare creates the "remote" repository behind github.com/acme/<name>.
func bare(name string) {
	dir := filepath.Join(bareDir, name+".git")
	if _, err := os.Stat(dir); err == nil {
		return
	}
	work := filepath.Join(os.TempDir(), "seed-"+name)
	must(os.RemoveAll(work))
	must(os.MkdirAll(work, 0o755))
	git(work, "init", "-q")
	write(filepath.Join(work, "README.md"), "# "+name+"\n\nPart of the made-up acme organisation used in the hopsesh demo.\n")
	write(filepath.Join(work, ".gitignore"), ".claude/worktrees/\n")
	git(work, "add", "-A")
	git(work, "commit", "-q", "-m", "initial commit")
	git("", "clone", "-q", "--bare", work, dir)
	must(os.RemoveAll(work))
}

func clone(name, url, parent string) string {
	dir := filepath.Join(home, parent, name)
	git("", "clone", "-q", url, dir)
	return dir
}

// session writes one made-up transcript; status "busy" or "idle" also registers it as
// running, backed by a sleeping process so `kill -0` finds it.
func session(cwd, branch, title string, age time.Duration, status, first, last, file string) {
	id := uuid()
	cfg := filepath.Join(home, ".claude")
	dir := filepath.Join(cfg, "projects", claude.Slug(cwd))
	must(os.MkdirAll(dir, 0o755))
	t0 := now.Add(-age - 40*time.Minute)
	ts := func(d time.Duration) string { return t0.Add(d).Format(time.RFC3339Nano) }
	full := filepath.Join(cwd, file)
	var recs []map[string]any
	add := func(r map[string]any) {
		r["sessionId"], r["cwd"], r["version"], r["entrypoint"] = id, cwd, version, "cli"
		if branch != "" {
			r["gitBranch"] = branch
		}
		recs = append(recs, r)
	}
	u1, a1, u2, a2, u3 := uuid(), uuid(), uuid(), uuid(), uuid()
	add(map[string]any{"type": "user", "uuid": u1, "parentUuid": nil, "timestamp": ts(0),
		"message": map[string]any{"role": "user", "content": first}})
	add(map[string]any{"type": "assistant", "uuid": a1, "parentUuid": u1, "timestamp": ts(time.Minute),
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "I'll start by reading " + file + "."},
			map[string]any{"type": "tool_use", "id": "toolu_" + id[:8], "name": "Read", "input": map[string]any{"file_path": full}},
		}}})
	add(map[string]any{"type": "user", "uuid": u2, "parentUuid": a1, "timestamp": ts(2 * time.Minute),
		"toolUseResult": map[string]any{"filePath": full},
		"message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_" + id[:8], "content": "(file contents)"},
		}}})
	parent := u2
	for i, st := range richSteps(cwd)[title] {
		a, u, tid := uuid(), uuid(), fmt.Sprintf("toolu_%s%02d", id[:6], i)
		var content []any
		if st.thinking != "" {
			content = append(content, map[string]any{"type": "thinking", "thinking": st.thinking, "signature": "demo-signature-" + id[:8]})
		}
		content = append(content, map[string]any{"type": "text", "text": st.text},
			map[string]any{"type": "tool_use", "id": tid, "name": st.tool, "input": st.input})
		at := time.Duration(3+i*5) * time.Minute
		add(map[string]any{"type": "assistant", "uuid": a, "parentUuid": parent, "timestamp": ts(at),
			"message": map[string]any{"role": "assistant", "content": content}})
		add(map[string]any{"type": "user", "uuid": u, "parentUuid": a, "timestamp": ts(at + time.Minute),
			"toolUseResult": map[string]any{"stdout": st.result},
			"message": map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": tid, "content": st.result},
			}}})
		parent = u
	}
	add(map[string]any{"type": "assistant", "uuid": a2, "parentUuid": parent, "timestamp": ts(30 * time.Minute),
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "Done. I changed " + full + " and ran the tests in " + cwd + "."},
		}}})
	add(map[string]any{"type": "user", "uuid": u3, "parentUuid": a2, "timestamp": ts(40 * time.Minute),
		"message": map[string]any{"role": "user", "content": last}})
	recs = append(recs, map[string]any{"type": "ai-title", "aiTitle": title, "sessionId": id},
		map[string]any{"type": "last-prompt", "lastPrompt": last, "sessionId": id})
	f, err := os.Create(filepath.Join(dir, id+".jsonl"))
	must(err)
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, r := range recs {
		must(enc.Encode(r))
	}
	must(f.Close())
	end := now.Add(-age)
	must(os.Chtimes(filepath.Join(dir, id+".jsonl"), end, end))
	if status == "" {
		return
	}
	sleep := exec.Command("sleep", "infinity")
	sleep.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	must(sleep.Start())
	live := filepath.Join(cfg, "sessions")
	must(os.MkdirAll(live, 0o755))
	b, _ := json.Marshal(map[string]any{"pid": sleep.Process.Pid, "sessionId": id, "cwd": cwd, "status": status,
		"name": title, "version": version, "entrypoint": "cli", "kind": "interactive", "startedAt": t0.UnixMilli()})
	must(os.WriteFile(filepath.Join(live, fmt.Sprintf("%d.json", sleep.Process.Pid)), b, 0o644))
}

func write(path, body string) {
	must(os.MkdirAll(filepath.Dir(path), 0o755))
	must(os.WriteFile(path, []byte(body), 0o644))
}

func git(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		fail(fmt.Errorf("git %v: %v\n%s", args, err, out))
	}
}

func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// step is one tool round in a longer made-up session: optional reasoning, a line of text,
// one tool call and its result.
type step struct {
	thinking, text, tool string
	input                map[string]any
	result               string
}

// richSteps make the hero session long enough that the loss report has something to say
// (reasoning blocks are left out when a session continues in another vendor's agent).
func richSteps(cwd string) map[string][]step {
	spec := filepath.Join(cwd, "src", "checkout", "cart.spec.ts")
	return map[string][]step{"Fix flaky checkout tests": {
		{thinking: "One failure in ten points at timing, not logic. Run the suite repeatedly to see which test flakes.",
			text: "Let me reproduce it first.", tool: "Bash",
			input:  map[string]any{"command": "npx playwright test src/checkout --repeat-each 20", "description": "Run the checkout tests 20 times"},
			result: "38 passed, 2 failed\n  checkout › applies a coupon: timed out waiting for text \"Total: $90.00\""},
		{text: "Only the coupon test fails. Reading it.", tool: "Read", input: map[string]any{"file_path": spec},
			result: "await page.click('#apply-coupon');\nawait expect(page.locator('.total')).toHaveText('Total: $90.00');"},
		{thinking: "The click fires before the coupon request resolves, and the assertion reads the old total when the response is slow.",
			text: "The test asserts before the coupon request finishes. I'll wait for that response.", tool: "Edit",
			input: map[string]any{"file_path": spec, "old_string": "await page.click('#apply-coupon');",
				"new_string": "const applied = page.waitForResponse('**/api/coupons/apply');\nawait page.click('#apply-coupon');\nawait applied;"},
			result: "The file " + spec + " has been updated."},
		{text: "Running it 20 more times.", tool: "Bash",
			input:  map[string]any{"command": "npx playwright test src/checkout --repeat-each 20"},
			result: "40 passed"},
		{thinking: "Stable now. Check the diff is limited to the test.",
			text: "Stable. Checking the change is limited to the test.", tool: "Bash", input: map[string]any{"command": "git diff --stat"},
			result: " src/checkout/cart.spec.ts | 4 +++-\n 1 file changed, 3 insertions(+), 1 deletion(-)"},
	}}
}

func must(err error) {
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "demoseed:", err)
	os.Exit(1)
}
