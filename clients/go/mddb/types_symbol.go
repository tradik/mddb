package mddb

import "context"

// SymbolRequest asks where a symbol is defined and used (GET/POST
// /v1/symbol). Name is bare (Close) or, where the collection carries
// semantic facts, qualified (Conn.Close); Path picks the definition in one
// file when a name has several.
type SymbolRequest struct {
	Collection     string   `json:"collection"`
	Name           string   `json:"name"`
	Path           string   `json:"path,omitempty"`
	Include        []string `json:"include,omitempty"` // definitions, references, callers, callees, tests, siblings, implementations
	Limit          int      `json:"limit,omitempty"`
	IncludeContent bool     `json:"includeContent,omitempty"`
}

// PassageLines is a 1-based, inclusive line range.
type PassageLines struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Passage is a place in a document: path, lines, the definition holding
// them and optionally their text. Container and Target come from semantic
// facts: the type holding a definition, and "<path>#<qualified name>" of the
// looked-up definition the passage is about.
type Passage struct {
	Collection string       `json:"collection"`
	Key        string       `json:"key"`
	Lang       string       `json:"lang,omitempty"`
	Path       string       `json:"path"`
	Lines      PassageLines `json:"lines"`
	Symbol     string       `json:"symbol,omitempty"`
	Kind       string       `json:"kind,omitempty"`
	Container  string       `json:"container,omitempty"`
	Target     string       `json:"target,omitempty"`
	Content    string       `json:"content,omitempty"`
	Score      float64      `json:"score,omitempty"`
	Source     string       `json:"source,omitempty"`
	Tokens     int          `json:"tokens"`
}

// SymbolCandidate is one definition an ambiguous name may mean; ask again
// with its Name and Path.
type SymbolCandidate struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// SymbolResult is the /v1/symbol response. Precision is "semantic",
// "mixed" or "heuristic", and empty for a heuristic answer to a request
// that used neither Path nor implementations.
type SymbolResult struct {
	Collection      string            `json:"collection"`
	Symbol          string            `json:"symbol"`
	Definitions     []Passage         `json:"definitions"`
	References      []Passage         `json:"references,omitempty"`
	Callers         []Passage         `json:"callers,omitempty"`
	Callees         []Passage         `json:"callees,omitempty"`
	Tests           []Passage         `json:"tests,omitempty"`
	Siblings        []Passage         `json:"siblings,omitempty"`
	Implementations []Passage         `json:"implementations,omitempty"`
	Precision       string            `json:"precision,omitempty"`
	SemanticSource  string            `json:"semanticSource,omitempty"`
	Ambiguous       bool              `json:"ambiguous,omitempty"`
	Candidates      []SymbolCandidate `json:"candidates,omitempty"`
	Truncated       bool              `json:"truncated,omitempty"`
	Limit           int               `json:"limit,omitempty"`
	LimitCapped     bool              `json:"limitCapped,omitempty"`
}

// CodeSymbol looks a symbol up: definitions, references, callers, callees,
// tests, siblings and, with semantic facts, implementations.
func (c *Client) CodeSymbol(ctx context.Context, req SymbolRequest) (*SymbolResult, error) {
	var out SymbolResult
	if err := c.doJSON(ctx, "POST", "/v1/symbol", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
