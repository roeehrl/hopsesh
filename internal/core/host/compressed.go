package host

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Agents may keep a session they have not written for a while compressed: Codex rewrites a
// rollout idle for a week as <name>.jsonl.zst (one Zstandard frame that states its size,
// the modification time kept) and, when the thread is resumed, writes <name>.jsonl back and
// removes the .zst. While both exist the plain file is the one in use.
//
// compressedFS shows a machine's files the way the agents see them: a session file by its
// plain name, whichever form it is kept in. Stat, ReadDir, Open and ReadFile give the
// decompressed file (ReadDir lists <name>.jsonl.zst as <name>.jsonl, and leaves it out
// beside a plain file of that name). Writing to a compressed file first turns it back into
// a plain one, with its permissions and modification time, as the agent does on resume;
// renaming, removing or touching it acts on the .zst. So a session keeps one path, which
// lineage files, the journal and the catalog use, as the agent compresses it and back.
// The compressed file itself, named with its .zst, is read and written as it is.
type compressedFS struct{ FS }

// compressedSuffix is what an agent adds to a compressed session file's plain name.
const compressedSuffix = ".zst"

// plainSuffix is the end of a session file's name that may have a compressed form.
const plainSuffix = ".jsonl"

// withCompressed wraps fsys, keeping the optional abilities it has.
func withCompressed(fsys FS) FS {
	c := compressedFS{fsys}
	if ex, ok := fsys.(exclusiveCreator); ok {
		return compressedExclusiveFS{c, ex}
	}
	return c
}

type exclusiveCreator interface {
	CreateExclusive(p string, b []byte, perm fs.FileMode) error
}

type compressedExclusiveFS struct {
	compressedFS
	ex exclusiveCreator
}

func (f compressedExclusiveFS) CreateExclusive(p string, b []byte, perm fs.FileMode) error {
	return f.ex.CreateExclusive(p, b, perm)
}

// FreeSpace passes through when the filesystem can tell.
func (f compressedFS) FreeSpace(p string) (int64, error) {
	if sr, ok := f.FS.(agent.SpaceReporter); ok {
		return sr.FreeSpace(p)
	}
	return 0, agent.ErrUnsupported
}

// packed is the compressed file p stands for: p names a session file that is missing and
// its compressed form exists.
func (f compressedFS) packed(p string) (string, fs.FileInfo, bool) {
	if !strings.HasSuffix(p, plainSuffix) {
		return "", nil, false
	}
	if _, err := f.FS.Stat(p); !errors.Is(err, fs.ErrNotExist) {
		return "", nil, false
	}
	z := p + compressedSuffix
	zi, err := f.FS.Stat(z)
	if err != nil || !zi.Mode().IsRegular() {
		return "", nil, false
	}
	return z, zi, true
}

func (f compressedFS) Stat(p string) (fs.FileInfo, error) {
	fi, err := f.FS.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		if z, zi, ok := f.packed(p); ok {
			return f.info(z, zi), nil
		}
	}
	return fi, err
}

