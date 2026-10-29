package main

import (
	"bytes"
	"path"
	"regexp"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/codeintel"
	"mddb/internal/fts"
	"mddb/internal/storage"
)

// The relations of a symbol lookup (CODE-008), each derived from documents
// already found through the metadata index.

// occurrenceLines returns every line on which a form of the symbol appears as
// a whole word — every line, not only the first: a reference list that stops
// at one occurrence per file misses the second call site.
func occurrenceLines(content, symbol string) []int {
	var out []int
	seen := map[int]bool{}
	for _, needle := range symbolNeedles(symbol) {
		for i, line := range strings.Split(content, "\n") {
			if !seen[i+1] && containsWord(line, needle) {
				seen[i+1] = true
				out = append(out, i+1)
			}
		}
		if len(out) > 0 {
			break // the most specific form that occurs wins, as in firstSymbolLine
		}
	}
	sort.Ints(out)
	return out
}

// containsWord reports whether needle occurs in s not glued to a longer name.
func containsWord(s, needle string) bool {
	for from := 0; ; {
		i := strings.Index(s[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		before := i == 0 || !isNameByte(s[i-1])
		after := i+len(needle) >= len(s) || !isNameByte(s[i+len(needle)])
		if before && after {
			return true
		}
		from = i + 1
	}
}

func isNameByte(c byte) bool {
	return c == '_' || c == '$' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// symbolReferences lists each line that uses the symbol, with the definition
// it sits in as Symbol — the caller, found without parsing a call. With
// callsOnly, a line counts only where the name is called.
func symbolReferences(req SymbolRequest, users []*sourceText, callsOnly bool) []Passage {
	call := callPattern(req.Name)
	var out []Passage
	for _, st := range users {
		// A mention in prose, or in a comment, is not a use.
		if DocumentKind(st.doc) != fts.KindCode {
			continue
		}
		code := codeintel.WithoutComments(CodeLanguage(st.doc), st.doc.ContentMD)
		for _, line := range occurrenceLines(code, req.Name) {
			if callsOnly && !call.MatchString(lineOf(code, line)) {
				continue
			}
			p := st.passage(req.Collection, line, line, RelReferences, true)
			p.Content = strings.TrimSpace(p.Content)
			p.Tokens = approxTokens(p.Content)
			if p.Symbol == req.Name {
				continue // the definition naming itself, or a recursive call
			}
			out = append(out, p)
		}
	}
	return out
}

// symbolCallers turns references into the definitions that contain them, one
// passage per caller.
func symbolCallers(req SymbolRequest, users []*sourceText, refs []Passage) []Passage {
	byKey := make(map[string]*sourceText, len(users))
	for _, st := range users {
		byKey[st.doc.Key] = st
	}
	seen := map[string]bool{}
	var out []Passage
	for _, ref := range refs {
		st := byKey[ref.Key]
		sp := st.enclosing(ref.Lines.Start)
		if sp == nil || seen[ref.Key+"\x00"+sp.Symbol] {
			continue
		}
		seen[ref.Key+"\x00"+sp.Symbol] = true
		p := st.passage(req.Collection, sp.StartLine, sp.EndLine, RelCallers, req.IncludeContent)
		p.Symbol, p.Kind = sp.Symbol, sp.Kind
		out = append(out, p)
	}
	return out
}

// symbolCallees lists the definitions of the names used inside the symbol's
// own span: what it calls, wherever that is defined.
func (s *Server) symbolCallees(req SymbolRequest, defs []*sourceText) []Passage {
	var names []string
	seen := map[string]bool{req.Name: true}
	for _, st := range defs {
		candidates := append(append([]string{}, st.doc.Meta[MetaKeyUses]...), st.doc.Meta[MetaKeyDefines]...)
		for _, sp := range st.spans {
			if sp.Symbol != req.Name {
				continue
			}
			body := st.text(sp.StartLine+1, sp.EndLine)
			if sp.StartLine == sp.EndLine {
				body = st.text(sp.StartLine, sp.EndLine)
			}
			for _, name := range candidates {
				if !seen[name] && callsName(body, name) {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
	}
	sort.Strings(names)
	var out []Passage
	for _, name := range names {
		if len(out) > req.Limit {
			break
		}
		// name is a non-empty meta value, so the lookup cannot be refused.
		def, _ := s.LookupSymbol(SymbolRequest{Collection: req.Collection, Name: name, Include: []string{RelDefinitions},
			Limit: req.Limit, IncludeContent: req.IncludeContent})
		for _, p := range def.Definitions {
			p.Source = RelCallees
			out = append(out, p)
		}
	}
	return out
}

// callsName reports whether body calls name: "name(" directly or through a
// value or package, "x.name(".
func callsName(body, name string) bool {
	return callPattern(name).MatchString(body)
}

func callPattern(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^\w$])` + regexp.QuoteMeta(name) + `\s*\(`)
}

// lineOf returns line n (1-based) of text.
func lineOf(text string, n int) string {
	for i := 1; i < n; i++ {
		nl := strings.IndexByte(text, '\n')
		if nl < 0 {
			return ""
		}
		text = text[nl+1:]
	}
	if nl := strings.IndexByte(text, '\n'); nl >= 0 {
		return text[:nl]
	}
	return text
}

// symbolSiblings lists the other definitions in the directories that define
// the symbol: what lives next to it, in the same package or folder.
func (s *Server) symbolSiblings(req SymbolRequest, defs []*sourceText) []Passage {
	var dirs []string
	for _, st := range defs {
		if d := path.Dir(st.doc.Key); !containsString(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	var out []Passage
	for _, dir := range dirs {
		s.eachSourceInDir(req.Collection, dir, func(st *sourceText) bool {
			for _, sp := range st.spans {
				if sp.Symbol == req.Name || sp.Enclosing != "" {
					continue // top-level definitions only: a method is its type's
				}
				p := st.passage(req.Collection, sp.StartLine, sp.EndLine, RelSiblings, false)
				p.Symbol, p.Kind = sp.Symbol, sp.Kind
				out = append(out, p)
			}
			// One past the limit is enough to report truncation; a large
			// directory is not read to the end for a list that is cut anyway.
			return len(out) <= req.Limit
		})
	}
	return out
}

// eachSourceInDir visits the code documents directly in dir (not below it),
// in key order, until visit returns false.
func (s *Server) eachSourceInDir(collection, dir string, visit func(*sourceText) bool) {
	prefix := string(storage.ByKeyCollectionPrefix(collection))
	if dir != "." && dir != "" {
		prefix = string(storage.ByKeyPathPrefix(collection, dir+"/"))
	}
	seen := 0
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		bByK, bDocs := tx.Bucket(s.BucketNames.ByKey), tx.Bucket(s.BucketNames.Docs)
		if bByK == nil || bDocs == nil {
			return nil
		}
		c := bByK.Cursor()
		for k, v := c.Seek([]byte(prefix)); k != nil && bytes.HasPrefix(k, []byte(prefix)) && seen < symbolScanDocs; k, v = c.Next() {
			rest := string(k[len(prefix):]) // "<name>|<lang>"
			if i := strings.LastIndexByte(rest, '|'); i < 0 || strings.Contains(rest[:i], "/") {
				continue
			}
			doc, err := loadDoc(bDocs.Get(storage.DocKey(collection, string(v))))
			if err != nil || doc == nil || len(doc.Meta[MetaKeyDefines]) == 0 {
				continue
			}
			seen++
			if !visit(newSourceText(doc)) {
				return nil
			}
		}
		return nil
	})
}

// isTestPath recognises test files by the naming conventions of the
// languages MDDB extracts symbols from.
func isTestPath(p string) bool {
	base := path.Base(p)
	lower := strings.ToLower(p)
	switch {
	case strings.HasSuffix(base, "_test.go"), strings.HasSuffix(base, "_test.py"),
		strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"),
		strings.Contains(base, ".spec."), strings.Contains(base, ".test."),
		strings.HasSuffix(base, "Test.java"), strings.HasSuffix(base, "Tests.java"),
		strings.HasSuffix(base, "_spec.rb"), strings.HasSuffix(base, "_test.rb"),
		strings.HasSuffix(base, "Test.php"):
		return true
	}
	for _, dir := range []string{"/tests/", "/test/", "/spec/", "/__tests__/"} {
		if strings.Contains("/"+lower, dir) {
			return true
		}
	}
	return false
}
