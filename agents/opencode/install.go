package opencode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A moved session is installed by OpenCode itself: the core places the session's JSON
// lines at its anchor (and its children's beside it), and AfterInstall has OpenCode's
// program import them (`opencode session import <file> --directory <dir>`), the parent
// first. A copy kept beside the original (keep-both) comes in under the id the core minted
// for it; its children and every message get new ids too, since OpenCode's message ids are
// keys of the whole database. The new ids are made from the old ones and the copy's id, so
// running the install again (a recovery) makes the same ones.

// InstallsViaVendor reports that OpenCode's import is the install.
func (*Module) InstallsViaVendor() bool { return true }

// placed is one session of a placed copy, as its import needs it.
type placedSession struct {
	id, parent, version, dir string
	info                     map[string]json.RawMessage // OpenCode's info, without hopsesh's members
	msgs                     []json.RawMessage
}

// ids are the placed session's message ids, in order.
func (p *placedSession) ids() []string {
	out := make([]string, 0, len(p.msgs))
	for _, m := range p.msgs {
		var v struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(m, &v)
		out = append(out, v.ID)
	}
	return out
}

// transfer is the placed session as OpenCode's import reads it: {info, messages}.
func (p *placedSession) transfer() ([]byte, error) {
	msgs := p.msgs
	if msgs == nil {
		msgs = []json.RawMessage{}
	}
	return json.Marshal(struct {
		Info     map[string]json.RawMessage `json:"info"`
		Messages []json.RawMessage          `json:"messages"`
	}{p.info, msgs})
}

// parsePlaced reads a session's JSON lines for its import.
func parsePlaced(path string, data []byte) (*placedSession, error) {
	s, msgs, err := decodeJSONL(path, data)
	if err != nil {
		return nil, err
	}
	first, _, _ := bytes.Cut(bytes.TrimLeft(data, " \t\r\n"), []byte("\n"))
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(first, &fields); err != nil {
		return nil, &agent.FormatError{Path: path, Line: 1, Err: err}
	}
	delete(fields, "kind")    // hopsesh's own members
	delete(fields, "version") // (OpenCode sets the version an import writes)
	p := &placedSession{id: s.ID, parent: s.ParentID, version: s.Version, dir: s.Directory, info: fields}
	for _, m := range msgs {
		p.msgs = append(p.msgs, m.Raw)
	}
	return p, nil
}

// placedFamily is the copy the core placed for key: the session at its anchor and the
// child sessions beside it, parents first.
func placedFamily(ctx context.Context, h agent.Host, in agent.Install, key agent.SessionID) ([]*placedSession, error) {
	anchor := anchorOf(h, in, key)
	b, err := agent.ReadNative(ctx, h.FS(), anchor)
	if err != nil {
		return nil, fmt.Errorf("the placed copy of the OpenCode session %s: %w", key, err)
	}
	root, err := parsePlaced(anchor, b)
	if err != nil {
		return nil, err
	}
	if root.id != string(key) {
		return nil, fmt.Errorf("the placed copy at %s is the session %s, not %s", anchor, root.id, key)
	}
	dir := strings.TrimSuffix(anchor, ".jsonl") + ".children"
	entries, err := h.FS().ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var kids []*placedSession
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		p := h.Path().Join(dir, e.Name())
		b, err := agent.ReadNative(ctx, h.FS(), p)
		if err != nil {
			return nil, err
		}
		kid, err := parsePlaced(p, b)
		if err != nil {
			return nil, err
		}
		kids = append(kids, kid)
	}
	return parentsFirst(root, kids)
}

// parentsFirst orders a family for its import: OpenCode imports a child only once its
// parent is there. A child whose parent is not in the family hangs from the root.
func parentsFirst(root *placedSession, kids []*placedSession) ([]*placedSession, error) {
	sort.Slice(kids, func(i, j int) bool { return kids[i].id < kids[j].id })
	byID := map[string]bool{root.id: true}
	for _, k := range kids {
		byID[k.id] = true
	}
	ids, parents := []string{root.id}, []string{""}
	for _, k := range kids {
		if k.parent == "" || !byID[k.parent] || k.parent == k.id {
			k.parent = root.id
			k.info["parentID"], _ = json.Marshal(root.id)
		}
		ids, parents = append(ids, k.id), append(parents, k.parent)
	}
	order, err := familyOrder(ids, parents)
	if err != nil {
		return nil, fmt.Errorf("the placed copy of the OpenCode session %s: %w", root.id, err)
	}
	all := append([]*placedSession{root}, kids...)
	out := make([]*placedSession, 0, len(order))
	for _, i := range order {
		out = append(out, all[i])
	}
	return out, nil
}

