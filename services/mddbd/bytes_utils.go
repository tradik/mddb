package main

import (
	"bytes"
)

// BytesSplit splits bytes without allocating strings
// Returns indices of parts, not copies
func BytesSplit(data []byte, sep byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	
	// Count separators
	n := 1
	for i := 0; i < len(data); i++ {
		if data[i] == sep {
			n++
		}
	}
	
	// Allocate result slice once
	result := make([][]byte, 0, n)
	start := 0
	
	for i := 0; i < len(data); i++ {
		if data[i] == sep {
			result = append(result, data[start:i])
			start = i + 1
		}
	}
	
	// Add last part
	result = append(result, data[start:])
	
	return result
}

// BytesHasPrefix checks if bytes has prefix without string conversion
func BytesHasPrefix(b, prefix []byte) bool {
	return len(b) >= len(prefix) && bytes.Equal(b[:len(prefix)], prefix)
}

// BytesIndexByte finds first occurrence of byte
func BytesIndexByte(b []byte, c byte) int {
	for i := 0; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// BytesLastIndexByte finds last occurrence of byte
func BytesLastIndexByte(b []byte, c byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == c {
			return i
		}
	}
	return -1
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

// AppendBytes appends bytes without intermediate allocations
func AppendBytes(dst []byte, parts ...[]byte) []byte {
	// Calculate total size
	totalSize := len(dst)
	for _, part := range parts {
		totalSize += len(part)
	}
	
	// Grow if needed
	if cap(dst) < totalSize {
		newDst := make([]byte, len(dst), totalSize)
		copy(newDst, dst)
		dst = newDst
	}
	
	// Append all parts
	for _, part := range parts {
		dst = append(dst, part...)
	}
	
	return dst
}

// BytesToLower converts bytes to lowercase in-place
func BytesToLower(b []byte) {
	for i := 0; i < len(b); i++ {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
}

// CompareBytes compares two byte slices
func CompareBytes(a, b []byte) int {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	
	for i := 0; i < minLen; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	
	return 0
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
