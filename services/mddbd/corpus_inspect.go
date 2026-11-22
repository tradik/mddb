package main

import (
	"io"
	"net/http"
)

// Inspecting a pack before it is installed (FE-022): POST /v1/corpus/inspect
// takes the pack, or only its beginning, as POST /v1/corpus/install does,
// reads nothing past manifest.json and answers what the pack holds and why
// it could not be installed here, if it could not. Nothing is written. The
// panel shows this before it uploads a whole pack.

// corpusInspectMax bounds what an inspection reads of an upload: the
// manifest is the first member and at most corpusManifestMax bytes, so a
// few compressed megabytes always hold it.
const corpusInspectMax = 16 << 20

// corpusInspectResult is the answer of POST /v1/corpus/inspect.
type corpusInspectResult struct {
	Manifest *corpusManifest `json:"manifest"`
	// Conflicts are why an install would answer 409; empty when it would not.
	Conflicts []string `json:"conflicts"`
}

// inspectCorpus reads the manifest of the pack r begins with.
func (s *Server) inspectCorpus(r io.Reader) (*corpusInspectResult, error) {
	zr, tr := openCorpusArchive(&corpusCapReader{r: r, left: corpusInspectMax})
	defer zr.Close()
	m, err := readCorpusManifest(tr)
	if err != nil {
		return nil, err
	}
	return &corpusInspectResult{Manifest: m, Conflicts: s.corpusConflicts(m)}, nil
}

// handleCorpusInspect is POST /v1/corpus/inspect: a multipart upload with
// the pack, or its first megabytes, in the field "pack". Admin, as an
// install is.
func (s *Server) handleCorpusInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	part, err := corpusUpload(r)
	if err != nil {
		bad(w, err)
		return
	}
	res, err := s.inspectCorpus(part)
	if err != nil {
		corpusError(w, err)
		return
	}
	ok(w, res)
}
