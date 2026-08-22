package main

import (
	"bytes"
)

// BytesHasPrefix checks if bytes has prefix without string conversion
func BytesHasPrefix(b, prefix []byte) bool {
	return len(b) >= len(prefix) && bytes.Equal(b[:len(prefix)], prefix)
}

// ExtractPart extracts Nth part from pipe-separated bytes
// Returns nil if part doesn't exist
func ExtractPart(data []byte, partIndex int) []byte {
	if len(data) == 0 {
		return nil
	}

	currentPart := 0
	start := 0

	for i := 0; i < len(data); i++ {
		if data[i] == '|' {
			if currentPart == partIndex {
				return data[start:i]
			}
			currentPart++
			start = i + 1
		}
	}

	// Last part (no trailing |)
	if currentPart == partIndex {
		return data[start:]
	}

	return nil
}

// FormatTimestamp formats int64 as 20-digit zero-padded bytes
// Optimized version without fmt.Sprintf
func FormatTimestamp(timestamp int64, buf []byte) []byte {
	if len(buf) < 20 {
		buf = make([]byte, 20)
	}

	// Convert to string representation
	digits := make([]byte, 0, 20)
	n := timestamp

	if n == 0 {
		for i := 0; i < 20; i++ {
			buf[i] = '0'
		}
		return buf[:20]
	}

	// Extract digits
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}

	// Reverse and pad
	padding := 20 - len(digits)
	for i := 0; i < padding; i++ {
		buf[i] = '0'
	}

	for i := 0; i < len(digits); i++ {
		buf[padding+i] = digits[len(digits)-1-i]
	}

	return buf[:20]
}

// CopyBytes makes a copy of bytes
func CopyBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}
