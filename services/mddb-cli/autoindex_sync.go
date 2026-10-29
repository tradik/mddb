package main

import (
	"context"
	"sort"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// Keeping a collection in step with its directory (AGT-001).

// pruneFacetLimit asks for every distinct path at once: the facet walks the
// metadata index, not the documents, so the size is paths, not bytes.
const pruneFacetLimit = 1_000_000

// prunable lists the paths the collection holds that the directory no longer
// does. Only documents with a meta.path are considered, so documents added
// by other means are never touched.
func prunable(ctx context.Context, client *mddb.Client, collection string, files []autoFile) ([]string, error) {
	agg, err := client.Aggregate(ctx, mddb.AggregateRequest{Collection: collection,
		Facets: []mddb.FacetRequest{{Field: "path", OrderBy: "value"}}, MaxFacetSize: pruneFacetLimit})
	if err != nil {
		return nil, err
	}
	present := make(map[string]bool, len(files))
	for _, f := range files {
		present[f.Rel] = true
	}
	var gone []string
	for _, b := range agg.Facets["path"] {
		if !present[b.Value] {
			gone = append(gone, b.Value)
		}
	}
	sort.Strings(gone)
	return gone, nil
}

// pruneDocs deletes the documents of files that are gone. Autoindex keys a
// document by its path, so the path is the key.
func pruneDocs(ctx context.Context, client *mddb.Client, rep *autoindexReport, lang string) {
	refs := make([]mddb.DocumentRef, 0, len(rep.ToPrune))
	for _, p := range rep.ToPrune {
		refs = append(refs, mddb.DocumentRef{Key: p, Lang: lang})
	}
	resp, err := client.DeleteBatch(ctx, mddb.DeleteBatchRequest{Collection: rep.Collection, Documents: refs})
	if err != nil {
		rep.Errors = append(rep.Errors, "prune: "+err.Error())
		return
	}
	rep.Pruned = resp.Deleted
	rep.Errors = append(rep.Errors, resp.Errors...)
}

// setIncludePaths stores the C/C++ include roots before the files are
// written — they apply at write time — unless the collection already has
// include paths of its own.
func setIncludePaths(ctx context.Context, client *mddb.Client, collection string, dirs []string) error {
	if len(dirs) == 0 {
		return nil
	}
	cur, err := client.GetCollectionConfig(ctx, collection)
	if err != nil {
		return err
	}
	cfg := cur.Config
	if cfg.Code != nil && len(cfg.Code.IncludePaths) > 0 {
		return nil
	}
	// Read, change, write the whole config: a PUT replaces it, and the
	// server keeps stored secrets when the masked ones come back empty.
	return client.SetCollectionConfig(ctx, mddb.SetCollectionConfigRequest{
		Collection: collection, Type: cfg.Type, Description: cfg.Description, Icon: cfg.Icon, Color: cfg.Color,
		CustomMeta: cfg.CustomMeta, StorageBackend: cfg.StorageBackend, StorageConfig: cfg.StorageConfig,
		Quantization: cfg.Quantization, DiskOnlyVectors: cfg.DiskOnlyVectors, MaxRevisions: cfg.MaxRevisions,
		Encrypted: cfg.Encrypted, WordPress: cfg.WordPress, Retrieval: cfg.Retrieval, ResponsePrompt: cfg.ResponsePrompt,
		Code: &mddb.CodeConfig{IncludePaths: dirs}, Embedding: cfg.Embedding, FTS: cfg.FTS,
		TrackAccess: cfg.TrackAccess, TrackHot: cfg.TrackHot, SpellCorrect: cfg.SpellCorrect, SpellLang: cfg.SpellLang,
	})
}
