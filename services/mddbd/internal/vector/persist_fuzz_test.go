package vector

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// FuzzDecodeCheckpoint feeds the checkpoint decoder arbitrary files
// (SRCH-023). Whatever the bytes, it returns a checkpoint or an error —
// never a panic — and a checkpoint it accepts can be handed to every index
// without one either.
func FuzzDecodeCheckpoint(f *testing.F) {
	idx := persistIndexes()
	for _, s := range idx {
		for id, v := range persistVecs(24, 4, 31) {
			_ = s.Add("c", id, v)
		}
	}
	for _, s := range idx {
		if tr, ok := s.(Trainable); ok {
			tr.Train("c", persistVecs(24, 4, 31))
		}
	}
	path := filepath.Join(f.TempDir(), "c.ann")
	if _, err := WriteCheckpoint(path, CheckpointMeta{Collection: "c"}, idx); err != nil {
		f.Fatal(err)
	}
	seed, err := os.ReadFile(path) // #nosec G304 -- a test file
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add(seed[:len(seed)/2])
	f.Add([]byte(persistMagic))
	f.Add([]byte{})

	vecs := persistVecs(24, 4, 31)
	f.Fuzz(func(t *testing.T, file []byte) {
		cp, err := decodeCheckpoint(bytes.NewReader(file), int64(len(file)))
		if err != nil {
			return
		}
		for name, s := range persistIndexes() {
			if cp.Has(name) {
				_, _ = cp.Restore(name, s, vecs)
				_ = s.Search("c", vecs["d0"], 5, -1, nil)
			}
		}
	})
}
