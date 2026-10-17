package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/internal/localstate"
	"github.com/roeehrl/hopsesh/internal/testkit/runtimecases"
)

// Actual connector/owner processes and SQLite/R2, with disposable provider
// records. No hosted provider startup, pause or rebuild is implied by this lane.
func runRuntimeCloudRow(t *testing.T, bin string, row runtimecases.Row) {
	t.Helper()
	ctx, _, origin, cert, client := startSQLiteRelayFixture(t, 2*time.Minute)
	f := newRelayFleet(t, ctx, bin, origin, cert, client)
	root := t.TempDir()
	cloud := newMachineHome(t, root, "cloud-matrix", true)
	cloud.writeConfig(t, config.Defaults())
	session := sid
	transcript := filepath.Join(cloud.home, ".claude", "projects", claude.Slug(cloud.repo), sid+".jsonl")
	nativeRoot := filepath.Join(cloud.home, ".claude", "projects")
	if row.Provider == "codex-legacy" {
		loc := newLocation(t, cloud.name, root)
		install := seedCodex(t, loc)
		sessions := listAgent(t, loc, codex.New(), install)
		if len(sessions) == 0 {
			t.Fatal("missing native Codex fixture")
		}
		session, transcript = string(sessions[0].Key.Session), sessions[0].Path
		nativeRoot = filepath.Join(cloud.home, ".codex")
	}
	// Module listing may retain /var while the cloud CLI requires the actual
	// canonical root (/private/var on macOS). Keep the same physical file.
	transcript, err := filepath.EvalSymlinks(transcript)
	if err != nil {
		t.Fatal(err)
	}
	const privateText = "CLOUD-MATRIX-PRIVATE-CONVERSATION-CONTENT"
	if row.Provider == "codex-legacy" {
		appendCodexTurn(t, transcript, privateText, "private agent response")
	} else {
		appendTurn(t, transcript, privateText)
	}
	native, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	protected := map[string][]byte{transcript: bytes.Clone(native)}
	t.Cleanup(func() {
		for path, before := range protected {
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Error("cloud matrix modified provider transcript", err)
			}
		}
	})
	run := func(input []byte, succeeds bool, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env, cmd.Stdin = cloud.env(), bytes.NewReader(input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if (err == nil) != succeeds {
			t.Fatalf("cloud matrix %s expected success=%t: %v %s", args[1], succeeds, err, stderr.String())
		}
		if bytes.Contains(out, []byte(`"token"`)) || bytes.Contains(out, []byte(`"ticket"`)) {
			t.Fatal("cloud command exposed a routing credential")
		}
		return out
	}
	prepare := func(id string) cloudintegration.Incarnation {
		t.Helper()
		args := []string{"cloud-integration", "prepare", "--provider", row.Provider, "--session", id, "--workspace", cloud.repo}
		if row.Provider == "claude-hosted" || row.Provider == "codex-legacy" {
			path := transcript
			if id != session {
				path = strings.Replace(transcript, session, id, 1)
				protected[path] = bytes.ReplaceAll(native, []byte(session), []byte(id))
				if err := os.WriteFile(path, protected[path], 0600); err != nil {
					t.Fatal(err)
				}
			}
			args = append(args, "--native-root", nativeRoot, "--transcript", path)
			if row.Integration == "exportable" {
				args = append(args, "--allow-transcript-export")
			}
		}
		var instance cloudintegration.Incarnation
		if err := json.Unmarshal(run(nil, true, args...), &instance); err != nil {
			t.Fatal(err)
		}
		return instance
	}
	instance := prepare(session)
	var fork cloudintegration.Incarnation
	if row.Topology == "fork" {
		fork = prepare("independent-fork-session")
		if fork.ID == instance.ID || fork.Public.ID == instance.Public.ID {
			t.Fatal("fork reused original incarnation authority")
		}
		if _, err := cloudintegration.Load(ctx, instance.Directory); err != nil {
			t.Fatal("fork superseded original", err)
		}
	}
	if row.Provider == "codex-current" || row.Provider == "work-cloud" {
		run(nil, false, "cloud-integration", "prepare", "--provider", row.Provider, "--session", session, "--workspace", cloud.repo, "--native-root", nativeRoot, "--transcript", transcript, "--allow-transcript-export")
		if _, err := cloudintegration.Load(ctx, instance.Directory); err != nil {
			t.Fatal("rejected unsupported export superseded valid observation scope", err)
		}
	}
	if row.Integration == "disconnected" {
		for _, item := range []cloudintegration.Incarnation{instance, fork} {
			if item.ID == "" {
				continue
			}
			if _, err := (relay.Store{Directory: item.Directory}).Connection(ctx); err == nil {
				t.Fatal("preparation invented routing authority")
			}
			run(nil, false, "cloud-integration", "serve", item.Directory)
		}
		if _, err := (relay.Store{Directory: filepath.Join(cloud.home, "state", "relay")}).Public(); !os.IsNotExist(err) {
			t.Fatal("preparation created a personal device")
		}
		return
	}
	ownerStore := relay.Store{Directory: filepath.Join(f.homes['A'].home, "state", "relay")}
	owner, err := ownerStore.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := ownerStore.Connection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	admissions := relay.AdmissionStore{Directory: filepath.Join(f.homes['A'].home, "state", "cloud-admissions")}
	claim := func(item cloudintegration.Incarnation, resume string) relay.AdmissionRecord {
		t.Helper()
		record, err := admissions.IssueTask(ctx, owner, connection, row.Provider, item.Session, 10*time.Minute, resume)
		if err != nil {
			t.Fatal(err)
		}
		invitation, err := localstate.ReadPrivateFile(record.Path, 8192)
		if err != nil {
			t.Fatal(err)
		}
		run(invitation, true, "cloud-integration", "claim", item.Directory, "--fingerprint", owner.Public.ID, "--ca-file", cert)
		methods := []string{"observe"}
		if row.Integration == "exportable" {
			methods = append(methods, "export")
		}
		if err := ownerStore.Approve(ctx, relay.Grant{Peer: item.Public, Endpoint: item.Public.Endpoint, Kind: "cloud-session", SendMethods: methods, Expires: item.Expires.Unix()}); err != nil {
			t.Fatal(err)
		}
		return record
	}
	record := claim(instance, "")
	var forkRecord relay.AdmissionRecord
	if fork.ID != "" {
		forkRecord = claim(fork, "")
		if forkRecord.TaskID == record.TaskID {
			t.Fatal("independent cloud fork inherited original logical task")
		}
	}
	start := func(item cloudintegration.Incarnation) func() {
		t.Helper()
		cmd := exec.CommandContext(ctx, bin, "cloud-integration", "serve", item.Directory)
		cmd.Env = cloud.env()
		var logs bytes.Buffer
		cmd.Stdout, cmd.Stderr = &logs, &logs
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		stopped := false
		stop := func() {
			if !stopped {
				stopped = true
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}
		t.Cleanup(func() {
			stop()
			if t.Failed() {
				body := logs.Bytes() // Wait has joined both output writers.
				if len(body) > 8192 {
					body = body[len(body)-8192:]
				}
				t.Logf("disposable cloud connector %s: %s", item.ID, body)
			}
		})
		return stop
	}
	call := func(owner byte, item cloudintegration.Incarnation, method string, params any, result any) error {
		t.Helper()
		operation, err := relay.NewOperationID()
		if err != nil {
			t.Fatal(err)
		}
		requestLimit := 20 * time.Second
		if row.Network == "websocket-blocked" {
			// An idle receiver may legitimately wait its full fallback interval.
			// The sender's pending call wakes its own listener, so allow one
			// receiver interval plus transport/processing, not two idle periods.
			requestLimit = relay.MaxHTTPReconcileInterval + 15*time.Second
		}
		bounded, cancel := context.WithTimeout(ctx, requestLimit)
		defer cancel()
		started := time.Now()
		err = runtimeMatrixClient(t, f.homes[owner]).Call(bounded, "relay.call", map[string]any{"peer": item.Public.ID, "operation": operation, "method": method, "params": params}, result)
		t.Logf("cloud request owner=%c method=%s elapsed=%s failed=%t", owner, method, time.Since(started).Round(time.Millisecond), err != nil)
		return err
	}
	observe := func(item cloudintegration.Incarnation, result *cloudintegration.Observation) error {
		t.Helper()
		var raw json.RawMessage
		if err := call('A', item, "observe", nil, &raw); err != nil {
			if len(raw) != 0 {
				t.Fatal("denied observation returned data")
			}
			return err
		}
		if bytes.Contains(raw, []byte(privateText)) {
			t.Fatal("cloud observation exposed conversation content")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		return decoder.Decode(result)
	}
	checkFork := func() {
		t.Helper()
		if fork.ID == "" {
			return
		}
		var sibling cloudintegration.Observation
		if err := observe(fork, &sibling); err != nil || sibling.Task == nil || sibling.Task.ID != forkRecord.TaskID || sibling.Session != fork.Session || sibling.Incarnation != fork.ID {
			f.logHealth(t)
			t.Fatal("original lifecycle changed independent fork scope", err)
		}
	}
	stop := start(instance)
	var initial cloudintegration.Observation
	if err := observe(instance, &initial); err != nil || initial.Incarnation != instance.ID {
		t.Fatal("connector did not become observable before fault injection", err)
	}
	if row.Failure == "connector-restart" {
		stop()
		stop = start(instance)
	}
	if row.Failure == "rebuild" {
		old := instance
		stop()
		instance = prepare(session)
		next := claim(instance, record.TaskID)
		if instance.Public.ID == old.Public.ID || next.TaskID != record.TaskID || next.Generation <= record.Generation {
			t.Fatal("rebuild reused authority or lost logical task generation")
		}
		if _, err := cloudintegration.Load(ctx, old.Directory); err == nil {
			t.Fatal("superseded incarnation retained local authority")
		}
		record = next
		stop = start(instance)
	}
	if row.Network != "unrestricted" {
		stop()
		f.applyNetworkPolicy(t, origin, client, row.Network)
		policy, err := ownerStore.Connection(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range []cloudintegration.Incarnation{instance, fork} {
			if item.ID == "" {
				continue
			}
			store := relay.Store{Directory: item.Directory}
			c, err := store.Connection(ctx)
			if err != nil {
				t.Fatal(err)
			}
			c.URL, c.CAFile = policy.URL, policy.CAFile
			if err := store.SetConnection(ctx, c); err != nil {
				t.Fatal(err)
			}
		}
		start(instance)
	}
	if fork.ID != "" {
		start(fork)
	}
	if row.Failure == "owner-restart" {
		f.restartRuntime(t, 'A')
	}
	if row.Failure == "grant-revoked" {
		if err := ownerStore.Revoke(ctx, instance.Public.ID); err != nil {
			t.Fatal(err)
		}
	}
	if fork.ID != "" {
		if _, err := cloudintegration.Load(ctx, fork.Directory); err != nil {
			t.Fatal("original lifecycle invalidated independent fork incarnation", err)
		}
	}
	wantError := ""
	switch row.Network {
	case "post-blocked":
		wantError = "POST access is required"
	case "untrusted-ca":
		wantError = "check proxy, trust roots and network access"
	}
	if row.Failure == "grant-revoked" {
		wantError = relay.ErrRevoked.Error()
	}
	var observed cloudintegration.Observation
	err = observe(instance, &observed)
	if wantError != "" {
		if err == nil || !strings.Contains(err.Error(), wantError) || strings.Contains(err.Error(), "PRIVATE-PROXY-ERROR") || observed.Incarnation != "" {
			t.Fatal("cloud denial lost its cause or returned scoped data", err)
		}
		if row.Failure == "grant-revoked" && row.Network != "post-blocked" && row.Network != "untrusted-ca" {
			checkFork()
		}
		return
	}
	if err != nil || observed.Check(instance.Public, time.Now()) != nil || observed.Task == nil || observed.Session != session || observed.Incarnation != instance.ID || observed.Task.ID != record.TaskID || observed.Generation != record.Generation || observed.ExportAllowed != (row.Integration == "exportable") {
		t.Fatal("cloud observation lost capability, task or incarnation scope", err, observed)
	}
	if row.Scope == "wrong-owner" {
		other := relay.Store{Directory: filepath.Join(f.homes['B'].home, "state", "relay")}
		if err := other.Approve(ctx, relay.Grant{Peer: instance.Public, Endpoint: instance.Public.Endpoint, Kind: "cloud-session", SendMethods: []string{"observe", "export"}, Expires: instance.Expires.Unix()}); err != nil {
			t.Fatal(err)
		}
		assertCloudWrongOwnerRejected(t, f, instance, other)
		if err := observe(instance, &observed); err != nil {
			t.Fatal("rejected owner blocked the approved owner's next observation", err)
		}
	}
	for _, method := range []string{"preview", "plan", "apply", "undo"} {
		var denied json.RawMessage
		if err := call('A', instance, method, nil, &denied); err == nil || len(denied) != 0 {
			t.Fatal("cloud connector allowed machine method", method, err)
		}
	}
	var widened json.RawMessage
	if err := call('A', instance, "observe", map[string]string{"session": "unapproved-session"}, &widened); err == nil || !strings.Contains(err.Error(), "cannot widen the bound session") || len(widened) != 0 {
		f.logHealth(t)
		f.probeRelay(t, 'A')
		t.Fatal("cloud request widened bound session", err)
	}
	var exported cloudintegration.Export
	err = call('A', instance, "export", nil, &exported)
	if row.Integration == "exportable" {
		if err != nil || exported.Check(instance.Public, time.Now()) != nil || !bytes.Equal(exported.Data, native) || exported.Checkpoint.OmittedTail != 0 {
			t.Fatal("cloud export changed native bytes or omitted checkpoint fidelity", err)
		}
	} else if err == nil || len(exported.Data) != 0 {
		t.Fatal("observation-only connector exported native data", err)
	}
	checkFork()
	t.Logf("provider contract %s exercised through %s; hosted VM qualification remains separate", row.Provider, row.Transport)
}

func assertCloudWrongOwnerRejected(t *testing.T, f *relayFleet, instance cloudintegration.Incarnation, other relay.Store) {
	t.Helper()
	identity, err := other.Identity(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := relay.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
	defer cancel()
	client := runtimeMatrixClient(t, f.homes['B'])
	var result json.RawMessage
	var callErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		callErr = client.Call(ctx, "relay.call", map[string]any{"peer": instance.Public.ID, "operation": operation, "method": "observe"}, &result)
	}()
	defer func() {
		cancel()
		<-done
		if callErr == nil || len(result) != 0 {
			t.Error("wrong owner received cloud observation", callErr)
		} else if t.Failed() {
			// A failed submit cannot qualify rejection by the connector. Preserve
			// its redacted transport cause instead of reporting only our timeout.
			t.Logf("wrong-owner delivery request ended: %v", callErr)
		}
	}()
	for {
		paths, err := filepath.Glob(filepath.Join(instance.Directory, "rejected-*.json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			body, err := localstate.ReadPrivateFile(path, 8192)
			if err != nil {
				t.Fatal(err)
			}
			var receipt struct {
				Envelope relay.Envelope `json:"envelope"`
				Reason   string         `json:"reason"`
				Digest   string         `json:"ciphertextDigest"`
			}
			if err := json.Unmarshal(body, &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.Envelope.Operation == operation {
				if receipt.Envelope.From != identity.Public.ID || receipt.Envelope.To != instance.Public.ID || receipt.Reason != relay.ErrRevoked.Error() || len(receipt.Envelope.Ciphertext) != 0 || len(receipt.Digest) != 64 {
					t.Fatal("wrong-owner rejection lost exact routing/cause or retained ciphertext")
				}
				return
			}
		}
		select {
		case <-done:
			f.logHealth(t)
			f.probeRelay(t, 'B')
			t.Fatal("wrong-owner request ended before durable rejection", callErr)
		case <-ctx.Done():
			f.logHealth(t)
			f.probeRelay(t, 'B')
			t.Fatal("missing durable rejection for actual wrong-owner delivery", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}
