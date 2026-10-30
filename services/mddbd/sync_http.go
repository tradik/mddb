package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The HTTP(S) sources of a sync (INT-022): a sitemap (or sitemap index) and
// a list of URLs. Every request — sitemap, robots.txt, page — goes through
// fetchOutbound, so through the SSRF guard (SEC-004/SEC-011) and the pooled
// client; a page is fetched conditionally on the validators of the last run.

// Bounds of the HTTP sources.
const (
	syncFetchTimeout = 30 * time.Second
	maxSitemapBytes  = 50 << 20 // the sitemap protocol's own limit, uncompressed
	maxSitemapFiles  = 50       // the sitemaps of an index one run reads
	maxRobotsBytes   = 512 << 10
)

// syncUserAgent names the sync to the sites it reads, and is the robots.txt
// group it follows before the "*" one.
const syncUserAgent = "mddb"

// httpSyncSource reads a sitemap or a list of URLs.
type httpSyncSource struct {
	action *SyncAction
}

// list returns the pages to sync: the action's URLs, or the sitemap's pages
// on the sitemap's own host that robots.txt lets the sync read.
func (h *httpSyncSource) list(ctx context.Context) ([]syncItem, bool, error) {
	a := h.action
	refs, truncated := a.URLs, false
	if a.Source == SyncSourceSitemap {
		var err error
		if refs, truncated, err = h.sitemapPages(ctx); err != nil {
			return nil, false, err
		}
	}
	seen := map[string]bool{}
	var items []syncItem
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if len(items) == a.MaxDocuments {
			return items, true, nil
		}
		u, _ := url.Parse(ref) // checked by normalize or sitemapPages
		items = append(items, syncItem{ref: ref, key: syncKey(u.Path)})
	}
	return items, truncated, nil
}

// sitemapPages reads the sitemap and, for an index, its sitemaps. truncated
// is set when the index has more sitemaps than one run reads.
func (h *httpSyncSource) sitemapPages(ctx context.Context) ([]string, bool, error) {
	a := h.action
	root, _ := url.Parse(a.URL) // checked by normalize
	doc, err := readSitemap(ctx, a.URL)
	if err != nil {
		return nil, false, err
	}
	robots := readRobots(ctx, root)
	var pages []string
	add := func(locs []sitemapLoc) {
		for _, l := range locs {
			u, err := url.Parse(strings.TrimSpace(l.Loc))
			// The protocol keeps a sitemap's pages on its own host; one that
			// names another host is not followed there.
			if err == nil && sameOrigin(u, root) && robots.allowed(u.RequestURI()) {
				pages = append(pages, u.String())
			}
		}
	}
	add(doc.URLs)
	for i, sm := range doc.Sitemaps {
		if i == maxSitemapFiles {
			return pages, true, nil
		}
		if len(pages) > a.MaxDocuments {
			break // list cuts it and reports the cut
		}
		u, err := url.Parse(strings.TrimSpace(sm.Loc))
		if err != nil || !sameOrigin(u, root) {
			continue
		}
		child, err := readSitemap(ctx, u.String())
		if err != nil {
			return nil, false, err
		}
		add(child.URLs)
	}
	return pages, false, nil
}

// sameOrigin reports whether u has root's scheme and host.
func sameOrigin(u, root *url.URL) bool {
	return u.Scheme == root.Scheme && strings.EqualFold(u.Host, root.Host)
}

// fetch GETs a page, conditionally when the last run left validators.
func (h *httpSyncSource) fetch(ctx context.Context, it syncItem, prev syncState) (syncFetched, error) {
	header := syncHeader()
	if prev.etag != "" {
		header.Set("If-None-Match", prev.etag)
	}
	if prev.lastModified != "" {
		header.Set("If-Modified-Since", prev.lastModified)
	}
	res, err := fetchOutbound(ctx, it.ref, h.action.MaxBytes, syncFetchTimeout, header)
	if err != nil {
		return syncFetched{}, err
	}
	switch res.status {
	case http.StatusOK:
	case http.StatusNotModified:
		return syncFetched{notModified: true}, nil
	case http.StatusNotFound, http.StatusGone:
		return syncFetched{}, errSourceGone
	default:
		return syncFetched{}, fmt.Errorf("fetch failed: %s", res.statusText)
	}
	if res.overLimit {
		return syncFetched{}, fmt.Errorf("larger than maxBytes (%d)", h.action.MaxBytes)
	}
	return syncFetched{
		body:        res.body,
		etag:        res.header.Get("ETag"),
		modified:    res.header.Get("Last-Modified"),
		contentType: res.header.Get("Content-Type"),
	}, nil
}

// syncHeader is the header of every request a sync makes.
func syncHeader() http.Header {
	return http.Header{"User-Agent": {syncUserAgent}}
}

// sitemapDoc is a sitemap (<urlset>) or a sitemap index (<sitemapindex>).
type sitemapDoc struct {
	URLs     []sitemapLoc `xml:"url"`
	Sitemaps []sitemapLoc `xml:"sitemap"`
}

// sitemapLoc is the <loc> of a page or a sitemap.
type sitemapLoc struct {
	Loc string `xml:"loc"`
}

// readSitemap fetches and parses one sitemap, gzipped or not.
func readSitemap(ctx context.Context, rawURL string) (sitemapDoc, error) {
	var doc sitemapDoc
	res, err := fetchOutbound(ctx, rawURL, maxSitemapBytes, syncFetchTimeout, syncHeader())
	if err != nil {
		return doc, err
	}
	if res.status != http.StatusOK {
		return doc, fmt.Errorf("sitemap %s: %s", rawURL, res.statusText)
	}
	body := res.body
	if res.overLimit {
		return doc, fmt.Errorf("sitemap %s: larger than %d bytes", rawURL, maxSitemapBytes)
	}
	if bytes.HasPrefix(body, []byte{0x1f, 0x8b}) {
		if body, err = gunzipLimited(body, maxSitemapBytes); err != nil {
			return doc, fmt.Errorf("sitemap %s: %w", rawURL, err)
		}
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return doc, fmt.Errorf("sitemap %s: %w", rawURL, err)
	}
	return doc, nil
}

// gunzipLimited decompresses data, refusing more than limit bytes out.
func gunzipLimited(data []byte, limit int64) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(io.LimitReader(zr, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > limit {
		return nil, fmt.Errorf("larger than %d bytes uncompressed", limit)
	}
	return out, nil
}
