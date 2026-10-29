package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"mddb/internal/fts"
	"mddb/internal/temporal"
	"mddb/internal/tracing"
	proto "mddb/proto"

	json "mddb/internal/jsonx"
)

// ---- HTTP types for /v1/add-batch ----

// AddBatchHTTPRequest is the HTTP request body for adding documents in batch.
type AddBatchHTTPRequest struct {
	Collection string             `json:"collection"`
	Documents  []AddBatchDocument `json:"documents"`
}

// AddBatchDocument represents a single document within a batch add request.
type AddBatchDocument struct {
	Key          string              `json:"key"`
	Lang         string              `json:"lang"`
	Meta         map[string][]string `json:"meta,omitempty"`
	ContentMD    string              `json:"contentMd"`
	SaveRevision bool                `json:"saveRevision,omitempty"`
}

// AddBatchHTTPResponse is the HTTP response body for a batch add operation.
type AddBatchHTTPResponse struct {
	Added   int      `json:"added"`
	Updated int      `json:"updated"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
	// EmbeddingDropped counts documents written without their vector because
	// the embedding queue could not take them (#232). Omitted when zero, so a
	// healthy import is unchanged and a lossy one cannot be mistaken for one:
	// `failed` stays 0 because the documents ARE stored — they are simply
	// invisible to vector and hybrid search until reindexed.
	EmbeddingDropped int `json:"embeddingDropped,omitempty"`
}

// handleAddBatch handles POST /v1/add-batch
func (s *Server) handleAddBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// A batch's work outlives the write timeout on a large collection or a
	// slow disk, and is not cancelled by it; only the answer was lost (#315).
	liftWriteDeadline(w, r)

	var req AddBatchHTTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	if len(req.Documents) == 0 {
		ok(w, AddBatchHTTPResponse{})
		return
	}

	// Convert to proto BatchDocument
	protoDocs := make([]*proto.BatchDocument, len(req.Documents))
	for i, d := range req.Documents {
		protoDocs[i] = &proto.BatchDocument{
			Key:          d.Key,
			Lang:         d.Lang,
			Meta:         toProtoMeta(d.Meta),
			ContentMd:    d.ContentMD,
			SaveRevision: d.SaveRevision,
		}
	}

	// Process batch
	resp, processed, err := s.processBatchWithDocs(r.Context(), req.Collection, protoDocs)
	if err != nil {
		bad(w, err)
		return
	}

	// Fire post-batch hooks
	dropped := s.firePostBatchHooks(req.Collection, processed, postBatchOptions{})

	s.Metrics.IncOp("add_batch")

	ok(w, AddBatchHTTPResponse{
		Added:            int(resp.Added),
		Updated:          int(resp.Updated),
		Failed:           int(resp.Failed),
		Errors:           resp.Errors,
		EmbeddingDropped: dropped,
	})
}

// postBatchOptions controls which post-commit hooks to fire.
type postBatchOptions struct {
	SkipEmbeddings bool
	SkipFTS        bool
	SkipWebhooks   bool
	// Trace is the span of the request that wrote the batch: the embedding
	// jobs carry it so their ingest.embed spans join its trace (OPS-024).
	Trace tracing.SpanContext
}

// processBatchWithDocs runs the batch processor and returns both the response and processed docs.
func (s *Server) processBatchWithDocs(ctx context.Context, collection string, protoDocs []*proto.BatchDocument) (*proto.AddBatchResponse, []*ProcessedDoc, error) {
	now := time.Now().Unix()

	var resp *proto.AddBatchResponse
	var processed []*ProcessedDoc
	var err error

	if s.UseExtreme && s.finalBatchProcessor != nil {
		// FinalBatchProcessor: single read tx → parallel marshal → single write tx
		existingMap := s.finalBatchProcessor.batchReadAll(collection, protoDocs)
		all := s.finalBatchProcessor.parallelMarshal(ctx, collection, protoDocs, existingMap, now)
		resp, processed, err = s.finalBatchProcessor.commitBatch(collection, all, now)
	} else {
		bp := NewBatchProcessor(s, 8)
		all := bp.parallelProcess(ctx, collection, protoDocs, now)
		resp, processed, err = bp.commitBatch(collection, all, now)
	}
	if err != nil {
		return nil, nil, err // a quota refusal (GO-043)
	}

	if resp.Failed == safeInt32(len(protoDocs)) && len(resp.Errors) > 0 {
		err = fmt.Errorf("all documents failed: %s", resp.Errors[0])
	}

	// `processed` is now the set of durably-committed docs, so the caller's
	// firePostBatchHooks only fires for documents that actually landed.
	return resp, processed, err
}

// firePostBatchHooks fires embedding, FTS, webhook, TTL, and automation hooks
// for all successfully processed documents after batch commit.
// It returns the number of documents whose embedding job the queue refused,
// which the caller is expected to pass on rather than discard (#232).
func (s *Server) firePostBatchHooks(collection string, processed []*ProcessedDoc, opts postBatchOptions) int {
	// Every document's FTS work is gathered and written in one transaction
	// after the loop. Indexing a document touches three indexes, and each
	// entry point opens its own BoltDB commit, so doing it per document cost
	// 3N commits per batch — measured at 238 docs/s against 46k docs/s for the
	// same writes without indexing.
	var ftsBatch []fts.BulkDoc
	embeddingDropped := 0

	for _, p := range processed {
		if p.Error != nil {
			continue
		}

		// Embedding
		if !opts.SkipEmbeddings && s.EmbeddingWorker != nil && p.Doc.ContentMD != "" {
			if !s.EmbeddingWorker.Enqueue(EmbeddingJob{
				Collection: collection,
				DocID:      p.DocID,
				ContentMD:  p.Doc.ContentMD,
				ChunkMode:  s.chunkModeFor(collection, &p.Doc),
				ChunkSize:  s.chunkSizeFor(collection),
				Summary:    documentSummary(&p.Doc), // RAG-009
				trace:      opts.Trace,
			}) {
				embeddingDropped++
			}
		}

		// TTL
		if s.TTLManager != nil && p.Doc.ExpiresAt > 0 {
			_ = s.TTLManager.Set(collection, p.DocID, p.Doc.ExpiresAt)
		}

		// Geo indexing (R-tree + geohash + GeoStore) — parity with the
		// single-doc path (GO-001); previously every batch path skipped geo.
		if s.GeoIndex != nil && s.GeoStore != nil {
			if lat, lng, okGeo := s.GeoIndex.AddFromMeta(collection, p.DocID, p.Doc.Meta); okGeo {
				_ = s.GeoStore.Put(collection, p.DocID, lat, lng)
				if s.GeoHashIndex != nil {
					s.GeoHashIndex.Add(collection, p.DocID, lat, lng)
				}
			} else if p.IsUpdate {
				s.GeoIndex.Remove(collection, p.DocID)
				if s.GeoHashIndex != nil {
					s.GeoHashIndex.Remove(collection, p.DocID)
				}
				_ = s.GeoStore.Delete(collection, p.DocID)
			}
		}

		// Temporal tracking — parity with the single-doc path (GO-001).
		if s.TemporalManager != nil {
			et := temporal.EventUpdate
			if !p.IsUpdate {
				et = temporal.EventCreate
			}
			s.TemporalManager.RecordAsync(collection, p.DocID, et, "")
		}

		// FTS indexing is collected here and written once for the whole batch
		// below (GO-027) — see indexBatchFTS.
		if !opts.SkipFTS && s.FTSIndex != nil && p.Doc.ContentMD != "" {
			bd := ftsBulkDoc(&p.Doc)
			bd.DocID = p.DocID
			ftsBatch = append(ftsBatch, bd)
		}

		// Webhooks + SSE
		if !opts.SkipWebhooks {
			event := "doc.updated"
			if !p.IsUpdate {
				event = "doc.added"
			}
			if s.WebhookManager != nil {
				s.WebhookManager.Fire(event, collection, p.Doc.Key, p.Doc.Lang, &p.Doc)
			}
			if s.SSEHub != nil {
				s.SSEHub.BroadcastWithAuth(event, collection, p.Doc.Key, p.Doc.Lang, s.AuthManager)
			}
		}

		// Automation triggers
		if s.AutomationManager != nil && env("MDDB_TRIGGERS", "false") == "true" {
			triggerEvent := "insert"
			if p.IsUpdate {
				triggerEvent = "update"
			}
			go s.AutomationManager.EvaluateTriggers(collection, p.Doc, triggerEvent)
		}
	}

	s.indexBatchFTS(collection, ftsBatch)
	// The batch changed the collection's contents (GO-031).
	s.invalidateSearchCache(collection)

	if embeddingDropped > 0 {
		slog.Warn("documents were written without vectors: the embedding queue could not take them",
			"collection", collection, "dropped", embeddingDropped, "batch", len(processed),
			"remedy", "POST /v1/vector-reindex, or raise MDDB_EMBEDDING_QUEUE_SIZE / MDDB_EMBEDDING_QUEUE_WAIT")
	}
	return embeddingDropped
}

// indexBatchFTS writes a batch's full-text entries in one transaction, falling
// back to per-document indexing if that fails.
//
// The batch is atomic, so one unindexable document would otherwise take the
// rest of the batch down with it. The per-document path was the behaviour
// before batching — it swallowed individual failures — so the fallback keeps
// that contract while the fast path handles the normal case.
func (s *Server) indexBatchFTS(collection string, batch []fts.BulkDoc) {
	if len(batch) == 0 || s.FTSIndex == nil || !s.ftsEnabledFor(collection) {
		return
	}
	if err := s.FTSIndex.IndexDocs(collection, batch); err == nil {
		return
	} else {
		slog.Warn("batch FTS indexing failed, retrying per document",
			"collection", collection, "documents", len(batch), "err", err)
	}
	for _, d := range batch {
		if err := s.FTSIndex.IndexDocs(collection, []fts.BulkDoc{d}); err != nil {
			slog.Warn("FTS indexing failed", "collection", collection, "docID", d.DocID, "err", err)
		}
	}
}
