package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

const analysisRecordCountLimit = 1_000_000

// Large rollouts repeat replacement histories and runtime metadata. Scan them
// without retaining those copies. Keep every valid record position/ordinal so
// native anchors and pagination verification remain identical to readLines.
// The retained conversation cap is independent of the raw file's byte length.
func readAnalysisLines(ctx context.Context, r io.Reader) ([]line, int64, error) {
	l := ir.LimitsFrom(ctx)
	return readAnalysisLinesWithLimits(ctx, r, l.ReadBytes, l.RecordBytes)
}

func readAnalysisLinesWithLimits(ctx context.Context, r io.Reader, retainedLimit, recordLimit int64) ([]line, int64, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var records []line
	var offset, retained int64
	paginated := false
	for {
		var raw []byte
		for {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			part, err := br.ReadSlice('\n')
			if int64(len(raw)+len(part)) > recordLimit {
				return nil, 0, &ir.LimitError{Stage: ir.StageRecord, Limit: recordLimit, Size: int64(len(raw) + len(part)), Detail: fmt.Sprintf("Codex native record at byte %d cannot be analyzed safely", offset)}
			}
			raw = append(raw, part...)
			if err == io.EOF {
				return records, offset, nil
			} // ignore an unfinished trailing record
			if err == bufio.ErrBufferFull {
				continue
			}
			if err != nil {
				return nil, 0, err
			}
			break
		}
		offset += int64(len(raw))
		var l line
		if json.Unmarshal(raw, &l) != nil || l.Type == "" {
			if paginated {
				return nil, 0, fmt.Errorf("%w: invalid record in paginated history at byte %d", agent.ErrDiverged, offset)
			}
			continue
		}
		if len(records) == 0 && l.Type == "session_meta" {
			var mt meta
			if json.Unmarshal(l.Payload, &mt) == nil {
				paginated = mt.HistoryMode == "paginated"
			}
		}
		switch l.Type {
		case "session_meta", "response_item", "turn_context":
			// Needed by the native reader, including reasoning provenance and model.
		case "compacted":
			var compact struct {
				Message     string            `json:"message"`
				Replacement []json.RawMessage `json:"replacement_history"`
			}
			if json.Unmarshal(l.Payload, &compact) == nil {
				if compact.Replacement != nil {
					size := 0
					for _, item := range compact.Replacement {
						size += len(item) + 32
					}
					l.compactBytes = &size
				}
				l.Payload, _ = json.Marshal(struct {
					Message string `json:"message"`
				}{compact.Message})
			} else {
				l.Payload = nil
			}
		case "event_msg":
			var event struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(l.Payload, &event) != nil || event.Type != "item_completed" {
				l.Payload = nil
			}
		default:
			l.Payload = nil
		}
		retained += int64(len(l.Payload))
		if retained > retainedLimit {
			return nil, 0, &ir.LimitError{Stage: ir.StageRead, Limit: retainedLimit, Size: retained, Detail: "Codex retained conversation; no history was truncated"}
		}
		if len(records) >= analysisRecordCountLimit {
			return nil, 0, fmt.Errorf("Codex transcript exceeds %d native records", analysisRecordCountLimit)
		}
		records = append(records, l)
	}
}
