package vector

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// What a checkpoint write does when the disk fails it, and the restore
// paths only a damaged section reaches (SRCH-023).

// fakePersistFile is a checkpoint file whose operations fail on request.
type fakePersistFile struct {
	bytes.Buffer
	writeErr, syncErr, closeErr error
}

func (f *fakePersistFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.Buffer.Write(p)
}
func (f *fakePersistFile) Sync() error  { return f.syncErr }
func (f *fakePersistFile) Close() error { return f.closeErr }

// withPersistFile makes every checkpoint write go to f, or fail to open.
func withPersistFile(t *testing.T, f *fakePersistFile, openErr error) {
	t.Helper()
	old := persistCreate
	persistCreate = func(string) (persistFile, error) {
		if openErr != nil {
			return nil, openErr
		}
		return f, nil
	}
	t.Cleanup(func() { persistCreate = old })
}

func TestPersistWriteFailuresLeaveTheOldFile(t *testing.T) {
	idx := persistIndexes()
	persistFill(t, idx, "c", persistVecs(60, 8, 21))
	dir := t.TempDir()
	path := filepath.Join(dir, "c.ann")
	if _, err := WriteCheckpoint(path, CheckpointMeta{Collection: "c"}, idx); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	cases := map[string]*fakePersistFile{
		"write": {writeErr: boom},
		"sync":  {syncErr: boom},
		"close": {closeErr: boom},
	}
	for name, f := range cases {
		withPersistFile(t, f, nil)
		if _, err := WriteCheckpoint(path, CheckpointMeta{Collection: "c"}, idx); !errors.Is(err, boom) {
			t.Errorf("%s: %v", name, err)
		}
	}
	withPersistFile(t, nil, boom)
	if _, err := WriteCheckpoint(path, CheckpointMeta{Collection: "c"}, idx); !errors.Is(err, boom) {
		t.Errorf("open: %v", err)
	}
	persistCreate = func(p string) (persistFile, error) {
		return os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- a test file
	}
	oldRename := persistRename
	persistRename = func(string, string) error { return boom }
	if _, err := WriteCheckpoint(path, CheckpointMeta{Collection: "c"}, idx); !errors.Is(err, boom) {
		t.Errorf("rename: %v", err)
	}
	persistRename = oldRename
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the temporary file was left: %v", err)
	}
	if _, err := ReadCheckpoint(path); err != nil {
		t.Errorf("the previous checkpoint was damaged: %v", err)
	}
	// A directory that cannot be made: its parent is a file.
	if _, err := WriteCheckpoint(filepath.Join(path, "x", "c.ann"), CheckpointMeta{}, idx); err == nil {
		t.Error("a checkpoint under a file was written")
	}
}

func TestPersistReadStatFailure(t *testing.T) {
	idx := persistIndexes()
	persistFill(t, idx, "c", persistVecs(40, 8, 22))
	_, _, path := persistWrite(t, idx, "c")
	old := persistStat
	persistStat = func(*os.File) (os.FileInfo, error) { return nil, io.ErrClosedPipe }
	defer func() { persistStat = old }()
	if _, err := ReadCheckpoint(path); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}

func TestPersistFrameReaderAfterTheEnd(t *testing.T) {
	file := persistFrame(PersistFormatVersion, []byte{1, 2, 3})
	r := newFrameReader(bytes.NewReader(file[12:]), int64(len(file)-12))
	if b, err := io.ReadAll(r); err != nil || len(b) != 3 {
		t.Fatalf("%v %v", b, err)
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestPersistDamagedSectionsAreRefused(t *testing.T) {
	vecs := map[string][]float32{"a": {1, 0}, "b": {0, 1}}
	fp := func(id string) uint64 { return vectorFingerprint(vecs[id]) }
	entries := func(codes ...byte) *trainedSection {
		return &trainedSection{dim: 2, ids: []string{"a", "b"}, hashes: []uint64{fp("a"), fp("b")}, codes: codes}
	}
	ivf := entries(5, 0, 0, 0)
	ivf.codeWidth, ivf.matrices = 2, [][][]float32{{{1, 0}, {0, 1}}}
	ivfDim := entries(0, 0, 0, 0)
	ivfDim.codeWidth, ivfDim.matrices = 2, [][][]float32{{{1, 0, 0}}}
	pq := entries(0, 9)
	pq.codeWidth, pq.matrices = 1, [][][]float32{{{1, 0}}}
	sqRows := entries()
	sqRows.matrices = [][][]float32{{{1, 0}, {1, 0}}, {{1, 0}}, {{1, 0}}}
	sqStale := entries()
	sqStale.hashes = []uint64{1, 2}
	sqStale.matrices = [][][]float32{{{1, 0}}, {{1, 0}}, {{1, 0}}}
	sq4Rows := entries()
	sq4Rows.matrices = [][][]float32{{{1, 0}}, {{1}}}
	sq4Stale := entries()
	sq4Stale.hashes = []uint64{1, 2}
	sq4Stale.matrices = [][][]float32{{{1, 0}}, {{1, 0}}}

	cases := []struct {
		name    string
		idx     Persistable
		section any
		want    error
	}{
		{"ivf cluster", NewIVFIndex(1, 1), ivf, ErrCheckpointCorrupt},
		{"ivf centroid dimension", NewIVFIndex(1, 1), ivfDim, ErrCheckpointCorrupt},
		{"pq codeword", NewPQIndex(1, 4, 1), pq, ErrCheckpointCorrupt},
		{"sq rows", NewSQIndex(), sqRows, ErrCheckpointCorrupt},
		{"sq stale", NewSQIndex(), sqStale, ErrCheckpointStale},
		{"sq4 rows", NewSQ4Index(), sq4Rows, ErrCheckpointCorrupt},
		{"sq4 stale", NewSQ4Index(), sq4Stale, ErrCheckpointStale},
	}
	for _, c := range cases {
		if _, err := c.idx.persistRestore("c", c.section, vecs); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if got := NewSQIndex().Collections(); len(got) != 0 {
		t.Fatalf("a refused section left %v", got)
	}
}

func TestPersistGraphWithTwoLiveNodesForADocument(t *testing.T) {
	v := []float32{1, 0}
	s := &graphSection{dim: 2, keys: []string{"a\x001", "a\x002"}, live: []bool{true, true},
		hashes: []uint64{vectorFingerprint(v), vectorFingerprint(v)}, vecs: make([][]float32, 2)}
	if _, _, err := s.attach(map[string][]float32{"a": v}); !errors.Is(err, ErrCheckpointCorrupt) {
		t.Fatal(err)
	}
}
