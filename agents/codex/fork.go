package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func nativeValue(payload []byte) []byte {
	var value any
	d := json.NewDecoder(bytes.NewReader(payload))
	d.UseNumber()
	if d.Decode(&value) != nil {
		return nil
	}
	b, _ := json.Marshal(value)
	return b
}

// VerifyNativeFork uses Codex's forked_from_id and optional exclusive ordinal, then
// compares copied native records. Text, timestamps or a title do not imply ancestry.
func (m *Module) VerifyNativeFork(ctx context.Context, h agent.Host, in agent.Install, parent, child agent.Summary) ([]agent.NativeInheritance, error) {
	// The prefix check walks both files line by line: it never holds a whole rollout
	// (hundreds of megabytes for a long session) or its parsed lines in memory.
	pf, err := openNative(ctx, h.FS(), parent.Path)
	if err != nil {
		return nil, err
	}
	defer pf.Close()
	cf, err := openNative(ctx, h.FS(), child.Path)
	if err != nil {
		return nil, err
	}
	defer cf.Close()
	pr, cr := newLineStream(pf), newLineStream(cf)
	pl, pok, err := pr.next()
	if err != nil {
		return nil, err
	}
	cl, cok, err := cr.next()
	if err != nil {
		return nil, err
	}
	pm, err := metaOf(pl, pok)
	if err != nil {
		return nil, err
	}
	cm, err := metaOf(cl, cok)
	if err != nil {
		return nil, err
	}
	if cm.ForkedFromID != pm.ID || cm.ID == pm.ID || child.Key.Agent != parent.Key.Agent || child.Key.Profile != parent.Key.Profile || string(parent.Key.Session) != pm.ID || string(child.Key.Session) != cm.ID {
		return nil, fmt.Errorf("%w: native fork parent is not declared", agent.ErrDiverged)
	}
	if cm.HistoryBase != nil {
		return nil, fmt.Errorf("%w: paginated fork ancestry is not materialized", agent.ErrUnsupported)
	}
	boundary := cm.ForkedFromOrdinalExclusive
	if boundary != nil && *boundary < 2 {
		return nil, fmt.Errorf("%w: declared fork boundary is not materialized", agent.ErrDiverged)
	}
	ordinal := func(l line, i uint64) bool { return l.Ordinal != nil && *l.Ordinal == i }
	if boundary != nil && (!ordinal(pl, 0) || !ordinal(cl, 0)) {
		return nil, fmt.Errorf("%w: fork prefix ordinals are missing or discontinuous", agent.ErrDiverged)
	}
	last, comparing := 1, true
	for i := uint64(1); ; i++ {
		if pl, pok, err = pr.next(); err != nil {
			return nil, err
		}
		if cl, cok, err = cr.next(); err != nil {
			return nil, err
		}
		before := boundary != nil && i < *boundary
		if before && (!pok || !cok) {
			return nil, fmt.Errorf("%w: declared fork boundary is not materialized", agent.ErrDiverged)
		}
		if before && (!ordinal(pl, i) || !ordinal(cl, i)) {
			return nil, fmt.Errorf("%w: fork prefix ordinals are missing or discontinuous", agent.ErrDiverged)
		}
		if comparing {
			if !pok || !cok {
				comparing = false
			} else {
				if boundary != nil {
					if pl.Ordinal == nil {
						return nil, fmt.Errorf("%w: fork boundary requires native ordinals", agent.ErrUnsupported)
					}
					if *pl.Ordinal >= *boundary {
						comparing = false
					}
				}
				if comparing && (pl.Type != cl.Type || pl.Timestamp != cl.Timestamp || !bytes.Equal(nativeValue(pl.Payload), nativeValue(cl.Payload))) {
					if boundary != nil {
						return nil, fmt.Errorf("%w: history differs before declared fork boundary", agent.ErrDiverged)
					}
					comparing = false
				}
				if comparing {
					last++
				}
			}
		}
		if !comparing && !before {
			break
		}
	}
	pf.Close()
	cf.Close()
	ps, err := m.Read(ctx, h, in, parent, ir.Cursor{})
	if err != nil {
		return nil, err
	}
	cs, err := m.Read(ctx, h, in, child, ir.Cursor{})
	if err != nil {
		return nil, err
	}
	by := map[string]ir.Node{}
	for _, n := range ps.Nodes {
		by[n.Native.Anchor] = n
	}
	var out []agent.NativeInheritance
	for _, n := range cs.Nodes {
		var index int
		_, _ = fmt.Sscanf(n.Native.Anchor, "line:%d/", &index)
		if index >= last {
			continue
		}
		pn, ok := by[n.Native.Anchor]
		if !ok || ir.ContentHash(pn) != ir.ContentHash(n) {
			return nil, fmt.Errorf("%w: native fork prefix changed representation", agent.ErrDiverged)
		}
		out = append(out, agent.NativeInheritance{ParentAnchor: pn.Native.Anchor, ChildAnchor: n.Native.Anchor, Hash: ir.ContentHash(n)})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: native fork has no verified inherited conversation", agent.ErrDiverged)
	}
	return out, nil
}

// openNative opens a native file for streaming, within the native file size limit.
func openNative(ctx context.Context, fsys agent.FS, p string) (agent.File, error) {
	limit := ir.LimitsFrom(ctx).NativeFileBytes
	if fi, err := fsys.Stat(p); err == nil && fi.Size() > limit {
		return nil, &ir.LimitError{Stage: ir.StageNative, Limit: limit, Size: fi.Size(), Detail: p}
	}
	return fsys.Open(p)
}

// lineStream yields a rollout's records one at a time, with readLines' rules: a line
// that is not a record is skipped (an error in paginated history), and a partial last
// line is not a record.
type lineStream struct {
	br        *bufio.Reader
	off       int64
	started   bool
	paginated bool
}

func newLineStream(r io.Reader) *lineStream { return &lineStream{br: bufio.NewReaderSize(r, 1<<20)} }

func (s *lineStream) next() (line, bool, error) {
	for {
		b, err := s.br.ReadBytes('\n')
		if err == io.EOF {
			return line{}, false, nil
		}
		if err != nil {
			return line{}, false, err
		}
		s.off += int64(len(b))
		var l line
		if json.Unmarshal(b, &l) == nil && l.Type != "" {
			if !s.started && l.Type == "session_meta" {
				var mt meta
				if json.Unmarshal(l.Payload, &mt) == nil {
					s.paginated = mt.HistoryMode == "paginated"
				}
			}
			s.started = true
			return l, true, nil
		}
		if s.paginated {
			return line{}, false, fmt.Errorf("%w: invalid record in paginated history at byte %d", agent.ErrDiverged, s.off)
		}
	}
}

// metaOf is firstMeta for a rollout's first record.
func metaOf(l line, ok bool) (meta, error) {
	if !ok || l.Type != "session_meta" {
		return meta{}, errors.New("a rollout starts with session_meta")
	}
	var m meta
	if err := json.Unmarshal(l.Payload, &m); err != nil {
		return meta{}, err
	}
	if m.ID == "" || m.CWD == "" {
		return meta{}, errors.New("session_meta lacks id or cwd")
	}
	return m, nil
}
