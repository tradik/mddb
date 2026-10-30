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
	// ExcludeMetaKeys are meta keys kept out of full-text search (#367).
	// Keys used only as filters — url, license, a vulnerability flag — then
	// write no meta.<key> postings, and changing them costs no FTS work at
	// all. filterMeta and facets still work on them: they read the metadata
	// index, not FTS.
	//
	// An exclusion list rather than an allowlist: every meta key is
	// searchable by default, as it always was, so the list names only the
	// exceptions, and a meta key a writer adds later is found without a
	// config change. An allowlist would make a new key silently unsearchable.
	//
	// It applies to documents indexed afterwards. A change does not touch
	// postings already written: run an fts-reindex to apply it to them.
	ExcludeMetaKeys []string `json:"excludeMetaKeys,omitempty"`
}

// collectionFTS is a collection's full-text setting; nil when it has none.
func (s *Server) collectionFTS(collection string) *CollectionFTSDef {
	if s.CollectionManager == nil {
		return nil
	}
	if cfg, ok := s.CollectionManager.Get(collection); ok {
		return cfg.FTS
	}
	return nil
}

// ftsEnabledFor reports whether a collection's documents get postings.
func (s *Server) ftsEnabledFor(collection string) bool {
	f := s.collectionFTS(collection)
	return f == nil || f.Enabled == nil || *f.Enabled
}

// ftsExcludedMetaKeys is the set of meta keys a collection keeps out of FTS.
func (s *Server) ftsExcludedMetaKeys(collection string) map[string]bool {
	f := s.collectionFTS(collection)
	if f == nil || len(f.ExcludeMetaKeys) == 0 {
		return nil
	}
	out := make(map[string]bool, len(f.ExcludeMetaKeys))
	for _, k := range f.ExcludeMetaKeys {
		out[k] = true
	}
	return out
}

// ftsReindexResult is what an fts-reindex did, for every transport.
type ftsReindexResult struct {
	Status                     string // ok, partial, or removed (FTS off)
	Reindexed, Skipped, Failed int
}

// reindexCollectionFTS rebuilds a collection's postings from its documents,
// or removes them when the collection has full-text indexing off. HTTP,
// gRPC and MCP had a copy each; two of them reported "ok" whatever failed.
// Every document is rebuilt from scratch, not only the changed ones (#367):
// this is how a new stemmer, stop word list or excludeMetaKeys reaches
// documents that did not change.
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
		if err := s.indexDocumentFTS(collection, d, true); err != nil {
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
