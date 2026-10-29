package main

import (
	"fmt"
	"strconv"
)

// Converted documents in the search advisor (GO-046). A document converted
// badly — a scan with no text layer, a file in an encoding the converter
// misread — is in the collection and in no result: there is nothing in it to
// match. The advisor cannot fix that, but it can say how many there are and
// how to find them.

// countConversion counts a sampled document's conversion score.
func (p *CollectionProfile) countConversion(meta map[string][]string) {
	v := meta["conversionConfidence"]
	if len(v) == 0 {
		return
	}
	p.ConvertedDocuments++
	if f, err := strconv.ParseFloat(v[0], 64); err == nil && f < lowConfidence {
		p.LowConfidenceDocuments++
	}
}

// adviseConversions warns when sampled documents converted with low
// confidence, and names the filter that lists them.
func adviseConversions(r *SearchRecommendation, p *CollectionProfile) {
	if p.LowConfidenceDocuments == 0 {
		return
	}
	r.Warnings = append(r.Warnings, fmt.Sprintf(
		"%d of %d sampled documents (%.0f%%) converted with conversionConfidence below %.1f: scans without a text layer or files the converter misread, which no search can find. "+
			`List them with filter {"conversionConfidence": {"lt": %.1f}} and re-convert them with OCR on the client.`,
		p.LowConfidenceDocuments, p.Sampled, p.LowConfidenceRatio*100, lowConfidence, lowConfidence))
}
