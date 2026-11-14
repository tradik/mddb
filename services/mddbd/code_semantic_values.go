package main

import (
	"slices"
	"strconv"
	"strings"
)

// Semantic fact values (CODE-010): parsing one value of symDefs, symRefs or
// symImpls, and decoding stored values into types code_symbol can use.

// SemanticFacts are the facts a code document carries, decoded.
type SemanticFacts struct {
	// Source is the language server that computed them, e.g. gopls/0.24.0.
	Source string
	// Truncated is set when the server dropped values to stay under its limit.
	Truncated bool
	Defs      []SymbolDefFact
	Refs      []SymbolRefFact
	Impls     []SymbolImplFact
}

// SymbolDefFact is a definition in this document.
type SymbolDefFact struct {
	Name, Kind string // qualified name, e.g. Server.LookupSymbol
	Start, End int    // 1-based line span
	Container  string // enclosing type or class; "" at top level
}

// SymbolRefFact is every use in this document of one symbol defined at
// Path, its continuation values merged.
type SymbolRefFact struct {
	Path, Name string
	Lines      []int // ascending, unique
}

// SymbolImplFact is a type here implementing the interface Name at Path.
type SymbolImplFact struct {
	Path, Name, Local string
}

// decodeSemanticFacts reads the facts stored on a document. It does not
// re-check the content hash — the write that stored them did — and it is
// tolerant: a value it cannot read is skipped, never an error. ok is false
// when there is no well-formed symIndex, i.e. no facts at all.
func decodeSemanticFacts(meta map[string][]string) (facts SemanticFacts, ok bool) {
	index, ok := parseSymIndex(meta[MetaKeySymIndex])
	if !ok {
		return SemanticFacts{}, false
	}
	facts.Source, facts.Truncated = index.source, index.truncated
	for _, v := range meta[MetaKeySymDefs] {
		if d, ok := decodeDefFact(v); ok {
			facts.Defs = append(facts.Defs, d)
		}
	}
	refAt := map[string]int{}
	for _, v := range meta[MetaKeySymRefs] {
		r, ok := decodeRefFact(v)
		if !ok {
			continue
		}
		target := r.Path + "#" + r.Name
		if i, seen := refAt[target]; seen {
			facts.Refs[i].Lines = append(facts.Refs[i].Lines, r.Lines...)
			continue
		}
		refAt[target] = len(facts.Refs)
		facts.Refs = append(facts.Refs, r)
	}
	// Continuation values sort as text, so "100,..." precedes "9,...".
	for i := range facts.Refs {
		slices.Sort(facts.Refs[i].Lines)
		facts.Refs[i].Lines = slices.Compact(facts.Refs[i].Lines)
	}
	for _, v := range meta[MetaKeySymImpls] {
		if validFactValue(MetaKeySymImpls, v) {
			target, local, _ := strings.Cut(v, " ")
			path, name, _ := splitFactTarget(target)
			facts.Impls = append(facts.Impls, SymbolImplFact{Path: path, Name: name, Local: local})
		}
	}
	return facts, true
}

func decodeDefFact(v string) (SymbolDefFact, bool) {
	if !validFactValue(MetaKeySymDefs, v) {
		return SymbolDefFact{}, false
	}
	parts := strings.Split(v, " ")
	from, to, _ := strings.Cut(parts[2], "-")
	start, err1 := strconv.Atoi(from)
	end, err2 := strconv.Atoi(to)
	if err1 != nil || err2 != nil {
		return SymbolDefFact{}, false // valid, but past int range
	}
	d := SymbolDefFact{Name: parts[0], Kind: parts[1], Start: start, End: end}
	if len(parts) == 4 {
		d.Container = parts[3]
	}
	return d, true
}

func decodeRefFact(v string) (SymbolRefFact, bool) {
	if !validFactValue(MetaKeySymRefs, v) {
		return SymbolRefFact{}, false
	}
	target, list, _ := strings.Cut(v, " ")
	path, name, _ := splitFactTarget(target)
	r := SymbolRefFact{Path: path, Name: name}
	for _, n := range strings.Split(list, ",") {
		line, err := strconv.Atoi(n)
		if err != nil {
			return SymbolRefFact{}, false
		}
		r.Lines = append(r.Lines, line)
	}
	return r, true
}

// validFactValue reports whether v is one well-formed value of key
// (symDefs, symRefs or symImpls) under the rules in the contract README.
// Numbers are compared as decimal text, so a value the reference accepts is
// never refused here for not fitting an int.
func validFactValue(key, v string) bool {
	if len(v) > maxFactValueBytes || strings.Contains(v, "|") {
		return false
	}
	parts := strings.Split(v, " ")
	if key == MetaKeySymDefs {
		return validDefParts(parts)
	}
	if len(parts) != 2 {
		return false
	}
	if _, _, ok := splitFactTarget(parts[0]); !ok {
		return false
	}
	if key == MetaKeySymRefs {
		return validRefLines(parts[1])
	}
	return isFactName(parts[1])
}

// validDefParts checks `<name> <kind> <start>-<end>[ <container>]`.
func validDefParts(parts []string) bool {
	if len(parts) != 3 && len(parts) != 4 {
		return false
	}
	if !isFactName(parts[0]) || !semanticFactKinds[parts[1]] {
		return false
	}
	from, to, ok := strings.Cut(parts[2], "-")
	if !ok || !isLineNumber(from) || !isLineNumber(to) || decimalLess(to, from) {
		return false
	}
	return len(parts) == 3 || isFactName(parts[3])
}

// validRefLines checks 1..64 comma-separated line numbers, strictly ascending.
func validRefLines(list string) bool {
	nums := strings.Split(list, ",")
	if len(nums) > maxRefLinesPerValue {
		return false
	}
	for i, n := range nums {
		if !isLineNumber(n) || (i > 0 && !decimalLess(nums[i-1], n)) {
			return false
		}
	}
	return true
}

// splitFactTarget splits `<path>#<name>` at the last `#`: a path may hold one.
func splitFactTarget(s string) (path, name string, ok bool) {
	i := strings.LastIndexByte(s, '#')
	if i < 0 {
		return "", "", false
	}
	path, name = s[:i], s[i+1:]
	return path, name, isFactPath(path) && isFactName(name)
}

// isLineNumber matches [1-9][0-9]*.
func isLineNumber(s string) bool {
	if s == "" || s[0] == '0' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// decimalLess compares two line numbers without converting them.
func decimalLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}
