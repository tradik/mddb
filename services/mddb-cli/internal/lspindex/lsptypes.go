package lspindex

import (
	"net/url"
	"path/filepath"
)

// The LSP 3.17 structures the driver reads and writes, reduced to the fields
// it uses.

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

// contains reports whether p lies inside r, end exclusive.
func (r lspRange) contains(p position) bool {
	return !p.before(r.Start) && p.before(r.End)
}

// before orders positions.
func (p position) before(q position) bool {
	return p.Line < q.Line || (p.Line == q.Line && p.Character < q.Character)
}

// lines is the 1-based inclusive line span of r. An end at character 0 of a
// later line means the range stops at the end of the previous one.
func (r lspRange) lines() (start, end int) {
	start, end = r.Start.Line+1, r.End.Line+1
	if r.End.Character == 0 && end > start {
		end--
	}
	return start, end
}

type location struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type workspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type textDocumentID struct {
	URI string `json:"uri"`
}

type positionParams struct {
	TextDocument textDocumentID `json:"textDocument"`
	Position     position       `json:"position"`
}

type referenceParams struct {
	positionParams
	Context struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
}

// documentSymbol decodes both answers documentSymbol may give: the
// hierarchical DocumentSymbol (range, selectionRange, children) and the flat
// SymbolInformation (location, containerName) of older servers.
type documentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail"`
	Kind           int              `json:"kind"`
	Range          *lspRange        `json:"range"`
	SelectionRange *lspRange        `json:"selectionRange"`
	Children       []documentSymbol `json:"children"`
	Location       *location        `json:"location"`
	ContainerName  string           `json:"containerName"`
}

// serverInfo is InitializeResult.serverInfo.
type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// LSP SymbolKind values the driver treats specially.
const (
	symbolClass       = 5
	symbolMethod      = 6
	symbolConstructor = 9
	symbolEnum        = 10
	symbolInterface   = 11
	symbolFunction    = 12
	symbolStruct      = 23
)

// fileURI turns an absolute path into a file:// URI, escaping as servers do.
func fileURI(abs string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}

// uriPath turns a file:// URI back into a path; ok is false for any other
// scheme (servers report locations in untitled buffers and jar-like archives).
func uriPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return "", false
	}
	return filepath.FromSlash(u.Path), true
}
