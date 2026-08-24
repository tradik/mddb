package main

import (
	"bytes"
	"testing"
)

// --- BytesSplit ---

// --- BytesHasPrefix ---

func TestBytesHasPrefix_True(t *testing.T) {
	if !BytesHasPrefix([]byte("doc|blog|x"), []byte("doc|")) {
		t.Error("should have prefix 'doc|'")
	}
}

func TestBytesHasPrefix_False(t *testing.T) {
	if BytesHasPrefix([]byte("doc|blog"), []byte("rev|")) {
		t.Error("should not have prefix 'rev|'")
	}
}

func TestBytesHasPrefix_EmptyPrefix(t *testing.T) {
	if !BytesHasPrefix([]byte("anything"), []byte{}) {
		t.Error("any string has empty prefix")
	}
}

func TestBytesHasPrefix_LongerPrefix(t *testing.T) {
	if BytesHasPrefix([]byte("ab"), []byte("abcdef")) {
		t.Error("shorter data cannot have longer prefix")
	}
}

func TestBytesHasPrefix_ExactMatch(t *testing.T) {
	if !BytesHasPrefix([]byte("exact"), []byte("exact")) {
		t.Error("exact match should return true")
	}
}

// --- BytesIndexByte ---

// --- BytesLastIndexByte ---

// --- ExtractPart ---

func TestExtractPart_Basic(t *testing.T) {
	data := []byte("doc|blog|post1")

	tests := []struct {
		index int
		want  string
	}{
		{0, "doc"},
		{1, "blog"},
		{2, "post1"},
	}

	for _, tt := range tests {
		got := ExtractPart(data, tt.index)
		if string(got) != tt.want {
			t.Errorf("ExtractPart(%d) = %q, want %q", tt.index, got, tt.want)
		}
	}
}

func TestExtractPart_OutOfRange(t *testing.T) {
	data := []byte("a|b")
	if got := ExtractPart(data, 5); got != nil {
		t.Errorf("ExtractPart(5) = %q, want nil", got)
	}
}

func TestExtractPart_Empty(t *testing.T) {
	if got := ExtractPart(nil, 0); got != nil {
		t.Errorf("ExtractPart(nil, 0) = %v, want nil", got)
	}
	if got := ExtractPart([]byte{}, 0); got != nil {
		t.Errorf("ExtractPart([], 0) = %v, want nil", got)
	}
}

func TestExtractPart_SinglePart(t *testing.T) {
	got := ExtractPart([]byte("solo"), 0)
	if string(got) != "solo" {
		t.Errorf("ExtractPart = %q, want %q", got, "solo")
	}
}

// --- FormatTimestamp ---

func TestFormatTimestamp_Basic(t *testing.T) {
	buf := make([]byte, 20)
	result := FormatTimestamp(1700000000, buf)

	expected := "00000000001700000000"
	if string(result) != expected {
		t.Errorf("FormatTimestamp = %q, want %q", result, expected)
	}
}

func TestFormatTimestamp_Zero(t *testing.T) {
	buf := make([]byte, 20)
	result := FormatTimestamp(0, buf)

	expected := "00000000000000000000"
	if string(result) != expected {
		t.Errorf("FormatTimestamp(0) = %q, want %q", result, expected)
	}
}

func TestFormatTimestamp_SmallBuffer(t *testing.T) {
	// Buffer too small: should allocate new
	buf := make([]byte, 5)
	result := FormatTimestamp(42, buf)

	if len(result) != 20 {
		t.Errorf("result len = %d, want 20", len(result))
	}
	expected := "00000000000000000042"
	if string(result) != expected {
		t.Errorf("FormatTimestamp = %q, want %q", result, expected)
	}
}

func TestFormatTimestamp_LargeValue(t *testing.T) {
	buf := make([]byte, 20)
	result := FormatTimestamp(99999999999999999, buf)

	if len(result) != 20 {
		t.Errorf("result len = %d, want 20", len(result))
	}
	// Should be zero-padded to 20 digits
	expected := "00099999999999999999"
	if string(result) != expected {
		t.Errorf("FormatTimestamp = %q, want %q", result, expected)
	}
}

// --- AppendBytes ---

// --- BytesToLower ---

// --- CompareBytes ---

// --- CopyBytes ---

func TestCopyBytes_Basic(t *testing.T) {
	src := []byte("hello")
	dst := CopyBytes(src)

	if !bytes.Equal(dst, src) {
		t.Errorf("CopyBytes = %q, want %q", dst, src)
	}

	// Verify it's a true copy (modifying dst does not affect src)
	dst[0] = 'X'
	if src[0] == 'X' {
		t.Error("CopyBytes did not create independent copy")
	}
}

func TestCopyBytes_Nil(t *testing.T) {
	if got := CopyBytes(nil); got != nil {
		t.Errorf("CopyBytes(nil) = %v, want nil", got)
	}
}

func TestCopyBytes_Empty(t *testing.T) {
	got := CopyBytes([]byte{})
	if got == nil {
		t.Error("CopyBytes(empty) should not return nil")
	}
	if len(got) != 0 {
		t.Errorf("CopyBytes(empty) len = %d, want 0", len(got))
	}
}
