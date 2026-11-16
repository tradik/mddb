package main

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// The write scenarios. Ingest writes the same seeded documents one per
// request and in batches, on a server that embeds nothing and on one that
// embeds with the built-in lexical provider — no network either way — and
// counts a document as written once its vector is indexed. Mixed runs
// searches and writes against one collection at the same time.

// textCorpus is a corpus used for its document texts alone.
func textCorpus(collection string, n int) *corpus {
	return &corpus{collection: collection, perTopic: 200, topics: max(n/200, 1)}
}

// ingest measures single and batched writes on the server at url, which
// embeds with embedding ("none" for no provider).
func ingest(client *http.Client, url string, p profile, embedding string, log io.Writer) ([]IngestMetric, error) {
	var out []IngestMetric
	for _, run := range []struct {
		kind         string
		docs, batch  int
		parallelReqs int
	}{
		{"single", p.IngestSingle, 1, 1},
		{"batch", p.IngestBatch, p.LoadBatch, p.LoadParallel},
	} {
		m := IngestMetric{Name: fmt.Sprintf("%s-%s", run.kind, embedding), Docs: run.docs, BatchSize: run.batch, Embedding: embedding}
		c := textCorpus("ingest-"+m.Name, run.docs)
		w := writer{client: client, url: url, collection: c.collection}
		start := time.Now()
		var t timed
		var err error
		if run.batch == 1 {
			t, err = fanOut(run.docs, 1, func(i int) error { return w.addOne(c.document(i)) })
		} else {
			t, err = w.addAll(run.docs, run.batch, run.parallelReqs, c.document)
		}
		if err == nil && embedding != "none" {
			err = w.waitEmbedded(p.Timeout)
		}
		if err != nil {
			return out, fmt.Errorf("%s: %w", m.Name, err)
		}
		t.Elapsed = time.Since(start) // with the embeddings drained
		m.DocsPerSec, m.Latency = t.perSecond(run.docs), summarize(t.Millis)
		_, _ = fmt.Fprintf(log, "  %-16s %9.0f docs/s  request p50 %8.3f  p95 %8.3f  p99 %8.3f ms\n",
			m.Name, m.DocsPerSec, m.P50, m.P95, m.P99)
		out = append(out, m)
	}
	return out, nil
}

// mixed runs Clients readers searching the HNSW index and one writer adding
// documents to the same collection for p.Mixed. The writer rewrites corpus
// documents under new keys, so the server embeds and indexes each one.
func mixed(s searcher, p profile) (*MixedMetric, error) {
	w := writer{client: s.client, url: s.url, collection: s.c.collection}
	sc := scenario{Kind: "vector", Algorithm: "hnsw"}
	var (
		stop      atomic.Bool
		mu        sync.Mutex
		firstErr  error
		readMs    []float64
		writeMs   []float64
		wg        sync.WaitGroup
		queryPool = len(s.c.queries)
	)
	loop := func(sink *[]float64, call func(i int) error) {
		defer wg.Done()
		for i := 0; !stop.Load(); i++ {
			began := time.Now()
			err := call(i)
			took := float64(time.Since(began).Microseconds()) / 1000
			mu.Lock()
			if err != nil && firstErr == nil {
				firstErr = err
				stop.Store(true)
			}
			*sink = append(*sink, took)
			mu.Unlock()
		}
	}
	start := time.Now()
	for r := 0; r < p.Clients; r++ {
		wg.Add(1)
		go loop(&readMs, func(i int) error {
			_, err := s.query(sc, (i*p.Clients+r)%queryPool, false)
			return err
		})
	}
	wg.Add(1)
	go loop(&writeMs, func(i int) error {
		doc := s.c.document(i % len(s.c.docs))
		doc["key"] = fmt.Sprintf("m%08d", i)
		return w.addOne(doc)
	})
	time.Sleep(p.Mixed)
	stop.Store(true)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	t := timed{Elapsed: time.Since(start)}
	return &MixedMetric{
		Seconds: round3(t.Elapsed.Seconds()), Readers: p.Clients, Writers: 1,
		Read: summarize(readMs), ReadQPS: t.perSecond(len(readMs)),
		Write: summarize(writeMs), WritesPerSec: t.perSecond(len(writeMs)),
	}, nil
}
