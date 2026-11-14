package main

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"unicode"

	"mddb/internal/envconf"
	"mddb/internal/fts"
	"mddb/internal/storage"
)

// Semantic code facts (CODE-010).
//
// mddb-cli `autoindex --semantic` asks a language server where each symbol is
// defined, referenced and implemented, and sends the answers as four flat meta
// keys. The format is the contract in testdata/semantic-facts/README.md; the
// CLI and this file implement it separately and both run cases.json.
//
// Facts are true only of the content they were computed from, so every write
// checks them against the content being stored and drops them when they no
// longer match. Unlike defines/uses/imports, which the extractor owns and
// rewrites, these come from the client: the server only keeps or discards.
const (
	MetaKeySymIndex = "symIndex"
	MetaKeySymDefs  = "symDefs"
	MetaKeySymRefs  = "symRefs"
	MetaKeySymImpls = "symImpls"

	// DefaultMaxSemanticFacts caps the values a document keeps across
	// symDefs, symRefs and symImpls (MDDB_CODE_MAX_FACTS). Meta is loaded with
	// the document on every read, so a generated file with thousands of
	// references must not make each read of it heavy.
	DefaultMaxSemanticFacts = 2048

	semanticFactsVersion   = "v1"
	semanticFactsTruncated = "truncated"
	// maxFactValueBytes keeps every value far below bbolt's key limit: each
	// value becomes a metadata index key (meta|coll|key|value|docID).
	maxFactValueBytes   = 1024
	maxRefLinesPerValue = 64
)

// semanticFactKeys lists every key this convention owns.
var semanticFactKeys = [...]string{MetaKeySymIndex, MetaKeySymDefs, MetaKeySymRefs, MetaKeySymImpls}

// semanticFactPriority is the order values survive the limit in: a
// definition answers "where is it", which a reference list cannot.
var semanticFactPriority = [...]string{MetaKeySymDefs, MetaKeySymImpls, MetaKeySymRefs}

// semanticFactLanguages are the languages mddb-cli computes facts for; GET
// /v1/code-languages announces them so the CLI keeps no list of its own.
var semanticFactLanguages = [...]string{"go", "php", "python"}

var semanticFactKinds = map[string]bool{
	"function": true, "method": true, "class": true, "interface": true,
	"struct": true, "type": true, "enum": true, "constructor": true,
}

// enforceSemanticFacts applies keepSemanticFacts with the configured limit.
// The environment is read only for a document that carries facts, which
// almost none do.
func enforceSemanticFacts(doc *storage.Doc) {
	if doc == nil || !hasSemanticFacts(doc.Meta) {
		return
	}
	keepSemanticFacts(doc, envconf.Int("MDDB_CODE_MAX_FACTS", DefaultMaxSemanticFacts))
}

// keepSemanticFacts reduces a document's sym* keys to what the contract lets
// the server store: nothing for a document that is not code or whose
// symIndex does not match its content; otherwise the well-formed values in
// sorted order, at most maxFacts of them.
func keepSemanticFacts(doc *storage.Doc, maxFacts int) {
	if doc == nil || !hasSemanticFacts(doc.Meta) {
		return
	}
	index, ok := parseSymIndex(doc.Meta[MetaKeySymIndex])
	if !ok || index.contentHash != semanticContentHash(doc.ContentMD) || DocumentKind(doc) != fts.KindCode {
		hideSemanticFacts(doc)
		return
	}
	budget := max(maxFacts, 0)
	truncated := index.truncated
	for _, key := range semanticFactPriority {
		vals := validFactValues(key, doc.Meta[key])
		if len(vals) > budget {
			vals, truncated = vals[:budget], true
		}
		budget -= len(vals)
		if len(vals) == 0 {
			delete(doc.Meta, key)
		} else {
			doc.Meta[key] = vals
		}
	}
	doc.Meta[MetaKeySymIndex] = []string{index.value(truncated)}
}

// hasSemanticFacts reports whether meta holds any sym* key.
func hasSemanticFacts(meta map[string][]string) bool {
	for _, key := range semanticFactKeys {
		if _, ok := meta[key]; ok {
			return true
		}
	}
	return false
}

// hideSemanticFacts removes every sym* key from a document. Write paths use
// it to discard facts; result paths use it because facts are for code_symbol,
// and a search hit carrying hundreds of reference lines would bury the rest.
func hideSemanticFacts(doc *storage.Doc) {
	if doc == nil {
		return
	}
	for _, key := range semanticFactKeys {
		delete(doc.Meta, key)
	}
}

// loadResultDoc is loadDoc for a document returned as a search or listing
// result: the same document without its semantic facts. A single-document
// read uses loadDoc and returns them.
func loadResultDoc(data []byte) (*storage.Doc, error) {
	doc, err := loadDoc(data)
	hideSemanticFacts(doc)
	return doc, err
}

// semanticContentHash is the first 12 hex digits of SHA-256 over content as
// stored.
func semanticContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:6])
}

// symIndex is a parsed `v1 <contentHash> <factsDigest> <source>[ truncated]`.
type symIndex struct {
	contentHash, factsDigest, source string
	truncated                        bool
}

// parseSymIndex accepts exactly one v1 value. The server's own " truncated"
// suffix is accepted too, so a document it already trimmed survives the next
// write (a TTL change, a meta patch) unchanged.
func parseSymIndex(vals []string) (symIndex, bool) {
	if len(vals) != 1 {
		return symIndex{}, false
	}
	parts := strings.Split(vals[0], " ")
	truncated := len(parts) == 5 && parts[4] == semanticFactsTruncated
	if truncated {
		parts = parts[:4]
	}
	if len(parts) != 4 || parts[0] != semanticFactsVersion || !isFactName(parts[3]) {
		return symIndex{}, false
	}
	return symIndex{contentHash: parts[1], factsDigest: parts[2], source: parts[3], truncated: truncated}, true
}

func (x symIndex) value(truncated bool) string {
	v := strings.Join([]string{semanticFactsVersion, x.contentHash, x.factsDigest, x.source}, " ")
	if truncated {
		v += " " + semanticFactsTruncated
	}
	return v
}

// validFactValues returns the values of key that parse, sorted bytewise.
func validFactValues(key string, vals []string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if validFactValue(key, v) {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// isFactSpace matches what the reference encoder's `\s` does on text: Unicode
// white space and the information separators U+001C..U+001F.
func isFactSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// isFactName reports a name, kind, container or source: non-empty, with no
// white space, `|` or `#`.
func isFactName(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(r rune) bool { return r == '|' || r == '#' || isFactSpace(r) })
}

// isFactPath reports a path: non-empty, with no white space or `|`.
func isFactPath(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(r rune) bool { return r == '|' || isFactSpace(r) })
}
