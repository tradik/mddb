package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
)

// The one implementation of the module switches (GO-049) that REST, gRPC
// and MCP call. Listing needs any authenticated caller, switching a global
// administrator — checked by each surface before it gets here.

// featureDisabledError is what a call into a disabled module gets.
type featureDisabledError struct{ feature string }

func (e featureDisabledError) Error() string { return "feature disabled: " + e.feature }

// featureEnabled reports whether a module works now; a name outside the
// registry is core and always works.
func (s *Server) featureEnabled(name string) bool { return s.Features.Enabled(name) }

// FeaturesList describes every module, with the store's version.
func (s *Server) FeaturesList() featureList {
	return featureList{Features: s.Features.List(), Version: s.Features.Version()}
}

// SetFeature switches a module on or off, recording who did it, and returns
// its new state.
func (s *Server) SetFeature(ctx context.Context, name string, enabled bool) (FeatureState, error) {
	if s.Features == nil {
		return FeatureState{}, errFeatureUnavailable
	}
	by := ""
	if claims, ok := GetClaimsFromContext(ctx); ok {
		by = claims.Username
	}
	if err := s.Features.Set(name, enabled, by); err != nil {
		return FeatureState{}, err
	}
	st, _ := s.Features.State(name)
	return st, nil
}

// The REST API:
//
//	GET /v1/features          every module, with the version
//	PUT /v1/features/{name}   {"enabled": bool}, a global administrator
const featuresPath = "/v1/features"

// featureSetBody is the body of PUT /v1/features/{name}.
type featureSetBody struct {
	Enabled *bool `json:"enabled"`
}

func (s *Server) handleFeatures(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, featuresPath), "/")
	switch {
	case r.Method == http.MethodGet && name == "":
		ok(w, s.FeaturesList())
	case r.Method == http.MethodPut && name != "":
		s.guardWrite(func(w http.ResponseWriter, r *http.Request) { s.putFeature(w, r, name) })(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) putFeature(w http.ResponseWriter, r *http.Request, name string) {
	if !s.requireAdmin(w, r) {
		return
	}
	var body featureSetBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Enabled == nil {
		bad(w, errors.New(`body must be {"enabled": true|false}`))
		return
	}
	st, err := s.SetFeature(r.Context(), name, *body.Enabled)
	if err != nil {
		featureError(w, name, err)
		return
	}
	ok(w, map[string]any{"feature": st, "version": s.Features.Version()})
}

// featureError answers a failed switch: 404 unknown, 409 locked, 503 when the
// store is not loaded, 500 otherwise.
func featureError(w http.ResponseWriter, name string, err error) {
	body := map[string]string{"error": err.Error(), "feature": name}
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, errFeatureUnknown):
		code = http.StatusNotFound
	case errors.Is(err, errFeatureLocked):
		code, body["error"], body["lockedBy"] = http.StatusConflict, "feature locked", featuresForceOffEnv
	case errors.Is(err, errFeatureUnavailable):
		code = http.StatusServiceUnavailable
	}
	writeJSONStatus(w, code, body)
}

// writeFeatureDisabled is the 404 a route of a disabled module answers.
func writeFeatureDisabled(w http.ResponseWriter, feature string) {
	writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "feature disabled", "feature": feature})
}

// writeJSONStatus writes a JSON body with a status code.
func writeJSONStatus(w http.ResponseWriter, code int, body any) {
	b, _ := json.Marshal(body)
	w.Header().Set(headerContentType, "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(b) // #nosec G705 -- response write to http.ResponseWriter
}
