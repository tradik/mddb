package fts

import "sort"

// Byte offset → line number (CODE-002).
//
// A search result that names the document answers "which file"; a result that
// names the line answers "where", which is what an agent needs before it can
// edit anything. Issue #192 measured the difference: without lines, finding
// one CSS declaration costs reading the whole file.
//
// Offsets are already known — highlights carry them and chunks are cut at
// known positions — so this is a mapping, not a second search.

// LineIndex maps byte offsets in a document to 1-based line numbers.
// The zero value is unusable; build one with NewLineIndex.
type LineIndex struct {
	// starts[i] is the byte offset where line i+1 begins. starts[0] is always
	// 0, so a document with no newlines still has line 1.
	starts []int
	length int
}

// NewLineIndex scans content once and records where each line begins.
//
// Only "\n" is treated as a terminator: CRLF ends with one too, so a Windows
// file lands on the same line numbers an editor would show.
func NewLineIndex(content string) *LineIndex {
	starts := make([]int, 1, 1+len(content)/40) // ~40 bytes per line
	starts[0] = 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &LineIndex{starts: starts, length: len(content)}
}

// LineAt returns the 1-based line containing the given byte offset.
//
// Offsets outside the content are clamped rather than rejected: a caller
// mapping a range whose end sits one past the last byte is asking a reasonable
// question, and returning 0 would be read as "no line" rather than "the last".
func (li *LineIndex) LineAt(offset int) int {
	if li == nil || len(li.starts) == 0 {
		return 0
	}
	if offset < 0 {
		offset = 0
	}
	if offset > li.length {
		offset = li.length
	}
	// The last start not greater than offset.
	i := sort.SearchInts(li.starts, offset+1) - 1
	if i < 0 {
		i = 0
	}
	return i + 1
}

// Range returns the 1-based line numbers spanning a byte range.
//
// An end offset that lands exactly on a line start belongs to the previous
// line: a fragment ending at the newline covers the line it ended, not the one
// that has not started yet.
// Lines reports how many lines the indexed content has.
//
// No production caller needs this — the server maps offsets to lines, never the
// other way round. It stays because TestChunkSpansCoverEveryLine asserts that no
// chunk ends past the last line, and computing the bound inside the test would
// mean reimplementing the thing under test.
func (li *LineIndex) Lines() int {
	if li == nil {
		return 0
	}
	return len(li.starts)
}

func (li *LineIndex) Range(startOffset, endOffset int) (startLine, endLine int) {
	if li == nil {
		return 0, 0
	}
	startLine = li.LineAt(startOffset)
	if endOffset <= startOffset {
		return startLine, startLine
	}
	endLine = li.LineAt(endOffset - 1)
	if endLine < startLine {
		endLine = startLine
	}
	return startLine, endLine
}
