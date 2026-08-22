package main

import (
	"fmt"
	"strings"
	"testing"

	"mddb/internal/fts"
)

// CODE-002. Chunks are re-derived from the parent rather than stored, so a
// vector hit arrived knowing its index and nothing about where it came from.
// ChunkSpans adds the position — but only safely if it segments text exactly
// as ChunkText does, since embeddings were computed from those texts.

// chunkCorpora are the shapes that exercise every branch of the segmenter.
func chunkCorpora() map[string]string {
	longSentence := strings.Repeat("word ", 400)
	return map[string]string{
		"empty":               "",
		"whitespace only":     "   \n\n  \t ",
		"short":               "one small paragraph",
		"leading whitespace":  "\n\n   # Title\n\nBody text here.",
		"two paragraphs":      "First paragraph.\n\nSecond paragraph.",
		"many paragraphs":     strings.Repeat("A paragraph of some length here.\n\n", 200),
		"one huge paragraph":  strings.Repeat("Sentence number one. ", 300),
		"unsplittable run":    longSentence,
		"blank lines between": "alpha\n\n\n\nbeta\n\n\n\ngamma",
		"crlf":                "first line\r\n\r\nsecond para\r\n\r\nthird para",
		"code":                ".a { color: red; }\n\n.b { color: blue; }\n\n.c { margin: 0 }",
	}
}

// The guarantee everything else depends on.
func TestChunkSpansSegmentsExactlyLikeChunkText(t *testing.T) {
	for name, corpus := range chunkCorpora() {
		for _, size := range []int{0, 50, 200, 1500} {
			want := ChunkText(corpus, size)
			spans := ChunkSpans(corpus, size)

			if len(spans) != len(want) {
				t.Errorf("%s (max=%d): %d spans vs %d chunks", name, size, len(spans), len(want))
				continue
			}
			for i := range want {
				if spans[i].Text != want[i] {
					t.Errorf("%s (max=%d): chunk %d differs\n  ChunkText:  %q\n  ChunkSpans: %q",
						name, size, i, trunc(want[i]), trunc(spans[i].Text))
				}
			}
		}
	}
}

func trunc(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

// Positions must land inside the source and move forward.
func TestChunkSpansPositionsAreSaneAndOrdered(t *testing.T) {
	for name, corpus := range chunkCorpora() {
		spans := ChunkSpans(corpus, 200)
		prevEnd := -1
		for i, s := range spans {
			if s.Start < 0 || s.End > len(corpus) {
				t.Errorf("%s: chunk %d spans %d-%d, outside a %d-byte document",
					name, i, s.Start, s.End, len(corpus))
			}
			if s.End < s.Start {
				t.Errorf("%s: chunk %d has a backwards span %d-%d", name, i, s.Start, s.End)
			}
			if s.Start < prevEnd {
				t.Errorf("%s: chunk %d starts at %d, before chunk %d ended at %d",
					name, i, s.Start, i-1, prevEnd)
			}
			prevEnd = s.End
		}
	}
}

// A span should point at the text the chunk carries: its first word must be
// findable at the start offset.
func TestChunkSpansPointAtTheirOwnText(t *testing.T) {
	corpus := strings.Repeat("Paragraph with distinctive words here.\n\n", 100)
	for i, s := range ChunkSpans(corpus, 200) {
		first := strings.Fields(s.Text)
		if len(first) == 0 {
			continue
		}
		window := corpus[s.Start:min(s.Start+len(first[0])+8, len(corpus))]
		if !strings.Contains(window, first[0]) {
			t.Errorf("chunk %d claims to start at %d, but the source there is %q, not %q",
				i, s.Start, window, first[0])
		}
	}
}

// The point of all this: a chunk reports the lines it occupies.
func TestChunkLineRanges(t *testing.T) {
	var b strings.Builder
	for i := range 60 {
		fmt.Fprintf(&b, ".rule-%02d {\n  color: #ccc;\n}\n\n", i)
	}
	css := b.String()

	li := fts.NewLineIndex(css)
	spans := ChunkSpans(css, 300)
	if len(spans) < 3 {
		t.Fatalf("expected the stylesheet to split into several chunks, got %d", len(spans))
	}

	prevEnd := 0
	for i, s := range spans {
		startLine, endLine := li.Range(s.Start, s.End)
		if startLine < 1 || endLine < startLine {
			t.Errorf("chunk %d has line range %d-%d", i, startLine, endLine)
		}
		if startLine < prevEnd {
			t.Errorf("chunk %d starts at line %d, before chunk %d ended at %d",
				i, startLine, i-1, prevEnd)
		}
		if endLine > li.Lines() {
			t.Errorf("chunk %d ends at line %d, past the %d-line document", i, endLine, li.Lines())
		}
		prevEnd = endLine
	}

	// The first chunk must start at line 1, not somewhere arbitrary.
	if s, _ := li.Range(spans[0].Start, spans[0].End); s != 1 {
		t.Errorf("the first chunk starts at line %d, want 1", s)
	}
}

func TestChunkSpansOnEmptyInput(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n\n"} {
		if got := ChunkSpans(in, 100); got != nil {
			t.Errorf("ChunkSpans(%q) = %v, want nil", in, got)
		}
	}
}
