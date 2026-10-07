package ir

import (
	"context"
	"fmt"
	"io"
)

// BoundedReader caps memory exposure in native decoders and checks cancellation on
// every read. Crossing the cap is an explicit error; it never truncates a transcript.
type BoundedReader struct {
	Context   context.Context
	Reader    io.Reader
	ReadBytes int64
}

const MaxTranscriptBytes = 256 << 20

func (r *BoundedReader) Read(p []byte) (int, error) {
	if err := r.Context.Err(); err != nil {
		return 0, err
	}
	if r.ReadBytes >= MaxTranscriptBytes {
		var probe [1]byte
		n, err := r.Reader.Read(probe[:])
		if n == 0 {
			return 0, err
		}
		return 0, fmt.Errorf("transcript exceeds the %d MiB analysis limit; use the agent's native compaction or select a smaller branch", MaxTranscriptBytes>>20)
	}
	p = p[:min(int64(len(p)), MaxTranscriptBytes-r.ReadBytes)]
	n, err := r.Reader.Read(p)
	r.ReadBytes += int64(n)
	return n, err
}
