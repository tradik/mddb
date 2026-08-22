package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSearchCacheStoresAndReturns(t *testing.T) {
	sc := NewSearchCache(10)
	key := sc.Key("docs", []byte(`{"q":"golang"}`))

	if _, ok := sc.Get(key); ok {
		t.Error("an empty cache should miss")
	}
	sc.Set(key, []byte(`{"results":[]}`), 60)

	got, ok := sc.Get(key)
	if !ok {
		t.Fatal("the stored response should be returned")
	}
	if string(got) != `{"results":[]}` {
		t.Errorf("got %q", got)
	}

	hits, misses, size := sc.Stats()
	if hits != 1 || misses != 1 || size != 1 {
		t.Errorf("stats = hits %d, misses %d, size %d; want 1, 1, 1", hits, misses, size)
	}
}

// A request that did not ask for caching must not populate the cache — the
// promise is that behaviour is unchanged without the parameter.
func TestSearchCacheIgnoresNonPositiveTTL(t *testing.T) {
	sc := NewSearchCache(10)
	key := sc.Key("docs", []byte(`{"q":"x"}`))

	sc.Set(key, []byte("data"), 0)
	sc.Set(key, []byte("data"), -5)
	if _, _, size := sc.Stats(); size != 0 {
		t.Errorf("nothing should have been stored, size = %d", size)
	}
}

func TestSearchCacheExpires(t *testing.T) {
	sc := NewSearchCache(10)
	key := sc.Key("docs", []byte(`{"q":"x"}`))
	sc.Set(key, []byte("data"), 1)

	// Entries carry second granularity, so step past the boundary.
	sc.mu.Lock()
	sc.entries[key].ExpiresAt = time.Now().Unix() - 1
	sc.mu.Unlock()

	if _, ok := sc.Get(key); ok {
		t.Error("an expired entry should miss")
	}
	if _, _, size := sc.Stats(); size != 0 {
		t.Errorf("an expired entry should be dropped on read, size = %d", size)
	}
}

// GO-002 is the reason this test exists: a cache without invalidation serves
// documents that have been deleted.
func TestWriteInvalidatesTheCollection(t *testing.T) {
	sc := NewSearchCache(10)
	req := []byte(`{"q":"golang"}`)

	before := sc.Key("docs", req)
	sc.Set(before, []byte("stale results"), 300)
	if _, ok := sc.Get(before); !ok {
		t.Fatal("precondition: the entry should be cached")
	}

	sc.Invalidate("docs")

	after := sc.Key("docs", req)
	if after == before {
		t.Fatal("invalidation must change the key for the same request")
	}
	if _, ok := sc.Get(after); ok {
		t.Error("the same query after a write must not hit the pre-write entry")
	}
}

func TestInvalidateAffectsOnlyOneCollection(t *testing.T) {
	sc := NewSearchCache(10)
	req := []byte(`{"q":"x"}`)
	otherKey := sc.Key("other", req)
	sc.Set(otherKey, []byte("other results"), 300)

	sc.Invalidate("docs")

	if sc.Key("other", req) != otherKey {
		t.Error("invalidating one collection must not move another's key")
	}
	if _, ok := sc.Get(otherKey); !ok {
		t.Error("another collection's entry should survive")
	}
}

func TestKeyDependsOnCollectionAndRequest(t *testing.T) {
	sc := NewSearchCache(10)
	base := sc.Key("docs", []byte(`{"q":"a"}`))

	if sc.Key("other", []byte(`{"q":"a"}`)) == base {
		t.Error("different collections must not share a key")
	}
	if sc.Key("docs", []byte(`{"q":"b"}`)) == base {
		t.Error("different requests must not share a key")
	}
	if sc.Key("docs", []byte(`{"q":"a"}`)) != base {
		t.Error("the same collection and request must produce the same key")
	}
}

func TestSearchCacheEvictsWhenFull(t *testing.T) {
	sc := NewSearchCache(3)
	for i := range 5 {
		sc.Set(sc.Key("docs", fmt.Appendf(nil, `{"q":%d}`, i)), []byte("data"), 300)
	}
	if _, _, size := sc.Stats(); size > 3 {
		t.Errorf("the cache grew past its limit: %d entries", size)
	}
}

// Eviction should sacrifice an expired entry before a live one.
func TestEvictionPrefersExpiredEntries(t *testing.T) {
	sc := NewSearchCache(2)
	expired := sc.Key("docs", []byte(`{"q":"old"}`))
	live := sc.Key("docs", []byte(`{"q":"live"}`))

	sc.Set(expired, []byte("old"), 300)
	sc.Set(live, []byte("live"), 300)
	sc.mu.Lock()
	sc.entries[expired].ExpiresAt = time.Now().Unix() - 10
	sc.mu.Unlock()

	sc.Set(sc.Key("docs", []byte(`{"q":"new"}`)), []byte("new"), 300)

	if _, ok := sc.Get(live); !ok {
		t.Error("the live entry should have survived eviction")
	}
}

// A disabled cache must be inert rather than special-cased at every call site.
func TestDisabledCacheIsInert(t *testing.T) {
	for _, sc := range []*SearchCache{NewSearchCache(0), NewSearchCache(-1), nil} {
		if sc.Enabled() {
			t.Error("this cache should report itself disabled")
		}
		key := sc.Key("docs", []byte(`{"q":"x"}`))
		sc.Set(key, []byte("data"), 300)
		if _, ok := sc.Get(key); ok {
			t.Error("a disabled cache must never hit")
		}
		sc.Invalidate("docs")
		sc.Clear()
		if got := sc.Generation("docs"); got != 0 {
			t.Errorf("Generation on a disabled cache = %d, want 0", got)
		}
		if h, m, s := sc.Stats(); s != 0 {
			t.Errorf("Stats on a disabled cache = %d, %d, %d", h, m, s)
		}
	}
}

func TestClearKeepsGenerations(t *testing.T) {
	sc := NewSearchCache(10)
	sc.Invalidate("docs")
	gen := sc.Generation("docs")

	sc.Set(sc.Key("docs", []byte(`{"q":"x"}`)), []byte("data"), 300)
	sc.Clear()

	if _, _, size := sc.Stats(); size != 0 {
		t.Errorf("Clear should empty the cache, size = %d", size)
	}
	if got := sc.Generation("docs"); got != gen {
		t.Errorf("Clear must not roll back invalidation: generation %d, want %d", got, gen)
	}
}

// The cache is read and written from concurrent request handlers.
func TestSearchCacheIsConcurrencySafe(t *testing.T) {
	sc := NewSearchCache(50)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			req := fmt.Appendf(nil, `{"q":%d}`, n%5)
			for range 50 {
				key := sc.Key("docs", req)
				if _, ok := sc.Get(key); !ok {
					sc.Set(key, []byte("data"), 60)
				}
				if n%7 == 0 {
					sc.Invalidate("docs")
				}
			}
		}(i)
	}
	wg.Wait()
}
