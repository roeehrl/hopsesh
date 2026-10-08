package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

const cloudHandoffLinkLimit = 16 << 20

var savedHandoffID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

type CloudHandoffOption struct {
	Journal  string    `json:"journal"`
	Provider string    `json:"provider"`
	Title    string    `json:"title"`
	Session  string    `json:"session"`
	Family   string    `json:"family"`
	Branch   string    `json:"branch"`
	Time     time.Time `json:"time"`
}

type CloudHandoffLinkPlan struct {
	Task    string             `json:"task"`
	Handoff CloudHandoffOption `json:"handoff"`
	Fork    bool               `json:"fork"`
	Review  string             `json:"review"`
}

type CloudHandoffLinkInfo struct {
	Task    string    `json:"task"`
	Journal string    `json:"journal,omitempty"`
	Family  string    `json:"family,omitempty"`
	Fork    bool      `json:"fork"`
	Time    time.Time `json:"time,omitempty"`
	Problem string    `json:"problem,omitempty"`
}

func (a *App) CloudHandoffLinks() ([]CloudHandoffLinkInfo, error) {
	root := filepath.Join(a.StateDir, "cloud-task-links")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return []CloudHandoffLinkInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries) > 1024 {
		return nil, errors.New("saved task provenance limit reached")
	}
	if err := localstate.PrivateDirectory(root); err != nil {
		return nil, err
	}
	owner, err := (relay.Store{Directory: filepath.Join(a.StateDir, "relay")}).Public()
	if err != nil {
		return nil, err
	}
	out := []CloudHandoffLinkInfo{}
	var totalBytes int64
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".link-") {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			return nil, errors.New("invalid saved task provenance filename")
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("invalid saved task provenance")
		}
		totalBytes += info.Size()
		if totalBytes > 64<<20 {
			return nil, errors.New("saved task provenance byte limit reached")
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		task, err := (relay.AdmissionStore{Directory: filepath.Join(a.StateDir, "cloud-admissions")}).Task(id, owner.ID)
		if err != nil {
			return nil, err
		}
		link, graph, err := a.cloudHandoffLink(task)
		row := CloudHandoffLinkInfo{Task: task.ID}
		if err != nil {
			row.Problem = err.Error()
		} else if link != nil {
			row.Journal = link.Origin.Operation
			row.Family = graph.Family
			row.Fork = link.Origin.Fork
			row.Time = link.Origin.Time
		}
		out = append(out, row)
	}
	return out, nil
}

// This private signed capsule preserves a reviewed native journal receipt. No
// cloud peer can submit its own graph or select a filesystem path as provenance.
type cloudHandoffLink struct {
	Schema    int                    `json:"schema"`
	Task      relay.CloudTask        `json:"task"`
	Origin    move.CheckpointHandoff `json:"origin"`
	Graph     json.RawMessage        `json:"graph"`
	Signature []byte                 `json:"signature"`
}

