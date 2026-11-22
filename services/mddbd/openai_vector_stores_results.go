package main

import (
	"strings"

	"mddb/internal/storage"
)

// The response of POST /v1/vector_stores/{id}/search (#446): each hit as an
// OpenAI search result, in the fields Open WebUI reads.

// openAISearchPage is the response.
type openAISearchPage struct {
	Object      string               `json:"object"`
	SearchQuery []string             `json:"search_query"`
	Data        []openAISearchResult `json:"data"`
	HasMore     bool                 `json:"has_more"`
	NextPage    *string              `json:"next_page"`
}

// openAISearchResult is one passage: Open WebUI joins content[].text, titles
// it with filename, links attributes.url and keeps file_id as the source id.
type openAISearchResult struct {
	FileID     string          `json:"file_id"`
	Filename   string          `json:"filename"`
	Score      float64         `json:"score"`
	Attributes map[string]any  `json:"attributes"`
	Content    []openAIContent `json:"content"`
}

type openAIContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// openAIResults shapes hits as OpenAI search results, dropping those under
// ranking_options.score_threshold.
func openAIResults(collection string, queries []string, hits []openAIHit, threshold float64) []openAISearchResult {
	words := queryTerms(strings.Join(queries, " "))
	out := []openAISearchResult{}
	for i := range hits {
		h := &hits[i]
		if h.score < threshold {
			continue
		}
		terms := h.terms
		if len(terms) == 0 {
			terms = words // a hit only the vector half found
		}
		p := passageAtHit(collection, &h.doc, terms, h.highlights, PassageOptions{}, h.score, "openai")
		out = append(out, openAISearchResult{FileID: h.doc.ID, Filename: h.doc.Key, Score: h.score,
			Attributes: openAIAttributes(collection, &h.doc), Content: []openAIContent{{Type: "text", Text: p.Content}}})
	}
	return out
}

// openAIAttributes is the document's metadata as OpenAI attributes (a value
// per key: several values are joined with ", "), plus collection, lang, and
// url when the metadata has one.
func openAIAttributes(collection string, doc *storage.Doc) map[string]any {
	attrs := make(map[string]any, len(doc.Meta)+3)
	for k, vs := range doc.Meta {
		attrs[k] = strings.Join(vs, ", ")
	}
	if urls := doc.Meta["url"]; len(urls) > 0 {
		attrs["url"] = urls[0]
	}
	attrs["collection"] = collection
	attrs["lang"] = doc.Lang
	return attrs
}
