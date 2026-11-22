package main

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	"mddb/internal/storage"
)

// What /v1/web-search searches and what it answers with (#449).

// webSearchSnippetChars bounds a snippet: with Open WebUI's "Bypass Web
// Loader" on, the snippet is the whole content it gets, so it is a passage
// of about twenty lines rather than a one-line teaser, but never a document.
const webSearchSnippetChars = 4096

// webSearchLinkKeys are the metadata fields that carry a document's own URL,
// in the order they are tried: import and sync write source_url.
var webSearchLinkKeys = []string{"url", "source_url", "link"}

// webSearchTargets is the collections a web search reads, or the status and
// error that refuse it. Named on the URL (?collections=a,b or repeated), every
// one must be readable: the admin who wrote the URL asked for them. Else the
// server's MDDB_WEBSEARCH_COLLECTIONS, else every collection, each kept only
// when the caller may read it, which also keeps a tenant inside its own.
func (s *Server) webSearchTargets(r *http.Request) ([]string, int, error) {
	if err := s.authorize(r.Context(), ruleAuthenticated, nil); err != nil {
		return nil, http.StatusUnauthorized, err // before any list, so an empty one is not an answer
	}
	if named := splitList(r.URL.Query()["collections"]); len(named) > 0 {
		targets := s.expandTargets(s.resolveScopeNames(named))
		if len(targets) == 0 {
			return nil, http.StatusBadRequest, errors.New("no collection matches collections")
		}
		for _, c := range targets {
			if err := s.authorize(r.Context(), ruleRead, []string{c}); err != nil {
				return nil, accessStatus(err), err
			}
		}
		return targets, 0, nil
	}
	candidates := s.expandTargets(s.resolveScopeNames(splitList([]string{os.Getenv(webSearchCollectionsEnv)})))
	if len(candidates) == 0 {
		snap := s.dbStats(statsWait())
		if snap == nil {
			return nil, http.StatusServiceUnavailable, errors.New("the collection list is being computed; retry shortly")
		}
		for _, c := range snap.sortedNames() {
			if !webSearchInternal(c) {
				candidates = append(candidates, c)
			}
		}
	}
	out := candidates[:0]
	for _, c := range candidates {
		if s.authorize(r.Context(), ruleRead, []string{c}) == nil {
			out = append(out, c)
		}
	}
	return out, 0, nil
}

// accessStatus is the HTTP status of an authorize error.
func accessStatus(err error) int {
	if errors.Is(err, ErrUnauthorized) {
		return http.StatusUnauthorized
	}
	return http.StatusForbidden
}

// splitList reads comma-separated and repeated values as one list.
func splitList(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// webSearchInternal reports the server's own stores, which hold agent memory
// and chat transcripts rather than documents: searched only when named.
func webSearchInternal(c string) bool {
	for _, m := range memoryCollections {
		if c == m {
			return true
		}
	}
	return c == chatConversationsCollection || strings.HasSuffix(c, TenantSeparator+chatConversationsCollection)
}

// webSearchResults turns search hits into the answer, in rank order.
func (s *Server) webSearchResults(r *http.Request, query string, items []CrossSearchResultItem) []webSearchResult {
	terms := queryTerms(query)
	origin := requestOrigin(r)
	out := make([]webSearchResult, 0, len(items))
	for i := range items {
		it := &items[i]
		p := passageAtHit(it.Collection, &it.Document, terms, nil, PassageOptions{}, 0, "")
		snippet, _ := truncateRunes(p.Content, webSearchSnippetChars)
		out = append(out, webSearchResult{Link: docLink(origin, it.Collection, &it.Document),
			Title: docTitle(&it.Document), Snippet: snippet})
	}
	return out
}

// docLink is the document's own http(s) URL from its metadata, else its REST
// URL on this server: unique per document, since the caller de-duplicates
// results by link.
func docLink(origin, collection string, doc *storage.Doc) string {
	for _, k := range webSearchLinkKeys {
		for _, v := range doc.Meta[k] {
			if strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://") {
				return v
			}
		}
	}
	q := url.Values{"collection": {collection}, "key": {doc.Key}, "lang": {doc.Lang}}
	return origin + "/v1/doc-meta?" + q.Encode()
}

// docTitle is meta.title, else the key.
func docTitle(doc *storage.Doc) string {
	for _, t := range doc.Meta["title"] {
		if strings.TrimSpace(t) != "" {
			return t
		}
	}
	return doc.Key
}

// requestOrigin is the scheme and host the request reached this server at.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
