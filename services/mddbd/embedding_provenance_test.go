package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"mddb/internal/embedding"
	vec "mddb/internal/vector"

	bolt "go.etcd.io/bbolt"
)

// provProvider is an embedding provider whose model and document variant the
// test controls. It implements embedding.DocumentVariant only when variant is
// set, mirroring the real providers: OpenAI has no variant to declare.
type provProvider struct {
	model   string
	variant string
}

func (p *provProvider) Embed(context.Context, string, embedding.Role) ([]float32, error) {
	return []float32{1, 0, 0}, nil
}

func (p *provProvider) EmbedBatch(_ context.Context, texts []string, _ embedding.Role) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}

func (p *provProvider) Model() string           { return p.model }
func (p *provProvider) Dimensions() int         { return 3 }
func (p *provProvider) DocumentVariant() string { return p.variant }

func provTestStore(t *testing.T) *vec.VectorStore {
	t.Helper()
	db, err := bolt.Open(filepath.Join(t.TempDir(), "prov.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := vec.NewVectorStore(db)
	if err := store.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestNoteEmbeddingProvenanceRecordsTheProvidersVariant(t *testing.T) {
	store := provTestStore(t)
	noteEmbeddingProvenance(store, "docs", &provProvider{model: "nomic-embed-text", variant: "search_document: "})

	got, found, err := store.Provenance("docs")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v, want the provenance to have been recorded", found, err)
	}
	if got.Model != "nomic-embed-text" || got.Variant != "search_document: " {
		t.Errorf("recorded %+v, want the provider's own model and variant", got)
	}
}

// Vector search is optional, so both arguments can legitimately be absent.
func TestNoteEmbeddingProvenanceToleratesNothingConfigured(t *testing.T) {
	noteEmbeddingProvenance(nil, "docs", &provProvider{model: "m"})
	noteEmbeddingProvenance(provTestStore(t), "docs", nil)
}

func TestReportStaleEmbeddingsNamesTheCollectionsToReindex(t *testing.T) {
	store := provTestStore(t)
	if err := store.Put("legacy", "d1", []float32{1, 0, 0}, "m", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("fresh", "d1", []float32{1, 0, 0}, "m", "hash"); err != nil {
		t.Fatal(err)
	}
	noteEmbeddingProvenance(store, "fresh", &provProvider{model: "m", variant: "search_document: "})

	msg := reportStaleEmbeddings(store, &provProvider{model: "m", variant: "search_document: "})
	if !strings.Contains(msg, "legacy") {
		t.Errorf("report %q does not name the collection that needs reindexing", msg)
	}
	if strings.Contains(msg, "fresh") {
		t.Errorf("report %q names a collection whose embedding already matches", msg)
	}
}

func TestReportStaleEmbeddingsIsSilentWhenEverythingMatches(t *testing.T) {
	store := provTestStore(t)
	if err := store.Put("docs", "d1", []float32{1, 0, 0}, "m", "hash"); err != nil {
		t.Fatal(err)
	}
	provider := &provProvider{model: "m", variant: "search_document: "}
	noteEmbeddingProvenance(store, "docs", provider)

	if msg := reportStaleEmbeddings(store, provider); msg != "" {
		t.Errorf("got %q, want silence: nothing needs reindexing", msg)
	}
}

// A provider that declares no variant embeds documents the way MDDB always
// has, so collections written before provenance existed are not stale.
func TestReportStaleEmbeddingsIsSilentForAProviderThatNeverChanged(t *testing.T) {
	store := provTestStore(t)
	if err := store.Put("legacy", "d1", []float32{1, 0, 0}, "m", "hash"); err != nil {
		t.Fatal(err)
	}
	if msg := reportStaleEmbeddings(store, &provProvider{model: "m"}); msg != "" {
		t.Errorf("got %q, want silence: this provider's documents are unchanged", msg)
	}
}

func TestReportStaleEmbeddingsToleratesNothingConfigured(t *testing.T) {
	if msg := reportStaleEmbeddings(nil, &provProvider{model: "m"}); msg != "" {
		t.Errorf("got %q, want an empty report", msg)
	}
	if msg := reportStaleEmbeddings(provTestStore(t), nil); msg != "" {
		t.Errorf("got %q, want an empty report", msg)
	}
}

// closedStore returns a store whose database is shut, so every access fails.
func closedStore(t *testing.T) *vec.VectorStore {
	t.Helper()
	db, err := bolt.Open(filepath.Join(t.TempDir(), "closed.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := vec.NewVectorStore(db)
	if err := store.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return store
}

// A provenance write that fails must not fail the document write with it: the
// vector is correct either way.
func TestNoteEmbeddingProvenanceSurvivesAStorageFailure(t *testing.T) {
	noteEmbeddingProvenance(closedStore(t), "docs", &provProvider{model: "m", variant: "v"})
}

func TestReportStaleEmbeddingsIsSilentWhenItCannotCheck(t *testing.T) {
	if msg := reportStaleEmbeddings(closedStore(t), &provProvider{model: "m", variant: "v"}); msg != "" {
		t.Errorf("got %q, want silence: the check itself failed and proved nothing", msg)
	}
}
