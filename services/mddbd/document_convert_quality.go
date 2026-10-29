package main

import (
	"fmt"
	"math"
	"strconv"
	"unicode"
	"unicode/utf8"
)

// Measuring a conversion (GO-046).
//
// A converter that "succeeds" on a scanned PDF stores an empty document, and
// one that misreads an encoding stores noise; both used to look like any other
// document. conversionConfidence is a number from 0 to 1, from heuristics
// only — no model, no OCR — with conversionWarnings saying what lowered it, so
// `filter: {conversionConfidence: {lt: 0.5}}` finds the documents worth a
// second look and the search advisor can say how many there are.

// conversionSignals is what a converter knows about its own result beyond the
// text: how many pages or sheets it saw and how many of them were empty.
type conversionSignals struct {
	Pages, PagesWithoutText int
	Sheets, EmptySheets     int
	// Container marks formats whose file holds more than text (zip, PDF):
	// their text-to-size ratio says something; HTML's does not.
	Container bool
	// Warnings are the converter's own remarks; they do not change the score.
	Warnings []string
}

const (
	// badShareWeight: each 1% of replacement or non-printable characters
	// costs 4% of confidence; a quarter of them leaves nothing.
	badShareWeight = 4.0
	// badShareWarn is the share above which the warning is worth reading.
	badShareWarn = 0.01
	// minTextRatio is the text-to-file-size ratio below which a container
	// is mostly something else — images, embedded objects, fonts.
	minTextRatio = 0.005
	// lowConfidence is the threshold the search advisor reports.
	lowConfidence = 0.5
)

// assessConversion scores a conversion of data into md and says why.
func assessConversion(data []byte, md string, sig conversionSignals) (float64, []string) {
	warnings := append([]string(nil), sig.Warnings...)
	score := 1.0

	if sig.Pages > 0 && sig.PagesWithoutText > 0 {
		score *= float64(sig.Pages-sig.PagesWithoutText) / float64(sig.Pages)
		warnings = append(warnings, fmt.Sprintf("%d of %d pages have no extractable text (scanned, or a font encoding the converter cannot read; the server does no OCR)",
			sig.PagesWithoutText, sig.Pages))
	}
	if len(data) > 0 && !hasLetterOrDigit(md) {
		return 0, append(warnings, "no text extracted")
	}
	if sig.Sheets > 0 && sig.EmptySheets > 0 {
		score *= 1 - 0.5*float64(sig.EmptySheets)/float64(sig.Sheets)
		warnings = append(warnings, fmt.Sprintf("%d of %d sheets are empty", sig.EmptySheets, sig.Sheets))
	}
	if bad := badCharShare(md); bad > 0 {
		score *= math.Max(0, 1-badShareWeight*bad)
		if bad > badShareWarn {
			warnings = append(warnings, fmt.Sprintf("%.1f%% of characters are replacement or non-printable characters", 100*bad))
		}
	}
	if sig.Container && len(data) > 0 {
		if ratio := float64(len(md)) / float64(len(data)); ratio < minTextRatio {
			// At most halves the score: a slide deck of photographs is not a
			// failed conversion, but its text is not the whole document.
			score *= 0.5 + 0.5*ratio/minTextRatio
			warnings = append(warnings, fmt.Sprintf("%d bytes of text from a %d-byte file: images or embedded objects hold the rest", len(md), len(data)))
		}
	}
	return math.Round(score*100) / 100, warnings
}

// hasLetterOrDigit reports whether s has any letter or digit — whether there
// is text at all, beyond table rules and whitespace.
func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// badCharShare is the share of characters that are U+FFFD, invalid UTF-8, or
// neither printable nor whitespace.
func badCharShare(s string) float64 {
	total, bad := 0, 0
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		total++
		if r == utf8.RuneError || (!unicode.IsPrint(r) && !unicode.IsSpace(r)) {
			bad++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(bad) / float64(total)
}

// conversionMeta writes a conversion's score and warnings into meta.
func conversionMeta(meta map[string][]string, c conversion) {
	if !c.Converted {
		return
	}
	meta["conversionConfidence"] = []string{strconv.FormatFloat(c.Confidence, 'f', 2, 64)}
	if len(c.Warnings) > 0 {
		meta["conversionWarnings"] = c.Warnings
	}
	for k, v := range c.Meta {
		if _, set := meta[k]; !set {
			meta[k] = v
		}
	}
}
