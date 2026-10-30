package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
)

// The HTTP side of the access gate (SEC-019): every route is registered
// through a gatedMux, which puts httpGate in front of the handler with the
// route's rule from httpAccessRules.

// headerContentType is the Content-Type header.
const headerContentType = "Content-Type"

// routeMux is what routes are registered on: an *http.ServeMux, or a wrapper
// that gates or records them.
type routeMux interface {
	Handle(pattern string, handler http.Handler)
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// gatedMux registers each route behind the access gate.
type gatedMux struct {
	mux routeMux
	s   *Server
}

// gated wraps a mux so every route registered on it is gated.
func (s *Server) gated(mux routeMux) gatedMux { return gatedMux{mux: mux, s: s} }

// Handle registers h behind the gate for pattern.
func (g gatedMux) Handle(pattern string, h http.Handler) {
	g.mux.Handle(pattern, g.s.httpGate(pattern, h))
}

// HandleFunc registers h behind the gate for pattern.
func (g gatedMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	g.Handle(pattern, http.HandlerFunc(h))
}

// httpGate applies the route's rule before the handler. With auth off, and on
// the endpoints the auth middleware treats as public, it does nothing.
func (s *Server) httpGate(pattern string, next http.Handler) http.Handler {
	base := httpAccessRules[pattern]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authOn() || s.AuthManager.isPublicEndpoint(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		rule := base.forMethod(r.Method)
		var names []string
		if rule.class == accessCollection && !rule.all {
			var err error
			if names, err = requestCollections(r); err != nil {
				writeBodyError(w, err)
				return
			}
		}
		if err := s.authorize(r.Context(), rule, names); err != nil {
			writeAccessDenied(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestCollections reads the collections a request names, from the query
// string and from a JSON body, which it leaves for the handler to read again.
// The body is already capped by withMaxBody (MDDB_MAX_BODY_BYTES).
func requestCollections(r *http.Request) ([]string, error) {
	q := r.URL.Query()
	names := append(append([]string(nil), q["collection"]...), q["collections"]...)
	ct := r.Header.Get(headerContentType)
	if r.Body == nil || r.Body == http.NoBody || (ct != "" && !strings.Contains(ct, "json")) {
		return names, nil
	}
	raw, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var body struct {
		Collection        string   `json:"collection"`
		SourceCollection  string   `json:"sourceCollection"`
		Collections       []string `json:"collections"`
		TargetCollections []string `json:"targetCollections"`
	}
	// A body that is not a JSON object names no collection; the handler
	// judges it. Fields of another type are left out the same way.
	_ = json.Unmarshal(raw, &body)
	names = append(names, body.Collection, body.SourceCollection)
	names = append(names, body.Collections...)
	return append(names, body.TargetCollections...), nil
}

// writeBodyError answers a body that could not be read: 413 over the cap,
// 400 otherwise.
func writeBodyError(w http.ResponseWriter, err error) {
	w.Header().Set(headerContentType, "application/json")
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(`{"error":"request body too large"}`))
		return
	}
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`{"error":"unreadable request body"}`))
}

// writeAccessDenied answers a refusal: 401 without credentials, 403 without
// the permission.
func writeAccessDenied(w http.ResponseWriter, err error) {
	w.Header().Set(headerContentType, "application/json")
	if errors.Is(err, ErrUnauthorized) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		return
	}
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":"forbidden"}`))
}

// allowed applies a rule inside a handler that authorizes itself
// (accessHandler), once it knows the collections; it answers the refusal.
func (s *Server) allowed(w http.ResponseWriter, r *http.Request, rule accessRule, names ...string) bool {
	if err := s.authorize(r.Context(), rule, names); err != nil {
		writeAccessDenied(w, err)
		return false
	}
	return true
}
