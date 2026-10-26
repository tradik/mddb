package main

import (
	"log/slog"

	"mddb/internal/storage"
)

// Full-text indexing per collection (#335).
//
// Postings are most of a database's size: a code collection of 3 GB of
// content made a 101 GB file. A collection used only for metadata lookups
// or vectors pays for them anyway, whoever writes to it — skipFts exists per
// ingest batch, not per collection. `fts: {enabled: false}` in its config
// stops them being written, and an fts-reindex of such a collection removes
// the postings it already has.

// CollectionFTSDef is a collection's full-text setting.
type CollectionFTSDef struct {
	// Enabled is nil when not set, which means on.
	Enabled *bool `json:"enabled,omitempty"`
}

// ftsEnabledFor reports whether a collection's documents get postings.
func (s *Server) ftsEnabledFor(collection string) bool {
	if s.CollectionManager == nil {
		return true
	}
	cfg, ok := s.CollectionManager.Get(collection)
	return !ok || cfg.FTS == nil || cfg.FTS.Enabled == nil || *cfg.FTS.Enabled
}

// ftsReindexResult is what an fts-reindex did, for every transport.
type ftsReindexResult struct {
	Status                     string // ok, partial, or removed (FTS off)
	Reindexed, Skipped, Failed int
}

// reindexCollectionFTS rebuilds a collection's postings from its documents,
// or removes them when the collection has full-text indexing off. HTTP,
// gRPC and MCP had a copy each; two of them reported "ok" whatever failed.
func (s *Server) reindexCollectionFTS(collection string) (ftsReindexResult, error) {
	var docs []*storage.Doc
	var all []string
	res := ftsReindexResult{Status: "ok"}
	now := currentUnix()
	// Collected first, indexed outside the read transactions: indexing writes.
	if err := s.DBScanC(collection, s.BucketNames.Docs, []byte("doc|"+collection+"|"), func(_, v []byte) bool {
		doc, err := loadDoc(v)
		if err != nil {
			res.Skipped++
			return true
		}
		all = append(all, doc.ID)
		if doc.ContentMD == "" || (doc.ExpiresAt > 0 && doc.ExpiresAt < now) {
			res.Skipped++
			return true
		}
		docs = append(docs, doc)
		return true
	}); err != nil {
		return res, err
	}

	if !s.ftsEnabledFor(collection) {
		res.Status, res.Skipped = "removed", len(all)
		return res, s.FTSIndex.RemoveDocs(collection, all)
	}
	for _, d := range docs {
		if err := s.indexDocumentFTS(collection, d); err != nil {
			res.Failed++
			slog.Warn("FTS reindex failed", "collection", collection, "docID", d.ID, "err", err)
			continue
		}
		res.Reindexed++
	}
	if res.Failed > 0 {
		res.Status = "partial"
	}
	return res, nil
}
