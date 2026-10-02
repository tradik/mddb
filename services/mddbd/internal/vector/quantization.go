package vector

import (
	"fmt"
	"math"
)

// QuantizationType defines the vector quantization level.
type QuantizationType string

const (
	QuantNone QuantizationType = "float32" // no quantization, full precision
	QuantInt8 QuantizationType = "int8"    // 4x compression
	QuantInt4 QuantizationType = "int4"    // 8x compression
)

// ParseQuantization parses a quantization type string. Returns QuantNone for unknown values.
func ParseQuantization(s string) QuantizationType {
	switch s {
	case "int8":
		return QuantInt8
	case "int4":
		return QuantInt4
	case "float32", "":
		return QuantNone
	default:
		return QuantNone
	}
}

// QuantizedVector holds a quantized vector with calibration params for dequantization.
// For int8: each dimension is mapped to [0,255] using min/scale.
// For int4: each dimension is mapped to [0,15], packed 2 per byte.
type QuantizedVector struct {
	Type QuantizationType
	Min  float32 // minimum value in original vector
	Max  float32 // maximum value in original vector
	Data []byte  // quantized data
	Dims int     // original dimension count
}

// QuantizeFloat32 quantizes a float32 vector to the specified type.
func QuantizeFloat32(vec []float32, qt QuantizationType) *QuantizedVector {
	switch qt {
	case QuantInt8:
		return quantizeInt8(vec)
	case QuantInt4:
		return quantizeInt4(vec)
	default:
		return nil // no quantization needed
	}
}

// DequantizeToFloat32 restores a quantized vector back to float32.
func DequantizeToFloat32(qv *QuantizedVector) []float32 {
	switch qv.Type {
	case QuantInt8:
		return dequantizeInt8(qv)
	case QuantInt4:
		return dequantizeInt4(qv)
	default:
		return nil
	}
}

// --- INT8 Scalar Quantization ---
// Maps [min, max] → [0, 255]

func quantizeInt8(vec []float32) *QuantizedVector {
	if len(vec) == 0 {
		return &QuantizedVector{Type: QuantInt8, Dims: 0}
	}

	minVal, maxVal := vec[0], vec[0]
	for _, v := range vec[1:] {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}

	scale := maxVal - minVal
	if scale == 0 {
		scale = 1 // avoid div by zero for constant vectors
	}

	data := make([]byte, len(vec))
	for i, v := range vec {
		normalized := (v - minVal) / scale // [0, 1]
		quantized := math.Round(float64(normalized) * 255.0)
		if quantized < 0 {
			quantized = 0
		}
		if quantized > 255 {
			quantized = 255
		}
		data[i] = byte(quantized)
	}

	return &QuantizedVector{
		Type: QuantInt8,
		Min:  minVal,
		Max:  maxVal,
		Data: data,
		Dims: len(vec),
	}
}

func dequantizeInt8(qv *QuantizedVector) []float32 {
	if len(qv.Data) < qv.Dims {
		return nil
	}
	scale := qv.Max - qv.Min
	vec := make([]float32, qv.Dims)
	for i := 0; i < qv.Dims; i++ {
		vec[i] = qv.Min + (float32(qv.Data[i])/255.0)*scale
	}
	return vec
}

// --- INT4 Scalar Quantization ---
// Maps [min, max] → [0, 15], 2 values packed per byte (high nibble first)

func quantizeInt4(vec []float32) *QuantizedVector {
	if len(vec) == 0 {
		return &QuantizedVector{Type: QuantInt4, Dims: 0}
	}

	minVal, maxVal := vec[0], vec[0]
	for _, v := range vec[1:] {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}

	scale := maxVal - minVal
	if scale == 0 {
		scale = 1
	}

	// Pack 2 values per byte
	byteLen := (len(vec) + 1) / 2
	data := make([]byte, byteLen)

	for i, v := range vec {
		normalized := (v - minVal) / scale
		quantized := math.Round(float64(normalized) * 15.0)
		if quantized < 0 {
			quantized = 0
		}
		if quantized > 15 {
			quantized = 15
		}
		q := byte(quantized)

		byteIdx := i / 2
		if i%2 == 0 {
			data[byteIdx] = q << 4 // high nibble
		} else {
			data[byteIdx] |= q // low nibble
		}
	}

	return &QuantizedVector{
		Type: QuantInt4,
		Min:  minVal,
		Max:  maxVal,
		Data: data,
		Dims: len(vec),
	}
}

