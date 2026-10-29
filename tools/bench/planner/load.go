package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"mddb-bench/internal/benchkit"
)

// Loading a corpus into MDDB and waiting until every vector is indexed.

// load (re)creates the collection with Parallel add-batch requests in flight.
// After the first refusal the remaining batches are skipped.
func load(client *http.Client, cfg config, c *corpus) error {
	_, _ = benchkit.Post(client, cfg.URL+"/v1/delete-collection", map[string]any{"collection": c.collection})
	starts := make(chan int)
	var (
		mu    sync.Mutex
		first error
		wg    sync.WaitGroup
	)
	for w := 0; w < cfg.Parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for from := range starts {
				mu.Lock()
				failed := first != nil
				mu.Unlock()
				if failed {
					continue
				}
				if err := addBatch(client, cfg, c, from, min(from+cfg.Batch, len(c.docs))); err != nil {
					mu.Lock()
					first = cmp.Or(first, err)
					mu.Unlock()
				}
			}
		}()
	}
	for from := 0; from < len(c.docs); from += cfg.Batch {
		starts <- from
	}
	close(starts)
	wg.Wait()
	return first
}

// addBatch writes documents [from, to) and refuses an answer that stored any
// of them without its vector.
func addBatch(client *http.Client, cfg config, c *corpus, from, to int) error {
	docs := make([]map[string]any, 0, to-from)
	for i := from; i < to; i++ {
		slice := "common"
		if c.rare(i) {
			slice = "rare"
		}
		docs = append(docs, map[string]any{"key": docKey(i), "lang": "en", "contentMd": c.docText(i),
			"meta": map[string][]string{"slice": {slice}}})
	}
	raw, err := benchkit.Post(client, cfg.URL+"/v1/add-batch", map[string]any{"collection": c.collection, "documents": docs})
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
		return fmt.Errorf("add-batch %d–%d: %s", from, to, raw)
	}
	return nil
}

// waitEmbedded polls the indexing status until no embedding is pending.
func waitEmbedded(client *http.Client, cfg config, collection string) error {
	deadline := time.Now().Add(cfg.Timeout)
	for {
		resp, err := client.Get(cfg.URL + "/v1/indexing-status?collection=" + url.QueryEscape(collection)) // #nosec G704 -- the operator's own server, from -url
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
			return fmt.Errorf("%d embeddings still pending after %s", st.Pending, cfg.Timeout)
		}
		time.Sleep(cfg.PollPeriod)
	}
}
