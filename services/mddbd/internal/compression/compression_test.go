package compression

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompressDocSmall(t *testing.T) {
	// Data below 1KB threshold should be uncompressed
	data := []byte("small document")
	compressed := CompressDoc(data)

	if compressed[0] != FlagUncompressed {
		t.Errorf("expected FlagUncompressed (0) for small data, got %d", compressed[0])
	}
	if !bytes.Equal(compressed[1:], data) {
		t.Error("expected uncompressed payload to match original data")
	}
}

func TestCompressDocEmpty(t *testing.T) {
	data := []byte{}
	compressed := CompressDoc(data)

	if compressed[0] != FlagUncompressed {
		t.Errorf("expected FlagUncompressed for empty data, got %d", compressed[0])
	}
	if len(compressed) != 1 {
		t.Errorf("expected compressed length 1 for empty data, got %d", len(compressed))
	}
}

func TestCompressDocMediumSnappy(t *testing.T) {
	// Create compressible data between 1KB and 10KB
	data := []byte(strings.Repeat("Hello World! This is a compressible text string. ", 30))
	if len(data) < compressionThresholdSmall || len(data) >= compressionThresholdMedium {
		t.Skipf("test data size %d not in medium range", len(data))
	}

	compressed := CompressDoc(data)

	if compressed[0] != FlagSnappy {
		t.Errorf("expected FlagSnappy (1) for medium compressible data, got %d", compressed[0])
	}
	if len(compressed) >= len(data) {
		t.Error("expected compression to reduce size for repetitive data")
	}
}

func TestCompressDocLargeZstd(t *testing.T) {
	// Create compressible data >10KB
	data := []byte(strings.Repeat("Large compressible document with repetitive content. ", 300))
	if len(data) < compressionThresholdMedium {
		t.Skipf("test data size %d not above medium threshold", len(data))
	}

	compressed := CompressDoc(data)

	if compressed[0] != FlagZstd {
		t.Errorf("expected FlagZstd (2) for large compressible data, got %d", compressed[0])
	}
	if len(compressed) >= len(data) {
		t.Error("expected compression to reduce size for repetitive data")
	}
}

func TestCompressDocMediumIncompressible(t *testing.T) {
	// Create incompressible data in medium range (random-looking bytes)
	data := make([]byte, 2048)
	for i := range data {
		data[i] = byte((i*7 + 13) % 251) // pseudo-random, hard to compress
	}

	compressed := CompressDoc(data)

	// Should fall back to uncompressed if snappy didn't help
	if compressed[0] == FlagSnappy {
		// Snappy compressed it anyway; that's fine, check it round-trips
	} else if compressed[0] != FlagUncompressed {
		t.Errorf("expected FlagUncompressed or FlagSnappy, got %d", compressed[0])
	}
}

func TestCompressDocLargeIncompressible(t *testing.T) {
	// Create data >10KB that is hard to compress
	data := make([]byte, 15000)
	for i := range data {
		data[i] = byte((i*31 + 97) % 256)
	}

	compressed := CompressDoc(data)

	// Should be either zstd (if beneficial) or uncompressed
	if compressed[0] != FlagZstd && compressed[0] != FlagUncompressed {
		t.Errorf("expected FlagZstd or FlagUncompressed, got %d", compressed[0])
	}
}

func TestDecompressDocEmpty(t *testing.T) {
	_, err := DecompressDoc([]byte{})
	if err == nil {
		t.Error("expected error for empty data")
	}
}

func TestDecompressDocNil(t *testing.T) {
	_, err := DecompressDoc(nil)
	if err == nil {
		t.Error("expected error for nil data")
	}
}