func (c cloudHandoffLink) signed() []byte {
	c.Signature = nil
	body, _ := json.Marshal(c)
	return append([]byte("hopsesh-cloud-handoff-link-v1\x00"), body...)
}
func (c cloudHandoffLink) review() string {
	c.Origin.Time = time.Time{}
	digest := sha256.Sum256(c.signed())
	return hex.EncodeToString(digest[:])
}
func (a *App) savedCloudHandoff(id string) (CloudHandoffOption, *move.Handoff, *lineage.Manifest, lineage.ReplicaID, error) {
	fail := func(err error) (CloudHandoffOption, *move.Handoff, *lineage.Manifest, lineage.ReplicaID, error) {
		return CloudHandoffOption{}, nil, nil, "", err
	}
	if !savedHandoffID.MatchString(id) {
		return fail(errors.New("invalid saved handoff ID"))
	}
	body, err := localstate.ReadPrivateFile(filepath.Join(a.StateDir, "handoffs", id+".json"), 1<<20)
	if err != nil {
		return fail(err)
	}
	var h move.Handoff
	if json.Unmarshal(body, &h) != nil || h.Journal != id {
		return fail(errors.New("saved handoff identity changed"))
	}
	provider := map[string]string{"claude-cloud": "claude-hosted", "codex-cloud": "codex-legacy"}[h.Cloud]
	if provider == "" {
		return fail(errors.New("this cloud has no supported native checkpoint provenance"))
	}
	body, err = localstate.ReadPrivateFile(filepath.Join(journal.Dir(a.StateDir), id, "journal.json"), cloudHandoffLinkLimit)
	if err != nil {
		return fail(err)
	}
	var j journal.Journal
	if json.Unmarshal(body, &j) != nil || j.ID != id || j.Kind != journal.KindHandoff || j.Undone || !j.Time.Equal(h.Time) {
		return fail(errors.New("saved handoff journal is invalid or undone"))
	}
	created := false
	for _, e := range j.Entries {
		if e.Op == journal.OpCloud && e.Cloud == h.Cloud && e.Key != nil && *e.Key == h.Session {
			created = true
		}
	}
	if !created {
		return fail(errors.New("saved journal does not confirm this cloud session creation"))
	}
	var graph *lineage.Manifest
	var cloud lineage.ReplicaID
	for _, receipt := range j.Receipts {
		if !receipt.Applied || receipt.Machine != h.Machine {
			continue
		}
		m, err := lineage.Parse(receipt.Body)
		if err != nil {
			return fail(err)
		}
		for _, compensation := range m.Compensations {
			if compensation.Operation == id {
				return fail(errors.New("handoff provenance contains an undo"))
			}
		}
		for _, hop := range m.Hops {
			if hop.ID != id || hop.Kind != lineage.HopHandoff {
				continue
			}
			target := m.Replica(hop.To)
			if target.Key != h.Session || target.Location != h.Cloud {
				return fail(errors.New("handoff receipt names a different cloud session"))
			}
			candidate := m.ForBranch(target.Line)
			if graph != nil && !bytes.Equal(graph.Encode(), candidate.Encode()) {
				return fail(errors.New("handoff has ambiguous saved receipts"))
			}
			graph, cloud = candidate, target.ID
		}
	}
	if graph == nil {
		return fail(errors.New("handoff has no applied verified lineage receipt"))
	}
	return CloudHandoffOption{Journal: id, Provider: provider, Title: h.Title, Session: string(h.Session.Session), Family: graph.Family, Branch: graph.Branch, Time: h.Time}, &h, graph, cloud, nil
}

