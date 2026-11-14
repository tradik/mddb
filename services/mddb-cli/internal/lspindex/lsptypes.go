package lspindex

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
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
	// Tags are SymbolTag values; clangd 23 marks a definition (19) apart
	// from a declaration (18), the LSP 3.18 proposal's values.
	Tags []int `json:"tags"`
}

// serverInfo is InitializeResult.serverInfo.
type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// allSymbolKinds declares every SymbolKind of LSP 3.17 (File=1 to
// TypeParameter=26) known to the client.
var allSymbolKinds = map[string]any{"valueSet": func() []int {
	kinds := make([]int, 26)
	for i := range kinds {
		kinds[i] = i + 1
	}
	return kinds
}()}

// LSP SymbolKind values the driver treats specially.
const (
	symbolModule      = 2
	symbolNamespace   = 3
	symbolClass       = 5
	symbolMethod      = 6
	symbolConstructor = 9
	symbolEnum        = 10
	symbolInterface   = 11
	symbolFunction    = 12
	symbolObject      = 19
	symbolStruct      = 23
	// symbolTypeParameter is what rust-analyzer reports a type alias as.
	symbolTypeParameter = 26
)

// windowsPaths says whether paths carry a drive letter (C:\src). A variable,
// so the drive-letter handling is tested on every platform.
var windowsPaths = runtime.GOOS == "windows"

// fileURI turns an absolute path into a file:// URI, escaping as servers do.
// A Windows path gets the slash before its drive letter: C:\src\a.go is
// file:///C:/src/a.go, not file://C:/src/a.go, where C: would be the host.
func fileURI(abs string) string {
	p := filepath.ToSlash(abs)
	if windowsPaths {
		p = strings.ReplaceAll(p, `\`, "/")
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// uriPath turns a file:// URI back into a path; ok is false for any other
// scheme (servers report locations in untitled buffers and jar-like archives).
// On Windows the slash before the drive letter goes again (/c:/src → c:/src;
// servers often write the colon as %3A, which url.Parse decodes).
func uriPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return "", false
	}
	p := u.Path
	if windowsPaths && len(p) >= 3 && p[0] == '/' && p[2] == ':' && isASCIILetter(p[1]) {
		p = p[1:]
	}
	return filepath.FromSlash(p), true
}

func isASCIILetter(b byte) bool { return (b|0x20) >= 'a' && (b|0x20) <= 'z' }
