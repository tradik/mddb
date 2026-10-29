package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
)

// Where an alias becomes a collection (GO-044): once per request, at each
// surface's entry, before permissions are checked or a key is built.

// Field names that hold a collection, in the JSON (camelCase) and the MCP/
// gRPC (snake_case) spellings.
var (
	aliasStringFields = []string{"collection", "sourceCollection", "source_collection"}
	aliasListFields   = []string{"collections", "targetCollections", "target_collections"}
)

// resolveAliasArgs rewrites the collection fields of a decoded request. It
// reports whether anything changed.
func (a *AliasManager) resolveAliasArgs(args map[string]interface{}) bool {
	changed := false
	for _, f := range aliasStringFields {
		if v, ok := args[f].(string); ok {
			if r := a.Resolve(v); r != v {
				args[f], changed = r, true
			}
		}
	}
	for _, f := range aliasListFields {
		list, ok := args[f].([]interface{})
		if !ok {
			continue
		}
		for i, item := range list {
			if v, ok := item.(string); ok {
				if r := a.Resolve(v); r != v {
					list[i], changed = r, true
				}
			}
		}
	}
	return changed
}

// withAliases resolves aliases in an HTTP request's query string and JSON
// body. The alias API itself is left alone, and so are multipart uploads,
// whose handlers resolve their form field themselves.
func (s *Server) withAliases(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Aliases.active() || strings.HasPrefix(r.URL.Path, "/v1/aliases") {
			next.ServeHTTP(w, r)
			return
		}
		q := r.URL.Query()
		if c := q.Get("collection"); c != "" {
			if t := s.Aliases.Resolve(c); t != c {
				q.Set("collection", t)
				r.URL.RawQuery = q.Encode()
			}
		}
		if ct := r.Header.Get("Content-Type"); r.Body != nil && (ct == "" || strings.Contains(ct, "json")) {
			s.resolveAliasBody(r)
		}
		next.ServeHTTP(w, r)
	})
}

// resolveAliasBody rewrites a JSON object body. A body that is not a JSON
// object is passed on as it was, for the handler to judge.
func (s *Server) resolveAliasBody(r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	restore := func(b []byte) {
		r.Body = io.NopCloser(bytes.NewReader(b))
		r.ContentLength = int64(len(b))
	}
	if err != nil || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		restore(raw)
		return
	}
	var body map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&body) != nil || !s.Aliases.resolveAliasArgs(body) {
		restore(raw)
		return
	}
	out, err := json.Marshal(body)
	if err != nil {
		restore(raw)
		return
	}
	restore(out)
}
