package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"mddb/internal/fts"
)

// code_search: one small search tool for agents working on code.
//
// The search tools return JSON documents or passages and take forty
// parameters between them; their schemas alone cost an agent more than a
// grep answer does. code_search has five parameters and answers in lines
// (mcp_lines.go). It runs the same searches — nothing is indexed or ranked
// differently — and has no output schema: a tool with one must return its
// data as structuredContent, which is JSON again.

// Modes of code_search.
const (
	CodeSearchExact   = "exact"
	CodeSearchWords   = "words"
	CodeSearchMeaning = "meaning"

	codeSearchHits = 5 // default hits, words and meaning
	// codeSearchContext is the lines kept on each side of a ranked hit.
	codeSearchContext = 2
	// codeSearchExactLines is the default number of matching lines shown.
	codeSearchExactLines = 50
	// codeSearchScanDocs is the most documents an exact search reads, a
	// page of codeSearchPage at a time; past it the count says the scan
	// stopped, and a path prefix narrows it.
	codeSearchScanDocs = 5000
	codeSearchPage     = 200
)

var errCodeSearchRequest = errors.New("collection and query are required")

// mcpCodeSearchTool is the tool's definition. Its schema is under a budget
// test (mcp_budget_test.go): keep descriptions to what an agent cannot guess.
func mcpCodeSearchTool() MCPTool {
	str := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	return MCPTool{
		Name: "code_search",
		Description: "Search a code collection. Answers like grep: path:line: text, then a count saying " +
			"whether that is all. For a known symbol's callers or implementations use code_symbol.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"collection": map[string]interface{}{"type": "string"},
				"query":      map[string]interface{}{"type": "string"},
				"mode": map[string]interface{}{"type": "string", "enum": []string{CodeSearchExact, CodeSearchWords, CodeSearchMeaning},
					"description": "exact: every line with this identifier or literal; words (default): ranked; meaning: described in other words"},
				"path":  str("Directory prefix, e.g. internal/"),
				"limit": map[string]interface{}{"type": "integer", "description": "Hits (default 5; exact: 50 lines)"},
			},
			"required": []string{"collection", "query"},
		},
	}
}

// codeSearchRequest is a code_search call.
type codeSearchRequest struct {
	collection, query string
	limit             int
	filter            MetaFilter
}

// toolCodeSearch answers code_search.
func (s *MCPToolServer) toolCodeSearch(ctx context.Context, args map[string]interface{}) (string, error) {
	req := codeSearchRequest{collection: mcpGetString(args, "collection"),
		query: strings.TrimSpace(mcpGetString(args, "query")), limit: mcpGetInt(args, "limit")}
	if req.collection == "" || req.query == "" {
		return "", errCodeSearchRequest
	}
	if prefix := mcpGetString(args, "path"); prefix != "" {
		req.filter = MetaFilter{fts.MetaKeyPath: MetaCondition{Prefix: prefix}}
	}
	switch mode := mcpGetString(args, "mode"); mode {
	case CodeSearchExact:
		return s.codeSearchExact(ctx, req)
	case "", CodeSearchWords:
		return s.codeSearchWords(ctx, req)
	case CodeSearchMeaning:
		return s.codeSearchMeaning(ctx, req)
	default:
		return "", fmt.Errorf("unknown mode %q (known: exact, words, meaning)", mode)
	}
}

// hits is the number of ranked hits to show.
func (r codeSearchRequest) hits() int {
	if r.limit <= 0 {
		return codeSearchHits
	}
	return r.limit
}

// passageOptions asks for short passages; one more hit than shown tells
// whether the list is complete.
func (r codeSearchRequest) passageOptions() PassageOptions {
	return PassageOptions{Format: FormatPassages, PassageLines: codeSearchContext}
}

// rankedLines renders the passages of a ranked search, at most hits of them.
func (r codeSearchRequest) rankedLines(p *PassagesResponse) string {
	if p == nil {
		return passageLines(nil, false)
	}
	shown := p.Passages
	more := len(shown) > r.hits()
	if more {
		shown = shown[:r.hits()]
	}
	return passageLines(shown, more)
}

