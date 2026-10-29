package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"mddb/internal/embedding"
	"mddb/internal/storage"
	vec "mddb/internal/vector"
)

// Embedding an image document's picture (RAG-010), shared by the embedding
// worker and the reindex.
//
// An image is embedded when the provider can embed images and the bytes or
// the model changed: the stored record carries the hash of the bytes and the
// model, as a chunk carries its text's hash (RAG-003). The call spends the
// collection's embedding budget (GO-043) — one call, and tokens counted the
// way Voyage counts pixels — and, in the worker, a spent budget parks the job.
// A provider that cannot embed images skips them: the image is still found by
// its alt text and caption, and indexing-status says so (imageStatus).

// imagePixelsPerToken is how many pixels count as one token: Voyage's rule,
// used for every provider as the budget's estimate.
const imagePixelsPerToken = 560

// isImageDoc reports whether a document is an image document.
func isImageDoc(doc *storage.Doc) bool {
	return len(doc.Meta[metaKind]) > 0 && doc.Meta[metaKind][0] == kindImage
}

// imageIndexer is where image vectors go and where their bytes come from.
// The server's indexes implement it; a worker built without them skips
// images.
type imageIndexer interface {
	IndexImage(collection, docID string, vector []float32) error
	HasImage(collection, docID string) bool
	ImageBytes(collection, docID string) []byte
}

// imageRefresh is what embedding an image needs.
type imageRefresh struct {
	provider embedding.Provider
	store    *vec.VectorStore
	index    imageIndexer
	budget   *embeddingBudget // nil: no budget
	attempts int              // provider calls before giving up; < 1 means 1
	// exists reports whether the document is still stored; nil assumes it is.
	exists func() bool
}

// run brings an image document's vector in line with its bytes. jobParked
// means the budget is spent and nothing was called.
func (r imageRefresh) run(ctx context.Context, collection, docID string, force bool) (jobOutcome, error) {
	ie, ok := embedding.ImageEmbedderOf(r.provider)
	if !ok {
		return jobDone, nil // found by its text only; imageStatus says why
	}
	data := r.index.ImageBytes(collection, docID)
	if data == nil {
		return jobDone, nil
	}
	mime, w, h, err := validateImage(data)
	if err != nil {
		return jobFailed, fmt.Errorf("image %s: %w", docID, err)
	}
	hash := vec.ContentHash(string(data))
	if rec := r.store.GetImage(collection, docID); !force && rec != nil && rec.ContentHash == hash && rec.Model == ie.ImageModel() {
		if !r.index.HasImage(collection, docID) {
			return jobDone, r.index.IndexImage(collection, docID, rec.Vector)
		}
		return jobDone, nil
	}
	if r.budget != nil && r.budget.check(collection) != nil {
		return jobParked, nil
	}
	vector, err := r.embed(ctx, ie, collection, embedding.Image{Data: data, MimeType: mime}, int64(w*h/imagePixelsPerToken+1))
	if err != nil {
		return jobFailed, fmt.Errorf("image %s: %w", docID, err)
	}
	if r.exists != nil && !r.exists() {
		return jobDone, nil // deleted while it was embedded (#331)
	}
	if err := r.store.PutImage(collection, docID, vector, ie.ImageModel(), hash); err != nil {
		return jobFailed, fmt.Errorf("image %s: store: %w", docID, err)
	}
	if err := r.index.IndexImage(collection, docID, vector); err != nil {
		return jobFailed, fmt.Errorf("image %s: index: %w", docID, err)
	}
	return jobDone, nil
}

// embed calls the provider, retrying with the worker's backoff.
func (r imageRefresh) embed(ctx context.Context, ie embedding.ImageEmbedder, collection string, img embedding.Image, tokens int64) ([]float32, error) {
	var err error
	for attempt := 0; attempt < max(r.attempts, 1); attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * embeddingRetryDelay)
		}
		ectx, cancel := context.WithTimeout(ctx, 60*time.Second)
		var v []float32
		v, err = ie.EmbedImage(ectx, img, embedding.RoleDocument)
		cancel()
		if r.budget != nil {
			r.budget.spendTokens(collection, tokens)
		}
		if err == nil {
			return v, nil
		}
	}
	return nil, err
}

