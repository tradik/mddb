package main

import (
	"errors"

	"github.com/golang/snappy"
)

const (
	compressionThreshold = 1024 // Compress documents larger than 1KB
	flagUncompressed     = byte(0)
	flagCompressed       = byte(1)
)

// compressDoc compresses document data if it's larger than threshold
func compressDoc(data []byte) []byte {
	if len(data) < compressionThreshold {
		// Small document - don't compress
		result := make([]byte, len(data)+1)
		result[0] = flagUncompressed
		copy(result[1:], data)
		return result
	}

	// Large document - compress with snappy
	compressed := snappy.Encode(nil, data)
	
	// Only use compression if it actually reduces size
	if len(compressed) >= len(data) {
		// Compression didn't help
		result := make([]byte, len(data)+1)
		result[0] = flagUncompressed
		copy(result[1:], data)
		return result
	}

	// Compression helped
	result := make([]byte, len(compressed)+1)
	result[0] = flagCompressed
	copy(result[1:], compressed)
	return result
}

// decompressDoc decompresses document data if it was compressed
func decompressDoc(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty data")
	}

	flag := data[0]
	payload := data[1:]

	switch flag {
	case flagUncompressed:
		return payload, nil
	case flagCompressed:
		decompressed, err := snappy.Decode(nil, payload)
		if err != nil {
			return nil, err
		}
		return decompressed, nil
	default:
		// No flag - assume old format (uncompressed)
		return data, nil
	}
}
