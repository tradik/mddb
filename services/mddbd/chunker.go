package main

import (
	"strings"
)

// ChunkText splits text into chunks by paragraph boundaries (\n\n) with a max character limit.
// If a single paragraph exceeds maxChars, it splits on sentence boundaries (". ").
// As a last resort, it hard-splits at maxChars.
// Returns the original text as a single-element slice if it fits within maxChars.
// Kept as the reference implementation, not as production code (GO-021).
//
// The span-based chunker replaced it in CODE-003, and nothing in the server
// calls this any more. It stays because
// TestChunkSpansSegmentsExactlyLikeChunkText holds the new chunker against it:
// segmentation had to stay byte-identical or every embedding made under the old
// one would point at the wrong passage. Deleting this would leave that
// guarantee asserted against nothing.
//
// Do not call it from the server. If the compatibility test is ever retired,
// this goes with it.
func ChunkText(text string, maxChars int) []string {
	if maxChars <= 0 {
		maxChars = 1500
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	if len(text) <= maxChars {
		return []string{text}
	}

	// Split on paragraph boundaries
	paragraphs := strings.Split(text, "\n\n")

	var chunks []string
	var current strings.Builder

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		// If adding this paragraph would exceed limit, flush current
		if current.Len() > 0 && current.Len()+2+len(para) > maxChars {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}

		// If the paragraph itself exceeds maxChars, split it further
		if len(para) > maxChars {
			// Flush anything accumulated
			if current.Len() > 0 {
				chunks = append(chunks, strings.TrimSpace(current.String()))
				current.Reset()
			}
			chunks = append(chunks, splitLargeParagraph(para, maxChars)...)
			continue
		}

		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(para)
	}

	if current.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}

	return chunks
}

// splitLargeParagraph splits a paragraph that exceeds maxChars on sentence
// boundaries. Reached only through ChunkText, and kept for the same reason.
func splitLargeParagraph(para string, maxChars int) []string {
	sentences := splitSentences(para)

	var chunks []string
	var current strings.Builder

	for _, sent := range sentences {
		sent = strings.TrimSpace(sent)
		if sent == "" {
			continue
		}

		// If a single sentence exceeds maxChars, hard-split it
		if len(sent) > maxChars {
			if current.Len() > 0 {
				chunks = append(chunks, strings.TrimSpace(current.String()))
				current.Reset()
			}
			chunks = append(chunks, hardSplit(sent, maxChars)...)
			continue
		}

		if current.Len() > 0 && current.Len()+1+len(sent) > maxChars {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}

		if current.Len() > 0 {
			current.WriteByte(' ')
		}
		current.WriteString(sent)
	}

	if current.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}

	return chunks
}

// splitSentences splits text on sentence boundaries (". ", "! ", "? ").
// Reached only through splitLargeParagraph, and kept for the same reason.
func splitSentences(text string) []string {
	var sentences []string
	start := 0

	for i := 0; i < len(text)-1; i++ {
		if (text[i] == '.' || text[i] == '!' || text[i] == '?') && text[i+1] == ' ' {
			sentences = append(sentences, text[start:i+1])
			start = i + 2
		}
	}

	if start < len(text) {
		sentences = append(sentences, text[start:])
	}

	return sentences
}

// hardSplit splits text at maxChars boundaries, preferring word breaks.
func hardSplit(text string, maxChars int) []string {
	var chunks []string

	for len(text) > maxChars {
		// Try to find a space near the boundary to avoid splitting words
		splitAt := maxChars
		for i := maxChars - 1; i > maxChars/2; i-- {
			if text[i] == ' ' {
				splitAt = i
				break
			}
		}
		chunks = append(chunks, strings.TrimSpace(text[:splitAt]))
		text = strings.TrimSpace(text[splitAt:])
	}

	if len(text) > 0 {
		chunks = append(chunks, text)
	}

	return chunks
}
