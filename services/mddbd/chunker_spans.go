package main

import "strings"

// Chunk positions (CODE-002).
//
// Chunks are re-derived from the parent document rather than stored, which
// keeps storage simple but means a chunk arrives with no idea where it came
// from. A vector hit could name the document and the chunk index; it could not
// say "lines 41-58", which is what makes a result somewhere to edit rather
// than something to read (issue #192).
//
// The text a chunk carries is not a verbatim slice of the source — paragraphs
// are trimmed and rejoined — so positions cannot be recovered by searching for
// the chunk afterwards. They are tracked while the chunk is being built.

// ChunkSpan is a chunk together with where it came from in the source.
// Start and End are byte offsets into the original content, End exclusive.
type ChunkSpan struct {
	Text  string
	Start int
	End   int
}

// paragraphSpan is one paragraph with its position in the source.
type paragraphSpan struct {
	text  string
	start int
	end   int
}

// splitParagraphSpans splits on blank-line boundaries while keeping each
// paragraph's position. Equivalent to strings.Split(text, "\n\n") followed by
// TrimSpace, except that nothing is thrown away.
func splitParagraphSpans(text string) []paragraphSpan {
	var out []paragraphSpan
	offset := 0
	for _, raw := range strings.Split(text, "\n\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" {
			lead := strings.Index(raw, trimmed)
			if lead < 0 {
				lead = 0
			}
			out = append(out, paragraphSpan{
				text:  trimmed,
				start: offset + lead,
				end:   offset + lead + len(trimmed),
			})
		}
		offset += len(raw) + 2 // the separator that Split consumed
	}
	return out
}

// ChunkSpans splits text the same way ChunkText does, and reports where each
// chunk sits in the original.
//
// The segmentation is shared with ChunkText — that function is a wrapper over
// this one — so the two can never disagree about where a chunk ends.
func ChunkSpans(text string, maxChars int) []ChunkSpan {
	if maxChars <= 0 {
		maxChars = 1500
	}

	// Offsets are into the original string, so the leading whitespace that
	// TrimSpace removes has to be accounted for once, here.
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	base := strings.Index(text, trimmed)
	if base < 0 {
		base = 0
	}

	if len(trimmed) <= maxChars {
		return []ChunkSpan{{Text: trimmed, Start: base, End: base + len(trimmed)}}
	}

	paragraphs := splitParagraphSpans(trimmed)

	var spans []ChunkSpan
	var current []paragraphSpan
	currentLen := 0

	flush := func() {
		if len(current) == 0 {
			return
		}
		parts := make([]string, len(current))
		for i, p := range current {
			parts[i] = p.text
		}
		spans = append(spans, ChunkSpan{
			Text:  strings.Join(parts, "\n\n"),
			Start: base + current[0].start,
			End:   base + current[len(current)-1].end,
		})
		current = nil
		currentLen = 0
	}

	for _, para := range paragraphs {
		if currentLen > 0 && currentLen+2+len(para.text) > maxChars {
			flush()
		}

		// A paragraph larger than a chunk is split further; its pieces are
		// positioned relative to where the paragraph starts.
		if len(para.text) > maxChars {
			flush()
			spans = append(spans, splitLargeParagraphSpans(para, maxChars, base)...)
			continue
		}

		if currentLen > 0 {
			currentLen += 2
		}
		currentLen += len(para.text)
		current = append(current, para)
	}
	flush()

	return spans
}

// splitLargeParagraphSpans splits an oversized paragraph on sentence
// boundaries, then hard-splits any sentence still too long, tracking positions
// throughout. Mirrors splitLargeParagraph.
func splitLargeParagraphSpans(para paragraphSpan, maxChars, base int) []ChunkSpan {
	var spans []ChunkSpan

	// Sentence offsets relative to the paragraph.
	type sentSpan struct {
		text  string
		start int
	}
	var sentences []sentSpan
	start := 0
	for i := 0; i < len(para.text)-1; i++ {
		if (para.text[i] == '.' || para.text[i] == '!' || para.text[i] == '?') && para.text[i+1] == ' ' {
			sentences = append(sentences, sentSpan{text: para.text[start : i+1], start: start})
			start = i + 2
		}
	}
	if start < len(para.text) {
		sentences = append(sentences, sentSpan{text: para.text[start:], start: start})
	}

	var cur []sentSpan
	curLen := 0
	flush := func() {
		if len(cur) == 0 {
			return
		}
		parts := make([]string, len(cur))
		for i, s := range cur {
			parts[i] = strings.TrimSpace(s.text)
		}
		first, last := cur[0], cur[len(cur)-1]
		spans = append(spans, ChunkSpan{
			Text:  strings.TrimSpace(strings.Join(parts, " ")),
			Start: base + para.start + first.start,
			End:   base + para.start + last.start + len(last.text),
		})
		cur = nil
		curLen = 0
	}

	for _, s := range sentences {
		text := strings.TrimSpace(s.text)
		if text == "" {
			continue
		}
		if len(text) > maxChars {
			flush()
			offset := s.start
			for _, piece := range hardSplit(text, maxChars) {
				spans = append(spans, ChunkSpan{
					Text:  piece,
					Start: base + para.start + offset,
					End:   base + para.start + offset + len(piece),
				})
				offset += len(piece) + 1
			}
			continue
		}
		if curLen > 0 && curLen+1+len(text) > maxChars {
			flush()
		}
		if curLen > 0 {
			curLen++
		}
		curLen += len(text)
		cur = append(cur, s)
	}
	flush()

	return spans
}
