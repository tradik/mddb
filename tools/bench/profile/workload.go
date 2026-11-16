package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"mddb-bench/internal/benchkit"
)

// What every scenario is made of: timed calls on a number of workers, their
// latencies summarised, documents written in batches, and the wait for the
// server to finish embedding them.

// timed is the outcome of a batch of calls.
type timed struct {
	Millis  []float64 // one per successful call
	Elapsed time.Duration
}

// perSecond is n things over the batch's wall time.
func (t timed) perSecond(n int) float64 {
	if t.Elapsed <= 0 {
		return 0
	}
	return round3(float64(n) / t.Elapsed.Seconds())
}

// fanOut calls fn(0..n-1) on workers goroutines and times every call. After
// the first failure the remaining calls are skipped and that error returned.
func fanOut(n, workers int, fn func(i int) error) (timed, error) {
	var (
		mu    sync.Mutex
		first error
		out   timed
		wg    sync.WaitGroup
	)
	next := make(chan int)
	start := time.Now()
	for w := 0; w < max(workers, 1); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				mu.Lock()
				failed := first != nil
				mu.Unlock()
				if failed {
					continue
				}
				began := time.Now()
				err := fn(i)
				took := float64(time.Since(began).Microseconds()) / 1000
				mu.Lock()
				if err != nil && first == nil {
					first = err
				} else if err == nil {
					out.Millis = append(out.Millis, took)
				}
				mu.Unlock()
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
	out.Elapsed = time.Since(start)
	return out, first
}

// summarize is the percentiles of a set of latencies.
func summarize(millis []float64) Latency {
	return Latency{
		P50: round3(benchkit.Percentile(millis, 50)),
		P95: round3(benchkit.Percentile(millis, 95)),
		P99: round3(benchkit.Percentile(millis, 99)),
	}
}

// medianLatency is, percentile by percentile, the median over rounds: one
// round disturbed by something else on the machine does not set the result.
func medianLatency(rounds []Latency) Latency {
	pick := func(get func(Latency) float64) float64 {
		xs := make([]float64, 0, len(rounds))
		for _, r := range rounds {
			xs = append(xs, get(r))
		}
		if len(xs) == 0 {
			return 0
		}
		sort.Float64s(xs)
		return xs[(len(xs)-1)/2]
	}
	return Latency{
		P50: pick(func(l Latency) float64 { return l.P50 }),
		P95: pick(func(l Latency) float64 { return l.P95 }),
		P99: pick(func(l Latency) float64 { return l.P99 }),
	}
}

func round3(x float64) float64 { return math.Round(x*1000) / 1000 }

// writer writes documents to one collection of one server.
type writer struct {
	client     *http.Client
	url        string
	collection string
}

// addOne writes one document with one request.
func (w writer) addOne(doc map[string]any) error {
	body := map[string]any{"collection": w.collection}
	for k, v := range doc {
		body[k] = v
	}
	_, err := benchkit.Post(w.client, w.url+"/v1/add", body)
	return err
}

// addBatch writes docs with one request and refuses an answer that failed
// any of them or stored one without its vector.
func (w writer) addBatch(docs []map[string]any) error {
	raw, err := benchkit.Post(w.client, w.url+"/v1/add-batch", map[string]any{"collection": w.collection, "documents": docs})
	if err != nil {
		return err
	}
	var resp struct {
		Failed           int `json:"failed"`
		EmbeddingDropped int `json:"embeddingDropped"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if resp.Failed+resp.EmbeddingDropped > 0 {
		return fmt.Errorf("add-batch: %s", raw)
	}
	return nil
}

// addAll writes documents doc(0..n-1) in batches of batch, parallel requests
// in flight, and times every request.
func (w writer) addAll(n, batch, parallel int, doc func(i int) map[string]any) (timed, error) {
	return fanOut((n+batch-1)/batch, parallel, func(b int) error {
		docs := make([]map[string]any, 0, batch)
		for i := b * batch; i < min((b+1)*batch, n); i++ {
			docs = append(docs, doc(i))
		}
		return w.addBatch(docs)
	})
}

// pollPeriod is how often a wait asks the server again.
var pollPeriod = 20 * time.Millisecond

// waitEmbedded polls the indexing status until no embedding is pending.
func (w writer) waitEmbedded(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		resp, err := w.client.Get(w.url + "/v1/indexing-status?collection=" + url.QueryEscape(w.collection))
		if err != nil {
			return err
		}
		var st struct {
			Pending int    `json:"pendingEmbeddings"`
			Failed  uint64 `json:"failedEmbeddings"`
		}
		err = json.NewDecoder(resp.Body).Decode(&st)
		_ = resp.Body.Close()
		switch {
		case err != nil:
			return fmt.Errorf("indexing status: %w", err)
		case st.Failed > 0:
			return fmt.Errorf("%d embeddings failed", st.Failed)
		case st.Pending == 0:
			return nil
		case time.Now().After(deadline):
			return fmt.Errorf("%d embeddings still pending after %s", st.Pending, timeout)
		}
		time.Sleep(pollPeriod)
	}
}
