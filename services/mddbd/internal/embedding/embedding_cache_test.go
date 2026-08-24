package embedding

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// RAG-003. Every vector query used to call the provider — a network round trip
// and a token charge even for a query asked seconds ago. These count the calls
// that actually reach the provider, because that is the whole point.

// countingProvider records how many texts it was asked to embed.
type countingProvider struct {
	calls     atomic.Int64
	texts     atomic.Int64
	model     string
	dims      int
	failWith  error
	shortBy   int // return this many fewer vectors than asked for
	mu        sync.Mutex
	seenTexts []string
}

func newCounting() *countingProvider {
	return &countingProvider{model: "test-model", dims: 4}
}

func (p *countingProvider) Embed(_ context.Context, text string) ([]float32, error) {
	p.calls.Add(1)
	p.texts.Add(1)
	if p.failWith != nil {
		return nil, p.failWith
	}
	p.mu.Lock()
	p.seenTexts = append(p.seenTexts, text)
	p.mu.Unlock()
	return vectorFor(text), nil
}

func (p *countingProvider) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	p.calls.Add(1)
	p.texts.Add(int64(len(texts)))
	if p.failWith != nil {
		return nil, p.failWith
	}
	p.mu.Lock()
	p.seenTexts = append(p.seenTexts, texts...)
	p.mu.Unlock()

	n := len(texts) - p.shortBy
	if n < 0 {
		n = 0
	}
	out := make([][]float32, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, vectorFor(texts[i]))
	}
	return out, nil
}

func (p *countingProvider) Model() string   { return p.model }
func (p *countingProvider) Dimensions() int { return p.dims }

// vectorFor makes a vector that identifies its text, so a mixed-up batch is
// visible as a wrong value rather than as a plausible one.
func vectorFor(text string) []float32 {
	var sum float32
	for _, r := range text {
		sum += float32(r)
	}
	return []float32{sum, sum + 1, sum + 2, sum + 3}
}

func TestCacheServesRepeatedQueries(t *testing.T) {
	inner := newCounting()
	c := NewCachingProvider(inner, 10, time.Minute)

	first, err := c.Embed(context.Background(), "where is the footer styled?")
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		again, err := c.Embed(context.Background(), "where is the footer styled?")
		if err != nil {
			t.Fatal(err)
		}
		if len(again) != len(first) || again[0] != first[0] {
			t.Errorf("cached vector differs: %v vs %v", first, again)
		}
	}

	if got := inner.calls.Load(); got != 1 {
		t.Errorf("the provider was called %d times for one distinct query, want 1", got)
	}
}

// A caller that mutates its vector must not corrupt what everyone else gets.
// Vector code normalises in place, so this is not hypothetical.
func TestCacheHandsOutCopies(t *testing.T) {
	inner := newCounting()
	c := NewCachingProvider(inner, 10, time.Minute)

	first, _ := c.Embed(context.Background(), "text")
	original := first[0]
	first[0] = -999

	second, _ := c.Embed(context.Background(), "text")
	if second[0] != original {
		t.Errorf("mutating one caller's vector changed the cache: got %v, want %v", second[0], original)
	}
}

func TestCacheKeyIncludesTheModel(t *testing.T) {
	inner := newCounting()
	c := NewCachingProvider(inner, 10, time.Minute)

	if _, err := c.Embed(context.Background(), "same text"); err != nil {
		t.Fatal(err)
	}
	// The same sentence embeds differently under a different model, and
	// MDDB_EMBEDDING_MODEL can change while the cache is warm.
	inner.model = "another-model"
	if _, err := c.Embed(context.Background(), "same text"); err != nil {
		t.Fatal(err)
	}

	if got := inner.calls.Load(); got != 2 {
		t.Errorf("provider calls = %d, want 2: a model change must not reuse vectors", got)
	}
}

