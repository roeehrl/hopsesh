package ir

import (
	"context"
	"io"
)

// BoundedReader caps memory exposure in native decoders and checks cancellation on
// every read. Crossing the cap is an explicit LimitError; it never truncates a transcript.
// Limit 0 uses the operation's read budget (LimitsFrom(Context).ReadBytes).
type BoundedReader struct {
	Context   context.Context
	Reader    io.Reader
	ReadBytes int64
	Limit     int64
}

// MaxTranscriptBytes is the default read budget (history.read_memory_mb).
const MaxTranscriptBytes = 256 << 20

func (r *BoundedReader) Read(p []byte) (int, error) {
	if err := r.Context.Err(); err != nil {
		return 0, err
	}
	if r.Limit <= 0 {
		r.Limit = LimitsFrom(r.Context).ReadBytes
	}
	if r.ReadBytes >= r.Limit {
		var probe [1]byte
		n, err := r.Reader.Read(probe[:])
		if n == 0 {
			return 0, err
		}
		return 0, &LimitError{Stage: StageRead, Limit: r.Limit, Size: r.ReadBytes + int64(n), Detail: "use the agent's native compaction, select a smaller branch, or raise the limit in Settings › History"}
	}
	p = p[:min(int64(len(p)), r.Limit-r.ReadBytes)]
	n, err := r.Reader.Read(p)
	r.ReadBytes += int64(n)
	return n, err
}