// codeSearchWords is the full-text search, ranked.
func (s *MCPToolServer) codeSearchWords(ctx context.Context, r codeSearchRequest) (string, error) {
	resp, err := s.client.FTSSearch(ctx, &MCPFTSSearchRequest{Collection: r.collection, Query: r.query,
		Limit: r.hits() + 1, Filter: r.filter, IncludeContent: true, PassageOptions: r.passageOptions()})
	if err != nil {
		return "", err
	}
	return r.rankedLines(resp.Passages), nil
}

// codeSearchMeaning is the hybrid search: full text and vectors together. A
// server without embeddings answers by words and says so, since an agent
// that asked in its own words still wants the nearest thing to an answer.
func (s *MCPToolServer) codeSearchMeaning(ctx context.Context, r codeSearchRequest) (string, error) {
	resp, err := s.client.HybridSearch(ctx, &MCPHybridSearchRequest{Collection: r.collection, Query: r.query,
		TopK: r.hits() + 1, Filter: r.filter, PassageOptions: r.passageOptions()})
	if err != nil {
		words, wordsErr := s.codeSearchWords(ctx, r)
		if wordsErr != nil {
			return "", err
		}
		return "meaning search unavailable (" + err.Error() + "); matched by words\n" + words, nil
	}
	return r.rankedLines(resp.Passages), nil
}

// exactFile is one file's lines holding the literal.
type exactFile struct {
	path  string
	st    *sourceText
	lines []int
}

// codeSearchExact lists every line holding the query as written, ignoring
// case: what grep -i would list. It reads the documents themselves, under the
// path prefix, because the count has to be true: the full-text index splits
// identifiers and stems prose, and a search through it missed files that hold
// the literal while saying that was all.
func (s *MCPToolServer) codeSearchExact(ctx context.Context, r codeSearchRequest) (string, error) {
	var files []exactFile
	scanned, total := 0, 0
	for scanned < codeSearchScanDocs {
		resp, err := s.client.Search(ctx, &MCPSearchRequest{Collection: r.collection, Filter: r.filter,
			Offset: scanned, Limit: codeSearchPage, IncludeContent: true, PassageOptions: PassageOptions{Format: FormatResults}})
		if err != nil {
			return "", err
		}
		files = append(files, exactFiles(resp.Documents, r.query)...)
		scanned, total = scanned+len(resp.Documents), resp.Total
		if len(resp.Documents) == 0 || scanned >= total {
			break
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	limit := r.limit
	if limit <= 0 {
		limit = codeSearchExactLines
	}
	out := exactLines(files, limit)
	if scanned < total {
		out += fmt.Sprintf("; read %d of %d files, narrow with path", scanned, total)
	}
	return out, nil
}

// exactFiles keeps the documents whose text holds the literal, with the
// lines it is on.
func exactFiles(docs []MCPDocument, literal string) []exactFile {
	literal = strings.ToLower(literal)
	var out []exactFile
	for i := range docs {
		if !strings.Contains(strings.ToLower(docs[i].ContentMD), literal) {
			continue
		}
		doc := mcpDocumentToDoc(docs[i])
		st := newSourceText(&doc)
		f := exactFile{path: docPath(&doc), st: st}
		for n, l := range st.lines {
			if strings.Contains(strings.ToLower(l), literal) {
				f.lines = append(f.lines, n+1)
			}
		}
		if len(f.lines) > 0 {
			out = append(out, f)
		}
	}
	return out
}

// exactLines renders the matching lines, "path:line: [Enclosing.Func] text",
// up to limit, and counts all of them.
func exactLines(files []exactFile, limit int) string {
	w := &lineWriter{}
	shown, total := 0, 0
	for _, f := range files {
		for _, n := range f.lines {
			if total++; shown >= limit {
				continue
			}
			shown++
			text := lineText(f.st.lines[n-1])
			if sym := f.st.symbolAt(n); sym != "" {
				text = "[" + sym + "] " + text
			}
			w.hit(f.path, fmt.Sprint(n), ": "+text)
		}
	}
	count := fmt.Sprintf("%d of %d lines in %d files", shown, total, len(files))
	if total > shown {
		count += ", more with limit or a narrower path"
	}
	w.line("%s", count)
	return w.String()
}
