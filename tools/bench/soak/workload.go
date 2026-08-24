package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// The traffic GO-041 needs is mixed, not a write flood. A leak in an update
// path, a search cache or an SSE subscriber list is invisible to a benchmark
// that only inserts — and inserting is what tools/bench already does.
//
// The mix is roughly what a documentation site does to MDDB: mostly reads,
// a steady trickle of edits, the occasional new page.
type opMix struct {
	adds, updates, gets, searches, hybrids, deletes atomic.Uint64
	errors                                          atomic.Uint64
}

func (m *opMix) total() uint64 {
	return m.adds.Load() + m.updates.Load() + m.gets.Load() +
		m.searches.Load() + m.hybrids.Load() + m.deletes.Load()
}

func (m *opMix) String() string {
	return fmt.Sprintf("add=%d update=%d get=%d fts=%d hybrid=%d delete=%d errors=%d",
		m.adds.Load(), m.updates.Load(), m.gets.Load(),
		m.searches.Load(), m.hybrids.Load(), m.deletes.Load(), m.errors.Load())
}

var words = strings.Fields(`deployment rollback ingress certificate rotation
	replica scaling drain node quorum snapshot binlog vector embedding index
	retention compaction throughput latency partition failover checkpoint`)

func sentence(rnd *rand.Rand, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(words[rnd.Intn(len(words))])
	}
	return b.String()
}

// worker drives one connection until stop closes. Each iteration picks an
// operation by weight, so the mix holds at any concurrency.
func worker(client *http.Client, baseURL, collection string, keyspace int,
	seed int64, mix *opMix, stop <-chan struct{}) {

	rnd := rand.New(rand.NewSource(seed)) // #nosec G404 -- load shaping, not security
	for {
		select {
		case <-stop:
			return
		default:
		}

		key := fmt.Sprintf("doc-%d", rnd.Intn(keyspace))
		var err error

		switch n := rnd.Intn(100); {
		case n < 5:
			err = put(client, baseURL, collection, key, rnd)
			mix.adds.Add(1)
		case n < 20:
			err = put(client, baseURL, collection, key, rnd) // same endpoint, existing key = update
			mix.updates.Add(1)
		case n < 60:
			err = get(client, baseURL, collection, key)
			mix.gets.Add(1)
		case n < 85:
			err = search(client, baseURL, "/v1/fts", collection, sentence(rnd, 2))
			mix.searches.Add(1)
		case n < 98:
			err = search(client, baseURL, "/v1/hybrid-search", collection, sentence(rnd, 3))
			mix.hybrids.Add(1)
		default:
			err = del(client, baseURL, collection, key)
			mix.deletes.Add(1)
		}
		if err != nil {
			mix.errors.Add(1)
		}
	}
}

func put(client *http.Client, baseURL, collection, key string, rnd *rand.Rand) error {
	body, _ := json.Marshal(map[string]any{
		"collection": collection,
		"key":        key,
		"lang":       "en",
		"contentMd":  sentence(rnd, 40+rnd.Intn(200)),
		"meta": map[string][]string{
			"tags":   {words[rnd.Intn(len(words))], words[rnd.Intn(len(words))]},
			"author": {fmt.Sprintf("author-%d", rnd.Intn(20))},
		},
	})
	return post(client, baseURL+"/v1/add", body)
}

func get(client *http.Client, baseURL, collection, key string) error {
	body, _ := json.Marshal(map[string]any{
		"collection": collection, "key": key, "lang": "en",
	})
	return post(client, baseURL+"/v1/get", body)
}

func search(client *http.Client, baseURL, path, collection, query string) error {
	body, _ := json.Marshal(map[string]any{
		"collection": collection,
		"query":      query,
		"limit":      10,
		"topK":       10,
	})
	return post(client, baseURL+path, body)
}

func del(client *http.Client, baseURL, collection, key string) error {
	body, _ := json.Marshal(map[string]any{
		"collection": collection, "key": key, "lang": "en",
	})
	return post(client, baseURL+"/v1/delete", body)
}

func post(client *http.Client, url string, body []byte) error {
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	// 400 is included deliberately. MDDB answers "document not found" with 400
	// rather than 404, so a get or delete for a key another worker has already
	// removed is a normal outcome of this mix, not an error. Counting it as one
	// would bury the errors that matter under tens of thousands that do not.
	return drainClose(resp, http.StatusOK, http.StatusCreated,
		http.StatusNotFound, http.StatusBadRequest)
}

// drainClose reads the body to completion before closing it. Leaving it
// unread returns the connection to the pool unusable, and a soak test that
// opens a fresh connection per request measures the wrong thing.
func drainClose(resp *http.Response, ok ...int) error {
	defer func() { _ = resp.Body.Close() }()
	var buf [4096]byte
	for {
		if _, err := resp.Body.Read(buf[:]); err != nil {
			break
		}
	}
	for _, code := range ok {
		if resp.StatusCode == code {
			return nil
		}
	}
	return fmt.Errorf("%s: status %d", resp.Request.URL.Path, resp.StatusCode)
}

func newClient(conns int) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        conns,
			MaxIdleConnsPerHost: conns,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

// seed fills the keyspace before the measurement starts.
//
// Without it the first minutes are dominated by reads that miss and searches
// over an empty index, which is a different workload from the one being
// measured — and it is the early minutes that decide whether the caches have
// settled by the time the first quarter is sampled.
func seed(client *http.Client, baseURL, collection string, keyspace int) error {
	rnd := rand.New(rand.NewSource(99)) // #nosec G404 -- fixture data
	for i := 0; i < keyspace; i++ {
		if err := put(client, baseURL, collection, fmt.Sprintf("doc-%d", i), rnd); err != nil {
			return fmt.Errorf("seeding %s: %w", collection, err)
		}
	}
	return nil
}
