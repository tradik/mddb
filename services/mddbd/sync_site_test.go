package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSite is a web site for the sync tests (INT-022): pages by path, a
// sitemap of them, an optional robots.txt, and ETag / Last-Modified answers
// that a conditional GET gets a 304 from.
type fakeSite struct {
	*httptest.Server
	mu    sync.Mutex
	pages map[string]string // path → HTML
	// noValidators serves pages without ETag and Last-Modified, so only the
	// content hash can tell an unchanged page.
	noValidators bool
	robots       string
	sitemap      string            // served instead of the generated sitemap when set
	status       map[string]int    // a status a path answers instead
	types        map[string]string // a Content-Type other than text/html
	served       map[string]int    // 200 answers per path
}

func newFakeSite(t *testing.T, pages map[string]string) *fakeSite {
	t.Helper()
	f := &fakeSite{pages: pages, status: map[string]int{}, types: map[string]string{}, served: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// set replaces (or adds) a page; del removes it.
func (f *fakeSite) set(path, html string) { f.mu.Lock(); f.pages[path] = html; f.mu.Unlock() }
func (f *fakeSite) del(path string)       { f.mu.Lock(); delete(f.pages, path); f.mu.Unlock() }

// hits returns how many 200 answers path got.
func (f *fakeSite) hits(path string) int { f.mu.Lock(); defer f.mu.Unlock(); return f.served[path] }

// siteModified is every page's Last-Modified.
var siteModified = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Format(http.TimeFormat)

func (f *fakeSite) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if code := f.status[r.URL.Path]; code != 0 {
		w.WriteHeader(code)
		return
	}
	switch r.URL.Path {
	case "/robots.txt":
		if f.robots == "" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, f.robots)
	case "/sitemap.xml":
		_, _ = fmt.Fprint(w, f.sitemapXML())
	default:
		html, found := f.pages[r.URL.Path]
		if !found {
			http.NotFound(w, r)
			return
		}
		if !f.noValidators {
			tag := etag([]byte(html))
			w.Header().Set("ETag", tag)
			w.Header().Set("Last-Modified", siteModified)
			if r.Header.Get("If-None-Match") == tag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		ct := f.types[r.URL.Path]
		if ct == "" {
			ct = "text/html; charset=utf-8"
		}
		w.Header().Set("Content-Type", ct)
		f.served[r.URL.Path]++
		_, _ = fmt.Fprint(w, html)
	}
}

// sitemapXML is the sitemap: the override, or every page.
func (f *fakeSite) sitemapXML() string {
	if f.sitemap != "" {
		return f.sitemap
	}
	paths := make([]string, 0, len(f.pages))
	for p := range f.pages {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return urlset(f.URL, paths...)
}

// urlset is a sitemap of base+path for each path.
func urlset(base string, paths ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, p := range paths {
		b.WriteString("<url><loc>" + base + p + "</loc></url>")
	}
	b.WriteString("</urlset>")
	return b.String()
}

// page is a small HTML page with a heading and a paragraph.
func page(title, text string) string {
	return "<html><body><h1>" + title + "</h1><p>" + text + "</p></body></html>"
}

// syncRule creates an enabled cron rule with action a and returns it as
// stored, credentials included.
func syncRule(t *testing.T, s *Server, a *SyncAction) AutomationRule {
	t.Helper()
	rule, err := s.AutomationManager.Create(AutomationRule{Type: "cron", Name: "sync", Enabled: true,
		Schedule: "0 * * * *", Sync: a})
	if err != nil {
		t.Fatal(err)
	}
	return *s.AutomationManager.stored(rule.ID)
}

// runSync runs rule's sync once.
func runSync(t *testing.T, s *Server, rule AutomationRule) (syncOutcome, error) {
	t.Helper()
	return s.runSourceSync(rule, time.Now())
}
