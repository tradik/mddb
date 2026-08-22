package compression

import (
	"errors"
	"fmt"

	"github.com/golang/snappy"
	"github.com/klauspost/compress/zstd"
)

const (
	FlagUncompressed = byte(0)
	FlagSnappy       = byte(1)
	FlagZstd         = byte(2)
)

var (
	compressionEnabled         = true
	compressionThresholdSmall  = 1024      // 1KB
	compressionThresholdMedium = 10 * 1024 // 10KB
)

// ConfigureCompression sets compression parameters from config.
func ConfigureCompression(enabled bool, smallThreshold, mediumThreshold int) {
	compressionEnabled = enabled
	if smallThreshold > 0 {
		compressionThresholdSmall = smallThreshold
	}
	if mediumThreshold > 0 {
		compressionThresholdMedium = mediumThreshold
	}
}

var (
	zstdEncoder *zstd.Encoder
	zstdDecoder *zstd.Decoder
)

func init() {
	var err error
	// Initialize zstd encoder (level 3 - balanced)
	zstdEncoder, err = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		panic(err)
	}

	// Initialize zstd decoder with a memory ceiling (TEST-003).
	//
	// DecodeAll with a nil destination allocates whatever the stream asks
	// for, and a zstd bomb is a few kilobytes claiming gigabytes. These
	// bytes are not always ours: a follower decodes what a leader replicated,
	// and loadDoc decodes whatever sits in the database file — including a
	// file restored from a backup someone else produced.
	zstdDecoder, err = zstd.NewReader(nil, zstd.WithDecoderMaxMemory(MaxDecompressedSize))
	if err != nil {
		panic(err)
	}
}

// MaxDecompressedSize caps what a single document may expand to.
//
// A compressed payload states its own decompressed size, and nothing checked
// it: five bytes of snappy header can claim two gigabytes, which the runtime
// then tries to allocate. Uploads are capped at 100 MB, so 256 MB leaves ample
// room for any real document while keeping a crafted one from exhausting the
// process.
//
// This matters most on the replication path, where a follower decodes bytes a
// different machine produced.
const MaxDecompressedSize = 256 << 20

// ErrDecompressedTooLarge reports a payload whose claimed size exceeds the cap.
var ErrDecompressedTooLarge = errors.New("decompressed size over limit")

// CompressDoc compresses document data with adaptive compression levels
func CompressDoc(data []byte) []byte {
	dataLen := len(data)

	// Compression disabled
	if !compressionEnabled {
		result := make([]byte, dataLen+1)
		result[0] = FlagUncompressed
		copy(result[1:], data)
		return result
	}

	// Small documents - no compression
	if dataLen < compressionThresholdSmall {
		result := make([]byte, dataLen+1)
		result[0] = FlagUncompressed
		copy(result[1:], data)
		return result
	}

	// Medium documents (1KB-10KB) - use Snappy (fast)
	if dataLen < compressionThresholdMedium {
		compressed := snappy.Encode(nil, data)

		// Only use if beneficial
		if len(compressed) < dataLen {
			result := make([]byte, len(compressed)+1)
			result[0] = FlagSnappy
			copy(result[1:], compressed)
			return result
		}

		// Compression didn't help
		result := make([]byte, dataLen+1)
		result[0] = FlagUncompressed
		copy(result[1:], data)
		return result
	}

	// Large documents (>10KB) - use Zstd (high ratio)
	compressed := zstdEncoder.EncodeAll(data, nil)

	// Only use if beneficial
	if len(compressed) < dataLen {
		result := make([]byte, len(compressed)+1)
		result[0] = FlagZstd
		copy(result[1:], compressed)
		return result
	}

	// Compression didn't help
	result := make([]byte, dataLen+1)
	result[0] = FlagUncompressed
	copy(result[1:], data)
	return result
}

// DecompressDoc decompresses document data with adaptive decompression
func DecompressDoc(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty data")
	}

	flag := data[0]
	payload := data[1:]

	switch flag {
	case FlagUncompressed:
		return payload, nil

	case FlagSnappy:
		// snappy.Decode allocates from a length prefix in the payload before
		// reading any of it: five bytes can claim two gigabytes. DecodedLen
		// reports that claim without allocating, which is the only cheap
		// place to refuse it.
		claimed, err := snappy.DecodedLen(payload)
		if err != nil {
			return nil, err
		}
		if claimed < 0 || uint64(claimed) > MaxDecompressedSize { // #nosec G115 -- negative rejected on the same line
			return nil, fmt.Errorf("%w: snappy payload claims %d bytes, limit is %d",
				ErrDecompressedTooLarge, claimed, MaxDecompressedSize)
		}
		decompressed, err := snappy.Decode(nil, payload)
		if err != nil {
			return nil, err
		}
		return decompressed, nil

	case FlagZstd:
		decompressed, err := zstdDecoder.DecodeAll(payload, nil)
		if err != nil {
			return nil, err
		}
		return decompressed, nil

	default:
		// No flag - assume old format (uncompressed)
		return data, nil
	}
}

// CompressionStats returns compression statistics
type CompressionStats struct {
	OriginalSize   int
	CompressedSize int
	Ratio          float64
	Method         string
}

// GetCompressionStats analyzes compression for data
func GetCompressionStats(data []byte) CompressionStats {
	compressed := CompressDoc(data)

	method := "none"
	switch compressed[0] {
	case FlagSnappy:
		method = "snappy"
	case FlagZstd:
		method = "zstd"
	}

	ratio := 1.0
	if len(data) > 0 {
		ratio = float64(len(compressed)) / float64(len(data))
	}

	return CompressionStats{
		OriginalSize:   len(data),
		CompressedSize: len(compressed),
		Ratio:          ratio,
		Method:         method,
	}
}
