package main

import (
	"context"
	"errors"
	"io"
	"mddb/internal/httpclient"
	"net/http"
	"net/url"
	"path"
	"time"

	json "mddb/internal/jsonx"
)

// ImportURLRequest represents a request to import a document from a URL
type ImportURLRequest struct {
	Collection string              `json:"collection"`
	URL        string              `json:"url"`
	Key        string              `json:"key"` // optional; derived from URL if empty
	Lang       string              `json:"lang"`
	Meta       map[string][]string `json:"meta"` // optional; merged with frontmatter
	TTL        int64               `json:"ttl,omitempty"`
}

func (s *Server) handleImportURL(w http.ResponseWriter, r *http.Request) {
	var req ImportURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" || req.URL == "" || req.Lang == "" {
		bad(w, errors.New("missing required fields: collection, url, lang"))
		return
	}
	// Fetching and indexing a remote document can outlast the write timeout
	// and is not cancelled by it; only the answer was lost (#315).
	liftWriteDeadline(w, r)

	// Derive key from URL if not provided
	if req.Key == "" {
		req.Key = deriveKeyFromURL(req.URL)
		if req.Key == "" {
			bad(w, errors.New("cannot derive key from URL; provide key explicitly"))
			return
		}
	}

	// Fetch content from URL
	content, err := fetchURL(r.Context(), req.URL)
	if err != nil {
		bad(w, err)
		return
	}

	// Parse frontmatter
	fmMeta, body := parseFrontmatter(content)

	// Merge: frontmatter meta as base, request meta overrides
	mergedMeta := fmMeta
	if mergedMeta == nil {
		mergedMeta = make(map[string][]string)
	}
	for k, v := range req.Meta {
		mergedMeta[k] = v
	}

	// Store via shared addDocument
	saved, _, err := s.addDocument(req.Collection, req.Key, req.Lang, mergedMeta, body, req.TTL, true)
	if err != nil {
		bad(w, err)
		return
	}

	ok(w, saved)
}

// fetchURL downloads content from a URL with safety limits. The pooled client
// uses an SSRF-safe dialer (SEC-004) that rejects private/loopback/link-local
// destinations and re-validates redirects.
func fetchURL(ctx context.Context, rawURL string) (string, error) {
	data, err := fetchURLBytes(ctx, rawURL, 10*1024*1024)
	return string(data), err
}

// fetchURLBytes downloads at most limit bytes from a URL through the SSRF
// guard: import-url's pages and, since RAG-010, remote images and image
// queries, one guard for every fetch a request can cause.
func fetchURLBytes(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	// GO-034: carried through the request's context, so an import from a slow
	// or hostile host is abandoned when the caller gives up rather than
	// holding the connection for the full client timeout.
	// Checked here as well as at dial time. The pooled transport already
	// refuses these destinations, so this is defence in depth — but it fails
	// with a message that names the reason instead of a dial error that reads
	// like a network problem, and it puts the guard where a reader can see it.
	// CodeQL reported this call as critical go/request-forgery precisely
	// because a dialer inside a transport is invisible to it.
	res, err := fetchOutbound(ctx, rawURL, limit, 10*time.Second, nil)
	if err != nil {
		return nil, err
	}
	if res.status != http.StatusOK {
		return nil, errors.New("fetch failed: " + res.statusText)
	}
	return res.body, nil
}

// outboundResponse is what fetchOutbound read.
type outboundResponse struct {
	status     int
	statusText string
	header     http.Header
	// body holds at most limit bytes, and only for a 200 answer; overLimit
	// says the body went on past them.
	body      []byte
	overLimit bool
}

// fetchOutbound GETs a URL through the SSRF guard with the given extra
// header and timeout — the one outbound fetch behind import-url, remote
// images and synced sources (INT-022). A status other than 200 is no error
// here: a synced source tells 304 and 404 apart.
func fetchOutbound(ctx context.Context, rawURL string, limit int64, timeout time.Duration, header http.Header) (outboundResponse, error) {
	resp, err := openOutbound(ctx, httpclient.NewPooledClientWithTimeout(timeout), rawURL, header)
	if err != nil {
		return outboundResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	out := outboundResponse{status: resp.StatusCode, statusText: resp.Status, header: resp.Header}
	if resp.StatusCode != http.StatusOK {
		return out, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return out, err
	}
	out.overLimit = int64(len(body)) > limit
	out.body = body[:min(int64(len(body)), limit)]
	return out, nil
}

// openOutbound validates a URL against the SSRF policy and GETs it with
// client, leaving the body to the caller: fetchOutbound reads it into memory,
// a corpus pack (AGT-005) is streamed to disk. client is the pooled client or
// one built on it, so its dialer refuses the same destinations.
func openOutbound(ctx context.Context, client *http.Client, rawURL string, header http.Header) (*http.Response, error) {
	safeURL, err := httpclient.ValidateOutboundURL(rawURL)
	if err != nil {
		return nil, err
	}

	// GET of a URL ValidateOutboundURL has just parsed: building the request
	// cannot fail.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, safeURL, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	return client.Do(req) // #nosec G107 G704 -- SSRF-guarded by ValidateOutboundURL and httpclient.SafeDialContext
}

// deriveKeyFromURL extracts a document key from the URL path.
func deriveKeyFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	base := path.Base(parsed.Path)
	// Remove extension
	ext := path.Ext(base)
	if ext != "" {
		base = base[:len(base)-len(ext)]
	}
	if base == "" || base == "." || base == "/" {
		return ""
	}
	return base
}
