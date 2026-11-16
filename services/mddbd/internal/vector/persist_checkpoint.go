package vector

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Writing and reading one collection's checkpoint (SRCH-023).
//
// A checkpoint is never the truth. It is written from the indexes as they
// are, some time after the vectors they were built from were stored, and a
// process can die between the two. So nothing in it is trusted because of
// when it was written: at load, every structure is matched vector by vector
// against what the store holds (see vectorFingerprint), the vectors the
// checkpoint does not know are added the usual way, and a structure that
// cannot be matched is dropped and built again. That is also what makes a
// kill during a checkpoint harmless — the file is replaced by a rename, so
// the old checkpoint is still whole, only older.

// Persistable is an index whose built structure can be checkpointed: the
// HNSW graph, and the indexes that train a structure (IVF, PQ, OPQ, SQ,
// SQ4). The flat and binary indexes have nothing to keep — they are their
// vectors — and are filled from the store as before.
type Persistable interface {
	VectorSearcher
	// PersistStamp changes whenever the collection's structure does, so a
	// caller can tell whether a checkpoint written earlier is still current.
	// It only ever grows while the collection exists.
	PersistStamp(collection string) uint64
	// persistKind is the section layout the index writes.
	persistKind() byte
	// persistSnapshot captures the collection's structure under the index's
	// read lock and returns what to write once the lock is released, or nil
	// when there is nothing built.
	persistSnapshot(collection string) persistSnapshot
	// persistRestore replaces the collection with a decoded section,
	// matched against the stored vectors. It returns the stored vectors the
	// section did not cover — the caller adds those — or why the section
	// cannot be used, in which case the index is left as it was.
	persistRestore(collection string, section any, vectors map[string][]float32) ([]string, error)
}

// persistSnapshot is one index's structure, captured and ready to write.
type persistSnapshot interface {
	encode(e *persistEncoder)
}

// Section layouts.
const (
	persistKindEnd     byte = 0
	persistKindGraph   byte = 1
	persistKindTrained byte = 2
)

// CheckpointMeta says what a checkpoint was built from.
type CheckpointMeta struct {
	// Collection is the collection the structures belong to.
	Collection string
	// Fingerprint identifies how the vectors were produced — the embedding
	// model and its variant. A checkpoint with another one is not used.
	Fingerprint string
	// Vectors is how many vectors the collection held, for the operator.
	Vectors uint64
	// CreatedUnix is when the checkpoint was written.
	CreatedUnix int64
}

// Checkpoint is a decoded, verified checkpoint file.
type Checkpoint struct {
	Meta     CheckpointMeta
	sections map[string]any // by index name: *graphSection or *trainedSection
}

// CheckpointInfo describes a checkpoint just written.
type CheckpointInfo struct {
	// Sections are the indexes that had a structure to write.
	Sections []string
	// Bytes is the size of the file; zero when nothing was written.
	Bytes int64
}

// The file operations of a checkpoint, variables so a test can fail each.
var (
	persistCreate = func(path string) (persistFile, error) {
		return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- derived from the operator's database path
	}
	persistRename = os.Rename
	persistStat   = func(f *os.File) (os.FileInfo, error) { return f.Stat() }
)

// persistFile is what a checkpoint is written to.
type persistFile interface {
	io.Writer
	Sync() error
	Close() error
}

// WriteCheckpoint writes the structures the persistable indexes among
// searchers hold for meta.Collection to path, atomically: a temporary file
// next to it, synced, then renamed over it. A reader sees the previous
// checkpoint or this one, never part of either.
//
// Each index is captured under its own read lock and written after the lock
// is released, one index at a time, so searches go on and the memory held
// is one index's capture — slice headers, about 70 bytes per vector — plus
// one frame.
func WriteCheckpoint(path string, meta CheckpointMeta, searchers map[string]VectorSearcher) (CheckpointInfo, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return CheckpointInfo{}, err
	}
	tmp := path + ".tmp"
	f, err := persistCreate(tmp)
	if err != nil {
		return CheckpointInfo{}, err
	}
	counter := &countingWriter{w: bufio.NewWriterSize(f, 1<<16)}
	sections, err := encodeCheckpoint(counter, meta, searchers)
	if err == nil {
		err = counter.w.Flush()
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && len(sections) > 0 {
		err = persistRename(tmp, path)
	}
	if err != nil || len(sections) == 0 {
		_ = os.Remove(tmp)
		return CheckpointInfo{}, err
	}
	syncDir(filepath.Dir(path))
	return CheckpointInfo{Sections: sections, Bytes: counter.n}, nil
}

// syncDir makes a rename in dir durable. Best effort: a directory cannot be
// opened for syncing on every platform, and a rename that is lost with the
// machine leaves the previous checkpoint, which is still a valid one.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil { // #nosec G304 -- derived from the operator's database path
		_ = d.Sync()
		_ = d.Close()
	}
}

