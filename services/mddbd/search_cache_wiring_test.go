package main

import (
	"testing"

	"mddb/internal/cache"
)

// GO-031 wiring. The cache itself is covered in internal/cache; these cover
// the decisions made at the request boundary — when to use it at all, what
// two requests must share, and that a write makes an entry unreachable.

func cacheTestServer(size int) *Server {
	return &Server{SearchCache: cache.NewSearchCache(size)}
}

// The promise of the feature: say nothing, get today's behaviour.
func TestNoCacheTTLMeansNoCaching(t *testing.T) {
	s := cacheTestServer(10)
	key, ttl := s.searchCacheLookup(&FTSSearchRequest{Collection: "docs", Query: "x"})
	if key != "" || ttl != 0 {
		t.Errorf("a request without cacheTtl must not touch the cache, got key=%q ttl=%d", key, ttl)
	}
}

func TestNegativeTTLIsIgnored(t *testing.T) {
	s := cacheTestServer(10)
	if key, _ := s.searchCacheLookup(&FTSSearchRequest{Collection: "docs", Query: "x", CacheTTL: -30}); key != "" {
		t.Error("a negative cacheTtl must not enable caching")
	}
}

func TestTTLIsCapped(t *testing.T) {
	s := cacheTestServer(10)
	_, ttl := s.searchCacheLookup(&FTSSearchRequest{Collection: "docs", Query: "x", CacheTTL: 99999})
	if ttl != searchCacheMaxTTL {
		t.Errorf("ttl = %d, want it capped at %d", ttl, searchCacheMaxTTL)
	}
	_, ttl = s.searchCacheLookup(&FTSSearchRequest{Collection: "docs", Query: "x", CacheTTL: 30})
	if ttl != 30 {
		t.Errorf("a TTL under the cap should pass through, got %d", ttl)
	}
}

func TestGloballyDisabledCacheIgnoresRequests(t *testing.T) {
	s := cacheTestServer(0)
	if key, _ := s.searchCacheLookup(&FTSSearchRequest{Collection: "docs", Query: "x", CacheTTL: 60}); key != "" {
		t.Error("MDDB_SEARCH_CACHE_SIZE=0 must win over the request")
	}
}

// Two callers asking the same question should share an answer even if one is
// willing to hold it longer — the TTL is not part of the question.
func TestKeyIgnoresTheRequestedTTL(t *testing.T) {
	s := cacheTestServer(10)
	shortTTL, _ := s.searchCacheLookup(&FTSSearchRequest{Collection: "docs", Query: "x", CacheTTL: 10})
	longTTL, _ := s.searchCacheLookup(&FTSSearchRequest{Collection: "docs", Query: "x", CacheTTL: 600})
	if shortTTL != longTTL {
		t.Error("the same query with different TTLs should share a cache entry")
	}
}

func TestKeyDistinguishesRequests(t *testing.T) {
	s := cacheTestServer(10)
	base, _ := s.searchCacheLookup(&FTSSearchRequest{Collection: "docs", Query: "x", CacheTTL: 60})

	for name, req := range map[string]*FTSSearchRequest{
		"different query":      {Collection: "docs", Query: "y", CacheTTL: 60},
		"different collection": {Collection: "other", Query: "x", CacheTTL: 60},
		"different limit":      {Collection: "docs", Query: "x", Limit: 5, CacheTTL: 60},
		"different algorithm":  {Collection: "docs", Query: "x", Algorithm: "bm25", CacheTTL: 60},
		"highlighting on":      {Collection: "docs", Query: "x", Highlight: true, CacheTTL: 60},
		"different fuzziness":  {Collection: "docs", Query: "x", Fuzzy: 2, CacheTTL: 60},
	} {
		if key, _ := s.searchCacheLookup(req); key == base {
			t.Errorf("%s should not share a cache key", name)
		}
	}
}

// GO-002 in miniature: a write must make the previous answer unreachable.
func TestInvalidationMakesCachedResultsUnreachable(t *testing.T) {
	s := cacheTestServer(10)
	req := &FTSSearchRequest{Collection: "docs", Query: "golang", CacheTTL: 300}

	key, ttl := s.searchCacheLookup(req)
	s.SearchCache.Set(key, []byte(`{"total":1}`), int64(ttl))
	if _, hit := s.SearchCache.Get(key); !hit {
		t.Fatal("precondition: the response should be cached")
	}

	s.invalidateSearchCache("docs")

	newKey, _ := s.searchCacheLookup(req)
	if newKey == key {
		t.Fatal("invalidation should move the key")
	}
	if _, hit := s.SearchCache.Get(newKey); hit {
		t.Error("the same query after a write must not hit the stale entry")
	}
}

func TestInvalidationIsScopedToOneCollection(t *testing.T) {
	s := cacheTestServer(10)
	other := &FTSSearchRequest{Collection: "other", Query: "x", CacheTTL: 300}
	otherKey, ttl := s.searchCacheLookup(other)
	s.SearchCache.Set(otherKey, []byte(`{"total":2}`), int64(ttl))

	s.invalidateSearchCache("docs")

	if _, hit := s.SearchCache.Get(otherKey); !hit {
		t.Error("writing to one collection must not drop another's cached results")
	}
}

func TestInvalidateOnServerWithoutCacheIsSafe(t *testing.T) {
	(&Server{}).invalidateSearchCache("docs")
	cacheTestServer(0).invalidateSearchCache("docs")
	var nilServer *Server
	nilServer.invalidateSearchCache("docs")
}
