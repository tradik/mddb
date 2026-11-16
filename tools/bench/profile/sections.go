package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// The sections that need a loaded server of their own: search (with mixed
// load and the restart behind it) and ingest.

// searchSection loads the corpus into a node whose embeddings come from the
// harness, measures every search scenario, restarts the node to time it to
// ready, and runs the mixed workload.
func searchSection(cfg config, root string, res *Result, log io.Writer) error {
	p := cfg.Profile
	c := newCorpus(p, cfg.Seed)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: c.embedder(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	n, err := newNode(cfg.Bin, root, "search",
		"MDDB_EMBEDDING_PROVIDER=openai", "MDDB_EMBEDDING_API_KEY=bench",
		"MDDB_EMBEDDING_API_URL=http://"+ln.Addr().String()+"/v1",
		fmt.Sprintf("MDDB_EMBEDDING_DIMENSIONS=%d", p.Dims))
	if err != nil {
		return err
	}
	defer n.kill()
	if err := n.start(); err != nil {
		return err
	}
	if _, err := n.waitHealthy(p.Timeout, true); err != nil {
		return err
	}

	w := writer{client: n.client, url: n.url(), collection: c.collection}
	s := searcher{client: n.client, url: n.url(), c: c, p: p}
	start := time.Now()
	if _, err := w.addAll(len(c.docs), p.LoadBatch, p.LoadParallel, c.document); err != nil {
		return fmt.Errorf("loading: %w", err)
	}
	if err := w.waitEmbedded(p.Timeout); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(log, "  %d documents loaded and embedded in %.1f s\n", len(c.docs), time.Since(start).Seconds())
	trained, err := s.waitAnswering(time.Now(), p.Timeout)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(log, "  first answer per vector index, seconds after loading: %v\n", trained)

	if res.Search, err = s.run(log); err != nil {
		return err
	}
	peak := n.peakRSS()

	n.stop()
	if err := n.start(); err != nil {
		return err
	}
	healthy, err := n.waitHealthy(p.Timeout, false)
	if err != nil {
		return err
	}
	ready, err := s.waitAnswering(n.started, p.Timeout)
	if err != nil {
		return err
	}
	res.Startup = &StartupMetric{HealthySeconds: round3(healthy.Seconds()), IndexReadySeconds: ready,
		IndexesReadySeconds: round3(time.Since(n.started).Seconds())}

	if res.Mixed, err = mixed(s, p); err != nil {
		return fmt.Errorf("mixed: %w", err)
	}
	res.Startup.PeakRSSMB = max(peak, n.peakRSS())
	return nil
}

// ingestSection measures writes on a node that embeds nothing and on one
// that embeds with the lexical provider.
func ingestSection(cfg config, root string, res *Result, log io.Writer) error {
	for _, mode := range []struct {
		embedding string
		env       []string
	}{
		{"none", nil},
		{"lexical", []string{"MDDB_EMBEDDING_PROVIDER=lexical", "MDDB_EMBEDDING_DIMENSIONS=256"}},
	} {
		n, err := newNode(cfg.Bin, root, "ingest-"+mode.embedding, mode.env...)
		if err != nil {
			return err
		}
		metrics, err := func() ([]IngestMetric, error) {
			defer n.kill()
			if err := n.start(); err != nil {
				return nil, err
			}
			if _, err := n.waitHealthy(cfg.Profile.Timeout, true); err != nil {
				return nil, err
			}
			return ingest(n.client, n.url(), cfg.Profile, mode.embedding, log)
		}()
		if err != nil {
			return err
		}
		res.Ingest = append(res.Ingest, metrics...)
	}
	return nil
}
