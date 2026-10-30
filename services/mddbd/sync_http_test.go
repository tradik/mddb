package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// gzipped compresses s.
func gzipped(t *testing.T, s string) string {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	_, _ = zw.Write([]byte(s))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// listRefs lists a sitemap source's pages.
func listRefs(t *testing.T, a *SyncAction) ([]string, bool, error) {
	t.Helper()
	if err := a.normalize(); err != nil {
		t.Fatal(err)
	}
	items, truncated, err := (&httpSyncSource{action: a}).list(context.Background())
	refs := make([]string, 0, len(items))
	for _, it := range items {
		refs = append(refs, it.ref)
	}
	return refs, truncated, err
}

// A sitemap index: its sitemaps are read (a gzipped one included), pages on
// other hosts and pages robots.txt disallows are left out, and a sitemap on
// another host is not followed.
func TestSyncSitemapIndexRobotsAndHosts(t *testing.T) {
	site := newFakeSite(t, map[string]string{})
	site.pages["/s1.xml"] = strings.Replace(urlset(site.URL, "/one", "/private/x", "/private/open", "/doc.pdf"),
		"</urlset>", "<url><loc>http://elsewhere.example/three</loc></url><url><loc>%zz</loc></url></urlset>", 1)
	site.pages["/s2.xml.gz"] = gzipped(t, urlset(site.URL, "/two"))
	site.sitemap = `<sitemapindex><sitemap><loc> ` + site.URL + `/s1.xml </loc></sitemap>` +
		`<sitemap><loc>` + site.URL + `/s2.xml.gz</loc></sitemap>` +
		`<sitemap><loc>http://elsewhere.example/s.xml</loc></sitemap><sitemap><loc>%zz</loc></sitemap></sitemapindex>`
	site.robots = "# comment\nUser-agent: *\nDisallow: /\n\nUser-agent: other\nUser-agent: MDDB\n" +
		"Disallow: /private\nAllow: /private/open\nDisallow: /*.pdf$\nDisallow:\nno colon here\n"
	refs, truncated, err := listRefs(t, &SyncAction{Source: SyncSourceSitemap, URL: site.URL + "/sitemap.xml", Collection: "c", Lang: "en"})
	want := []string{site.URL + "/one", site.URL + "/private/open", site.URL + "/two"}
	if err != nil || truncated || !slices.Equal(refs, want) {
		t.Errorf("pages %v (truncated %v, %v), want %v", refs, truncated, err, want)
	}
}

// The bounds of a sitemap: a page count over maxDocuments, more sitemaps in
// an index than one run reads.
func TestSyncSitemapBounds(t *testing.T) {
	site := newFakeSite(t, map[string]string{})
	site.pages["/s.xml"] = urlset(site.URL, "/1", "/2", "/3")
	var idx strings.Builder
	idx.WriteString("<sitemapindex>")
	for range maxSitemapFiles + 1 {
		idx.WriteString("<sitemap><loc>" + site.URL + "/s.xml</loc></sitemap>")
	}
	idx.WriteString("</sitemapindex>")
	site.sitemap = idx.String()
	a := &SyncAction{Source: SyncSourceSitemap, URL: site.URL + "/sitemap.xml", Collection: "c", Lang: "en"}
	if refs, truncated, err := listRefs(t, a); err != nil || !truncated || len(refs) != 3 {
		t.Errorf("an index past %d sitemaps: %d pages, truncated %v, %v", maxSitemapFiles, len(refs), truncated, err)
	}
	a.MaxDocuments = 2
	if refs, truncated, err := listRefs(t, a); err != nil || !truncated || len(refs) != 2 {
		t.Errorf("past maxDocuments: %v, truncated %v, %v", refs, truncated, err)
	}
}

// A sitemap that cannot be read fails the listing, so nothing is pruned.
func TestSyncSitemapErrors(t *testing.T) {
	site := newFakeSite(t, map[string]string{"/bad.xml": "<urlset><url>", "/bad.gz": "\x1f\x8bnot gzip",
		"/cut.gz": gzipped(t, urlset("http://x", "/1", "/2"))[:20],
		"/big.gz": gzipped(t, strings.Repeat(" ", maxSitemapBytes+1))})
	site.status["/down.xml"] = http.StatusServiceUnavailable
	for name, path := range map[string]string{"status": "/down.xml", "xml": "/bad.xml", "gzip": "/bad.gz",
		"cut gzip": "/cut.gz", "gzip bomb": "/big.gz"} {
		_, _, err := listRefs(t, &SyncAction{Source: SyncSourceSitemap, URL: site.URL + path, Collection: "c", Lang: "en"})
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// A broken sitemap inside an index fails the listing as well.
	site.sitemap = "<sitemapindex><sitemap><loc>" + site.URL + "/bad.xml</loc></sitemap></sitemapindex>"
	if _, _, err := listRefs(t, &SyncAction{Source: SyncSourceSitemap, URL: site.URL + "/sitemap.xml", Collection: "c", Lang: "en"}); err == nil {
		t.Error("a broken child sitemap: no error")
	}
}

// A sitemap over the protocol's 50 MB is refused, not cut.
func TestSyncSitemapTooLarge(t *testing.T) {
	site := newFakeSite(t, map[string]string{"/huge.xml": strings.Repeat(" ", maxSitemapBytes+1)})
	if _, _, err := listRefs(t, &SyncAction{Source: SyncSourceSitemap, URL: site.URL + "/huge.xml", Collection: "c", Lang: "en"}); err == nil ||
		!strings.Contains(err.Error(), "larger than") {
		t.Errorf("a huge sitemap: %v", err)
	}
}

// robots.txt rules: the longest match decides, Allow wins a tie, '$' ends
// the path, an unknown agent falls back to "*", none means no rules.
func TestParseRobots(t *testing.T) {
	rules := parseRobots("User-agent: *\nDisallow: /a\nAllow: /a\nDisallow: /b$\nDisallow: /c*d\n")
	for path, want := range map[string]bool{"/a/x": true, "/b": false, "/b/x": true, "/cxxd": false, "/z": true} {
		if got := rules.allowed(path); got != want {
			t.Errorf("%s: allowed %v, want %v", path, got, want)
		}
	}
	if parseRobots("User-agent: googlebot\nDisallow: /\n").allowed("/x") != true {
		t.Error("another bot's group applied")
	}
}

// fetch answers: an internal address refused, a status that is neither 200,
// 304 nor 404/410 an error.
func TestHTTPSyncSourceFetch(t *testing.T) {
	site := newFakeSite(t, map[string]string{})
	site.status["/teapot"] = http.StatusTeapot
	h := &httpSyncSource{action: &SyncAction{MaxBytes: 10}}
	if _, err := h.fetch(context.Background(), syncItem{ref: site.URL + "/teapot"}, syncState{}); err == nil ||
		!strings.Contains(err.Error(), "418") {
		t.Errorf("418: %v", err)
	}
	if _, err := h.fetch(context.Background(), syncItem{ref: "ftp://x/y"}, syncState{}); err == nil {
		t.Error("an ftp URL was fetched")
	}
}

// A body cut short of its Content-Length is a read error, not a page.
func TestFetchOutboundCutBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("only ten.."))
	}))
	t.Cleanup(ts.Close)
	if _, err := fetchOutbound(context.Background(), ts.URL, 1000, syncFetchTimeout, nil); err == nil {
		t.Error("a cut body read as whole")
	}
}

// syncFormat: the Content-Type when the converter knows it, else the path's
// extension; a ref that is no URL has no format.
func TestSyncFormat(t *testing.T) {
	for _, tc := range []struct{ ct, ref, want string }{
		{"text/html; charset=utf-8", "https://x/a.pdf", "html"},
		{"application/octet-stream", "https://x/a.pdf?v=1", "pdf"},
		{"", "docs/readme.md", "md"},
		{"", "https://x/%zz", ""},
	} {
		if got := syncFormat(tc.ct, tc.ref); got != tc.want {
			t.Errorf("syncFormat(%q, %q) = %q, want %q", tc.ct, tc.ref, got, tc.want)
		}
	}
}
