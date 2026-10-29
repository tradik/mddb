package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"mddb/internal/embedding"
	vec "mddb/internal/vector"
)

// Embedding a document's summary (RAG-009), shared by the embedding worker
// and the reindex so the two cannot drift.
//
// A summary is embedded only when its text changed: the stored record carries
// the summary's hash, as a chunk carries its own (RAG-003). It spends the
// collection's embedding budget like any other provider call (GO-043) and, in
// the worker, a spent budget parks the job instead of failing it. A document
// without a summary costs one map lookup: nothing is read or written unless
// it had one before.

// summaryIndexer is where summary vectors go. The server's indexes implement
// it; a worker built without them skips summaries.
type summaryIndexer interface {
	IndexSummary(collection, docID string, vector []float32) error
	RemoveSummary(collection, docID string)
	HasSummary(collection, docID string) bool
}

// summaryRefresh is what embedding a summary needs.
type summaryRefresh struct {
	provider embedding.Provider
	store    *vec.VectorStore
	index    summaryIndexer
	budget   *embeddingBudget // nil: no budget
	attempts int              // provider calls before giving up; < 1 means 1
	// exists reports whether the document is still stored; nil assumes it is.
	exists func() bool
}

// run brings a document's summary vector in line with its summary: embeds a
// new or changed one (every one with force), removes one the document no
// longer has. jobParked means the budget is spent and nothing was called.
func (r summaryRefresh) run(ctx context.Context, collection, docID, summary string, force bool) (jobOutcome, error) {
	if summary == "" {
		if r.index.HasSummary(collection, docID) {
			_ = r.store.DeleteSummary(collection, docID)
			r.index.RemoveSummary(collection, docID)
		}
		return jobDone, nil
	}
	hash := vec.ContentHash(summary)
	if rec := r.store.GetSummary(collection, docID); !force && rec != nil && rec.ContentHash == hash {
		if !r.index.HasSummary(collection, docID) {
			return jobDone, r.index.IndexSummary(collection, docID, rec.Vector)
		}
		return jobDone, nil
	}
	if r.budget != nil && r.budget.check(collection) != nil {
		return jobParked, nil
	}
	vector, err := r.embed(ctx, collection, summary)
	if err != nil {
		return jobFailed, fmt.Errorf("summary of %s: %w", docID, err)
	}
	if r.exists != nil && !r.exists() {
		return jobDone, nil // deleted while it was embedded (#331)
	}
	if err := r.store.PutSummary(collection, docID, vector, r.provider.Model(), hash); err != nil {
		return jobFailed, fmt.Errorf("summary of %s: store: %w", docID, err)
	}
	if err := r.index.IndexSummary(collection, docID, vector); err != nil {
		return jobFailed, fmt.Errorf("summary of %s: index: %w", docID, err)
	}
	return jobDone, nil
}

// embed calls the provider, retrying with the worker's backoff.
func (r summaryRefresh) embed(ctx context.Context, collection, summary string) ([]float32, error) {
	var err error
	for attempt := 0; attempt < max(r.attempts, 1); attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * embeddingRetryDelay)
		}
		ectx, cancel := context.WithTimeout(ctx, 30*time.Second)
		var v []float32
		v, err = r.provider.Embed(ectx, summary, embedding.RoleDocument)
		cancel()
		if r.budget != nil {
			r.budget.spend(collection, summary)
		}
		if err == nil {
			return v, nil
		}
	}
	return nil, err
}

// processSummary is the worker's summary step, after the chunks.
func (w *EmbeddingWorker) processSummary(job EmbeddingJob) jobOutcome {
	ix, ok := w.indexer.(summaryIndexer)
	if !ok {
		return jobDone
	}
	r := summaryRefresh{provider: w.provider, store: w.vectorStore, index: ix, budget: w.budget, attempts: 3}
	if checker, ok := w.indexer.(documentChecker); ok {
		r.exists = func() bool { return checker.DocumentExists(job.Collection, job.DocID) }
	}
	out, err := r.run(context.Background(), job.Collection, job.DocID, job.Summary, false)
	if err != nil {
		slog.Error("summary embedding failed", "collection", job.Collection, "docID", job.DocID, "err", err)
		if w.metrics != nil {
			w.metrics.IncOp("embedding", "error")
		}
	}
	return out
}

// reindexSummary is the reindex's summary step. A spent budget is reported,
// not parked: a reindex exists to spend calls, and says what it left undone.
func (s *Server) reindexSummary(ctx context.Context, collection, docID, summary string, force bool, res *vectorReindexResult) {
	r := summaryRefresh{provider: s.Embedding, store: s.VectorStore, index: serverIndexes{s}, budget: s.quotas().budget}
	out, err := r.run(ctx, collection, docID, summary, force)
	switch {
	case err != nil:
		res.Errors = append(res.Errors, err.Error())
	case out == jobParked:
		res.Errors = append(res.Errors, fmt.Sprintf("summary of %s: embedding budget exhausted", docID))
	}
}
