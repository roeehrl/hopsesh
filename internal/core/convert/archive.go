package convert

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Archive retains portable history independently of working-context limits. Vendor
// private state, signed reasoning and attachment bytes never cross this boundary.
func Archive(nodes []ir.Node, r Request) ([]byte, error) {
	w := NewArchiveWriter(r)
	if err := w.AddNodes(nodes); err != nil {
		return nil, err
	}
	if err := w.AddBriefing(); err != nil {
		return nil, err
	}
	return w.Bytes(), nil
}

// ArchiveWriter builds one portable archive incrementally: earlier archives are
// re-read record by record under the current redaction/mapping policy and merged with
// new history without holding several copies. Identical encoded records are kept once,
// differing renderings are all kept (fork evidence). Crossing the archive size limit
// is a LimitError; the archive is never truncated.
type ArchiveWriter struct {
	r     Request
	res   Result
	out   bytes.Buffer
	seen  map[[sha256.Size]byte]bool
	limit int64
}

// NewArchiveWriter starts an archive under r's mapping, redaction and limits.
func NewArchiveWriter(r Request) *ArchiveWriter {
	return &ArchiveWriter{r: r, seen: map[[sha256.Size]byte]bool{}, limit: r.Limits.Normalize().ArchiveBytes}
}

// AddNodes adds nodes in order.
func (w *ArchiveWriter) AddNodes(nodes []ir.Node) error {
	for _, n := range nodes {
		if err := w.AddNode(n); err != nil {
			return err
		}
	}
	return nil
}

// AddBriefing keeps optional briefing data even when the working copy must shorten it.
func (w *ArchiveWriter) AddBriefing() error {
	bf := w.r.Briefing
	if bf.Note == "" && len(bf.Rules) == 0 && len(bf.Missing) == 0 {
		return nil
	}
	raw, err := json.Marshal(bf)
	if err != nil {
		return err
	}
	return w.AddNode(ir.Node{Kind: ir.KindMessage, Actor: ir.Agent, Generated: true, Text: "[Hopsesh transfer briefing data]\n" + string(raw)})
}

// AddNode strips private state, maps every portable textual field (including tool
// arguments and fragments) and appends the record unless it is already present.
func (w *ArchiveWriter) AddNode(n ir.Node) error {
	if n.Kind == ir.KindReasoning {
		return nil
	}
	n.Native = nil
	n.Reasoning = nil
	if n.Attachment != nil {
		a := *n.Attachment
		a.Data = nil
		n.Attachment = &a
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	var data any
	if err = json.Unmarshal(raw, &data); err != nil {
		return err
	}
	var transform func(any) any
	transform = func(v any) any {
		switch x := v.(type) {
		case string:
			return w.res.mapText(w.r, x)
		case []any:
			for i := range x {
				x[i] = transform(x[i])
			}
		case map[string]any:
			for k, v := range x {
				x[k] = transform(v)
			}
		}
		return v
	}
	if raw, err = json.Marshal(transform(data)); err != nil {
		return err
	}
	return w.addRaw(raw)
}

// AddArchive re-reads an earlier portable archive record by record through AddNode.
func (w *ArchiveWriter) AddArchive(r io.Reader) error {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		raw, err := readRecord(br, w.limit)
		if len(bytes.TrimSpace(raw)) > 0 {
			var n ir.Node
			if e := json.Unmarshal(raw, &n); e != nil {
				return e
			}
			if e := w.AddNode(n); e != nil {
				return e
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (w *ArchiveWriter) addRaw(raw []byte) error {
	sum := sha256.Sum256(raw)
	if w.seen[sum] {
		return nil
	}
	if size := int64(w.out.Len() + len(raw) + 1); size > w.limit {
		return &ir.LimitError{Stage: ir.StageArchive, Limit: w.limit, Size: size, Detail: "no history was truncated"}
	}
	w.seen[sum] = true
	w.out.Write(raw)
	w.out.WriteByte('\n')
	return nil
}

// Bytes is the archive so far.
func (w *ArchiveWriter) Bytes() []byte { return w.out.Bytes() }

// Records is the number of records in the archive so far.
func (w *ArchiveWriter) Records() int { return len(w.seen) }

func readRecord(br *bufio.Reader, limit int64) ([]byte, error) {
	var raw []byte
	for {
		part, err := br.ReadSlice('\n')
		if int64(len(raw)+len(part)) > limit {
			return nil, &ir.LimitError{Stage: ir.StageArchive, Limit: limit, Size: int64(len(raw) + len(part)), Detail: "an archive record is larger than the archive limit"}
		}
		raw = append(raw, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return raw, err
	}
}

// MergeArchives deduplicates immutable portable records by their encoded content.
// It preserves differing renderings rather than silently dropping fork evidence.
func MergeArchives(limit int64, parts ...[]byte) ([]byte, error) {
	w := NewArchiveWriter(Request{Limits: ir.Limits{ArchiveBytes: limit}})
	for _, part := range parts {
		for _, raw := range bytes.Split(part, []byte{'\n'}) {
			if len(bytes.TrimSpace(raw)) == 0 {
				continue
			}
			var n ir.Node
			if err := json.Unmarshal(raw, &n); err != nil {
				return nil, err
			}
			if n.Native != nil || n.Reasoning != nil || n.Kind == ir.KindReasoning || n.Attachment != nil && len(n.Attachment.Data) > 0 {
				return nil, fmt.Errorf("archive contains vendor-private state")
			}
			if err := w.addRaw(raw); err != nil {
				return nil, err
			}
		}
	}
	return w.Bytes(), nil
}