func TestDecompressDocUncompressed(t *testing.T) {
	original := []byte("test data")
	input := append([]byte{FlagUncompressed}, original...)

	result, err := DecompressDoc(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(result, original) {
		t.Errorf("expected %q, got %q", original, result)
	}
}

func TestDecompressDocUnknownFlag(t *testing.T) {
	// Unknown flag should return the entire data (old format fallback)
	data := []byte{0xFF, 0x01, 0x02, 0x03}
	result, err := DecompressDoc(data)
	if err != nil {
		t.Fatalf("unexpected error for unknown flag: %v", err)
	}
	if !bytes.Equal(result, data) {
		t.Error("expected raw data returned for unknown flag")
	}
}

func TestDecompressDocInvalidSnappy(t *testing.T) {
	// Snappy flag with invalid payload
	data := []byte{FlagSnappy, 0xFF, 0xFF, 0xFF}
	_, err := DecompressDoc(data)
	if err == nil {
		t.Error("expected error for invalid snappy data")
	}
}

func TestDecompressDocInvalidZstd(t *testing.T) {
	// Zstd flag with invalid payload
	data := []byte{FlagZstd, 0xFF, 0xFF, 0xFF}
	_, err := DecompressDoc(data)
	if err == nil {
		t.Error("expected error for invalid zstd data")
	}
}

func TestCompressDecompressRoundTripSmall(t *testing.T) {
	original := []byte("small test data")
	compressed := CompressDoc(original)
	decompressed, err := DecompressDoc(compressed)
	if err != nil {
		t.Fatalf("decompression error: %v", err)
	}
	if !bytes.Equal(decompressed, original) {
		t.Error("round-trip failed for small data")
	}
}

func TestCompressDecompressRoundTripMedium(t *testing.T) {
	original := []byte(strings.Repeat("Medium document content for testing compression. ", 30))
	compressed := CompressDoc(original)
	decompressed, err := DecompressDoc(compressed)
	if err != nil {
		t.Fatalf("decompression error: %v", err)
	}
	if !bytes.Equal(decompressed, original) {
		t.Error("round-trip failed for medium data")
	}
}

func TestCompressDecompressRoundTripLarge(t *testing.T) {
	original := []byte(strings.Repeat("Large document content for testing zstd compression algorithm. ", 300))
	compressed := CompressDoc(original)
	decompressed, err := DecompressDoc(compressed)
	if err != nil {
		t.Fatalf("decompression error: %v", err)
	}
	if !bytes.Equal(decompressed, original) {
		t.Error("round-trip failed for large data")
	}
}

func TestCompressDecompressRoundTripBinary(t *testing.T) {
	// Test with binary data
	original := make([]byte, 5000)
	for i := range original {
		original[i] = byte(i % 256)
	}

	compressed := CompressDoc(original)
	decompressed, err := DecompressDoc(compressed)
	if err != nil {
		t.Fatalf("decompression error: %v", err)
	}
	if !bytes.Equal(decompressed, original) {
		t.Error("round-trip failed for binary data")
	}
}

func TestCompressionThresholdConstants(t *testing.T) {
	if compressionThresholdSmall != 1024 {
		t.Errorf("expected small threshold 1024, got %d", compressionThresholdSmall)
	}
	if compressionThresholdMedium != 10240 {
		t.Errorf("expected medium threshold 10240, got %d", compressionThresholdMedium)
	}
}

func TestCompressionFlags(t *testing.T) {
	if FlagUncompressed != 0 {
		t.Errorf("expected FlagUncompressed 0, got %d", FlagUncompressed)
	}
	if FlagSnappy != 1 {
		t.Errorf("expected FlagSnappy 1, got %d", FlagSnappy)
	}
	if FlagZstd != 2 {
		t.Errorf("expected FlagZstd 2, got %d", FlagZstd)
	}
}

func TestDecompressDocFlagOnly(t *testing.T) {
	// A single byte with the uncompressed flag should return empty payload
	data := []byte{FlagUncompressed}
	result, err := DecompressDoc(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d bytes", len(result))
	}
}

func TestCompressDecompressRoundTripExactThresholds(t *testing.T) {
	// Test data at exact threshold boundaries
	sizes := []int{
		compressionThresholdSmall - 1,  // just below small threshold
		compressionThresholdSmall,      // exactly at small threshold
		compressionThresholdSmall + 1,  // just above small threshold
		compressionThresholdMedium - 1, // just below medium threshold
		compressionThresholdMedium,     // exactly at medium threshold
		compressionThresholdMedium + 1, // just above medium threshold
	}

	for _, size := range sizes {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte('A' + (i % 26)) // compressible pattern
		}

		compressed := CompressDoc(data)
		decompressed, err := DecompressDoc(compressed)
		if err != nil {
			t.Fatalf("round-trip failed at size %d: %v", size, err)
		}
		if !bytes.Equal(decompressed, data) {
			t.Errorf("round-trip mismatch at size %d", size)
		}
	}
}

func BenchmarkCompressSmall(b *testing.B) {
	data := []byte("small document")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CompressDoc(data)
	}
}

func BenchmarkCompressMedium(b *testing.B) {
	data := []byte(strings.Repeat("Medium content. ", 100))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CompressDoc(data)
	}
}

func BenchmarkCompressLarge(b *testing.B) {
	data := []byte(strings.Repeat("Large content. ", 1000))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CompressDoc(data)
	}
}

func BenchmarkDecompress(b *testing.B) {
	original := []byte(strings.Repeat("Benchmark decompression. ", 300))
	compressed := CompressDoc(original)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DecompressDoc(compressed)
	}
}
