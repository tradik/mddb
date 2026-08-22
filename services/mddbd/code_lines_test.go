package main

import (
	"fmt"
	"strings"
	"testing"

	"mddb/internal/fts"
	proto "mddb/proto"
)

// CODE-002 / issue #192. A result that names the document says which file. The
// question an agent has is "where", and answering it by reading the file is
// what made a one-line CSS change cost thousands of tokens.

// themeStylesheet builds a stylesheet big enough that a fragment is a
// neighbourhood rather than the whole file, with a known target near the end.
func themeStylesheet() (css string, targetOffset int) {
	var b strings.Builder
	for i := range 40 {
		fmt.Fprintf(&b, ".filler-%02d {\n  color: #cccccc;\n}\n\n", i)
	}
	targetOffset = b.Len()
	b.WriteString(".hero-banner {\n  background: url(hero.png);\n  padding: 2rem;\n}\n")
	return b.String(), targetOffset
}

func seedTheme(t *testing.T, s *Server) (css string, targetLine int) {
	t.Helper()
	css, targetOffset := themeStylesheet()
	targetLine = fts.NewLineIndex(css).LineAt(targetOffset)

	_, processed, err := s.processBatchWithDocs(t.Context(), "theme", []*proto.BatchDocument{{
		Key:       "css/style.css",
		Lang:      "en",
		ContentMd: css,
		Meta:      map[string]*proto.MetaValues{"kind": {Values: []string{"code"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.firePostBatchHooks("theme", processed, postBatchOptions{})
	return css, targetLine
}

// The shape issue #192 asked for: file, lines, fragment.
func TestSearchAnswersWithFileAndLines(t *testing.T) {
	s, cleanup := newTestServerWithFTS(t)
	defer cleanup()
	css, targetLine := seedTheme(t, s)

	results, err := s.FTSIndex.Search("theme", "background", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("the declaration was not found at all")
	}

	hs := fts.ExtractHighlights(css, results[0].MatchedTerms, fts.HighlightOptions{FragmentSize: 80})
	if len(hs) == 0 {
		t.Fatal("no fragment was extracted for the match")
	}
	h := hs[0]

	if h.StartLine == 0 || h.EndLine == 0 {
		t.Fatalf("the fragment carries no line range: %+v", h)
	}
	if targetLine < h.StartLine || targetLine > h.EndLine {
		t.Errorf("the declaration is on line %d, but the fragment reports lines %d-%d",
			targetLine, h.StartLine, h.EndLine)
	}
	if !strings.Contains(h.Fragment, "background") {
		t.Errorf("the fragment does not contain the match: %q", h.Fragment)
	}
}

// Projection and highlighting are the two halves of one request — do not send
// the body, tell me where to look. Dropping the fragments when a projection is
// active would leave the caller with neither.
func TestProjectionKeepsHighlights(t *testing.T) {
	resp := &MCPFTSSearchResponse{
		Algorithm: "tfidf",
		Total:     1,
		Results: []MCPFTSResult{{
			Document:     MCPDocument{Key: "css/style.css"},
			Score:        1.0,
			MatchedTerms: []string{"background"},
			Highlights: []fts.Highlight{{
				Fragment: ".hero-banner { background: …", StartLine: 158, EndLine: 163,
			}},
		}},
	}

	projected := projectFTSResult(resp, []string{"key"}, false)
	results, ok := projected["results"].([]map[string]interface{})
	if !ok || len(results) != 1 {
		t.Fatalf("unexpected projected shape: %#v", projected)
	}
	hs, ok := results[0]["highlights"].([]fts.Highlight)
	if !ok || len(hs) != 1 {
		t.Fatalf("projection dropped the highlights: %#v", results[0])
	}
	if hs[0].StartLine != 158 || hs[0].EndLine != 163 {
		t.Errorf("line range lost in projection: %+v", hs[0])
	}
}

func TestProjectionWithoutHighlightsOmitsTheKey(t *testing.T) {
	resp := &MCPFTSSearchResponse{
		Results: []MCPFTSResult{{Document: MCPDocument{Key: "a"}, Score: 1}},
	}
	results := projectFTSResult(resp, []string{"key"}, false)["results"].([]map[string]interface{})
	if _, present := results[0]["highlights"]; present {
		t.Error("a result with no highlights should not carry an empty key")
	}
}

// Chunk passages report where they came from, so a vector hit is also a place.
func TestChunkPassageReportsItsLines(t *testing.T) {
	// chunkPassageWithLines reads the chunk size from the environment, so the
	// comparison has to use the same one — otherwise the test is measuring two
	// different segmentations.
	t.Setenv("MDDB_EMBEDDING_CHUNK_SIZE", "300")

	css, _ := themeStylesheet()
	li := fts.NewLineIndex(css)

	spans := ChunkSpans(css, 300)
	if len(spans) < 2 {
		t.Fatalf("expected several chunks, got %d", len(spans))
	}

	for i := range spans {
		text, startLine, endLine := chunkPassageWithLines(css, i, 0, ChunkModeProse)
		if text == "" {
			t.Errorf("chunk %d has no text", i)
		}
		if startLine < 1 || endLine < startLine || endLine > li.Lines() {
			t.Errorf("chunk %d reports lines %d-%d of a %d-line file", i, startLine, endLine, li.Lines())
		}
		wantStart, wantEnd := li.Range(spans[i].Start, spans[i].End)
		if startLine != wantStart || endLine != wantEnd {
			t.Errorf("chunk %d lines %d-%d disagree with its span %d-%d (lines %d-%d)",
				i, startLine, endLine, spans[i].Start, spans[i].End, wantStart, wantEnd)
		}
	}
}

// A window widens the passage, so it must widen the range with it.
func TestWindowedPassageWidensTheLineRange(t *testing.T) {
	t.Setenv("MDDB_EMBEDDING_CHUNK_SIZE", "300")
	css, _ := themeStylesheet()

	_, narrowStart, narrowEnd := chunkPassageWithLines(css, 2, 0, ChunkModeProse)
	_, wideStart, wideEnd := chunkPassageWithLines(css, 2, 1, ChunkModeProse)

	if wideStart > narrowStart {
		t.Errorf("a window should start no later than the chunk: %d vs %d", wideStart, narrowStart)
	}
	if wideEnd < narrowEnd {
		t.Errorf("a window should end no earlier than the chunk: %d vs %d", wideEnd, narrowEnd)
	}
	if wideStart == narrowStart && wideEnd == narrowEnd {
		t.Error("the window did not widen the range at all")
	}
}

func TestChunkPassageOnEmptyContent(t *testing.T) {
	text, start, end := chunkPassageWithLines("", 0, 0, ChunkModeProse)
	if text != "" || start != 0 || end != 0 {
		t.Errorf("empty content gave %q %d-%d", text, start, end)
	}
}

// An index out of range is clamped rather than panicking: chunk counts can
// change when the configured chunk size does, and a stale index must not take
// a search down.
func TestChunkPassageClampsTheIndex(t *testing.T) {
	t.Setenv("MDDB_EMBEDDING_CHUNK_SIZE", "300")
	css, _ := themeStylesheet()
	if _, s, e := chunkPassageWithLines(css, 9999, 0, ChunkModeProse); s == 0 || e < s {
		t.Errorf("an out-of-range index gave lines %d-%d", s, e)
	}
	if _, s, e := chunkPassageWithLines(css, -3, 0, ChunkModeProse); s != 1 || e < s {
		t.Errorf("a negative index should clamp to the first chunk, got %d-%d", s, e)
	}
}