// countingWriter counts what a checkpoint wrote.
type countingWriter struct {
	w *bufio.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// encodeCheckpoint writes the whole file to w and returns the sections it
// wrote, in name order so the same indexes always write the same file.
func encodeCheckpoint(w io.Writer, meta CheckpointMeta, searchers map[string]VectorSearcher) ([]string, error) {
	head := binary.LittleEndian.AppendUint32([]byte(persistMagic), PersistFormatVersion)
	if _, err := w.Write(head); err != nil {
		return nil, err
	}
	frames := newFrameWriter(w)
	e := &persistEncoder{w: frames, scratch: make([]byte, 0, 64)}
	e.str(meta.Collection)
	e.str(meta.Fingerprint)
	e.u64(meta.Vectors)
	e.u64(uint64(meta.CreatedUnix)) // #nosec G115 -- a timestamp, read back the same way

	names := make([]string, 0, len(searchers))
	for name := range searchers {
		names = append(names, name)
	}
	sort.Strings(names)
	var written []string
	for _, name := range names {
		p, ok := searchers[name].(Persistable)
		if !ok {
			continue
		}
		snap := p.persistSnapshot(meta.Collection)
		if snap == nil {
			continue
		}
		e.u8(p.persistKind())
		e.str(name)
		snap.encode(e)
		written = append(written, name)
	}
	e.u8(persistKindEnd)
	return written, frames.close()
}

// ReadCheckpoint reads and verifies the checkpoint at path. Any error means
// the file must not be used: os.ErrNotExist when there is none,
// ErrCheckpointVersion for another format, ErrCheckpointCorrupt otherwise.
func ReadCheckpoint(path string) (*Checkpoint, error) {
	f, err := os.Open(path) // #nosec G304 -- derived from the operator's database path
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := persistStat(f)
	if err != nil {
		return nil, err
	}
	return decodeCheckpoint(bufio.NewReaderSize(f, 1<<16), st.Size())
}

// decodeCheckpoint decodes a checkpoint of size bytes from r.
func decodeCheckpoint(r io.Reader, size int64) (*Checkpoint, error) {
	head := make([]byte, len(persistMagic)+4)
	if _, err := io.ReadFull(r, head); err != nil || string(head[:len(persistMagic)]) != persistMagic {
		return nil, corruptf("not an index checkpoint")
	}
	if v := binary.LittleEndian.Uint32(head[len(persistMagic):]); v != PersistFormatVersion {
		return nil, fmt.Errorf("%w: %d, this build reads %d", ErrCheckpointVersion, v, PersistFormatVersion)
	}
	d := &persistDecoder{r: newFrameReader(r, size-int64(len(head)))}
	cp := &Checkpoint{sections: map[string]any{}}
	cp.Meta.Collection = d.str()
	cp.Meta.Fingerprint = d.str()
	cp.Meta.Vectors = d.u64()
	cp.Meta.CreatedUnix = int64(d.u64()) // #nosec G115 -- written from an int64
	for d.err == nil {
		kind := d.u8()
		if kind == persistKindEnd {
			break
		}
		name := d.str()
		switch kind {
		case persistKindGraph:
			cp.sections[name] = decodeGraphSection(d)
		case persistKindTrained:
			cp.sections[name] = decodeTrainedSection(d)
		default:
			d.fail(corruptf("section of unknown kind %d", kind))
		}
	}
	if d.err == nil {
		// Reading on must find the trailer and the end of the file: a
		// stream that goes on after its last section is not one we wrote.
		if _, err := d.r.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
			d.fail(corruptf("data after the last section"))
		}
	}
	if d.err != nil {
		return nil, d.err
	}
	return cp, nil
}

// Has reports whether the checkpoint holds a structure for the named index.
func (cp *Checkpoint) Has(name string) bool {
	_, ok := cp.sections[name]
	return ok
}

// Restore gives the named index its structure from the checkpoint, matched
// against vectors — everything the store holds for the collection. It
// returns the vectors the checkpoint did not cover, for the caller to add,
// or why the index has to be built the usual way.
func (cp *Checkpoint) Restore(name string, searcher VectorSearcher, vectors map[string][]float32) ([]string, error) {
	p, ok := searcher.(Persistable)
	if !ok {
		return nil, fmt.Errorf("the %s index keeps no checkpoint", name)
	}
	section, ok := cp.sections[name]
	if !ok {
		return nil, fmt.Errorf("%w: no %s section", ErrCheckpointStale, name)
	}
	return p.persistRestore(cp.Meta.Collection, section, vectors)
}

// missingFrom lists the stored vectors an index did not take from a
// checkpoint, sorted so they are added in the same order every start.
func missingFrom(vectors, covered map[string][]float32) []string {
	var missing []string
	for id := range vectors {
		if _, ok := covered[id]; !ok {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	return missing
}
