package vector

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// The checkpoint file format (SRCH-023): framing, checksums, and what a
// damaged or crafted file is refused for.

// persistFrame wraps payload into a whole checkpoint file of version v.
func persistFrame(v uint32, payload []byte) []byte {
	var out bytes.Buffer
	out.WriteString(persistMagic)
	_ = binary.Write(&out, binary.LittleEndian, v)
	fw := newFrameWriter(&out)
	_, _ = fw.Write(payload)
	_ = fw.close()
	return out.Bytes()
}

// persistPayload builds a payload with the encoder.
func persistPayload(build func(e *persistEncoder)) []byte {
	var b bytes.Buffer
	e := &persistEncoder{w: &b, scratch: make([]byte, 0, 64)}
	build(e)
	return b.Bytes()
}

// persistHead writes the meta every payload starts with.
func persistHead(e *persistEncoder) {
	e.str("c")
	e.str("fp")
	e.u64(1)
	e.u64(2)
}

// persistDecode decodes a whole file held in memory.
func persistDecode(file []byte) (*Checkpoint, error) {
	return decodeCheckpoint(bytes.NewReader(file), int64(len(file)))
}

func TestPersistEmptyCheckpointDecodes(t *testing.T) {
	file := persistFrame(PersistFormatVersion, persistPayload(func(e *persistEncoder) {
		persistHead(e)
		e.u8(persistKindEnd)
	}))
	cp, err := persistDecode(file)
	if err != nil || cp.Meta.Collection != "c" || cp.Meta.Fingerprint != "fp" || cp.Meta.Vectors != 1 || cp.Meta.CreatedUnix != 2 {
		t.Fatalf("%+v %v", cp, err)
	}
}

func TestPersistLargePayloadSpansFrames(t *testing.T) {
	file := persistFrame(PersistFormatVersion, persistPayload(func(e *persistEncoder) {
		persistHead(e)
		// Unknown kinds are refused, so carry the bulk in a graph of one
		// tombstone with a wide vector: 2.5 frames.
		e.u8(persistKindGraph)
		e.str("hnsw")
		e.u32(2)
		e.u32(1)
		dim := (persistFrameSize*5/2)/4 + 1
		e.u32(uint32(dim))
		e.u32(0)
		e.u32(0)
		e.u64(9)
		e.u64(1)
		e.str("k\x001")
		e.u8(0)
		e.f32s(make([]float32, dim))
		e.u8(1)
		e.u16(0)
		e.u8(persistKindEnd)
	}))
	cp, err := persistDecode(file)
	if err != nil || !cp.Has("hnsw") {
		t.Fatalf("%v", err)
	}
}

func TestPersistDamagedFilesAreRefused(t *testing.T) {
	idx := persistIndexes()
	persistFill(t, idx, "c", persistVecs(120, 8, 11))
	_, _, path := persistWrite(t, idx, "c")
	good, err := os.ReadFile(path) // #nosec G304 -- a test file
	if err != nil {
		t.Fatal(err)
	}
	// Every single flipped byte, and every cut, is caught.
	for i := range good {
		bad := bytes.Clone(good)
		bad[i] ^= 0x5a
		if _, err := persistDecode(bad); err == nil {
			t.Fatalf("flipped byte %d was accepted", i)
		}
	}
	for _, n := range []int{0, 5, 12, 13, 20, len(good) / 2, len(good) - 1} {
		if _, err := persistDecode(good[:n]); !errors.Is(err, ErrCheckpointCorrupt) {
			t.Fatalf("cut at %d: %v", n, err)
		}
	}
	if _, err := persistDecode(append(bytes.Clone(good), 0)); !errors.Is(err, ErrCheckpointCorrupt) {
		t.Fatalf("trailing byte: %v", err)
	}
}

func TestPersistOtherVersionIsRefused(t *testing.T) {
	file := persistFrame(PersistFormatVersion+1, persistPayload(persistHead))
	if _, err := persistDecode(file); !errors.Is(err, ErrCheckpointVersion) {
		t.Fatal(err)
	}
}

func TestPersistReadCheckpointErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadCheckpoint(filepath.Join(dir, "none")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := ReadCheckpoint(dir); err == nil {
		t.Fatal("a directory read as a checkpoint")
	}
	junk := filepath.Join(dir, "junk")
	if err := os.WriteFile(junk, []byte("not a checkpoint at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCheckpoint(junk); !errors.Is(err, ErrCheckpointCorrupt) {
		t.Fatal(err)
	}
}

// TestPersistCraftedPayloadsAreRefused feeds streams that pass their
// checksums — what only a crafted file holds — and checks that each is
// refused before anything sized by it is allocated.
func TestPersistCraftedPayloadsAreRefused(t *testing.T) {
	graph := func(m, ef, dim, entry, top uint32, nodes uint64, node func(e *persistEncoder)) func(e *persistEncoder) {
		return func(e *persistEncoder) {
			persistHead(e)
			e.u8(persistKindGraph)
			e.str("hnsw")
			e.u32(m)
			e.u32(ef)
			e.u32(dim)
			e.u32(entry)
			e.u32(top)
			e.u64(1)
			e.u64(nodes)
			if node != nil {
				node(e)
			}
			e.u8(persistKindEnd)
		}
	}
	liveNode := func(key string, layers ...[]int32) func(e *persistEncoder) {
		return func(e *persistEncoder) {
			e.str(key)
			e.u8(1)
			e.u64(7)
			e.u8(uint8(len(layers))) // #nosec G115 -- test graphs have a handful of layers
			for _, l := range layers {
				e.u16(uint16(len(l))) // #nosec G115 -- test layers have a handful of neighbours
				e.i32s(l)
			}
		}
	}
	trained := func(dim uint32, trainedOn uint64, build func(e *persistEncoder)) func(e *persistEncoder) {
		return func(e *persistEncoder) {
			persistHead(e)
			e.u8(persistKindTrained)
			e.str("ivf")
			e.u32(dim)
			e.u64(trainedOn)
			build(e)
			e.u8(persistKindEnd)
		}
	}
	cases := map[string]func(e *persistEncoder){
		"long string": func(e *persistEncoder) { e.u32(persistMaxString + 1) },
		"string past the end": func(e *persistEncoder) {
			e.u32(1000)
		},
		"unknown kind": func(e *persistEncoder) {
			persistHead(e)
			e.u8(9)
			e.str("x")
		},
		"no end": func(e *persistEncoder) {
			persistHead(e)
		},
		"after the end": func(e *persistEncoder) {
			persistHead(e)
			e.u8(persistKindEnd)
			e.u8(1)
		},
		"graph m":            graph(1, 1, 1, 0, 0, 1, nil),
		"graph of 2^40":      graph(2, 1, 1, 0, 0, 1<<40, nil),
		"graph count":        graph(2, 1, 1, 0, 0, 1<<30, nil),
		"graph dimension":    graph(2, 1, 1<<21, 0, 0, 1, nil),
		"node layers":        graph(2, 1, 1, 0, 0, 1, liveNode("a\x001")),
		"node key":           graph(2, 1, 1, 0, 0, 1, liveNode("a", nil)),
		"node neighbours":    graph(2, 1, 1, 0, 0, 1, liveNode("a\x001", []int32{0, 0, 0, 0, 0})),
		"neighbour missing":  graph(2, 1, 1, 0, 0, 1, liveNode("a\x001", []int32{3})),
		"neighbour no layer": graph(2, 1, 1, 0, 1, 1, liveNode("a\x001", []int32{0}, []int32{-1})),
		"entry":              graph(2, 1, 1, 5, 0, 1, liveNode("a\x001", nil)),
		"entry layer":        graph(2, 1, 1, 0, 1, 1, liveNode("a\x001", nil)),
		"layer above entry": graph(2, 1, 1, 0, 0, 2, func(e *persistEncoder) {
			liveNode("a\x001", nil)(e)
			liveNode("b\x002", nil, nil)(e)
		}),
		"node cut": graph(2, 1, 1, 0, 0, 2, liveNode("a\x001", nil)),
		"trained dimension": trained(0, 1, func(e *persistEncoder) {
			e.u32(0)
		}),
		"matrices": trained(1, 1, func(e *persistEncoder) { e.u32(1 << 30) }),
		"empty matrix": trained(1, 1, func(e *persistEncoder) {
			e.u32(1)
			e.u32(0)
			e.u32(1)
		}),
		"huge matrix": trained(1, 1, func(e *persistEncoder) {
			e.u32(1)
			e.u32(1 << 31)
			e.u32(1 << 31)
		}),
		"code width": trained(1, 1, func(e *persistEncoder) {
			e.u32(0)
			e.u32(persistMaxCodeWidth + 1)
			e.u64(0)
		}),
		"entries": trained(1, 1, func(e *persistEncoder) {
			e.u32(0)
			e.u32(2)
			e.u64(1 << 40)
		}),
	}
	for name, build := range cases {
		if _, err := persistDecode(persistFrame(PersistFormatVersion, persistPayload(build))); !errors.Is(err, ErrCheckpointCorrupt) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPersistFrameReaderRefusesBadFrames(t *testing.T) {
	head := func(n, crc uint32) []byte {
		return binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint32(nil, n), crc)
	}
	cases := map[string][]byte{
		"frame too big":    head(persistFrameSize+1, 0),
		"frame past end":   head(100, 0),
		"no crc":           head(4, 0)[:6],
		"short payload":    append(head(4, 0), 1, 2),
		"trailer short":    {0, 0, 0, 0, 1},
		"trailer mismatch": append([]byte{0, 0, 0, 0}, make([]byte, 12)...),
	}
	for name, raw := range cases {
		r := newFrameReader(bytes.NewReader(raw), int64(len(raw))+64)
		if _, err := io.ReadAll(r); !errors.Is(err, ErrCheckpointCorrupt) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// failWriter fails every write after the first ok.
type failWriter struct{ ok int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.ok <= 0 {
		return 0, errors.New("disk full")
	}
	f.ok--
	return len(p), nil
}

func TestPersistFrameWriterKeepsTheFirstError(t *testing.T) {
	for ok := range 3 {
		fw := newFrameWriter(&failWriter{ok: ok})
		_, _ = fw.Write(make([]byte, persistFrameSize+10))
		_, _ = fw.Write([]byte{1})
		if err := fw.close(); err == nil {
			t.Fatalf("ok=%d: no error", ok)
		}
	}
	if _, err := encodeCheckpoint(&failWriter{}, CheckpointMeta{}, nil); err == nil {
		t.Fatal("header write error lost")
	}
}

func TestPersistDecoderReadErrorIsKept(t *testing.T) {
	d := &persistDecoder{r: newFrameReader(&errReader{}, 100)}
	if d.u32() != 0 || !errors.Is(d.err, ErrCheckpointCorrupt) {
		t.Fatalf("%v", d.err)
	}
	d.fail(errors.New("second"))
	if d.err.Error() == "second" {
		t.Fatal("the first error was replaced")
	}
}

// errReader fails with an error that is not the end of the file.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("i/o error") }

func TestPersistFingerprintSeesEveryChange(t *testing.T) {
	a := []float32{1, 2, 3}
	if vectorFingerprint(a) != vectorFingerprint([]float32{1, 2, 3}) {
		t.Fatal("not stable")
	}
	for _, b := range [][]float32{{1, 2, 4}, {1, 2}, {1, 2, 3, 0}, {3, 2, 1}} {
		if vectorFingerprint(a) == vectorFingerprint(b) {
			t.Fatalf("%v and %v collide", a, b)
		}
	}
}