// familyOrder orders a family's members (ids[0] the root, parents[i] the parent of
// ids[i]) for their import: the root, then each member once its parent is in. One whose
// parent is not in the family comes after the root. Members that do not hang from the
// root (their parents make a cycle) are an error: OpenCode would not import them.
func familyOrder(ids, parents []string) ([]int, error) {
	in := map[string]bool{}
	for _, x := range ids {
		in[x] = true
	}
	children := map[string][]int{}
	for i := 1; i < len(ids); i++ {
		parent := parents[i]
		if !in[parent] || parent == ids[i] {
			parent = ids[0]
		}
		children[parent] = append(children[parent], i)
	}
	out := []int{0}
	seen := map[int]bool{0: true}
	for k := 0; k < len(out); k++ {
		for _, c := range children[ids[out[k]]] {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	if len(out) != len(ids) {
		var lost []string
		for i := range ids {
			if !seen[i] {
				lost = append(lost, ids[i])
			}
		}
		return nil, fmt.Errorf("the child sessions %s do not hang from %s", strings.Join(lost, ", "), ids[0])
	}
	return out, nil
}

// remapIDs gives a keep-both copy's children and messages new ids, and puts them where
// OpenCode refers to them by id: the sessions' id and parentID, a pending revert's message
// (revert.messageID; 2.3.0's import drops it), the messages' id, a summary's kept message
// (metadata.keepID), a subagent's answer's session (metadata.childID) and a subagent
// call's session (state.metadata.sessionID). What the model reads (text, tool input and output) is left
// as it is, so the copy reads as the conversation hopsesh staged; a subagent's answer may
// still name the original's child. Each new id keeps the old one's time part, so the
// order stays, and takes the rest from the copy's id, so a second run gives the same ids.
func remapIDs(fam []*placedSession, from, to string) {
	pairs := map[string]string{}
	if from != "" && from != to {
		pairs[from] = to
	}
	for _, p := range fam[1:] {
		pairs[p.id] = remappedID(p.id, to)
	}
	for _, p := range fam {
		for _, mid := range p.ids() {
			if mid != "" {
				pairs[mid] = remappedID(mid, to)
			}
		}
	}
	swap := func(fields map[string]json.RawMessage, key string) bool {
		var old string
		if json.Unmarshal(fields[key], &old) != nil {
			return false
		}
		n, ok := pairs[old]
		if ok {
			fields[key], _ = json.Marshal(n)
		}
		return ok
	}
	for _, p := range fam {
		if n, ok := pairs[p.id]; ok {
			p.id = n
		}
		if n, ok := pairs[p.parent]; ok {
			p.parent = n
		}
		swap(p.info, "id")
		swap(p.info, "parentID")
		editObject(p.info, "revert", func(rv map[string]json.RawMessage) bool { return swap(rv, "messageID") })
		for i, raw := range p.msgs {
			var msg map[string]json.RawMessage
			if json.Unmarshal(raw, &msg) != nil {
				continue
			}
			swap(msg, "id")
			editObject(msg, "metadata", func(md map[string]json.RawMessage) bool {
				kept := swap(md, "keepID")
				return swap(md, "childID") || kept
			})
			var content []map[string]json.RawMessage
			if json.Unmarshal(msg["content"], &content) == nil {
				changed := false
				for _, it := range content {
					changed = editObject(it, "state", func(st map[string]json.RawMessage) bool {
						return editObject(st, "metadata", func(md map[string]json.RawMessage) bool { return swap(md, "sessionID") })
					}) || changed
				}
				if changed {
					msg["content"], _ = json.Marshal(content)
				}
			}
			p.msgs[i], _ = json.Marshal(msg)
		}
	}
}

// editObject edits the object at fields[key] with edit, and keeps it when edit changed it.
func editObject(fields map[string]json.RawMessage, key string, edit func(map[string]json.RawMessage) bool) bool {
	var obj map[string]json.RawMessage
	if json.Unmarshal(fields[key], &obj) != nil || obj == nil || !edit(obj) {
		return false
	}
	fields[key], _ = json.Marshal(obj)
	return true
}

// remappedID is old's id in the copy whose id is salt: the same prefix and time part, a
// tail made from both, and what followed the tail (a fork's "_<seq>").
func remappedID(old, salt string) string {
	sum := sha256.Sum256([]byte(salt + "\x00" + old))
	tail := make([]byte, idTail)
	for i := range tail {
		tail[i] = base62[int(sum[i])%len(base62)]
	}
	prefix, rest, ok := strings.Cut(old, "_")
	if ok && len(rest) >= 12 {
		if _, err := hex.DecodeString(rest[:12]); err == nil {
			suffix := ""
			if len(rest) > 12+idTail {
				suffix = rest[12+idTail:]
			}
			return prefix + "_" + rest[:12] + string(tail) + suffix
		}
	}
	if !ok {
		prefix = "ses"
	}
	return prefix + "_" + hex.EncodeToString(sum[idTail:idTail+6]) + string(tail)
}

// AfterInstall has OpenCode import the copy the core placed for key: the session, then
// its children. It is idempotent: a session already there with the placed messages is
// left as it is, and one there with other messages (work done since a first, interrupted
// run, or another session at the id) is ErrDiverged.
func (m *Module) AfterInstall(ctx context.Context, h agent.Host, in agent.Install, key agent.SessionKey, p agent.Placement) error {
	fam, err := placedFamily(ctx, h, in, key.Session)
	if err != nil {
		return err
	}
	if p.SourceID != "" && p.SourceID != key.Session {
		remapIDs(fam, string(p.SourceID), string(key.Session))
	}
	root := fam[0]
	if p.Title != "" {
		root.info["title"], _ = json.Marshal(p.Title)
	}
	svc, err := writeService(ctx, h, in)
	if err != nil {
		return err
	}
	if err := m.checkTarget(ctx, h, in, svc, fam); err != nil {
		return err
	}
	cwd := p.CWD
	if cwd == "" {
		cwd = nativeDir(h, root.dir)
	}
	for _, s := range fam {
		dir := cwd
		if s != root {
			dir = childDir(h, s, root, cwd)
		}
		write := func() (string, error) {
			b, err := s.transfer()
			if err != nil {
				return "", err
			}
			// Named after its content: a recovery writes the same file again, and another
			// operation's import of the session never rewrites this one's (the journal
			// would take that for this file being used since).
			sum := sha256.Sum256(b)
			tmp := h.Path().Join(in.Root(rootData), "hopsesh", string(id), "tmp", s.id+"-"+hex.EncodeToString(sum[:6])+".json")
			return tmp, h.FS().WriteFile(tmp, b, 0o600)
		}
		err := m.importSession(ctx, h, in, svc, agent.SessionID(s.id), s.ids(), dir, write)
		if err != nil && s != root && errors.Is(err, agent.ErrDiverged) {
			// The copy's root may be this run's import: only its own divergence means
			// this run made nothing (the core then leaves the session to undo alone).
			err = fmt.Errorf("its child session %s: %v", s.id, err)
		}
		if err != nil {
			return err
		}
	}
	if p.Title != "" {
		recordTitle(ctx, h, in, key.Session, p.Title)
	}
	return nil
}

// childDir is the folder a child session is imported in: its own when it is inside the
// copy's folder, else the copy's (a subagent works where its parent does).
func childDir(h agent.Host, s, root *placedSession, cwd string) string {
	d := nativeDir(h, s.dir)
	if d == "" || s.dir == root.dir || d == cwd {
		return cwd
	}
	if strings.HasPrefix(d, strings.TrimRight(cwd, h.Path().Sep())+h.Path().Sep()) {
		return d
	}
	return cwd
}

// checkTarget refuses a copy last written by a newer OpenCode than the one that imports it
// here (the running service, else the program): an older one may not read its messages.
func (m *Module) checkTarget(ctx context.Context, h agent.Host, in agent.Install, svc service, fam []*placedSession) error {
	here, err := importVersion(ctx, h, in, svc)
	if err != nil {
		return err
	}
	for _, s := range fam {
		if err := readsVersion(here, s.version); err != nil {
			return err
		}
	}
	return nil
}

// CheckInstall is checkTarget before anything changes here (agent.InstallChecker), for the
// version that last wrote the session moved; AfterInstall checks every session it imports.
func (m *Module) CheckInstall(ctx context.Context, h agent.Host, in agent.Install, version string) error {
	svc, err := writeService(ctx, h, in)
	if err != nil {
		return err
	}
	here, err := importVersion(ctx, h, in, svc)
	if err != nil {
		return err
	}
	return readsVersion(here, version)
}

// importVersion is the OpenCode that imports here: the running service's, else the
// program's.
func importVersion(ctx context.Context, h agent.Host, in agent.Install, svc service) (string, error) {
	if svc.State != serviceRunning {
		return in.Version, nil
	}
	v, err := serviceVersion(ctx, h, svc)
	if err != nil {
		return "", fmt.Errorf("hopsesh could not tell which version OpenCode's background service runs: %w", err)
	}
	return v, nil
}

// readsVersion refuses a session last written by a newer OpenCode than here.
func readsVersion(here, written string) error {
	if here != "" && written != "" && agent.VersionLess(here, written) {
		return fmt.Errorf("%w: the session was last written by OpenCode %s, and OpenCode here is %s, which may not read it; update OpenCode here, then try again",
			agent.ErrUnsupported, written, here)
	}
	return nil
}

// importSession imports one session unless OpenCode has it already: with the same
// messages (want, in order) it is done; with others it is ErrDiverged. write makes the
// file OpenCode imports and returns its path.
func (m *Module) importSession(ctx context.Context, h agent.Host, in agent.Install, svc service, sid agent.SessionID, want []string, dir string, write func() (string, error)) error {
	same := func() (bool, error) {
		have, err := m.messageIDs(ctx, h, in, sid)
		if err != nil {
			return false, err
		}
		if !slices.Equal(have, want) {
			return true, fmt.Errorf("%w: OpenCode has a session %s here with other messages than the copy hopsesh placed (it was used since, or is another session)", agent.ErrDiverged, sid)
		}
		return true, nil
	}
	there, err := same()
	switch {
	case there || err != nil && !absent(err):
		return err
	}
	file, err := write()
	if err != nil {
		return err
	}
	err = importFile(ctx, h, svc, sid, file, dir)
	if errors.Is(err, errExists) {
		// It appeared in between (or OpenCode said so for another reason): only the same
		// messages make that a success.
		if there, serr := same(); there || serr != nil && !absent(serr) {
			return serr
		}
	}
	return err
}

// absent reports an error that means OpenCode has no such session (or no database yet).
func absent(err error) bool {
	return errors.Is(err, agent.ErrNotFound) || errors.Is(err, agent.ErrNotInstalled)
}

// messageIDs are the ids of a session's settled messages, in order.
func (m *Module) messageIDs(ctx context.Context, h agent.Host, in agent.Install, sid agent.SessionID) ([]string, error) {
	_, msgs, err := m.transcript(ctx, h, in, sid)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, msg.ID)
	}
	return out, nil
}

