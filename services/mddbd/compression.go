package main

import (
	"errors"

	"github.com/golang/snappy"
	"github.com/klauspost/compress/zstd"
)

const (
	compressionThresholdSmall  = 1024      // 1KB
	compressionThresholdMedium = 10 * 1024 // 10KB
	flagUncompressed           = byte(0)
	flagSnappy                 = byte(1)
	flagZstd                   = byte(2)
)

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
	
	// Initialize zstd decoder
	zstdDecoder, err = zstd.NewReader(nil)
	if err != nil {
		panic(err)
	}
}

// compressDoc compresses document data with adaptive compression levels
func compressDoc(data []byte) []byte {
	dataLen := len(data)
	
	// Small documents - no compression
	if dataLen < compressionThresholdSmall {
		result := make([]byte, dataLen+1)
		result[0] = flagUncompressed
		copy(result[1:], data)
		return result
	}
	
	// Medium documents (1KB-10KB) - use Snappy (fast)
	if dataLen < compressionThresholdMedium {
		compressed := snappy.Encode(nil, data)
		
		// Only use if beneficial
		if len(compressed) < dataLen {
			result := make([]byte, len(compressed)+1)
			result[0] = flagSnappy
			copy(result[1:], compressed)
			return result
		}
		
		// Compression didn't help
		result := make([]byte, dataLen+1)
		result[0] = flagUncompressed
		copy(result[1:], data)
		return result
	}
	
	// Large documents (>10KB) - use Zstd (high ratio)
	compressed := zstdEncoder.EncodeAll(data, nil)
	
	// Only use if beneficial
	if len(compressed) < dataLen {
		result := make([]byte, len(compressed)+1)
		result[0] = flagZstd
		copy(result[1:], compressed)
		return result
	}
	
	// Compression didn't help
	result := make([]byte, dataLen+1)
	result[0] = flagUncompressed
	copy(result[1:], data)
	return result
}

// decompressDoc decompresses document data with adaptive decompression
func decompressDoc(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty data")
	}

	flag := data[0]
	payload := data[1:]

	switch flag {
	case flagUncompressed:
		return payload, nil
		
	case flagSnappy:
		decompressed, err := snappy.Decode(nil, payload)
		if err != nil {
			return nil, err
		}
		return decompressed, nil
		
	case flagZstd:
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
	compressed := compressDoc(data)
	
	method := "none"
	switch compressed[0] {
	case flagSnappy:
		method = "snappy"
	case flagZstd:
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
