package main

import "strings"

// Code-aware chunking (CODE-003).
//
// The prose chunker splits on blank lines and falls back to sentence
// boundaries — right for paragraphs, wrong for source. A period inside
// `url(a.png)` is not the end of a sentence, and a split there leaves half a
// declaration in one chunk and half in the next: a passage that reads as
// nothing and embeds as noise.
//
// This splits on blank lines too, but only where the bracket depth is zero, so
// a chunk never ends inside a rule, a function body or an object literal. The
// unit becomes something editable: one chunk is roughly one CSS rule or one
// function, which is what makes a vector hit somewhere to work rather than
// something to read (issue #192).
//
// Deliberately no per-language parsers. Bracket counting handles CSS, JS,
// JSON, Go and template languages well enough, and a parser per language is a
// dependency and a maintenance surface for gains this does not need (YAGNI).

// codeLine is one line of source with its position and the bracket depth after
// it. Depth is tracked cumulatively so a split point can be checked in O(1).
type codeLine struct {
	start int // byte offset where the line begins
	end   int // byte offset one past its last byte, excluding the newline
	blank bool
	// depthAfter is the bracket nesting depth once this line has been read.
	// A chunk boundary is only safe where it is zero.
	depthAfter int
}

// scanCodeLines walks the source once, recording each line's position, whether
// it is blank, and the bracket depth after it.
//
// Strings and comments are skipped so a brace inside "}" or /* } */ does not
// throw the depth off. This is a scanner, not a parser: it knows quotes,
// escapes, and the two comment forms every language in scope shares.
func scanCodeLines(src string) []codeLine {
	var lines []codeLine
	depth := 0
	lineStart := 0

	var (
		inString  bool
		quote     byte
		inLineCom bool
		inBlkCom  bool
	)

	for i := 0; i < len(src); i++ {
		c := src[i]

		if c == '\n' {
			lines = append(lines, codeLine{
				start:      lineStart,
				end:        i,
				blank:      strings.TrimSpace(src[lineStart:i]) == "",
				depthAfter: depth,
			})
			lineStart = i + 1
			inLineCom = false // a line comment ends with its line
			continue
		}

		// Inside a comment or a string, only the terminator matters.
		if inLineCom {
			continue // ends with the line, handled above
		}
		if inBlkCom {
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				inBlkCom = false
				i++
			}
			continue
		}
		if inString {
			switch c {
			case '\\':
				i++ // skip the escaped byte
			case quote:
				inString = false
			}
			continue
		}
		if c == '/' && i+1 < len(src) {
			switch src[i+1] {
			case '/':
				inLineCom = true
				i++
				continue
			case '*':
				inBlkCom = true
				i++
				continue
			}
		}

		switch c {
		case '"', '\'', '`':
			inString, quote = true, c
		case '{', '(', '[':
			depth++
		case '}', ')', ']':
			if depth > 0 {
				depth--
			}
		}
	}

	if lineStart <= len(src) {
		lines = append(lines, codeLine{
			start:      lineStart,
			end:        len(src),
			blank:      strings.TrimSpace(src[lineStart:]) == "",
			depthAfter: depth,
		})
	}
	return lines
}

