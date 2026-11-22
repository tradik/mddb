package main

import (
	"net/http"
)

// Endpoints in the request shapes Open WebUI sends to an external engine:
// web search (#449, web_search.go), reranking (#450, rerank_api.go) and
// document extraction (#451, extract_api.go). Each is a feature module that
// starts off (features.go) and is gated like every other module route
// (feature_gates.go); what they share is how a request is refused, here.

// allowMethod reports whether r uses method; otherwise it answers 405 with
// the Allow header, in JSON like every other error of these endpoints, so a
// client that logs the body logs something readable.
func allowMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed: use "+method)
	return false
}

// writeAPIError answers {"error": msg} with status.
func writeAPIError(w http.ResponseWriter, status int, msg string) {
	writeJSONStatus(w, status, map[string]string{"error": msg})
}