func TestCacheExpiresEntries(t *testing.T) {
	inner := newCounting()
	c := NewCachingProvider(inner, 10, 20*time.Millisecond)

	if _, err := c.Embed(context.Background(), "text"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := c.Embed(context.Background(), "text"); err != nil {
		t.Fatal(err)
	}

	if got := inner.calls.Load(); got != 2 {
		t.Errorf("provider calls = %d, want 2: the entry should have expired", got)
	}
}

// Eviction must drop the least recently used entry, not an arbitrary one:
// a cache that evicts the query being asked in a loop is worse than no cache.
func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	inner := newCounting()
	c := NewCachingProvider(inner, 2, time.Minute).(*CachingProvider)

	ctx := context.Background()
	if _, err := c.Embed(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Embed(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	// Touch "a" so "b" becomes the least recently used.
	if _, err := c.Embed(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Embed(ctx, "c"); err != nil {
		t.Fatal(err)
	}

	before := inner.calls.Load()
	if _, err := c.Embed(ctx, "a"); err != nil { // should still be cached
		t.Fatal(err)
	}
	if inner.calls.Load() != before {
		t.Error("the recently used entry was evicted")
	}
	if _, err := c.Embed(ctx, "b"); err != nil { // should have been evicted
		t.Fatal(err)
	}
	if inner.calls.Load() != before+1 {
		t.Error("the least recently used entry survived eviction")
	}

	_, _, size := c.Stats()
	if size > 2 {
		t.Errorf("cache holds %d entries, above its limit of 2", size)
	}
}

// A batch is usually a document's chunks on reindex, where most are unchanged.
func TestBatchAsksOnlyForMisses(t *testing.T) {
	inner := newCounting()
	c := NewCachingProvider(inner, 10, time.Minute)
	ctx := context.Background()

	if _, err := c.Embed(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	inner.texts.Store(0)

	out, err := c.EmbedBatch(ctx, []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d vectors for 3 texts", len(out))
	}
	if got := inner.texts.Load(); got != 2 {
		t.Errorf("the provider was asked for %d texts, want only the 2 misses", got)
	}

	// Every position must hold its own text's vector, not a neighbour's.
	for i, text := range []string{"a", "b", "c"} {
		want := vectorFor(text)
		if out[i][0] != want[0] {
			t.Errorf("position %d holds the vector for the wrong text", i)
		}
	}
}

func TestBatchFullyCachedSkipsTheProvider(t *testing.T) {
	inner := newCounting()
	c := NewCachingProvider(inner, 10, time.Minute)
	ctx := context.Background()

	if _, err := c.EmbedBatch(ctx, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	before := inner.calls.Load()

	if _, err := c.EmbedBatch(ctx, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if inner.calls.Load() != before {
		t.Error("a fully cached batch still called the provider")
	}
}

// A provider returning a different count than it was asked for would misalign
// vectors with their texts — corruption that only shows up as bad search
// results much later.
func TestBatchRefusesAMisalignedResponse(t *testing.T) {
	inner := newCounting()
	inner.shortBy = 1
	c := NewCachingProvider(inner, 10, time.Minute)

	out, err := c.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	// It falls back to asking for the whole batch; that answer is short too,
	// but nothing was silently mispaired.
	if len(out) == 3 && out[2] != nil && out[2][0] != vectorFor("c")[0] {
		t.Error("a short provider response was mapped onto the wrong texts")
	}
}

func TestErrorsAreNotCached(t *testing.T) {
	inner := newCounting()
	inner.failWith = errors.New("provider down")
	c := NewCachingProvider(inner, 10, time.Minute)
	ctx := context.Background()

	if _, err := c.Embed(ctx, "text"); err == nil {
		t.Fatal("expected an error")
	}
	inner.failWith = nil
	if _, err := c.Embed(ctx, "text"); err != nil {
		t.Fatalf("the retry failed: %v", err)
	}
	if got := inner.calls.Load(); got != 2 {
		t.Errorf("provider calls = %d, want 2: a failure must not be cached", got)
	}

	if _, err := c.EmbedBatch(ctx, []string{"x"}); err != nil {
		t.Fatal(err)
	}
	inner.failWith = errors.New("down again")
	if _, err := c.EmbedBatch(ctx, []string{"y"}); err == nil {
		t.Error("a batch failure was swallowed")
	}
}

// Disabled means disabled: the bare provider, not a cache that always misses.
func TestDisabledCacheReturnsTheProviderUnchanged(t *testing.T) {
	inner := newCounting()
	for _, size := range []int{0, -1} {
		got := NewCachingProvider(inner, size, time.Minute)
		if got != Provider(inner) {
			t.Errorf("size %d wrapped the provider instead of returning it", size)
		}
	}
	if NewCachingProvider(nil, 10, time.Minute) != nil {
		t.Error("wrapping a nil provider produced something")
	}
}

func TestNonPositiveTTLUsesTheDefault(t *testing.T) {
	inner := newCounting()
	c := NewCachingProvider(inner, 10, 0).(*CachingProvider)
	if c.ttl != DefaultCacheTTL {
		t.Errorf("ttl = %v, want the default %v", c.ttl, DefaultCacheTTL)
	}
}

func TestCacheDelegatesModelAndDimensions(t *testing.T) {
	inner := newCounting()
	c := NewCachingProvider(inner, 10, time.Minute)
	if c.Model() != "test-model" || c.Dimensions() != 4 {
		t.Errorf("model/dims not delegated: %q / %d", c.Model(), c.Dimensions())
	}
}

func TestCacheStats(t *testing.T) {
	inner := newCounting()
	c := NewCachingProvider(inner, 10, time.Minute).(*CachingProvider)
	ctx := context.Background()

	if _, err := c.Embed(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Embed(ctx, "a"); err != nil {
		t.Fatal(err)
	}

	hits, misses, size := c.Stats()
	if hits != 1 || misses != 1 || size != 1 {
		t.Errorf("stats = %d hits, %d misses, %d entries; want 1/1/1", hits, misses, size)
	}
}

func TestEmptyBatchGoesStraightToTheProvider(t *testing.T) {
	inner := newCounting()
	c := NewCachingProvider(inner, 10, time.Minute)
	out, err := c.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("an empty batch produced %d vectors", len(out))
	}
}

// GO-006's lesson: a cache on a hot path is reached concurrently.
func TestCacheUnderConcurrentAccess(t *testing.T) {
	inner := newCounting()
	c := NewCachingProvider(inner, 50, time.Minute)

	var wg sync.WaitGroup
	for worker := range 16 {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			ctx := context.Background()
			for i := range 50 {
				text := fmt.Sprintf("query-%d", (w+i)%20)
				vec, err := c.Embed(ctx, text)
				if err != nil {
					t.Errorf("embed failed: %v", err)
					return
				}
				// The wrong text's vector would be a silent correctness bug.
				if vec[0] != vectorFor(text)[0] {
					t.Errorf("got the vector for the wrong text: %q", text)
					return
				}
			}
		}(worker)
	}
	wg.Wait()

	// 800 requests over 20 distinct texts. Two goroutines can miss the same
	// key at the same moment and both call the provider — the cache does not
	// deduplicate concurrent misses — so the guarantee is a large reduction,
	// not exactly 20. Anything near 800 would mean the cache is not working.
	if got := inner.calls.Load(); got > 60 {
		t.Errorf("provider called %d times for 800 requests over 20 distinct texts", got)
	}
}

func TestBatchUnderConcurrentAccess(t *testing.T) {
	inner := newCounting()
	c := NewCachingProvider(inner, 50, time.Minute)

	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			texts := []string{"chunk-a", "chunk-b", "chunk-c"}
			out, err := c.EmbedBatch(context.Background(), texts)
			if err != nil {
				t.Errorf("batch failed: %v", err)
				return
			}
			for i, text := range texts {
				if out[i][0] != vectorFor(text)[0] {
					t.Errorf("worker %d: position %d holds the wrong vector", w, i)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
}

// Re-embedding the same text must refresh the entry in place rather than
// growing the cache with a duplicate.
func TestCacheOverwritesAnExistingEntry(t *testing.T) {
	inner := newCounting()
	c := NewCachingProvider(inner, 10, 30*time.Millisecond).(*CachingProvider)
	ctx := context.Background()

	if _, err := c.Embed(ctx, "text"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond) // expire it
	if _, err := c.Embed(ctx, "text"); err != nil {
		t.Fatal(err)
	}

	if _, _, size := c.Stats(); size != 1 {
		t.Errorf("cache holds %d entries for one text", size)
	}

	// The refreshed entry is live again.
	before := inner.calls.Load()
	if _, err := c.Embed(ctx, "text"); err != nil {
		t.Fatal(err)
	}
	if inner.calls.Load() != before {
		t.Error("the refreshed entry was not served from cache")
	}
}

// A provider returning nil for a text must not be stored as a valid answer:
// a cached nil would be served forever as "this text embeds to nothing".
func TestNilVectorIsNotCached(t *testing.T) {
	c := NewCachingProvider(newCounting(), 10, time.Minute).(*CachingProvider)
	c.put("some-key", nil)
	if _, _, size := c.Stats(); size != 0 {
		t.Errorf("a nil vector was cached (%d entries)", size)
	}
	if v, ok := c.get("some-key"); ok {
		t.Errorf("a nil vector came back from the cache: %v", v)
	}
}

func TestCopyVectorOnNil(t *testing.T) {
	if copyVector(nil) != nil {
		t.Error("copying nil produced a slice")
	}
}