// processImage is the worker's image step, after the chunks and summary.
func (w *EmbeddingWorker) processImage(job EmbeddingJob) jobOutcome {
	ix, ok := w.indexer.(imageIndexer)
	if !job.Image || !ok {
		return jobDone
	}
	r := imageRefresh{provider: w.provider, store: w.vectorStore, index: ix, budget: w.budget, attempts: 3}
	if checker, ok := w.indexer.(documentChecker); ok {
		r.exists = func() bool { return checker.DocumentExists(job.Collection, job.DocID) }
	}
	out, err := r.run(context.Background(), job.Collection, job.DocID, false)
	if err != nil {
		slog.Error("image embedding failed", "collection", job.Collection, "docID", job.DocID, "err", err)
		if w.metrics != nil {
			w.metrics.IncOp("embedding", "error")
		}
	}
	return out
}

// reindexImage is the reindex's image step. A spent budget is reported, not
// parked, as for summaries.
func (s *Server) reindexImage(ctx context.Context, collection, docID string, force bool, res *vectorReindexResult) {
	r := imageRefresh{provider: s.Embedding, store: s.VectorStore, index: serverIndexes{s}, budget: s.quotas().budget}
	out, err := r.run(ctx, collection, docID, force)
	switch {
	case err != nil:
		res.Errors = append(res.Errors, err.Error())
	case out == jobParked:
		res.Errors = append(res.Errors, fmt.Sprintf("image %s: embedding budget exhausted", docID))
	}
}

// embeddingJob is the job a stored document queues: its chunks, its summary
// (RAG-009) and, for an image document, its picture (RAG-010). One builder for
// every write path, so they cannot drift.
func (s *Server) embeddingJob(collection string, doc *storage.Doc) EmbeddingJob {
	return EmbeddingJob{
		Collection: collection,
		DocID:      doc.ID,
		ContentMD:  doc.ContentMD,
		ChunkMode:  s.chunkModeFor(collection, doc),
		ChunkSize:  s.chunkSizeFor(collection),
		Summary:    documentSummary(doc),
		Image:      isImageDoc(doc),
	}
}

// ImageIndexingStatus is the images part of indexing-status (RAG-010),
// present when the collection holds image documents.
type ImageIndexingStatus struct {
	// Documents is how many image documents hold an image.
	Documents int `json:"documents"`
	// Embedded is how many of them have an image vector.
	Embedded int `json:"embedded"`
	// Model is the multimodal model images are embedded with; empty when
	// the provider embeds no images.
	Model string `json:"model,omitempty"`
	// Note says why images are not embedded, when they are not: they are
	// then found by their alt text and captions only.
	Note string `json:"note,omitempty"`
}

// imageStatus reports a collection's images, or the server's for "", nil
// when there are none.
func (s *Server) imageStatus(collection string) *ImageIndexingStatus {
	n := s.countImages(collection)
	if n == 0 {
		return nil
	}
	st := &ImageIndexingStatus{Documents: n}
	if collection != "" {
		st.Embedded = s.imageIndex().size(collection)
	} else {
		st.Embedded = s.imageIndex().total()
	}
	ie, ok := embedding.ImageEmbedderOf(s.Embedding)
	switch {
	case ok:
		st.Model = ie.ImageModel()
	case s.Embedding == nil:
		st.Note = "no embedding provider is configured: images are found by their alt text and captions only"
	default:
		st.Note = "embedding provider " + strconv.Quote(s.Embedding.Model()) +
			" does not embed images: images are skipped and found by their alt text and captions only"
	}
	return st
}
