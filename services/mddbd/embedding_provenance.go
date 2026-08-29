package main

import (
	"fmt"
	"log/slog"
	"mddb/internal/embedding"
	vec "mddb/internal/vector"
	"strings"
)

// noteEmbeddingProvenance records how a collection is being embedded at the
// moment it is written.
//
// Failing to record it must not fail the write: the vector itself is correct
// either way, and the only thing lost is the ability to report the collection
// as needing a reindex later. So this logs and returns.
func noteEmbeddingProvenance(store *vec.VectorStore, collection string, provider embedding.Provider) {
	if store == nil || provider == nil {
		return
	}
	if err := store.NoteProvenance(collection, provider.Model(), embedding.DocumentVariantOf(provider)); err != nil {
		slog.Warn("could not record embedding provenance", "collection", collection, "err", err)
	}
}

// reportStaleEmbeddings names the collections whose vectors were produced
// differently from how the configured provider would produce them now.
//
// The vectors are not wrong in themselves; they are simply no longer in the
// same space as a query embedded today, which shows up as quietly worse
// results rather than as an error. Naming the collections is the whole point:
// a reindex is the operator's decision, not something to do to their data
// behind their back at startup.
func reportStaleEmbeddings(store *vec.VectorStore, provider embedding.Provider) string {
	if store == nil || provider == nil {
		return ""
	}
	stale, err := store.StaleCollections(provider.Model(), embedding.DocumentVariantOf(provider))
	if err != nil {
		slog.Warn("could not check embedding provenance", "err", err)
		return ""
	}
	if len(stale) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"%d collection(s) were embedded differently than %s produces now and should be reindexed for search quality: %s",
		len(stale), provider.Model(), strings.Join(stale, ", "),
	)
}
