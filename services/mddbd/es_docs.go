package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
)

// Single-document endpoints of the Elasticsearch-compatible subset
// (INT-019): PUT/POST /{index}/_doc[/{id}], _create/{id}, GET/HEAD/DELETE
// /{index}/_doc/{id}. `_id` is the document key; the language comes from
// _source.lang, else ?lang=, else "en". Writes go through addDocumentIf and
// deleteDocumentInternal, the write path every /v1 transport uses.

// esRefusedWriteParams change what a write means and are not translated.
var esRefusedWriteParams = []string{"if_seq_no", "if_primary_term", "version", "version_type", "pipeline", "routing"}

// esWriteParamsError refuses a write that carries one of them.
func esWriteParamsError(r *http.Request) error {
	for _, p := range esRefusedWriteParams {
		if r.URL.Query().Has(p) {
			return dslErr(p, "URL parameter is not supported on writes")
		}
	}
	return nil
}

// esLang is the language a request addresses: ?lang=, else the default.
func esLang(r *http.Request) string {
	if l := r.URL.Query().Get("lang"); l != "" {
		return l
	}
	return esDefaultLang
}

// esNewID is an identifier for a document posted without one: 15 random
// bytes, URL-safe, the length Elasticsearch's own IDs have.
func esNewID() string {
	b := make([]byte, 15)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// esWriteResult is the body of an index or delete response.
func esWriteResult(index, id, result string, status int) map[string]any {
	return map[string]any{
		"_index": index, "_id": id, "_version": 1, "result": result,
		"_shards": map[string]int{"total": 1, "successful": 1, "failed": 0},
		"_seq_no": 0, "_primary_term": 1, "status": status,
	}
}

// esIndexDoc writes one document. create refuses an existing one with 409;
// the check runs before the write, not inside its transaction.
func (s *Server) esIndexDoc(w http.ResponseWriter, r *http.Request, index, id string, create bool) {
	if !s.permitted(w, r, index, PermWrite) {
		return
	}
	if err := esWriteParamsError(r); err != nil {
		esFail(w, err)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		esBodyError(w, err)
		return
	}
	doc, err := esFromSource(body)
	if err != nil {
		esFail(w, err)
		return
	}
	if id == "" {
		id = esNewID()
	}
	status, result, err := s.esPut(index, id, doc, esLang(r), create)
	if err != nil {
		esFail(w, err)
		return
	}
	s.esRefreshAfterWrite(r, index)
	esJSON(w, status, esWriteResult(index, id, result, status))
}

// esPut writes a mapped document and reports the Elasticsearch status and
// result: 201 created or 200 updated.
func (s *Server) esPut(index, id string, doc *esDocument, lang string, create bool) (int, string, error) {
	if doc.Lang != "" {
		lang = doc.Lang
	}
	if create {
		if _, err := s.loadDocByRef(index, id, lang); err == nil {
			return 0, "", &esUpstreamError{Status: http.StatusConflict,
				Message: "[" + id + "]: version conflict, document already exists"}
		}
	}
	_, isNew, err := s.addDocumentIf(index, id, lang, doc.Meta, doc.Content, 0, true, "")
	if err != nil {
		return 0, "", err
	}
	if isNew {
		return http.StatusCreated, "created", nil
	}
	return http.StatusOK, "updated", nil
}

// esGetDoc answers GET (the document) and HEAD (whether it exists).
func (s *Server) esGetDoc(w http.ResponseWriter, r *http.Request, index, id string) {
	if !s.permitted(w, r, index, PermRead) {
		return
	}
	doc, err := s.loadDocByRef(index, id, esLang(r)) // an expired document is not found
	switch {
	case errors.Is(err, errNotFound):
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		esJSON(w, http.StatusNotFound, map[string]any{"_index": index, "_id": id, "found": false})
	case err != nil:
		esFail(w, err)
	case r.Method == http.MethodHead:
		w.WriteHeader(http.StatusOK)
	default:
		esJSON(w, http.StatusOK, map[string]any{
			"_index": index, "_id": doc.Key, "_version": 1, "_seq_no": 0, "_primary_term": 1,
			"found": true, "_source": esSource(doc, nil),
		})
	}
}

// esDeleteDoc deletes one document: 200 deleted, or 404 not_found.
func (s *Server) esDeleteDoc(w http.ResponseWriter, r *http.Request, index, id string) {
	if !s.permitted(w, r, index, PermWrite) {
		return
	}
	if err := esWriteParamsError(r); err != nil {
		esFail(w, err)
		return
	}
	status, result, err := s.esRemove(index, id, esLang(r))
	if err != nil {
		esFail(w, err)
		return
	}
	s.esRefreshAfterWrite(r, index)
	esJSON(w, status, esWriteResult(index, id, result, status))
}

// esRemove deletes a document and reports the Elasticsearch status and
// result.
func (s *Server) esRemove(index, id, lang string) (int, string, error) {
	err := s.deleteDocumentInternal(index, id, lang)
	switch {
	case err == nil:
		return http.StatusOK, "deleted", nil
	case errors.Is(err, errNotFound):
		return http.StatusNotFound, "not_found", nil
	default:
		return 0, "", err
	}
}
