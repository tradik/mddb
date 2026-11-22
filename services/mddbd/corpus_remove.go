package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
)

// Listing and removing installed corpus packs (AGT-005). A pack is known by
// its name, which every collection it installed records (CorpusInfo.Name): a
// pack of one collection is named after it, a pack of several (FE-022) has a
// name of its own and is removed as a whole.

// removeCorpus deletes an installed pack: the data, files and configs of
// every collection it installed. It returns the number of documents removed.
// Only a pack is removed here; a collection written by hand is deleted with
// /v1/delete-collection.
func (s *Server) removeCorpus(name string) (int, error) {
	collections := s.packCollections(name)
	if len(collections) == 0 {
		return 0, fmt.Errorf("no corpus pack named %q is installed: %w", name, errNotFound)
	}
	total := 0
	for _, c := range collections {
		n, err := s.removeCorpusCollection(c)
		total += n
		if err != nil {
			return total, err
		}
	}
	s.clearReadCaches()
	slog.Info("corpus pack removed", "pack", name, "collections", len(collections), "documents", total)
	return total, nil
}

// packCollections are the collections installed from the pack called name,
// sorted.
func (s *Server) packCollections(name string) []string {
	out := []string{}
	if s.CollectionManager == nil {
		return out
	}
	for collection, cfg := range s.CollectionManager.ListAll() {
		if cfg.Corpus != nil && cfg.Corpus.Name == name {
			out = append(out, collection)
		}
	}
	sort.Strings(out)
	return out
}

// removeCorpusCollection deletes one collection installed from a pack. The
// delete writes to the collection, so it is made writable first, and
// read-only again if the delete fails.
func (s *Server) removeCorpusCollection(name string) (int, error) {
	cfg, _ := s.CollectionManager.Get(name)
	writable := *cfg
	writable.ReadOnly = false
	if err := s.CollectionManager.put(name, &writable); err != nil {
		return 0, err
	}
	n, err := s.deleteCollection(name)
	if err != nil {
		_ = s.CollectionManager.put(name, cfg)
		return 0, err
	}
	return n, nil
}

// corpusPacks is every installed pack in the caller's tenant, by collection:
// GET /v1/corpus, the corpus_list MCP tool and the ListCorpusPacks RPC.
func (s *Server) corpusPacks(ctx context.Context) []corpusListEntry {
	out := []corpusListEntry{}
	if s.CollectionManager == nil {
		return out
	}
	tenant := TenantFromContext(ctx)
	for name, cfg := range s.CollectionManager.ListAll() {
		if cfg.Corpus != nil && CollectionInTenant(tenant, name) {
			out = append(out, corpusListEntry{Collection: name, ReadOnly: cfg.ReadOnly, Corpus: cfg.Corpus})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Collection < out[j].Collection })
	return out
}

// corpusListEntry is one collection installed from a pack.
type corpusListEntry struct {
	Collection string      `json:"collection"`
	ReadOnly   bool        `json:"readOnly"`
	Corpus     *CorpusInfo `json:"corpus"`
}
