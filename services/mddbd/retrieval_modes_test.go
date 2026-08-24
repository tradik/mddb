package main

import (
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

func TestValidRetrievalMode(t *testing.T) {
	for _, mode := range []string{"", "parent", "chunk", "window"} {
		if !validRetrievalMode(mode) {
			t.Errorf("validRetrievalMode(%q) = false, want true", mode)
		}
	}
	for _, mode := range []string{"document", "chunks", "PARENT", "x"} {
		if validRetrievalMode(mode) {
			t.Errorf("validRetrievalMode(%q) = true, want false", mode)
		}
	}
}

func TestSplitChunkKey(t *testing.T) {
	cases := []struct {
		key       string
		wantID    string
		wantChunk int
	}{
		{"post1#0", "post1", 0},
		{"post1#12", "post1", 12},
		{"post1", "post1", 0},         // legacy non-chunked
		{"po#st1#3", "po#st1", 3},     // docID containing '#'
		{"post1#-1", "post1#-1", 0},   // negative index is not a chunk suffix
		{"post1#abc", "post1#abc", 0}, // non-numeric suffix is part of the ID
	}
	for _, c := range cases {
		id, chunk := splitChunkKey(c.key)
		if id != c.wantID || chunk != c.wantChunk {
			t.Errorf("splitChunkKey(%q) = (%q, %d), want (%q, %d)", c.key, id, chunk, c.wantID, c.wantChunk)
		}
	}
}

func TestChunkPassage(t *testing.T) {
	// Build content that chunks deterministically: paragraphs well below the
	// 1500-char default so each paragraph groups predictably.
	paras := []string{
		strings.Repeat("alpha ", 200),   // ~1200 chars → chunk 0
		strings.Repeat("bravo ", 200),   // → chunk 1
		strings.Repeat("charlie ", 150), // → chunk 2
	}
	content := strings.Join(paras, "\n\n")
	chunks := ChunkText(content, 1500)
	if len(chunks) < 3 {
		t.Fatalf("test content produced %d chunks, want >= 3", len(chunks))
	}

	// Single chunk
	got := chunkPassage(content, 1, 0, ChunkModeProse)
	if got != chunks[1] {
		t.Errorf("chunkPassage(idx=1, window=0, ChunkModeProse) != chunks[1]")
	}

	// Window of 1 around the middle chunk joins its neighbors
	got = chunkPassage(content, 1, 1, ChunkModeProse)
	want := strings.Join(chunks[0:3], "\n\n")
	if got != want {
		t.Errorf("chunkPassage(idx=1, window=1, ChunkModeProse) mismatch")
	}

	// Window clamped at document start
	got = chunkPassage(content, 0, 1, ChunkModeProse)
	want = strings.Join(chunks[0:2], "\n\n")
	if got != want {
		t.Errorf("chunkPassage(idx=0, window=1, ChunkModeProse) mismatch")
	}

	// Out-of-range chunk index clamps to the last chunk
	got = chunkPassage(content, 99, 0, ChunkModeProse)
	if got != chunks[len(chunks)-1] {
		t.Errorf("chunkPassage(out-of-range, ChunkModeProse) should clamp to last chunk")
	}

	// Empty content
	if got := chunkPassage("", 0, 0, ChunkModeProse); got != "" {
		t.Errorf("chunkPassage(empty, ChunkModeProse) = %q, want empty", got)
	}
}

func TestHandleVectorSearchRetrievalModes(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.VectorIndex.SetReady()

	// Two chunks worth of content: chunk 0 is "alpha...", chunk 1 is "bravo..."
	content := strings.Repeat("alpha ", 250) + "\n\n" + strings.Repeat("bravo ", 250)
	doc := addTestDoc(t, s, "kb", "guide", "en", content, nil)
	s.VectorIndex.Add("kb", doc.ID+"#0", []float32{1, 0, 0})
	s.VectorIndex.Add("kb", doc.ID+"#1", []float32{0.9, 0.1, 0})

	search := func(mode string, windowSize int) VectorSearchResponseHTTP {
		t.Helper()
		rec := doRequest(t, s.handleVectorSearch, VectorSearchRequest{
			Collection:    "kb",
			QueryVector:   []float32{1, 0, 0},
			TopK:          5,
			RetrievalMode: mode,
			WindowSize:    windowSize,
		})
		if rec.Code != 200 {
			t.Fatalf("mode %q: status %d, body %s", mode, rec.Code, rec.Body.String())
		}
		var resp VectorSearchResponseHTTP
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp
	}

	// Parent mode (default): both chunk hits collapse into one document
	resp := search("", 0)
	if resp.Total != 1 {
		t.Fatalf("parent mode: total = %d, want 1 (deduplicated)", resp.Total)
	}
	if resp.Results[0].ChunkIndex != nil {
		t.Error("parent mode must not set chunkIndex")
	}

	// Chunk mode: both chunks returned with their passages
	resp = search("chunk", 0)
	if resp.Total != 2 {
		t.Fatalf("chunk mode: total = %d, want 2", resp.Total)
	}
	first := resp.Results[0]
	if first.ChunkIndex == nil || *first.ChunkIndex != 0 {
		t.Fatalf("chunk mode: first result chunkIndex = %v, want 0", first.ChunkIndex)
	}
	if !strings.Contains(first.ChunkText, "alpha") || strings.Contains(first.ChunkText, "bravo") {
		t.Errorf("chunk 0 passage should contain only alpha text")
	}

	// Window mode: passage widened to include the neighboring chunk
	resp = search("window", 1)
	if resp.Total != 2 {
		t.Fatalf("window mode: total = %d, want 2", resp.Total)
	}
	first = resp.Results[0]
	if !strings.Contains(first.ChunkText, "alpha") || !strings.Contains(first.ChunkText, "bravo") {
		t.Errorf("windowed passage should span both chunks")
	}

	// Invalid mode rejected
	rec := doRequest(t, s.handleVectorSearch, VectorSearchRequest{
		Collection:    "kb",
		QueryVector:   []float32{1, 0, 0},
		RetrievalMode: "bogus",
	})
	if rec.Code != 400 {
		t.Errorf("invalid retrievalMode: status = %d, want 400", rec.Code)
	}
}

func TestHandleVectorSearchMMR(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.VectorIndex.SetReady()

	// Two near-duplicate docs highly relevant to the query, one diverse doc.
	for _, d := range []struct {
		key string
		vec []float32
	}{
		{"dup1", []float32{1, 0, 0}},
		{"dup2", []float32{0.99, 0.01, 0}},
		{"diverse", []float32{0.5, 0.87, 0}},
	} {
		doc := addTestDoc(t, s, "mmr", d.key, "en", "content "+d.key, nil)
		s.VectorIndex.Add("mmr", doc.ID, d.vec)
	}

	query := VectorSearchRequest{
		Collection:  "mmr",
		QueryVector: []float32{1, 0, 0},
		TopK:        2,
	}

	// Without MMR: the two near-duplicates fill topK
	rec := doRequest(t, s.handleVectorSearch, query)
	var resp VectorSearchResponseHTTP
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("total = %d, want 2", resp.Total)
	}
	keys := []string{resp.Results[0].Document.Key, resp.Results[1].Document.Key}
	if keys[1] == "diverse" {
		t.Fatalf("test setup broken: diverse doc already ranks second without MMR")
	}

	// With MMR: the diverse doc displaces the near-duplicate
	query.MMR = true
	query.MMRLambda = 0.3
	rec = doRequest(t, s.handleVectorSearch, query)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("MMR total = %d, want 2", resp.Total)
	}
	if resp.Results[0].Document.Key != "dup1" {
		t.Errorf("MMR first pick = %q, want dup1 (most relevant)", resp.Results[0].Document.Key)
	}
	if resp.Results[1].Document.Key != "diverse" {
		t.Errorf("MMR second pick = %q, want diverse", resp.Results[1].Document.Key)
	}
}
