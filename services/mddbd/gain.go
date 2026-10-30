package main

import (
	"log/slog"
	"sync"
	"time"

	json "mddb/internal/jsonx"
	"mddb/internal/metrics"

	bolt "go.etcd.io/bbolt"
)

// What retrieval did for the caller (AGT-004).
//
// A search that returns a passage instead of the document it sits in saves
// the caller the rest of the document. The counters here add that up, per
// day, so "how much context did MDDB save my agents" has a number behind it.
//
// Definitions, kept conservative so the number can be defended:
//   - tokensReturned: estimated tokens of what a search handed back
//     (approxTokens, the estimate the context budget uses).
//   - tokensAvoided: for each hit returned as a fragment (passage, chunk,
//     window), the hit's full document minus what was returned. The
//     counterfactual is reading the matched document, never the corpus.
//   - fragmentHits / documentHits: hits returned as a fragment / whole.
//
// Only counts are kept: no query text, no keys, no collection names.

var bucketStats = []byte("stats")

// gainFlushEvery is how often counts reach the database (a variable so tests
// need not wait a minute).
var gainFlushEvery = 60 * time.Second

// GainCounters are one day's (or one window's) totals.
type GainCounters struct {
	Searches       map[string]int64 `json:"searches,omitempty"`   // by search type
	Algorithms     map[string]int64 `json:"algorithms,omitempty"` // by ranking/index algorithm
	Fusion         map[string]int64 `json:"fusion,omitempty"`     // hybrid fusion strategy
	TokensReturned int64            `json:"tokensReturned"`
	TokensAvoided  int64            `json:"tokensAvoided"`
	FragmentHits   int64            `json:"fragmentHits"`
	DocumentHits   int64            `json:"documentHits"`
}

// add folds o into c.
func (c *GainCounters) add(o GainCounters) {
	c.Searches = addCounts(c.Searches, o.Searches)
	c.Algorithms = addCounts(c.Algorithms, o.Algorithms)
	c.Fusion = addCounts(c.Fusion, o.Fusion)
	c.TokensReturned += o.TokensReturned
	c.TokensAvoided += o.TokensAvoided
	c.FragmentHits += o.FragmentHits
	c.DocumentHits += o.DocumentHits
}

func addCounts(dst, src map[string]int64) map[string]int64 {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]int64, len(src))
	}
	for k, v := range src {
		dst[k] += v
	}
	return dst
}

// gainKind names a search for the counters.
type gainKind struct{ Type, Algorithm, Fusion string }

// gainSize is one hit: tokens returned and tokens of its whole document.
type gainSize struct{ Returned, Full int }

// GainRecorder holds counts not yet written, by day (UTC, "2006-01-02").
type GainRecorder struct {
	mu sync.Mutex
	// flushMu runs one Flush at a time. A flush takes the pending counts and
	// puts them back if the write fails; a second flush in between found
	// nothing pending, so Stop returned nil on a write that failed (#381).
	flushMu sync.Mutex
	pending map[string]*GainCounters
	// lifetime counts since the process started, for /metrics.
	lifetime GainCounters
	db       func() *bolt.DB
	now      func() time.Time
	stop     chan struct{}
	stopped  sync.Once
	// afterTickFlush, when set before Start, sees each ticker flush's result.
	// Tests wait on it instead of sleeping; nil in production.
	afterTickFlush func(error)
}

// NewGainRecorder writes to the database db returns, which follows restores.
func NewGainRecorder(db func() *bolt.DB) *GainRecorder {
	return &GainRecorder{pending: map[string]*GainCounters{}, db: db, now: time.Now, stop: make(chan struct{})}
}

// Search counts one search by type, algorithm and fusion. Safe on nil.
func (g *GainRecorder) Search(kind gainKind) {
	if g == nil {
		return
	}
	c := GainCounters{Searches: map[string]int64{kind.Type: 1}}
	if kind.Algorithm != "" {
		c.Algorithms = map[string]int64{kind.Algorithm: 1}
	}
	if kind.Fusion != "" {
		c.Fusion = map[string]int64{kind.Fusion: 1}
	}
	g.add(c)
}

// Hits counts what one search returned. Safe on nil.
func (g *GainRecorder) Hits(hits []gainSize) {
	if g == nil || len(hits) == 0 {
		return
	}
	var c GainCounters
	for _, h := range hits {
		c.TokensReturned += int64(h.Returned)
		if h.Full > h.Returned {
			c.TokensAvoided += int64(h.Full - h.Returned)
			c.FragmentHits++
		} else {
			c.DocumentHits++
		}
	}
	g.add(c)
}

func (g *GainRecorder) add(c GainCounters) {
	day := g.now().UTC().Format(time.DateOnly)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending[day] == nil {
		g.pending[day] = &GainCounters{}
	}
	g.pending[day].add(c)
	g.lifetime.add(GainCounters{TokensReturned: c.TokensReturned, TokensAvoided: c.TokensAvoided,
		FragmentHits: c.FragmentHits, DocumentHits: c.DocumentHits})
}

// Lifetime is what was counted since the process started.
func (g *GainRecorder) Lifetime() metrics.RetrievalGainView {
	g.mu.Lock()
	defer g.mu.Unlock()
	l := g.lifetime
	return metrics.RetrievalGainView{TokensReturned: l.TokensReturned, TokensAvoided: l.TokensAvoided,
		FragmentHits: l.FragmentHits, DocumentHits: l.DocumentHits}
}

// Flush adds pending counts to the stored days. On failure the counts are
// kept for the next flush. Flushes run one at a time (flushMu).
func (g *GainRecorder) Flush() error {
	g.flushMu.Lock()
	defer g.flushMu.Unlock()
	g.mu.Lock()
	pending := g.pending
	g.pending = map[string]*GainCounters{}
	g.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	err := g.db().Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketStats)
		if err != nil {
			return err
		}
		for day, c := range pending {
			stored := readGainDay(b, day)
			stored.add(*c)
			v, err := json.Marshal(stored)
			if err != nil {
				return err
			}
			if err := b.Put(gainKey(day), v); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		g.mu.Lock()
		for day, c := range pending {
			if g.pending[day] == nil {
				g.pending[day] = &GainCounters{}
			}
			g.pending[day].add(*c)
		}
		g.mu.Unlock()
	}
	return err
}

func gainKey(day string) []byte { return []byte("gain|" + day) }

func readGainDay(b *bolt.Bucket, day string) GainCounters {
	var c GainCounters
	if v := b.Get(gainKey(day)); v != nil {
		_ = json.Unmarshal(v, &c)
	}
	return c
}

// Start flushes every gainFlushEvery until Stop.
func (g *GainRecorder) Start() {
	go func() {
		t := time.NewTicker(gainFlushEvery)
		defer t.Stop()
		for {
			select {
			case <-g.stop:
				return
			case <-t.C:
				err := g.Flush()
				if err != nil {
					slog.Warn("gain counters not stored; kept for the next try", "err", err)
				}
				if g.afterTickFlush != nil {
					g.afterTickFlush(err)
				}
			}
		}
	}()
}

// Stop ends the flusher and writes what is pending.
func (g *GainRecorder) Stop() error {
	g.stopped.Do(func() { close(g.stop) })
	return g.Flush()
}
