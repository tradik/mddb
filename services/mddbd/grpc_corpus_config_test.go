package main

import (
	"testing"
	"time"
)

// A corpus pack's read-only flag and its origin reach gRPC clients
// (AGT-005), so they can tell an installed pack from an ordinary collection.
func TestCollectionConfigToProtoCarriesTheCorpusPack(t *testing.T) {
	installed := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	got := collectionConfigToProto(&CollectionConfig{ReadOnly: true, Corpus: &CorpusInfo{
		Name: "go-stdlib", Version: "1.27", License: "BSD-3-Clause", MddbVersion: "2.18.10",
		DocCount: 412, SHA256: "ab12", InstalledAt: installed, VectorsDisabled: true,
		Embedding: &corpusEmbedding{Provider: "ollama", Model: "nomic-embed-text", Dims: 768},
	}})
	c := got.GetCorpus()
	if !got.GetReadOnly() || c.GetName() != "go-stdlib" || c.GetVersion() != "1.27" ||
		c.GetLicense() != "BSD-3-Clause" || c.GetMddbVersion() != "2.18.10" || c.GetDocCount() != 412 ||
		c.GetSha256() != "ab12" || c.GetInstalledAt() != installed.Unix() || !c.GetVectorsDisabled() ||
		c.GetEmbeddingProvider() != "ollama" || c.GetEmbeddingModel() != "nomic-embed-text" || c.GetEmbeddingDims() != 768 {
		t.Errorf("corpus pack on the wire: read_only=%v corpus=%+v", got.GetReadOnly(), c)
	}

	// A pack without embedding details, and an ordinary collection.
	if c := corpusInfoToProto(&CorpusInfo{Name: "p"}); c.GetEmbeddingModel() != "" || c.GetInstalledAt() != 0 {
		t.Errorf("no embedding, no install time: %+v", c)
	}
	if plain := collectionConfigToProto(&CollectionConfig{}); plain.GetReadOnly() || plain.GetCorpus() != nil {
		t.Error("an ordinary collection shows a corpus pack")
	}
}
