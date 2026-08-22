package main

import (
	"context"
	"errors"
	"io"
	"mddb/internal/httpclient"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	json "github.com/goccy/go-json"
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
	// GO-034: carried through the request's context, so an import from a slow
	// or hostile host is abandoned when the caller gives up rather than
	// holding the connection for the full client timeout.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpclient.NewPooledClientWithTimeout(10 * time.Second).Do(req) // #nosec G107 -- SSRF-guarded by httpclient.SafeDialContext
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return "", errors.New("fetch failed: " + resp.Status)
	}

	// Limit to 10MB
	limited := io.LimitReader(resp.Body, 10*1024*1024)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// parseFrontmatter extracts YAML frontmatter (between --- markers) from markdown.
// Returns extracted metadata and the remaining body content.
func parseFrontmatter(content string) (map[string][]string, string) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return nil, content
	}

	// Find closing ---
	rest := content[3:]
	if idx := strings.Index(rest, "\n"); idx >= 0 {
		rest = rest[idx+1:]
	} else {
		return nil, content
	}

	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return nil, content
	}

	fmBlock := rest[:endIdx]
	body := strings.TrimSpace(rest[endIdx+4:])

	// Simple YAML key: value parser
	meta := make(map[string][]string)
	for _, line := range strings.Split(fmBlock, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colonIdx])
		val := strings.TrimSpace(line[colonIdx+1:])
		if key == "" {
			continue
		}
		// Strip quotes
		val = strings.Trim(val, `"'`)
		if val != "" {
			// Support comma-separated values: tags: go, database, markdown
			if strings.Contains(val, ",") {
				parts := strings.Split(val, ",")
				for i := range parts {
					parts[i] = strings.TrimSpace(parts[i])
				}
				meta[key] = parts
			} else {
				meta[key] = []string{val}
			}
		}
	}

	return meta, body
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
