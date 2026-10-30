package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	json "mddb/internal/jsonx"
)

// Responses of the Elasticsearch-compatible subset (INT-019): JSON bodies,
// Elasticsearch-shaped errors, and the in-process call that lets the surface
// reuse a /v1 handler instead of copying it.

// esJSON writes v with status.
func esJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		esError(w, http.StatusInternalServerError, "exception", err.Error())
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(b) // #nosec G705 -- JSON response body
}

// esErrorBody is an Elasticsearch error object: the shape the clients parse
// to name the exception they raise.
func esErrorBody(typ, reason string) map[string]any {
	cause := map[string]string{"type": typ, "reason": reason}
	return map[string]any{"root_cause": []map[string]string{cause}, "type": typ, "reason": reason}
}

// esError writes an Elasticsearch error response.
func esError(w http.ResponseWriter, status int, typ, reason string) {
	b, _ := json.Marshal(map[string]any{"error": esErrorBody(typ, reason), "status": status})
	w.WriteHeader(status)
	_, _ = w.Write(b) // #nosec G705 -- JSON response body
}

// esUnsupported refuses an endpoint the subset does not have, naming it.
func esUnsupported(w http.ResponseWriter, r *http.Request) {
	esError(w, http.StatusBadRequest, "illegal_argument_exception",
		"the MDDB Elasticsearch subset does not support "+r.Method+" "+r.URL.Path+"; see docs/ELASTICSEARCH-COMPAT.md")
}

// esErrorType names the exception for an HTTP status, for errors that come
// from MDDB's own paths.
func esErrorType(status int) string {
	switch status {
	case http.StatusForbidden:
		return "security_exception"
	case http.StatusNotFound:
		return "resource_not_found_exception"
	case http.StatusConflict:
		return "version_conflict_engine_exception"
	case http.StatusTooManyRequests, http.StatusInsufficientStorage:
		return "quota_exceeded_exception"
	case http.StatusServiceUnavailable:
		return "unavailable_exception"
	default:
		return "illegal_argument_exception"
	}
}

// esStatusOf maps an error from MDDB's write path onto an HTTP status and
// exception type: quotas keep 507/429, a read-only collection is the 403
// cluster_block_exception Elasticsearch uses for a blocked index.
func esStatusOf(err error) (int, string) {
	var dsl *esDSLError
	var conflict *RevisionConflictError
	var upstream *esUpstreamError
	switch {
	case errors.As(err, &upstream):
		return upstream.Status, esErrorType(upstream.Status)
	case errors.As(err, &dsl):
		return http.StatusBadRequest, "parsing_exception"
	case asQuotaError(err) != nil:
		st := asQuotaError(err).Status()
		return st, esErrorType(st)
	case asReadOnlyError(err) != nil:
		return http.StatusForbidden, "cluster_block_exception"
	case errors.Is(err, errNotFound):
		return http.StatusNotFound, esErrorType(http.StatusNotFound)
	case errors.As(err, &conflict):
		return http.StatusConflict, esErrorType(http.StatusConflict)
	default:
		return http.StatusBadRequest, esErrorType(http.StatusBadRequest)
	}
}

// esFail writes err as an Elasticsearch error.
func esFail(w http.ResponseWriter, err error) {
	if qe := asQuotaError(err); qe != nil && qe.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(qe.retryAfterSeconds(), 10))
	}
	status, typ := esStatusOf(err)
	esError(w, status, typ, err.Error())
}

// esCapture records a /v1 handler's answer, so the surface can call the
// handler in-process and translate what it returned instead of copying its
// logic.
type esCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newESCapture() *esCapture { return &esCapture{header: http.Header{}, status: http.StatusOK} }

func (c *esCapture) Header() http.Header         { return c.header }
func (c *esCapture) Write(b []byte) (int, error) { return c.body.Write(b) }
func (c *esCapture) WriteHeader(status int)      { c.status = status }

// esCall runs a /v1 handler with body as its JSON request, on the caller's
// context (authentication travels with it), and decodes a 200 answer into
// out. Any other answer is returned as an error carrying its status.
func esCall(r *http.Request, h http.HandlerFunc, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	// Built by hand, not with NewRequest: it never leaves the process, and
	// nothing of the caller's request but its context (credentials) goes in.
	req := (&http.Request{
		Method: http.MethodPost, URL: &url.URL{Path: "/"}, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b)),
	}).WithContext(r.Context())
	rec := newESCapture()
	h(rec, req)
	if rec.status != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(rec.body.Bytes(), &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(rec.body.String())
		}
		return &esUpstreamError{Status: rec.status, Message: e.Error}
	}
	return json.Unmarshal(rec.body.Bytes(), out)
}

// esUpstreamError is a non-200 answer from a /v1 handler.
type esUpstreamError struct {
	Status  int
	Message string
}

func (e *esUpstreamError) Error() string { return e.Message }
