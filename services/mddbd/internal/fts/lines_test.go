package fts

import (
	"fmt"
	"strings"
	"testing"
)

// CODE-002. A result that names the document says which file; a result that
// names the line says where, which is what an agent needs before it can edit
// anything (issue #192).

func TestLineAt(t *testing.T) {
	// Offsets:      0123 4567890 1234
	content := "one\ntwo\nthree"
	li := NewLineIndex(content)

	for offset, want := range map[int]int{
		0: 1, 2: 1, 3: 1, // "one" and the newline that ends it
		4: 2, 6: 2, 7: 2, // "two"
		8: 3, 12: 3, // "three"
	} {
		if got := li.LineAt(offset); got != want {
			t.Errorf("LineAt(%d) = %d, want %d", offset, got, want)
		}
	}
	if got := li.Lines(); got != 3 {
		t.Errorf("Lines() = %d, want 3", got)
	}
}

func TestLineAtClampsOutOfRange(t *testing.T) {
	li := NewLineIndex("one\ntwo")
	// Clamped rather than rejected: a caller mapping a range that ends one
	// past the last byte is asking a fair question, and 0 would read as
	// "no line" rather than "the last one".
	if got := li.LineAt(-5); got != 1 {
		t.Errorf("LineAt(-5) = %d, want 1", got)
	}
	if got := li.LineAt(9999); got != 2 {
		t.Errorf("LineAt(past the end) = %d, want 2", got)
	}
}

func TestLineIndexHandlesEdgeShapes(t *testing.T) {
	for name, tc := range map[string]struct {
		content string
		lines   int
	}{
		"empty":            {"", 1},
		"no newline":       {"single line", 1},
		"trailing newline": {"a\n", 2}, // the file ends having started line 2
		"blank lines":      {"a\n\n\nb", 4},
		"only newlines":    {"\n\n", 3},
	} {
		if got := NewLineIndex(tc.content).Lines(); got != tc.lines {
			t.Errorf("%s: Lines() = %d, want %d", name, got, tc.lines)
		}
	}
}

// CRLF ends with \n too, so a Windows file lands on the numbers an editor
// would show rather than on doubled counts.
func TestLineIndexHandlesCRLF(t *testing.T) {
	li := NewLineIndex("one\r\ntwo\r\nthree")
	if got := li.Lines(); got != 3 {
		t.Errorf("Lines() = %d, want 3", got)
	}
	if got := li.LineAt(strings.Index("one\r\ntwo\r\nthree", "two")); got != 2 {
		t.Errorf("the second line should be line 2, got %d", got)
	}
}

func TestRangeSpansLines(t *testing.T) {
	content := "line one\nline two\nline three\nline four"
	li := NewLineIndex(content)

	start, end := li.Range(0, len("line one\nline two"))
	if start != 1 || end != 2 {
		t.Errorf("Range across two lines = %d-%d, want 1-2", start, end)
	}

	// A range wholly inside one line reports that line twice, not a span.
	start, end = li.Range(2, 5)
	if start != 1 || end != 1 {
		t.Errorf("Range within a line = %d-%d, want 1-1", start, end)
	}
}

// A fragment ending exactly on a newline covers the line it ended, not the one
// that has not started yet — otherwise every fragment would claim one line too
// many.
func TestRangeEndingOnALineBoundary(t *testing.T) {
	content := "alpha\nbeta\ngamma"
	li := NewLineIndex(content)

	start, end := li.Range(0, len("alpha\n"))
	if start != 1 || end != 1 {
		t.Errorf("a range ending on the newline = %d-%d, want 1-1", start, end)
	}
}

func TestRangeWithEmptyOrInvertedSpan(t *testing.T) {
	li := NewLineIndex("alpha\nbeta")
	if s, e := li.Range(7, 7); s != 2 || e != 2 {
		t.Errorf("an empty range = %d-%d, want 2-2", s, e)
	}
	if s, e := li.Range(7, 3); s != 2 || e != 2 {
		t.Errorf("an inverted range should not produce a backwards span, got %d-%d", s, e)
	}
}

func TestNilLineIndexIsSafe(t *testing.T) {
	var li *LineIndex
	if got := li.LineAt(5); got != 0 {
		t.Errorf("LineAt on nil = %d, want 0", got)
	}
	if s, e := li.Range(0, 5); s != 0 || e != 0 {
		t.Errorf("Range on nil = %d-%d, want 0-0", s, e)
	}
	if got := li.Lines(); got != 0 {
		t.Errorf("Lines on nil = %d, want 0", got)
	}
}

// The point of the feature: a highlight in a stylesheet reports the lines the
// declaration actually occupies, not the whole file.
func TestHighlightsCarryLineNumbers(t *testing.T) {
	// A fragment is bounded by FragmentSize, so the document has to be bigger
	// than one fragment for the range to be local — which is the real case: a
	// theme stylesheet is thousands of lines, not twelve.
	var b strings.Builder
	for i := range 60 {
		fmt.Fprintf(&b, ".filler-%02d {\n  color: #cccccc;\n  margin: 0;\n}\n\n", i)
	}
	before := b.String()
	target := ".hero-banner {\n  background: url(hero.png);\n  padding: 2rem;\n}\n"
	css := before + target + "\n.footer {\n  color: grey;\n}\n"

	li := NewLineIndex(css)
	targetLine := li.LineAt(len(before)) // where .hero-banner starts

	hs := ExtractHighlights(css, []string{"background"}, HighlightOptions{})
	if len(hs) == 0 {
		t.Fatal("expected a highlight for the matched declaration")
	}
	h := hs[0]

	if h.StartLine > targetLine+2 || h.EndLine < targetLine {
		t.Errorf("the fragment reports lines %d-%d, but `background` is at line %d",
			h.StartLine, h.EndLine, targetLine)
	}
	if h.StartLine > h.EndLine {
		t.Errorf("line range is backwards: %d-%d", h.StartLine, h.EndLine)
	}
	// The span must be a neighbourhood rather than the file — that is the
	// whole point. It is not tight: FragmentSize is a byte budget tuned for
	// prose, and code lines are short, so 150 bytes covers roughly fifteen
	// lines of CSS against two or three of prose. Callers indexing source
	// should lower fragmentSize; chunk mode (CODE-003) gives tighter bounds
	// again.
	if span := h.EndLine - h.StartLine; span > li.Lines()/4 {
		t.Errorf("the fragment spans %d lines of a %d-line file; that is not a place to edit",
			span, li.Lines())
	}
	// And the lines must agree with the offsets they were derived from.
	wantStart, wantEnd := li.Range(h.StartOffset, h.EndOffset)
	if h.StartLine != wantStart || h.EndLine != wantEnd {
		t.Errorf("lines %d-%d disagree with offsets %d-%d (which map to %d-%d)",
			h.StartLine, h.EndLine, h.StartOffset, h.EndOffset, wantStart, wantEnd)
	}
}

// Multibyte content must not shift the numbers: offsets are bytes, lines are
// counted from newlines, and neither cares how wide a rune is.
func TestLineNumbersWithMultibyteContent(t *testing.T) {
	content := "zażółć gęślą jaźń\nsecond line\ntrzecia linia"
	li := NewLineIndex(content)
	if got := li.Lines(); got != 3 {
		t.Errorf("Lines() = %d, want 3", got)
	}
	if got := li.LineAt(strings.Index(content, "second")); got != 2 {
		t.Errorf("the second line should be 2 even after multibyte text, got %d", got)
	}
	if got := li.LineAt(strings.Index(content, "trzecia")); got != 3 {
		t.Errorf("the third line should be 3, got %d", got)
	}
}