func (a *App) CloudHandoffOptions() ([]CloudHandoffOption, error) {
	entries, err := os.ReadDir(filepath.Join(a.StateDir, "handoffs"))
	if os.IsNotExist(err) {
		return []CloudHandoffOption{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries) > 10000 {
		return nil, errors.New("saved handoff listing exceeds bound")
	}
	out := []CloudHandoffOption{}
	var budget int64
	for i, inspected := len(entries)-1, 0; i >= 0 && inspected < 256 && len(out) < 64; i, inspected = i-1, inspected+1 {
		entry := entries[i]
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !savedHandoffID.MatchString(id) {
			continue
		}
		handoffInfo, e := entry.Info()
		journalInfo, jerr := os.Lstat(filepath.Join(journal.Dir(a.StateDir), id, "journal.json"))
		if e != nil || jerr != nil || !handoffInfo.Mode().IsRegular() || !journalInfo.Mode().IsRegular() {
			continue
		}
		budget += handoffInfo.Size() + journalInfo.Size()
		if budget > 64<<20 {
			break
		}
		option, _, _, _, err := a.savedCloudHandoff(id)
		if err == nil {
			out = append(out, option)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

func (a *App) prepareCloudHandoffLink(taskID, handoff string, fork bool) (CloudHandoffLinkPlan, cloudHandoffLink, error) {
	var empty CloudHandoffLinkPlan
	var link cloudHandoffLink
	owner, err := (relay.Store{Directory: filepath.Join(a.StateDir, "relay")}).Public()
	if err != nil {
		return empty, link, err
	}
	task, err := (relay.AdmissionStore{Directory: filepath.Join(a.StateDir, "cloud-admissions")}).Task(taskID, owner.ID)
	if err != nil {
		return empty, link, err
	}
	option, saved, graph, cloud, err := a.savedCloudHandoff(handoff)
	if err != nil {
		return empty, link, err
	}
	if option.Provider != task.Provider {
		return empty, link, errors.New("cloud task and saved handoff use different providers")
	}
	if fork {
		state, _ := graph.LatestState(cloud)
		graph.Branch = graph.Fork("cloud-task/"+task.ID, state.Heads)
	}
	link = cloudHandoffLink{Schema: 1, Task: task, Origin: move.CheckpointHandoff{Task: task.ID, Operation: handoff, CloudReplica: cloud, Brief: saved.Brief, Fork: fork}, Graph: graph.Encode()}
	return CloudHandoffLinkPlan{Task: task.ID, Handoff: option, Fork: fork, Review: link.review()}, link, nil
}

func (a *App) PlanCloudHandoffLink(task, handoff string, fork bool) (CloudHandoffLinkPlan, error) {
	plan, link, err := a.prepareCloudHandoffLink(task, handoff, fork)
	if err == nil {
		_, size, checkErr := a.checkCloudHandoffSlot(link)
		err = checkErr
		if estimate := len(link.signed()) + 256; err == nil && (estimate > cloudHandoffLinkLimit || size+int64(estimate) > 64<<20) {
			err = errors.New("saved task provenance byte limit reached")
		}
	}
	return plan, err
}

func (a *App) LinkCloudHandoff(ctx context.Context, task, handoff string, fork bool, review string) (CloudHandoffLinkPlan, error) {
	root := filepath.Join(a.StateDir, "cloud-checkpoints")
	if err := os.MkdirAll(root, 0700); err != nil {
		return CloudHandoffLinkPlan{}, err
	}
	if err := localstate.PrivateDirectory(root); err != nil {
		return CloudHandoffLinkPlan{}, err
	}
	lock, err := localstate.Lock(ctx, filepath.Join(root, ".admission.lock"))
	if err != nil {
		return CloudHandoffLinkPlan{}, err
	}
	defer lock.Close()
	plan, link, err := a.prepareCloudHandoffLink(task, handoff, fork)
	if err != nil {
		return plan, err
	}
	if review == "" || review != plan.Review {
		return plan, errors.New("saved handoff changed since review; review it again")
	}
	links := filepath.Join(a.StateDir, "cloud-task-links")
	if err := os.MkdirAll(links, 0700); err != nil {
		return plan, err
	}
	if err := localstate.PrivateDirectory(links); err != nil {
		return plan, err
	}
	path := filepath.Join(links, task+".json")
	existing, size, err := a.checkCloudHandoffSlot(link)
	if err != nil {
		return plan, err
	}
	if existing {
		return plan, nil
	}
	identity, err := (relay.Store{Directory: filepath.Join(a.StateDir, "relay")}).Identity(ctx, link.Task.Owner.Endpoint)
	if err != nil || identity.Public.ID != link.Task.Owner.ID {
		return plan, errors.New("cloud task owner changed")
	}
	link.Origin.Time = time.Now().UTC()
	link.Signature = ed25519.Sign(identity.Signing, link.signed())
	body, err := json.Marshal(link)
	if err != nil {
		return plan, err
	}
	if len(body) > cloudHandoffLinkLimit || size+int64(len(body)) > 64<<20 {
		return plan, errors.New("saved task provenance byte limit reached")
	}
	tmp, err := os.CreateTemp(links, ".link-")
	if err != nil {
		return plan, err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(body); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return plan, err
	}
	if closeErr != nil {
		return plan, closeErr
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return plan, err
	}
	return plan, nil
}

func (a *App) cloudHandoffLink(task relay.CloudTask) (*cloudHandoffLink, *lineage.Manifest, error) {
	if err := task.Verify(task.Owner.ID); err != nil {
		return nil, nil, err
	}
	if err := localstate.PrivateDirectory(filepath.Join(a.StateDir, "cloud-task-links")); err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	body, err := localstate.ReadPrivateFile(filepath.Join(a.StateDir, "cloud-task-links", task.ID+".json"), cloudHandoffLinkLimit)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var link cloudHandoffLink
	if json.Unmarshal(body, &link) != nil || link.Schema != 1 || link.Task.ID != task.ID || !bytes.Equal(link.Task.Signature, task.Signature) || link.Task.Verify(task.Owner.ID) != nil || link.Origin.Task != task.ID || !savedHandoffID.MatchString(link.Origin.Operation) || link.Origin.Time.IsZero() || !ed25519.Verify(task.Owner.Signing, link.signed(), link.Signature) {
		return nil, nil, errors.New("saved cloud handoff signature or task binding is invalid")
	}
	m, err := lineage.Parse(link.Graph)
	if err != nil {
		return nil, nil, err
	}
	// The signed historical capsule survives deliberate journal retirement, but
	// a locally recorded undo must prevent a new use of that handoff ancestry.
	if body, err := localstate.ReadPrivateFile(filepath.Join(journal.Dir(a.StateDir), link.Origin.Operation, "journal.json"), cloudHandoffLinkLimit); err == nil {
		var j journal.Journal
		if json.Unmarshal(body, &j) != nil || j.ID != link.Origin.Operation || j.Undone {
			return nil, nil, errors.New("associated handoff was undone or its journal is invalid")
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	return &link, m, nil
}

func (a *App) checkCloudHandoffSlot(link cloudHandoffLink) (bool, int64, error) {
	links := filepath.Join(a.StateDir, "cloud-task-links")
	if existing, _, err := a.cloudHandoffLink(link.Task); err == nil && existing != nil {
		if existing.review() == link.review() {
			return true, 0, nil
		}
		return false, 0, errors.New("cloud task already has a different saved handoff association")
	} else if err != nil {
		return false, 0, err
	}
	if _, err := os.Lstat(lineage.PathFor(filepath.Join(a.StateDir, "cloud-checkpoints", link.Task.ID, "source.jsonl"))); err == nil {
		return false, 0, errors.New("this task already has checkpoint lineage; preserve it and use a fresh task for another ancestry")
	} else if !os.IsNotExist(err) {
		return false, 0, err
	}
	// Planning freezes the association before returning a review. Linking and
	// planning share the admission lock, so a previously reviewed checkpoint can
	// never race a first association into a write with different ancestry.
	if err := a.checkCloudHandoffReviews(link.Task.ID); err != nil {
		return false, 0, err
	}
	entries, err := os.ReadDir(links)
	if os.IsNotExist(err) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	if err := localstate.PrivateDirectory(links); err != nil {
		return false, 0, err
	}
	if len(entries) >= 1024 {
		return false, 0, errors.New("saved task provenance limit reached")
	}
	var size int64
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".link-") {
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return false, 0, errors.New("invalid task provenance storage")
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return false, 0, errors.New("invalid task provenance file")
		}
		size += info.Size()
		if size > 64<<20 {
			return false, 0, errors.New("saved task provenance byte limit reached")
		}
		body, err := localstate.ReadPrivateFile(filepath.Join(links, entry.Name()), cloudHandoffLinkLimit)
		if err != nil {
			return false, 0, err
		}
		var other cloudHandoffLink
		if json.Unmarshal(body, &other) != nil || entry.Name() != other.Task.ID+".json" || other.Task.Verify(other.Task.Owner.ID) != nil || !ed25519.Verify(other.Task.Owner.Signing, other.signed(), other.Signature) {
			return false, 0, errors.New("invalid saved task provenance")
		}
		if other.Origin.Operation == link.Origin.Operation && !other.Origin.Fork && !link.Origin.Fork {
			return false, 0, errors.New("this handoff already belongs to another logical task; choose an explicit independent fork")
		}
	}
	return false, size, nil
}

func (a *App) checkCloudHandoffReviews(task string) error {
	root := filepath.Join(a.StateDir, "cloud-checkpoints")
	if err := localstate.PrivateDirectory(root); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var total int64
	count := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		count++
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || count > 32 {
			return errors.New("invalid checkpoint review storage")
		}
		total += info.Size()
		if total > 256<<20 {
			return errors.New("checkpoint review byte limit reached")
		}
		body, err := localstate.ReadPrivateFile(filepath.Join(root, entry.Name()), relayStateLimit)
		if err != nil {
			return err
		}
		// Decode metadata only; do not allocate another decoded transcript.
		var record struct {
			Export struct {
				Task *relay.CloudTask `json:"task"`
			} `json:"export"`
		}
		if json.Unmarshal(body, &record) != nil {
			return errors.New("invalid checkpoint review record")
		}
		if record.Export.Task != nil && record.Export.Task.ID == task {
			return errors.New("this task already has a reviewed checkpoint; remove its cached review before linking, or use a fresh task")
		}
	}
	return nil
}
