package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"mddb/internal/migrate"
)

// Corpus packs (AGT-005): a collection built once — documents, full-text
// index, vectors, geo points — shipped as one file and installed read-only
// on any server without re-embedding or reindexing. See docs/CORPUS-PACKS.md.
//
// A pack is a zstd-compressed tar holding two files, in this order:
//
//	manifest.json  what the pack is, where it came from, under which licence
//	collection.db  the collection's bbolt file, as the per-collection layout
//	               keeps it: every collection bucket, only this collection's keys

// corpusFormat is the version of the pack layout this build reads and writes.
const corpusFormat = 1

// The two members of a pack, by name.
const (
	corpusManifestName = "manifest.json"
	corpusDataName     = "collection.db"
)

// corpusManifest is manifest.json.
type corpusManifest struct {
	Format int `json:"format"`
	// Name is the collection the pack installs as. Keys inside the file
	// carry it, so it cannot be renamed on install.
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	// MddbVersion is the release that built the pack; SchemaVersion the
	// on-disk format of collection.db (DOC-017). A server installs a pack
	// whose format it knows or that it can migrate, never a newer one.
	MddbVersion   string    `json:"mddbVersion"`
	SchemaVersion uint64    `json:"schemaVersion"`
	CreatedAt     time.Time `json:"createdAt"`
	// License covers the pack as a whole; each source names its own. Both
	// are mandatory: a pack is only for what may be redistributed.
	License   string           `json:"license"`
	Sources   []corpusSource   `json:"sources,omitempty"`
	Embedding *corpusEmbedding `json:"embedding"` // null: full-text only
	DocCount  int              `json:"docCount"`
	SHA256    string           `json:"sha256"` // of collection.db
	Bytes     int64            `json:"bytes"`  // of collection.db
}

// corpusSource is one origin of a pack's documents.
type corpusSource struct {
	Repo    string `json:"repo"`
	Commit  string `json:"commit,omitempty"`
	License string `json:"license"`
}

// corpusEmbedding says how the pack's vectors were made. A server whose
// provider differs installs the pack with its vectors disabled.
type corpusEmbedding struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model"`
	Dims     int    `json:"dims"`
}

// CorpusInfo is what a collection's config records about the pack it was
// installed from (CollectionConfig.Corpus).
type CorpusInfo struct {
	Name        string    `json:"name"`
	Version     string    `json:"version,omitempty"`
	License     string    `json:"license"`
	MddbVersion string    `json:"mddbVersion"`
	DocCount    int       `json:"docCount"`
	SHA256      string    `json:"sha256"`
	InstalledAt time.Time `json:"installedAt"`
	// VectorsDisabled is set when the pack's vectors were made by another
	// model than the server's: they are not loaded, vector search finds
	// nothing in the collection, and hybrid search answers from the
	// full-text half.
	VectorsDisabled bool             `json:"vectorsDisabled,omitempty"`
	Embedding       *corpusEmbedding `json:"embedding,omitempty"`
}

// packNamePattern is a pack name: a collection name that is also a safe,
// portable file name.
var packNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// validPackName refuses a name that cannot be a pack's collection.
func validPackName(name string) error {
	if !packNamePattern.MatchString(name) {
		return fmt.Errorf("invalid pack name %q: 1 to 128 letters, digits, '.', '-' or '_', starting with a letter or digit", name)
	}
	return nil
}

// validateLicenses refuses a pack without a licence, or with a source
// without one.
func (m *corpusManifest) validateLicenses() error {
	if strings.TrimSpace(m.License) == "" {
		return errors.New("a corpus pack needs a licence: pack only what may be redistributed, and say under which terms (--license)")
	}
	for i, src := range m.Sources {
		if strings.TrimSpace(src.Repo) == "" {
			return fmt.Errorf("source %d has no repo", i+1)
		}
		if strings.TrimSpace(src.License) == "" {
			return fmt.Errorf("source %q has no licence", src.Repo)
		}
	}
	return nil
}

// validate checks a manifest read from a pack before anything is installed.
func (m *corpusManifest) validate() error {
	if m.Format != corpusFormat {
		return fmt.Errorf("unsupported pack format %d: this server reads format %d", m.Format, corpusFormat)
	}
	if err := validPackName(m.Name); err != nil {
		return err
	}
	if err := m.validateLicenses(); err != nil {
		return err
	}
	if m.SchemaVersion > migrate.Latest() {
		return fmt.Errorf("the pack was built by mddb %s with storage format %d; this server knows format %d — upgrade it first",
			m.MddbVersion, m.SchemaVersion, migrate.Latest())
	}
	if len(m.SHA256) != 64 {
		return errors.New("the manifest has no sha256 of collection.db")
	}
	return nil
}

// info is the part of a manifest a collection config keeps.
func (m *corpusManifest) info(vectorsDisabled bool, now time.Time) *CorpusInfo {
	return &CorpusInfo{
		Name: m.Name, Version: m.Version, License: m.License, MddbVersion: m.MddbVersion,
		DocCount: m.DocCount, SHA256: m.SHA256, InstalledAt: now.UTC(),
		VectorsDisabled: vectorsDisabled, Embedding: m.Embedding,
	}
}

// embeddingMismatch says why the pack's vectors cannot be searched with the
// server's provider, or "" when they can (or the pack has none). model and
// dims describe the server's provider; model "" means it has none.
func (m *corpusManifest) embeddingMismatch(model string, dims int) string {
	e := m.Embedding
	switch {
	case e == nil:
		return ""
	case model == "":
		return fmt.Sprintf("the pack's vectors were made with %s (%d dimensions) and this server has no embedding provider", e.Model, e.Dims)
	case e.Dims != dims:
		return fmt.Sprintf("the pack's vectors have %d dimensions (%s) and this server's provider makes %d (%s)", e.Dims, e.Model, dims, model)
	case e.Model != "" && e.Model != model:
		return fmt.Sprintf("the pack's vectors were made with %s and this server embeds queries with %s", e.Model, model)
	}
	return ""
}
