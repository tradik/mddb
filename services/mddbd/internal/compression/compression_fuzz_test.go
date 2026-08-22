package compression

import (
	"bytes"
	"errors"
	"testing"
)

// TEST-003. DecompressDoc is handed bytes from the database file and, on a
// follower, bytes a different machine replicated. A compressed payload states
// its own decompressed size, so the decoder must refuse an absurd claim rather
// than try to allocate it.

func FuzzDecompressDoc(f *testing.F) {
	f.Add(CompressDoc([]byte("a short document")))
	f.Add(CompressDoc(bytes.Repeat([]byte("compressible "), 4096)))
	f.Add(CompressDoc(nil))
	f.Add([]byte{})
	f.Add([]byte{FlagUncompressed})
	f.Add([]byte{FlagSnappy})
	// The bomb: five bytes of snappy header claiming two gigabytes.
	f.Add([]byte{FlagSnappy, 0xff, 0xff, 0xff, 0xff, 0x07})
	f.Add([]byte{FlagZstd, 0x28, 0xb5, 0x2f, 0xfd})
	f.Add([]byte{0xfe, 1, 2, 3})

	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := DecompressDoc(data)
		if err != nil {
			return
		}
		// Whatever decoded must respect the cap; otherwise the ceiling is
		// decorative.
		if uint64(len(out)) > MaxDecompressedSize {
			t.Fatalf("decompressed %d bytes, over the %d limit", len(out), MaxDecompressedSize)
		}
	})
}

// What is compressed must decompress to itself. A codec that loses bytes loses
// documents, and the loss would only surface when someone reads an old one.
func FuzzCompressRoundTrip(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte("x"), 100_000))
	f.Add([]byte{0, 1, 2, 255, 254})

	f.Fuzz(func(t *testing.T, data []byte) {
		// The fuzzer should explore encodings, not memory pressure.
		if len(data) > 1<<20 {
			return
		}

		got, err := DecompressDoc(CompressDoc(data))
		if err != nil {
			t.Fatalf("data this codec compressed does not decompress: %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("round trip changed the bytes: %d in, %d out", len(data), len(got))
		}
	})
}

// The cap must actually bite, and say why.
func TestDecompressRefusesABomb(t *testing.T) {
	bomb := []byte{FlagSnappy, 0xff, 0xff, 0xff, 0xff, 0x07} // claims ~2 GB

	out, err := DecompressDoc(bomb)
	if err == nil {
		t.Fatalf("a payload claiming 2 GB was accepted, returning %d bytes", len(out))
	}
	if !errors.Is(err, ErrDecompressedTooLarge) {
		t.Errorf("error = %v, want ErrDecompressedTooLarge", err)
	}
}