// ChunkSpansCode splits source into chunks that respect bracket nesting.
//
// Preference order for a boundary: a blank line at depth zero (a rule or
// function ended), then any line end at depth zero, then — for a minified file
// that is one enormous line — a hard split, which is the only option left.
func ChunkSpansCode(src string, maxChars int) []ChunkSpan {
	if maxChars <= 0 {
		maxChars = 1500
	}
	trimmed := strings.TrimSpace(src)
	if trimmed == "" {
		return nil
	}
	if len(trimmed) <= maxChars {
		base := strings.Index(src, trimmed)
		if base < 0 {
			base = 0
		}
		return []ChunkSpan{{Text: trimmed, Start: base, End: base + len(trimmed)}}
	}

	lines := scanCodeLines(src)

	var spans []ChunkSpan
	chunkStart := -1  // byte offset where the current chunk began
	lastSafeEnd := -1 // end of the last line that closed at depth zero

	emit := func(end int) {
		if chunkStart < 0 || end <= chunkStart {
			return
		}
		text := strings.TrimSpace(src[chunkStart:end])
		if text == "" {
			return
		}
		// Report the trimmed extent, so a span points at content rather than
		// at the whitespace around it.
		lead := strings.Index(src[chunkStart:end], text)
		if lead < 0 {
			lead = 0
		}
		spans = append(spans, ChunkSpan{
			Text:  text,
			Start: chunkStart + lead,
			End:   chunkStart + lead + len(text),
		})
	}

	for _, ln := range lines {
		if chunkStart < 0 {
			if ln.blank {
				continue // do not start a chunk on blank lines
			}
			chunkStart = ln.start
		}

		if ln.depthAfter == 0 {
			lastSafeEnd = ln.end
		}

		// Long enough to close, and a blank line at depth zero is the cleanest
		// place there is: a rule or a function just ended.
		if ln.blank && ln.depthAfter == 0 && lastSafeEnd > chunkStart &&
			lastSafeEnd-chunkStart >= maxChars/2 {
			emit(lastSafeEnd)
			chunkStart = -1
			lastSafeEnd = -1
			continue
		}

		if ln.end-chunkStart < maxChars {
			continue
		}

		// Over budget. Close at the last depth-zero line if there is one;
		// otherwise the construct is longer than a chunk and must not be cut
		// mid-nesting, so carry on until it closes.
		if lastSafeEnd > chunkStart {
			emit(lastSafeEnd)
			chunkStart = -1
			lastSafeEnd = -1
		}
	}

	if chunkStart >= 0 {
		emit(len(src))
	}

	// Spans can still exceed the budget, for two very different reasons, and
	// only one of them may be cut.
	return hardSplitOversizedSpans(spans, maxChars)
}

// hardSplitOversizedSpans breaks an oversized span only when it is a single
// line — a minified file, where there is no boundary to prefer and the budget
// is the only guide left.
//
// A multi-line span over the budget means one construct is larger than a
// chunk: a long function, a big rule. Cutting it would produce exactly what
// this chunker exists to prevent — half a construct, which reads as nothing
// and embeds as noise. So the budget yields to the boundary, and such a chunk
// is left whole and oversized. That is a deliberate trade: an oversized chunk
// costs tokens, a meaningless one costs the answer.
func hardSplitOversizedSpans(spans []ChunkSpan, maxChars int) []ChunkSpan {
	var out []ChunkSpan
	for _, s := range spans {
		if len(s.Text) <= maxChars || strings.Contains(s.Text, "\n") {
			out = append(out, s)
			continue
		}
		out = append(out, splitSingleLine(s, maxChars)...)
	}
	return out
}

// splitSingleLine cuts a minified line into pieces, preferring the last point
// within budget where the brackets are balanced.
//
// A minified stylesheet has no newlines, but it does have boundaries:
// `.a{color:red}.b{color:blue}` closes a rule after every `}`. Cutting there
// keeps the invariant even here, and only a line with no balanced point at all
// — a single enormous expression — falls back to cutting at the budget.
func splitSingleLine(s ChunkSpan, maxChars int) []ChunkSpan {
	var out []ChunkSpan
	offset := 0
	for offset < len(s.Text) {
		remaining := s.Text[offset:]
		if len(remaining) <= maxChars {
			out = append(out, ChunkSpan{
				Text:  remaining,
				Start: s.Start + offset,
				End:   s.Start + offset + len(remaining),
			})
			break
		}

		cut := lastBalancedPoint(remaining, maxChars)
		if cut <= 0 {
			cut = maxChars // nothing balanced in range: the budget is all there is
		}
		out = append(out, ChunkSpan{
			Text:  remaining[:cut],
			Start: s.Start + offset,
			End:   s.Start + offset + cut,
		})
		offset += cut
	}
	return out
}

// lastBalancedPoint returns the largest offset up to limit at which the
// brackets in s[:offset] are balanced, or 0 when there is none.
func lastBalancedPoint(s string, limit int) int {
	if limit > len(s) {
		limit = len(s)
	}
	depth := 0
	best := 0
	var inStr bool
	var quote byte

	for i := 0; i < limit; i++ {
		c := s[i]
		if inStr {
			switch c {
			case '\\':
				i++
			case quote:
				inStr = false
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			inStr, quote = true, c
		case '{', '(', '[':
			depth++
		case '}', ')', ']':
			if depth > 0 {
				depth--
			}
			if depth == 0 {
				best = i + 1 // just past the closing bracket
			}
		}
	}
	return best
}
