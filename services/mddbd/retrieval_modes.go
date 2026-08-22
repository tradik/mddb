package main

import (
	"mddb/internal/fts"
	"strconv"
	"strings"

	"mddb/internal/envconf"
)

// Retrieval modes for vector and hybrid search.
//
// Chunks are embedded and indexed individually (key "docID#N"), but callers
// have different context needs:
//
//   - "parent": one result per parent document, scored by its best-matching
//     chunk. This is the historical behavior and stays the default.
//   - "chunk": one result per matching chunk, carrying the exact passage that
//     matched (chunkIndex + chunkText) — precise context for LLM prompts.
//   - "window": like "chunk", but the passage is widened with N neighboring
//     chunks on each side, trading precision for surrounding context.
//
// Chunking is deterministic (ChunkText on the parent's content with the
// configured chunk size), so passages are re-derived from the parent document
// at query time — no extra storage, and always in sync with the content.
const (
	RetrievalModeParent = "parent"
	RetrievalModeChunk  = "chunk"
	RetrievalModeWindow = "window"
)

// validRetrievalMode reports whether mode is a supported retrieval mode.
// The empty string is valid and means RetrievalModeParent.
func validRetrievalMode(mode string) bool {
	switch mode {
	case "", RetrievalModeParent, RetrievalModeChunk, RetrievalModeWindow:
		return true
	}
	return false
}

// mmrLambdaOrDefault clamps the MMR lambda to [0,1], defaulting to 0.5 when
// unset (zero). Lambda balances relevance (1.0) against diversity (0.0).
func mmrLambdaOrDefault(lambda float64) float64 {
	if lambda <= 0 {
		return 0.5
	}
	if lambda > 1 {
		return 1
	}
	return lambda
}

// splitChunkKey splits an index key "docID#N" into the parent document ID and
// the chunk index. Legacy non-chunked keys map to chunk 0.
func splitChunkKey(key string) (docID string, chunkIndex int) {
	hashIdx := strings.LastIndexByte(key, '#')
	if hashIdx < 0 {
		return key, 0
	}
	n, err := strconv.Atoi(key[hashIdx+1:])
	if err != nil || n < 0 {
		return key, 0
	}
	return key[:hashIdx], n
}

// chunkPassage re-derives the chunk (or window of chunks) text from the
// parent document's content. windowSize is the number of neighboring chunks
// included on each side; 0 returns just the matching chunk.
func chunkPassage(contentMD string, chunkIndex, windowSize int, mode ChunkMode) string {
	text, _, _ := chunkPassageWithLines(contentMD, chunkIndex, windowSize, mode)
	return text
}

// chunkPassageWithLines is chunkPassage plus the 1-based, inclusive line range
// the passage occupies in the parent document (CODE-002).
//
// The lines are what turn a hit into a place to edit: "css/style.css lines
// 41-58" is a neighbourhood, where a document name and a chunk index mean
// reading the file to find out where the chunk was. Both come from the same
// segmentation, so the text and its range can never disagree.
func chunkPassageWithLines(contentMD string, chunkIndex, windowSize int, mode ChunkMode) (text string, startLine, endLine int) {
	chunkSize := envconf.Int("MDDB_EMBEDDING_CHUNK_SIZE", 1500)
	spans := ChunkSpansMode(contentMD, chunkSize, mode)
	if len(spans) == 0 {
		return "", 0, 0
	}
	if chunkIndex >= len(spans) {
		chunkIndex = len(spans) - 1
	}
	if chunkIndex < 0 {
		chunkIndex = 0
	}

	lo, hi := chunkIndex, chunkIndex+1
	if windowSize > 0 {
		lo = chunkIndex - windowSize
		if lo < 0 {
			lo = 0
		}
		hi = chunkIndex + windowSize + 1
		if hi > len(spans) {
			hi = len(spans)
		}
	}

	parts := make([]string, 0, hi-lo)
	for _, s := range spans[lo:hi] {
		parts = append(parts, s.Text)
	}
	li := fts.NewLineIndex(contentMD)
	startLine, endLine = li.Range(spans[lo].Start, spans[hi-1].End)
	return strings.Join(parts, "\n\n"), startLine, endLine
}
