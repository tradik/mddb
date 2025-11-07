package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// DeltaEncoder encodes revisions using delta compression
type DeltaEncoder struct{}

// NewDeltaEncoder creates a new delta encoder
func NewDeltaEncoder() *DeltaEncoder {
	return &DeltaEncoder{}
}

// Encode creates a delta between old and new data
func (de *DeltaEncoder) Encode(oldData, newData []byte) []byte {
	if oldData == nil || len(oldData) == 0 {
		// No base - store full data with flag
		result := make([]byte, len(newData)+1)
		result[0] = 0 // Full data flag
		copy(result[1:], newData)
		return result
	}
	
	// Calculate delta
	delta := de.calculateDelta(oldData, newData)
	
	// Check if delta is smaller than full data
	if len(delta) < len(newData) {
		// Use delta
		result := make([]byte, len(delta)+1)
		result[0] = 1 // Delta flag
		copy(result[1:], delta)
		return result
	}
	
	// Delta not beneficial - store full data
	result := make([]byte, len(newData)+1)
	result[0] = 0 // Full data flag
	copy(result[1:], newData)
	return result
}

// Decode reconstructs data from delta
func (de *DeltaEncoder) Decode(baseData, encodedData []byte) ([]byte, error) {
	if len(encodedData) == 0 {
		return nil, fmt.Errorf("empty encoded data")
	}
	
	flag := encodedData[0]
	data := encodedData[1:]
	
	if flag == 0 {
		// Full data
		return data, nil
	}
	
	// Delta - reconstruct
	if baseData == nil {
		return nil, fmt.Errorf("delta requires base data")
	}
	
	return de.applyDelta(baseData, data)
}

// calculateDelta creates a simple delta encoding
func (de *DeltaEncoder) calculateDelta(oldData, newData []byte) []byte {
	var buf bytes.Buffer
	
	// Write new data length
	binary.Write(&buf, binary.BigEndian, uint32(len(newData)))
	
	// Simple byte-level delta
	// Format: [commonPrefixLen:4][commonSuffixLen:4][middleData]
	
	// Find common prefix
	prefixLen := 0
	minLen := len(oldData)
	if len(newData) < minLen {
		minLen = len(newData)
	}
	
	for prefixLen < minLen && oldData[prefixLen] == newData[prefixLen] {
		prefixLen++
	}
	
	// Find common suffix
	suffixLen := 0
	oldEnd := len(oldData) - 1
	newEnd := len(newData) - 1
	
	for suffixLen < (minLen-prefixLen) && oldData[oldEnd-suffixLen] == newData[newEnd-suffixLen] {
		suffixLen++
	}
	
	// Write lengths
	binary.Write(&buf, binary.BigEndian, uint32(prefixLen))
	binary.Write(&buf, binary.BigEndian, uint32(suffixLen))
	
	// Write middle part (the actual difference)
	middleStart := prefixLen
	middleEnd := len(newData) - suffixLen
	if middleEnd > middleStart {
		buf.Write(newData[middleStart:middleEnd])
	}
	
	return buf.Bytes()
}

// applyDelta reconstructs data from base + delta
func (de *DeltaEncoder) applyDelta(baseData, delta []byte) ([]byte, error) {
	if len(delta) < 12 {
		return nil, fmt.Errorf("invalid delta format")
	}
	
	buf := bytes.NewReader(delta)
	
	// Read new data length
	var newLen uint32
	if err := binary.Read(buf, binary.BigEndian, &newLen); err != nil {
		return nil, err
	}
	
	// Read prefix and suffix lengths
	var prefixLen, suffixLen uint32
	if err := binary.Read(buf, binary.BigEndian, &prefixLen); err != nil {
		return nil, err
	}
	if err := binary.Read(buf, binary.BigEndian, &suffixLen); err != nil {
		return nil, err
	}
	
	// Read middle part
	middleData := make([]byte, buf.Len())
	buf.Read(middleData)
	
	// Reconstruct
	result := make([]byte, 0, newLen)
	
	// Add prefix from base
	if prefixLen > 0 {
		result = append(result, baseData[:prefixLen]...)
	}
	
	// Add middle (changed part)
	result = append(result, middleData...)
	
	// Add suffix from base
	if suffixLen > 0 {
		suffixStart := len(baseData) - int(suffixLen)
		result = append(result, baseData[suffixStart:]...)
	}
	
	return result, nil
}

// Stats calculates compression ratio
func (de *DeltaEncoder) Stats(oldData, newData []byte) (originalSize, deltaSize int, ratio float64) {
	originalSize = len(newData)
	encoded := de.Encode(oldData, newData)
	deltaSize = len(encoded)
	
	if originalSize > 0 {
		ratio = float64(deltaSize) / float64(originalSize)
	}
	
	return
}