// errExists is OpenCode's import finding a session at the id: it prints "Session already
// exists" on stderr and exits 0, so its exit status is no success.
var errExists = errors.New("OpenCode has a session with that id already")

// importFile runs `opencode session import <file> --directory <dir>`. It succeeds only
// when OpenCode says it imported sid.
func importFile(ctx context.Context, h agent.Host, svc service, sid agent.SessionID, file, dir string) error {
	argv := []string{"session", "import", file, "--directory", dir}
	if svc.standalone() {
		argv = append(argv, "--standalone")
	}
	res, err := run(ctx, h, argv, agent.RunOptions{})
	if err != nil {
		return err
	}
	out := string(res.Stdout)
	switch {
	case res.Code == 0 && strings.Contains(out, "Imported session: "+string(sid)):
		return nil
	case res.Code == 0 && strings.Contains(strings.ToLower(string(res.Stderr)), "already exists"):
		return errExists
	case res.Code == 0:
		return fmt.Errorf("opencode session import did not say it imported %s: %s", sid, strings.TrimSpace(out+" "+string(res.Stderr)))
	}
	return fmt.Errorf("OpenCode did not import the session %s: %w", sid, commandError("session import", res))
}

// Verify checks the staged copy reads as OpenCode sessions: the session at the placement's
// id, and each child file the child it is named for, hanging from the family.
func (*Module) Verify(ctx context.Context, h agent.Host, mp agent.MovePlan, staged map[string]string, p agent.Placement) error {
	ids := map[string]bool{string(p.Key.Session): true}
	var parents []string
	for _, f := range mp.Files {
		path := staged[agent.StagedKey(f)]
		b, err := agent.ReadNative(ctx, h.FS(), path)
		if err != nil {
			return err
		}
		s, _, err := decodeJSONL(path, b)
		if err != nil {
			return err
		}
		want := p.Key.Session
		if f.From.Role != agent.RoleMain {
			if want = childOf(p.Key.Session, f.ToRel); want == "" {
				return fmt.Errorf("%s is not a file of the OpenCode session %s", f.ToRel, p.Key.Session)
			}
			parents = append(parents, s.ParentID)
		}
		if s.ID != string(want) {
			return fmt.Errorf("the staged %s is the OpenCode session %s, not %s", f.ToRel, s.ID, want)
		}
		ids[s.ID] = true
	}
	for _, parent := range parents {
		if parent != "" && !ids[parent] {
			return fmt.Errorf("a staged child session hangs from %s, which is not in the copy", parent)
		}
	}
	return nil
}
