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
// does, with how many documents carry each. Only documents with a meta.path
// are considered, so documents added by other means are never touched.
func prunable(ctx context.Context, client *mddb.Client, collection string, files []autoFile) ([]string, map[string]int, error) {
	agg, err := client.Aggregate(ctx, mddb.AggregateRequest{Collection: collection,
		Facets: []mddb.FacetRequest{{Field: "path", OrderBy: "value"}}, MaxFacetSize: pruneFacetLimit})
	if err != nil {
		return nil, nil, err
	}
	present := make(map[string]bool, len(files))
	for _, f := range files {
		present[f.Rel] = true
	}
	var gone []string
	counts := make(map[string]int)
	for _, b := range agg.Facets["path"] {
		if !present[b.Value] {
			gone = append(gone, b.Value)
			counts[b.Value] = b.Count
		}
	}
	sort.Strings(gone)
	return gone, counts, nil
}

// pruneDocs deletes the documents of files that are gone. Autoindex keys a
// document by its path, so the path is the key — except that a mail file
// also leaves its messages and attachments (KEY/message-N,
// KEY/attachments/NAME) under the same meta.path. A path carried by more
// than one document is looked up, so those go with the file.
func pruneDocs(ctx context.Context, client *mddb.Client, rep *autoindexReport, lang string) {
	refs := make([]mddb.DocumentRef, 0, len(rep.ToPrune))
	seen := make(map[mddb.DocumentRef]bool)
	add := func(r mddb.DocumentRef) {
		if !seen[r] {
			seen[r] = true
			refs = append(refs, r)
		}
	}
	for _, p := range rep.ToPrune {
		add(mddb.DocumentRef{Key: p, Lang: lang})
		if rep.pruneCounts[p] < 2 {
			continue
		}
		docs, err := client.Search(ctx, mddb.SearchRequest{Collection: rep.Collection,
			FilterMeta: map[string][]string{"path": {p}}, Limit: rep.pruneCounts[p]})
		if err != nil {
			rep.Errors = append(rep.Errors, "prune "+p+": "+err.Error())
			continue
		}
		for _, d := range docs {
			add(mddb.DocumentRef{Key: d.Key, Lang: d.Lang})
		}
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
	return updateCollectionConfig(ctx, client, collection, func(req *mddb.SetCollectionConfigRequest) bool {
		if req.Code != nil && len(req.Code.IncludePaths) > 0 {
			return false
		}
		if req.Code == nil {
			req.Code = &mddb.CodeConfig{}
		}
		req.Code.IncludePaths = dirs
		return true
	})
}

// updateCollectionConfig reads a collection's config, lets change edit it,
// and writes the whole of it back when change reports a change. A PUT
// replaces the config, so every field is carried over; the server keeps
// stored secrets when the masked ones come back empty.
func updateCollectionConfig(ctx context.Context, client *mddb.Client, collection string, change func(*mddb.SetCollectionConfigRequest) bool) error {
	cur, err := client.GetCollectionConfig(ctx, collection)
	if err != nil {
		return err
	}
	cfg := cur.Config
	req := mddb.SetCollectionConfigRequest{
		Collection: collection, Type: cfg.Type, Description: cfg.Description, Icon: cfg.Icon, Color: cfg.Color,
		CustomMeta: cfg.CustomMeta, StorageBackend: cfg.StorageBackend, StorageConfig: cfg.StorageConfig,
		Quantization: cfg.Quantization, DiskOnlyVectors: cfg.DiskOnlyVectors, MaxRevisions: cfg.MaxRevisions,
		Encrypted: cfg.Encrypted, WordPress: cfg.WordPress, Retrieval: cfg.Retrieval, ResponsePrompt: cfg.ResponsePrompt,
		Code: cfg.Code, Embedding: cfg.Embedding, FTS: cfg.FTS,
		TrackAccess: cfg.TrackAccess, TrackHot: cfg.TrackHot, SpellCorrect: cfg.SpellCorrect, SpellLang: cfg.SpellLang,
	}
	if !change(&req) {
		return nil
	}
	return client.SetCollectionConfig(ctx, req)
}
