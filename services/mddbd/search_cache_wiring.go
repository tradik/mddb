package main

import (
	"mddb/internal/cache"
	"mddb/internal/envconf"

	json "mddb/internal/jsonx"
)

// Search-result caching (GO-031).
//
// The cache is opt-in per request: a caller states how stale an answer may be
// and gets one from memory if it is that fresh. A request without cacheTtl is
// neither served nor stored, so the default behaviour is exactly what it was.

// searchCacheMaxTTL bounds what a client may ask for. Without a ceiling a
// caller could pin a result set for a day and then be surprised by it.
const searchCacheMaxTTL = 3600

// newSearchCache builds the cache from the environment.
// MDDB_SEARCH_CACHE_SIZE=0 disables it globally, whatever requests ask for.
func newSearchCache() *cache.SearchCache {
	return cache.NewSearchCache(envconf.Int("MDDB_SEARCH_CACHE_SIZE", 500))
}

// searchCacheLookup returns the cache key and effective TTL for a request, or
// ("", 0) when this request should not touch the cache at all.
func (s *Server) searchCacheLookup(req *FTSSearchRequest) (string, int) {
	if req.CacheTTL <= 0 || !s.SearchCache.Enabled() {
		return "", 0
	}
	ttl := min(req.CacheTTL, searchCacheMaxTTL)

	// The key is built from the request with CacheTTL cleared: two callers
	// asking the same question should share an answer even if one is willing
	// to hold it longer than the other.
	keyReq := *req
	keyReq.CacheTTL = 0
	canonical, err := json.Marshal(keyReq)
	if err != nil {
		return "", 0
	}
	return s.SearchCache.Key(req.Collection, canonical), ttl
}

// invalidateSearchCache drops a collection's cached results. Called from every
// path that changes what a search would return — writes, deletes and reindexes
// — because a cache that outlives its data is how GO-002 happened.
func (s *Server) invalidateSearchCache(collection string) {
	if s == nil || !s.SearchCache.Enabled() {
		return
	}
	s.SearchCache.Invalidate(collection)
}
