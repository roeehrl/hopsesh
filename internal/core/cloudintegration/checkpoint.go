package cloudintegration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// Checkpoint is an immutable complete-record prefix at an explicit read boundary,
// never a claim that the provider has finished or cannot append more history.
type Checkpoint struct {
	Kind          string    `json:"kind"`
	Created       time.Time `json:"created"`
	Records       int       `json:"records"`
	ReadBytes     int64     `json:"readBytes"`
	ExportedBytes int64     `json:"exportedBytes"`
	OmittedTail   int64     `json:"omittedTailBytes"`
}

type Export struct {
	Observation
	Format     string     `json:"format"`
	Data       []byte     `json:"data"`
	SHA256     string     `json:"sha256"`
	Checkpoint Checkpoint `json:"checkpoint"`
}

func sealCheckpoint(body []byte) (data []byte, checkpoint Checkpoint, err error) {
	checkpoint = Checkpoint{Kind: "complete-record-prefix", Created: time.Now().UTC(), ReadBytes: int64(len(body))}
	start, boundary := 0, 0
	for start < len(body) {
		length := bytes.IndexByte(body[start:], '\n')
		end := len(body)
		if length >= 0 {
			end = start + length
		}
		line := bytes.TrimSpace(body[start:end])
		if len(line) != 0 && (!json.Valid(line) || line[0] != '{') {
			if length < 0 {
				break // final write is incomplete; report the exact omitted bytes
			}
			return nil, Checkpoint{}, errors.New("native transcript contains an invalid completed JSONL record")
		}
		if len(line) != 0 {
			checkpoint.Records++
		}
		boundary = end
		if length >= 0 {
			boundary++
		}
		start = boundary
	}
	if checkpoint.Records == 0 {
		return nil, Checkpoint{}, errors.New("native transcript has no complete records yet; try again after the next native write")
	}
	checkpoint.ExportedBytes = int64(boundary)
	checkpoint.OmittedTail = int64(len(body) - boundary)
	return bytes.Clone(body[:boundary]), checkpoint, nil
}

func sealedExport(observation Observation, body []byte) (Export, error) {
	data, checkpoint, err := sealCheckpoint(body)
	if err != nil {
		return Export{}, err
	}
	sum := sha256.Sum256(data)
	observation.Bytes = checkpoint.ReadBytes
	return Export{Observation: observation, Format: "native-jsonl", Data: data, SHA256: hex.EncodeToString(sum[:]), Checkpoint: checkpoint}, nil
}