func (f compressedFS) ReadDir(p string) ([]fs.FileInfo, error) {
	entries, err := f.FS.ReadDir(p)
	if err != nil {
		return entries, err
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	out := entries[:0]
	for _, e := range entries {
		if plain, ok := strings.CutSuffix(e.Name(), compressedSuffix); ok && strings.HasSuffix(plain, plainSuffix) && e.Mode().IsRegular() {
			if names[plain] {
				continue // the plain file is the one in use
			}
			e = f.info(joinIn(p, e.Name()), e)
		}
		out = append(out, e)
	}
	return out, nil
}

// joinIn joins a folder and a name in either path form (the remote side may be Windows).
func joinIn(dir, name string) string {
	if strings.HasSuffix(dir, "/") || strings.HasSuffix(dir, `\`) {
		return dir + name
	}
	if strings.Contains(dir, `\`) && !strings.Contains(dir, "/") {
		return dir + `\` + name
	}
	return dir + "/" + name
}

func (f compressedFS) Open(p string) (agent.File, error) {
	file, err := f.FS.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		if z, zi, ok := f.packed(p); ok {
			return f.open(z, zi)
		}
	}
	return file, err
}

func (f compressedFS) ReadFile(p string, limit int64) ([]byte, error) {
	b, err := f.FS.ReadFile(p, limit)
	if !errors.Is(err, fs.ErrNotExist) {
		return b, err
	}
	z, zi, ok := f.packed(p)
	if !ok {
		return b, err
	}
	file, err := f.open(z, zi)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	b, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", p, limit)
	}
	return b, nil
}

func (f compressedFS) RealPath(p string) (string, error) {
	r, err := f.FS.RealPath(p)
	if errors.Is(err, fs.ErrNotExist) {
		if z, _, ok := f.packed(p); ok {
			r, err = f.FS.RealPath(z)
			return strings.TrimSuffix(r, compressedSuffix), err
		}
	}
	return r, err
}

// WriteFile replaces the file; the compressed form it replaces goes.
func (f compressedFS) WriteFile(p string, b []byte, perm fs.FileMode) error {
	z, _, ok := f.packed(p)
	if err := f.FS.WriteFile(p, b, perm); err != nil {
		return err
	}
	if ok {
		return f.FS.Remove(z)
	}
	return nil
}

func (f compressedFS) Append(p string, b []byte, o agent.AppendOptions) error {
	if err := f.unpack(p); err != nil {
		return err
	}
	return f.FS.Append(p, b, o)
}

func (f compressedFS) Truncate(p string, size int64) error {
	if err := f.unpack(p); err != nil {
		return err
	}
	return f.FS.Truncate(p, size)
}

// Rename moves a compressed file as it is, to the compressed form of the new name.
func (f compressedFS) Rename(from, to string) error {
	if z, _, ok := f.packed(from); ok && strings.HasSuffix(to, plainSuffix) {
		return f.FS.Rename(z, to+compressedSuffix)
	}
	return f.FS.Rename(from, to)
}

func (f compressedFS) Remove(p string) error {
	err := f.FS.Remove(p)
	if errors.Is(err, fs.ErrNotExist) {
		if z, _, ok := f.packed(p); ok {
			return f.FS.Remove(z)
		}
	}
	return err
}

func (f compressedFS) Chtimes(p string, t time.Time) error {
	err := f.FS.Chtimes(p, t)
	if errors.Is(err, fs.ErrNotExist) {
		if z, _, ok := f.packed(p); ok {
			return f.FS.Chtimes(z, t)
		}
	}
	return err
}

// unpack turns a compressed file back into the plain one before it is changed, as the
// agent does when it resumes the session: same content, permissions and modification
// time, and the .zst removed.
func (f compressedFS) unpack(p string) error {
	z, zi, ok := f.packed(p)
	if !ok {
		return nil
	}
	file, err := f.open(z, zi)
	if err != nil {
		return err
	}
	b, err := io.ReadAll(file)
	file.Close()
	if err != nil {
		return fmt.Errorf("decompress %s: %w", z, err)
	}
	if _, err := f.FS.Stat(p); err == nil {
		return nil // the agent resumed it meanwhile; its plain file is the one in use
	}
	if err := f.FS.WriteFile(p, b, zi.Mode().Perm()); err != nil {
		return err
	}
	if err := f.FS.Chtimes(p, zi.ModTime()); err != nil {
		return err
	}
	return f.FS.Remove(z)
}

// info is a compressed file as its plain form: the plain name and the decompressed size.
func (f compressedFS) info(z string, zi fs.FileInfo) fs.FileInfo {
	return &plainInfo{FileInfo: zi, fsys: f.FS, z: z}
}

type plainInfo struct {
	fs.FileInfo // the compressed file's
	fsys        FS
	z           string
	once        sync.Once
	size        int64
}

func (i *plainInfo) Name() string { return strings.TrimSuffix(i.FileInfo.Name(), compressedSuffix) }

// Size is the decompressed size, read from the frame header (or by decompressing, for a
// frame that does not state it) when first asked; it falls back to the compressed size.
func (i *plainInfo) Size() int64 {
	i.once.Do(func() { i.size = plainSize(i.fsys, i.z, i.FileInfo) })
	return i.size
}

// plainSizes remembers decompressed sizes by file, size and modification time, so a
// listing reads each compressed file's header once.
var plainSizes sync.Map

type sizeKey struct {
	path string
	size int64
	mod  int64
}

func plainSize(fsys FS, z string, zi fs.FileInfo) int64 {
	key := sizeKey{z, zi.Size(), zi.ModTime().UnixNano()}
	if n, ok := plainSizes.Load(key); ok {
		return n.(int64)
	}
	n, err := decodedSize(fsys, z, zi)
	if err != nil {
		return zi.Size()
	}
	plainSizes.Store(key, n)
	return n
}

func decodedSize(fsys FS, z string, zi fs.FileInfo) (int64, error) {
	file, err := fsys.Open(z)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	head := make([]byte, zstd.HeaderMaxSize)
	n, err := file.ReadAt(head, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	var h zstd.Header
	// A frame that states its size (Codex writes one) needs no decompressing.
	if h.Decode(head[:n]) == nil && h.HasFCS {
		return int64(h.FrameContentSize), nil
	}
	d, err := newDecoder(io.NewSectionReader(file, 0, zi.Size()))
	if err != nil {
		return 0, err
	}
	defer d.Close()
	return io.Copy(io.Discard, d)
}

func newDecoder(r io.Reader) (*zstd.Decoder, error) {
	return zstd.NewReader(r, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
}

// open is a compressed file read as its plain form.
func (f compressedFS) open(z string, zi fs.FileInfo) (agent.File, error) {
	raw, err := f.FS.Open(z)
	if err != nil {
		return nil, err
	}
	return &plainFile{raw: raw, rawSize: zi.Size(), info: f.info(z, zi)}, nil
}

// plainFile reads a compressed file's decompressed bytes. Reads that go forward continue
// the one decompression; one that goes back starts it again.
type plainFile struct {
	mu      sync.Mutex
	raw     agent.File
	rawSize int64
	info    fs.FileInfo
	dec     *zstd.Decoder
	at      int64 // where dec is, in decompressed bytes
	read    int64 // where Read continues
}

func (p *plainFile) Stat() (fs.FileInfo, error) { return p.info, nil }

func (p *plainFile) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, err := p.readAt(b, p.read)
	p.read += int64(n)
	if n > 0 && errors.Is(err, io.EOF) {
		err = nil
	}
	return n, err
}

func (p *plainFile) ReadAt(b []byte, off int64) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.readAt(b, off)
}

func (p *plainFile) readAt(b []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if p.dec == nil || off < p.at {
		src := io.NewSectionReader(p.raw, 0, p.rawSize)
		if p.dec == nil {
			d, err := newDecoder(src)
			if err != nil {
				return 0, err
			}
			p.dec = d
		} else if err := p.dec.Reset(src); err != nil {
			return 0, err
		}
		p.at = 0
	}
	if off > p.at {
		skipped, err := io.CopyN(io.Discard, p.dec, off-p.at)
		p.at += skipped
		if err != nil {
			return 0, eof(err)
		}
	}
	n, err := io.ReadFull(p.dec, b)
	p.at += int64(n)
	return n, eof(err)
}

// eof is io.EOF for a decompression that ended early (a short read at the end).
func eof(err error) error {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return io.EOF
	}
	return err
}

func (p *plainFile) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dec != nil {
		p.dec.Close()
		p.dec = nil
	}
	return p.raw.Close()
}