func dequantizeInt4(qv *QuantizedVector) []float32 {
	expectedLen := (qv.Dims + 1) / 2
	if len(qv.Data) < expectedLen {
		return nil
	}
	scale := qv.Max - qv.Min
	vec := make([]float32, qv.Dims)
	for i := 0; i < qv.Dims; i++ {
		byteIdx := i / 2
		var q byte
		if i%2 == 0 {
			q = qv.Data[byteIdx] >> 4
		} else {
			q = qv.Data[byteIdx] & 0x0F
		}
		vec[i] = qv.Min + (float32(q)/15.0)*scale
	}
	return vec
}

// --- Quantized Similarity Functions ---
// These operate directly on quantized data without dequantization.

// CosineSimInt8 computes approximate cosine similarity between two int8 quantized vectors.
// Both vectors must have the same min/max calibration for accurate results.
// For cross-calibration (different min/max), we use the raw byte values which
// preserves relative ordering within each vector.
func CosineSimInt8(a, b *QuantizedVector) float32 {
	// A payload shorter than its dimensions — an int4 vector handed to the
	// int8 function, or a damaged record — is not read past its end (#269).
	if a.Dims != b.Dims || a.Dims == 0 || len(a.Data) < a.Dims || len(b.Data) < b.Dims {
		return 0
	}

	var dot, normA, normB int64
	for i := 0; i < a.Dims; i++ {
		ai := int64(a.Data[i])
		bi := int64(b.Data[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return float32(float64(dot) / math.Sqrt(float64(normA)*float64(normB)))
}

// CosineSimInt4 computes approximate cosine similarity between two int4 quantized vectors.
func CosineSimInt4(a, b *QuantizedVector) float32 {
	if a.Dims != b.Dims || a.Dims == 0 || len(a.Data) < (a.Dims+1)/2 || len(b.Data) < (b.Dims+1)/2 {
		return 0
	}

	var dot, normA, normB int64
	for i := 0; i < a.Dims; i++ {
		byteIdx := i / 2
		var ai, bi byte
		if i%2 == 0 {
			ai = a.Data[byteIdx] >> 4
			bi = b.Data[byteIdx] >> 4
		} else {
			ai = a.Data[byteIdx] & 0x0F
			bi = b.Data[byteIdx] & 0x0F
		}
		aiv := int64(ai)
		biv := int64(bi)
		dot += aiv * biv
		normA += aiv * aiv
		normB += biv * biv
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return float32(float64(dot) / math.Sqrt(float64(normA)*float64(normB)))
}

// QuantizeQueryForInt8 quantizes a float32 query vector using the same calibration as a stored int8 vector.
func QuantizeQueryForInt8(query []float32, min, max float32) *QuantizedVector {
	scale := max - min
	if scale == 0 {
		scale = 1
	}
	data := make([]byte, len(query))
	for i, v := range query {
		normalized := (v - min) / scale
		quantized := math.Round(float64(normalized) * 255.0)
		if quantized < 0 {
			quantized = 0
		}
		if quantized > 255 {
			quantized = 255
		}
		data[i] = byte(quantized)
	}
	return &QuantizedVector{Type: QuantInt8, Min: min, Max: max, Data: data, Dims: len(query)}
}

// QuantizeQueryForInt4 quantizes a float32 query vector using the same calibration as a stored int4 vector.
func QuantizeQueryForInt4(query []float32, min, max float32) *QuantizedVector {
	scale := max - min
	if scale == 0 {
		scale = 1
	}
	byteLen := (len(query) + 1) / 2
	data := make([]byte, byteLen)
	for i, v := range query {
		normalized := (v - min) / scale
		quantized := math.Round(float64(normalized) * 15.0)
		if quantized < 0 {
			quantized = 0
		}
		if quantized > 15 {
			quantized = 15
		}
		q := byte(quantized)
		byteIdx := i / 2
		if i%2 == 0 {
			data[byteIdx] = q << 4
		} else {
			data[byteIdx] |= q
		}
	}
	return &QuantizedVector{Type: QuantInt4, Min: min, Max: max, Data: data, Dims: len(query)}
}

// MarshalQuantizedVector serializes a QuantizedVector to binary.
// Format: [1B type][4B min][4B max][4B dims][data...]
// type: 0=float32, 1=int8, 2=int4
func MarshalQuantizedVector(qv *QuantizedVector) []byte {
	size := 1 + 4 + 4 + 4 + len(qv.Data) // type + min + max + dims + data
	buf := make([]byte, size)
	offset := 0

	// type
	switch qv.Type {
	case QuantInt8:
		buf[0] = 1
	case QuantInt4:
		buf[0] = 2
	default:
		buf[0] = 0
	}
	offset++

	// min
	putFloat32LE(buf[offset:], qv.Min)
	offset += 4

	// max
	putFloat32LE(buf[offset:], qv.Max)
	offset += 4

	// dims
	putUint32LE(buf[offset:], uint32(qv.Dims)) // #nosec G115 -- dims always positive and bounded
	offset += 4

	// data
	copy(buf[offset:], qv.Data)

	return buf
}

// UnmarshalQuantizedVector deserializes a QuantizedVector from binary.
func UnmarshalQuantizedVector(data []byte) (*QuantizedVector, error) {
	if len(data) < 13 { // 1 + 4 + 4 + 4
		return nil, errQuantizedTooShort
	}

	qv := &QuantizedVector{}
	offset := 0

	// type
	switch data[0] {
	case 1:
		qv.Type = QuantInt8
	case 2:
		qv.Type = QuantInt4
	default:
		qv.Type = QuantNone
	}
	offset++

	// min
	qv.Min = readFloat32LE(data[offset:])
	offset += 4

	// max
	qv.Max = readFloat32LE(data[offset:])
	offset += 4

	// dims
	qv.Dims = int(readUint32LE(data[offset:]))
	offset += 4

	// data
	qv.Data = make([]byte, len(data)-offset)
	copy(qv.Data, data[offset:])

	return qv, nil
}

// helper functions for binary encoding
func putFloat32LE(buf []byte, v float32) {
	bits := math.Float32bits(v)
	buf[0] = byte(bits)       // #nosec G115 -- intentional low-byte extraction for LE encoding
	buf[1] = byte(bits >> 8)  // #nosec G115 -- intentional low-byte extraction for LE encoding
	buf[2] = byte(bits >> 16) // #nosec G115 -- intentional low-byte extraction for LE encoding
	buf[3] = byte(bits >> 24) // #nosec G115 -- intentional low-byte extraction for LE encoding
}

func readFloat32LE(buf []byte) float32 {
	bits := uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24
	return math.Float32frombits(bits)
}

func putUint32LE(buf []byte, v uint32) {
	buf[0] = byte(v)       // #nosec G115 -- intentional low-byte extraction for LE encoding
	buf[1] = byte(v >> 8)  // #nosec G115 -- intentional low-byte extraction for LE encoding
	buf[2] = byte(v >> 16) // #nosec G115 -- intentional low-byte extraction for LE encoding
	buf[3] = byte(v >> 24) // #nosec G115 -- intentional low-byte extraction for LE encoding
}

func readUint32LE(buf []byte) uint32 {
	return uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24
}

var errQuantizedTooShort = fmt.Errorf("quantized vector data too short")
