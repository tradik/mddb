package main

import (
	"net/http"
	"sort"
)

// CodeLanguagesResponse is the extension table the server classifies source
// by (AGT-001). A client that walks a directory — mddb-cli autoindex — asks
// for it instead of carrying a copy, so the two cannot disagree about what is
// code.
type CodeLanguagesResponse struct {
	// Extensions maps a lower-case file extension (".go") to its language.
	Extensions map[string]string `json:"extensions"`
	// Languages lists the language names, sorted.
	Languages []string `json:"languages"`
}

// handleCodeLanguages serves GET /v1/code-languages.
func (s *Server) handleCodeLanguages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	ok(w, codeLanguages())
}

func codeLanguages() CodeLanguagesResponse {
	resp := CodeLanguagesResponse{Extensions: make(map[string]string, len(codeExtensions))}
	seen := map[string]bool{}
	for ext, lang := range codeExtensions {
		resp.Extensions[ext] = lang
		if !seen[lang] {
			seen[lang] = true
			resp.Languages = append(resp.Languages, lang)
		}
	}
	sort.Strings(resp.Languages)
	return resp
}
