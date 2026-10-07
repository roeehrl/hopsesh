package relay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

// ChunkBytes bounds each part. Private IPC bounds complete objects to 32 MiB.
// Relay frames remain small;
// chunks are individually authenticated/encrypted and never become router data.
const ChunkBytes = 4 << 20
const MaxObjectBytes = 24 << 20
const maxBlobBytes = 128 << 20

type blobDescriptor struct {
	ID        string `json:"id"`
	Operation string `json:"operation"`
	Method    string `json:"method"`
	Direction string `json:"direction"`
	SHA256    string `json:"sha256"`
	Bytes     int    `json:"bytes"`
	Parts     int    `json:"parts"`
	Expires   int64  `json:"expires"`
}
type blobRequest struct {
	Descriptor blobDescriptor `json:"descriptor"`
	Index      int            `json:"index"`
	Data       []byte         `json:"data,omitempty"`
}
type blobResult struct {
	Descriptor *blobDescriptor `json:"hopseshRelayBlob,omitempty"`
}

func descriptor(operation, method, direction string, body []byte, expires int64) blobDescriptor {
	sum := sha256.Sum256(body)
	d := blobDescriptor{Operation: operation, Method: method, Direction: direction, SHA256: hex.EncodeToString(sum[:]), Bytes: len(body), Parts: (len(body) + ChunkBytes - 1) / ChunkBytes, Expires: expires}
	d.ID = d.identity()
	return d
}
func (d blobDescriptor) identity() string {
	b, _ := json.Marshal(struct {
		Operation, Method, Direction, SHA string
		Bytes                             int
	}{d.Operation, d.Method, d.Direction, d.SHA256, d.Bytes})
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func (d blobDescriptor) check(now time.Time) error {
	if d.ID != d.identity() || !opaque(d.Operation) || d.Bytes < 1 || d.Bytes > MaxObjectBytes || d.Parts != (d.Bytes+ChunkBytes-1)/ChunkBytes || (d.Direction != "request" && d.Direction != "response") || d.Expires <= now.Unix() || d.Expires > now.Add(MaxLifetime).Unix() {
		return errors.New("invalid relay chunk scope or size")
	}
	if hash, err := hex.DecodeString(d.SHA256); err != nil || len(hash) != 32 {
		return errors.New("invalid relay object digest")
	}
	if d.Method == "blob.put" || d.Method == "blob.read" || d.Method == "blob.invoke" || d.Method == "" {
		return errors.New("nested relay chunk methods are forbidden")
	}
	return nil
}
func (d blobDescriptor) chunkSize(index int) int { return min(ChunkBytes, d.Bytes-index*ChunkBytes) }
func chunkOperation(d blobDescriptor, direction string, index int) string {
	s := sha256.Sum256(fmt.Appendf(nil, "hopsesh-chunk-v1:%s:%s:%d", d.ID, direction, index))
	return hex.EncodeToString(s[:])
}
func blobPermission(req Request, grant Grant, now time.Time) (string, *blobRequest, error) {
	if req.Method != "blob.put" && req.Method != "blob.read" && req.Method != "blob.invoke" {
		return req.Method, nil, nil
	}
	var b blobRequest
	if err := json.Unmarshal(req.Params, &b); err != nil {
		return "", nil, err
	}
	if err := b.Descriptor.check(now); err != nil {
		return "", nil, err
	}
	if req.Method == "blob.read" {
		if b.Descriptor.Direction != "response" {
			return "", nil, errors.New("only a bound response can be read")
		}
	} else if grant.Kind != "device" || b.Descriptor.Direction != "request" {
		return "", nil, errors.New("cloud connectors cannot upload requests")
	}
	if grant.Expires != 0 && b.Descriptor.Expires > grant.Expires {
		return "", nil, ErrRevoked
	}
	return b.Descriptor.Method, &b, nil
}

func (s Store) blobPath(peer string, d blobDescriptor) string {
	return filepath.Join(s.Directory, "blobs", peer, d.ID)
}

// Caller holds the relay state lock. Quotas include incomplete uploads; published
// metadata is immutable, and partial publication can be resumed by exact chunks.
func (s Store) prepareBlob(peer string, d blobDescriptor) (string, error) {
	root := filepath.Join(s.Directory, "blobs")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	if err := localstate.PrivateDirectory(root); err != nil {
		return "", err
	}
	dir := s.blobPath(peer, d)
	path := filepath.Join(dir, "object.json")
	if old, err := localstate.ReadPrivateFile(path, 8192); err == nil {
		var prior blobDescriptor
		if err = json.Unmarshal(old, &prior); err != nil {
			return "", err
		}
		if prior != d {
			return "", errors.New("relay object scope changed")
		}
		return dir, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	paths, err := filepath.Glob(filepath.Join(root, "*", "*", "object.json"))
	if err != nil {
		return "", err
	}
	var used int
	count := 0
	for _, p := range paths {
		body, err := localstate.ReadPrivateFile(p, 8192)
		if err != nil {
			return "", err
		}
		var old blobDescriptor
		if err = json.Unmarshal(body, &old); err != nil {
			return "", err
		}
		if old.Expires <= time.Now().Unix() {
			if err = os.RemoveAll(filepath.Dir(p)); err != nil {
				return "", err
			}
			continue
		}
		used += old.Bytes
		count++
	}
	if count >= 32 || used+d.Bytes > maxBlobBytes {
		return "", errors.New("relay chunk storage quota reached")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err = localstate.PrivateDirectory(filepath.Dir(dir)); err != nil {
		return "", err
	}
	if err = localstate.PrivateDirectory(dir); err != nil {
		return "", err
	}
	return dir, writeJSON(path, d)
}
func (s Store) putChunk(peer string, b blobRequest) error {
	d := b.Descriptor
	if b.Index < 0 || b.Index >= d.Parts || len(b.Data) != d.chunkSize(b.Index) {
		return errors.New("invalid relay chunk position or size")
	}
	dir, err := s.prepareBlob(peer, d)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("%03d.json", b.Index))
	if old, err := localstate.ReadPrivateFile(path, ChunkBytes*2); err == nil {
		var data []byte
		if err = json.Unmarshal(old, &data); err != nil {
			return err
		}
		if !bytes.Equal(data, b.Data) {
			return errors.New("relay chunk changed under the same object ID")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeJSON(path, b.Data)
}
func (s Store) readChunk(peer string, b blobRequest) ([]byte, error) {
	d := b.Descriptor
	if b.Index < 0 || b.Index >= d.Parts {
		return nil, errors.New("invalid relay chunk position")
	}
	dir := s.blobPath(peer, d)
	body, err := localstate.ReadPrivateFile(filepath.Join(dir, "object.json"), 8192)
	if err != nil {
		return nil, err
	}
	var old blobDescriptor
	if err = json.Unmarshal(body, &old); err != nil {
		return nil, err
	}
	if old != d {
		return nil, errors.New("relay chunk belongs to another response scope")
	}
	body, err = localstate.ReadPrivateFile(filepath.Join(dir, fmt.Sprintf("%03d.json", b.Index)), ChunkBytes*2)
	if err != nil {
		return nil, err
	}
	var data []byte
	if err = json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	if len(data) != d.chunkSize(b.Index) {
		return nil, errors.New("stored relay chunk size changed")
	}
	return data, nil
}
func (s Store) assemble(peer string, d blobDescriptor) (json.RawMessage, error) {
	body := make([]byte, 0, d.Bytes)
	for i := 0; i < d.Parts; i++ {
		part, err := s.readChunk(peer, blobRequest{Descriptor: d, Index: i})
		if err != nil {
			return nil, err
		}
		body = append(body, part...)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != d.SHA256 || !json.Valid(body) {
		return nil, errors.New("relay object integrity check failed")
	}
	return body, nil
}
func (p Processor) chunkHandle(ctx context.Context, g Grant, operation string, req Request, b *blobRequest, handle Handler) (any, error) {
	if b == nil {
		return handle(ctx, g, operation, req.Method, req.Params)
	}
	switch req.Method {
	case "blob.put":
		if operation != chunkOperation(b.Descriptor, "put", b.Index) {
			return nil, errors.New("chunk operation binding changed")
		}
		return struct{}{}, p.Store.putChunk(g.Peer.ID, *b)
	case "blob.read":
		if operation != chunkOperation(b.Descriptor, "read", b.Index) {
			return nil, errors.New("response chunk operation binding changed")
		}
		data, err := p.Store.readChunk(g.Peer.ID, *b)
		return struct {
			Data []byte `json:"data"`
		}{data}, err
	case "blob.invoke":
		if operation != b.Descriptor.Operation {
			return nil, errors.New("assembled operation binding changed")
		}
		body, err := p.Store.assemble(g.Peer.ID, b.Descriptor)
		if err != nil {
			return nil, err
		}
		return handle(ctx, g, operation, b.Descriptor.Method, body)
	}
	return nil, errors.New("unsupported chunk method")
}
func (p Processor) chunkResult(peer, operation, method string, body json.RawMessage, expires int64) (json.RawMessage, error) {
	if len(body) <= ChunkBytes || method == "blob.read" {
		return body, nil
	}
	if len(body) > MaxObjectBytes {
		return nil, errors.New("relay response exceeds bounded object size")
	}
	d := descriptor(operation, method, "response", body, expires)
	for i := 0; i < d.Parts; i++ {
		if err := p.Store.putChunk(peer, blobRequest{Descriptor: d, Index: i, Data: body[i*ChunkBytes : i*ChunkBytes+d.chunkSize(i)]}); err != nil {
			return nil, err
		}
	}
	return json.Marshal(blobResult{Descriptor: &d})
}

func (s *Service) Call(ctx context.Context, peer, operation, method string, params json.RawMessage) (json.RawMessage, error) {
	grant, err := s.Processor.Store.Grant(ctx, peer)
	if err != nil {
		return nil, err
	}
	if !grant.AllowsSend(method, time.Now()) {
		return nil, ErrRevoked
	}
	if len(params) > MaxObjectBytes {
		return nil, errors.New("relay request exceeds bounded object size")
	}
	wireMethod, wireParams := method, params
	if len(params) > ChunkBytes {
		if grant.Kind != "device" {
			return nil, errors.New("cloud connectors cannot upload requests")
		}
		expires := time.Now().Add(time.Hour).Unix()
		if grant.Expires != 0 {
			expires = min(expires, grant.Expires)
		}
		d := descriptor(operation, method, "request", params, expires)
		// The upload's lease is part of its immutable wire request. Freeze it
		// before sending the first chunk so a restarted sender reuses exact IDs.
		err = s.Processor.Store.withLock(ctx, func() error {
			path := filepath.Join(s.Processor.Store.Directory, "large-intent-"+replyKey(peer, operation, method)+".json")
			old, err := localstate.ReadPrivateFile(path, 8192)
			if err == nil {
				var prior blobDescriptor
				if err = json.Unmarshal(old, &prior); err != nil {
					return err
				}
				if prior.ID != d.ID {
					return errors.New("large operation reused for different input")
				}
				if err = prior.check(time.Now()); err != nil {
					return err
				}
				d = prior
				return nil
			}
			if !os.IsNotExist(err) {
				return err
			}
			return writeJSON(path, d)
		})
		if err != nil {
			return nil, err
		}
		for i := 0; i < d.Parts; i++ {
			b, _ := json.Marshal(blobRequest{Descriptor: d, Index: i, Data: params[i*ChunkBytes : i*ChunkBytes+d.chunkSize(i)]})
			if _, err = s.callSmall(ctx, peer, chunkOperation(d, "put", i), "blob.put", method, b); err != nil {
				return nil, err
			}
		}
		wireMethod = "blob.invoke"
		wireParams, _ = json.Marshal(blobRequest{Descriptor: d})
	}
	result, err := s.callSmall(ctx, peer, operation, wireMethod, method, wireParams)
	if err != nil {
		return nil, err
	}
	var ref blobResult
	if json.Unmarshal(result, &ref) != nil || ref.Descriptor == nil {
		return result, nil
	}
	d := *ref.Descriptor
	if err = d.check(time.Now()); err != nil {
		return nil, err
	}
	if d.Operation != operation || d.Method != method || d.Direction != "response" {
		return nil, errors.New("large reply belongs to another operation")
	}
	body := make([]byte, 0, d.Bytes)
	for i := 0; i < d.Parts; i++ {
		params, _ := json.Marshal(blobRequest{Descriptor: d, Index: i})
		result, err = s.callSmall(ctx, peer, chunkOperation(d, "read", i), "blob.read", method, params)
		if err != nil {
			return nil, err
		}
		var chunk struct {
			Data []byte `json:"data"`
		}
		if err = json.Unmarshal(result, &chunk); err != nil {
			return nil, err
		}
		if len(chunk.Data) != d.chunkSize(i) {
			return nil, errors.New("large reply chunk size changed")
		}
		body = append(body, chunk.Data...)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != d.SHA256 || !json.Valid(body) {
		return nil, errors.New("large relay reply integrity check failed")
	}
	return body, nil
}
