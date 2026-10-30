package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"mddb/internal/boltx"
	"mddb/internal/envconf"
	"mddb/internal/migrate"

	bolt "go.etcd.io/bbolt"
)

// Installing and removing corpus packs (AGT-005), with the server running.
//
// In the per-collection layout the pack's file becomes the collection's file:
// it is renamed into "<database>.d/" and opened (collectionFiles.attach).
// In the single layout there is no file of its own to attach, so the pack's
// keys are copied into the shared file, as -merge-collections copies a
// collection file back. Either way the collection's config is written first,
// read-only, so no write reaches the collection while it is being filled.

// errCorpusConflict is an install refused because the name is taken: 409.
var errCorpusConflict = errors.New("collection already exists")

// corpusInstallResult is the answer of POST /v1/corpus/install.
type corpusInstallResult struct {
	Name            string   `json:"name"`
	Version         string   `json:"version,omitempty"`
	License         string   `json:"license"`
	DocCount        int      `json:"docCount"`
	VectorsDisabled bool     `json:"vectorsDisabled"`
	Warnings        []string `json:"warnings"`
}

// corpusMaxBytes is MDDB_CORPUS_MAX_BYTES: the largest collection.db an
// install unpacks.
func corpusMaxBytes() int64 {
	return envconf.Int64("MDDB_CORPUS_MAX_BYTES", corpusMaxBytesDefault)
}

// installCorpus reads a pack from r and installs it as a read-only collection.
func (s *Server) installCorpus(r io.Reader) (*corpusInstallResult, error) {
	work, root, err := s.newCorpusWork()
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	m, err := readCorpusArchive(r, root, corpusMaxBytes())
	_ = root.Close() // before anything renames or removes in it (Windows)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(work, corpusDataName)
	if err := verifyDatabase(path); err != nil {
		return nil, fmt.Errorf("%w: collection.db is not a usable database: %w", errCorpusInvalid, err)
	}
	if err := checkPackFile(path, m, s.BucketNames.Docs); err != nil {
		return nil, err
	}

	res := &corpusInstallResult{Name: m.Name, Version: m.Version, License: m.License, DocCount: m.DocCount, Warnings: []string{}}
	if why := m.embeddingMismatch(s.embeddingIdentity()); why != "" {
		res.VectorsDisabled = true
		res.Warnings = append(res.Warnings, why+": its vectors are not loaded, and hybrid search on it uses full text only")
		slog.Warn("corpus pack installed with its vectors disabled", "pack", m.Name, "why", why)
	}
	if err := s.attachCorpus(m, path, res.VectorsDisabled); err != nil {
		return nil, err
	}
	s.loadInstalledCorpus(m.Name, res.VectorsDisabled)
	slog.Info("corpus pack installed", "pack", m.Name, "version", m.Version, "documents", m.DocCount, "license", m.License)
	return res, nil
}

// embeddingIdentity is the model and dimensions of the server's embedding
// provider, "" and 0 without one.
func (s *Server) embeddingIdentity() (string, int) {
	if s.Embedding == nil {
		return "", 0
	}
	return s.Embedding.Model(), s.Embedding.Dimensions()
}

// checkPackFile opens the unpacked collection.db and refuses it unless every
// bucket is a collection bucket holding only the pack's own collection's
// keys, and it holds the documents the manifest says. A pack that carried
// keys of another collection would write into that collection on install.
// The file is then brought to this build's format (DOC-017) and given every
// collection bucket.
func checkPackFile(path string, m *corpusManifest, docsBucket []byte) error {
	db, err := openBolt(path, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return fmt.Errorf("%w: collection.db: %w", errCorpusInvalid, err)
	}
	defer func() { _ = db.Close() }()
	if v, verr := migrate.Version(db); verr != nil || v > migrate.Latest() {
		return fmt.Errorf("%w: collection.db has storage format %d; this server knows %d", errCorpusInvalid, v, migrate.Latest())
	}
	if err := db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error { return checkPackBucket(name, b, m.Name) })
	}); err != nil {
		return fmt.Errorf("%w: %w", errCorpusInvalid, err)
	}
	if n := countPrefix(db, docsBucket, []byte("doc|"+m.Name+"|")); n != m.DocCount || n == 0 {
		return fmt.Errorf("%w: collection.db holds %d documents of %q, the manifest says %d", errCorpusInvalid, n, m.Name, m.DocCount)
	}
	return errors.Join(migrate.Run(db), boltx.EnsureBuckets(db, collectionBuckets...))
}

// checkPackBucket refuses a bucket a collection file has no business holding,
// a nested bucket, or a key of another collection.
func checkPackBucket(name []byte, b *bolt.Bucket, collection string) error {
	if bytes.Equal(name, migrate.Bucket) {
		return nil
	}
	if !isCollectionBucket(name) {
		return fmt.Errorf("collection.db holds bucket %q, which is not collection data", name)
	}
	return b.ForEach(func(k, v []byte) error {
		if c, ok := keyCollection(string(name), k); v == nil || !ok || c != collection {
			return fmt.Errorf("collection.db holds key %q of bucket %s, which is not collection %q's", k, name, collection)
		}
		return nil
	})
}

// attachCorpus records the pack in the collection's config, read-only, then
// puts its data in place; a failure takes the config back.
func (s *Server) attachCorpus(m *corpusManifest, path string, vectorsDisabled bool) error {
	if s.CollectionManager == nil {
		return errors.New("collection configs are not available on this server")
	}
	prev, hadPrev := s.CollectionManager.Get(m.Name)
	cfg := &CollectionConfig{Type: "default", Description: m.Description, ReadOnly: true, Corpus: m.info(vectorsDisabled, time.Now())}
	if err := s.CollectionManager.put(m.Name, cfg); err != nil {
		return err
	}
	// Checked with writes already refused, so none can land in between.
	err := s.fillCorpusCollection(m.Name, path)
	if err != nil {
		if hadPrev {
			_ = s.CollectionManager.put(m.Name, prev)
		} else {
			_ = s.CollectionManager.Delete(m.Name)
		}
	}
	return err
}

// fillCorpusCollection puts the pack's file in place: attached as the
// collection's file, or its keys copied into the shared file.
func (s *Server) fillCorpusCollection(name, path string) error {
	if s.collectionHasData(name) {
		return fmt.Errorf("%w: collection %q already holds data: remove the pack installed under that name (mddb-cli corpus remove) or delete the collection first", errCorpusConflict, name)
	}
	if cf := s.collectionStore(); cf != nil {
		return cf.attach(name, path)
	}
	src, err := openBolt(path, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	// The shared file keeps its own format stamp.
	if err := src.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket(migrate.Bucket) }); err != nil {
		return err
	}
	s.restoreMu.RLock()
	defer s.restoreMu.RUnlock()
	_, err = copyAllKeys(src, s.DB)
	return err
}

// collectionHasData reports whether a collection holds anything an install
// would mix with: in the per-collection layout a file, in the single layout
// a document.
func (s *Server) collectionHasData(name string) bool {
	if cf := s.collectionStore(); cf != nil {
		return cf.get(name) != nil
	}
	found := false
	prefix := []byte("doc|" + name + "|")
	_ = s.DBScanC(name, s.BucketNames.Docs, prefix, func(_, _ []byte) bool { found = true; return false })
	return found
}

// loadInstalledCorpus brings the in-memory indexes up to the collection just
// installed: its vectors, unless they are disabled, its geo points, and no
// cached answer from before it existed.
func (s *Server) loadInstalledCorpus(name string, vectorsDisabled bool) {
	if !vectorsDisabled && s.VectorIndex != nil && s.VectorStore != nil {
		if counts, err := s.VectorStore.CountByCollection(); err == nil && counts[name] > 0 {
			s.loadCollectionVectors(name, newVectorLoadState(map[string]int{name: counts[name]}))
		}
	}
	if s.GeoStore != nil && s.GeoIndex != nil && s.GeoHashIndex != nil {
		_, _ = s.GeoStore.Rebuild(s.GeoIndex, name)
		_, _ = s.GeoStore.RebuildHash(s.GeoHashIndex, name)
	}
	s.clearReadCaches()
}

// collectionVectorsDisabled reports whether a collection is a corpus pack
// whose vectors this server cannot search.
func (s *Server) collectionVectorsDisabled(collection string) bool {
	if s.CollectionManager == nil {
		return false
	}
	cfg, found := s.CollectionManager.Get(collection)
	return found && cfg.Corpus != nil && cfg.Corpus.VectorsDisabled
}

// removeCorpus deletes an installed pack: its data, its file and its config.
// It returns the number of documents removed. Only a pack is removed here; a
// collection written by hand is deleted with /v1/delete-collection.
func (s *Server) removeCorpus(name string) (int, error) {
	cfg, found := (*CollectionConfig)(nil), false
	if s.CollectionManager != nil {
		cfg, found = s.CollectionManager.Get(name)
	}
	if !found || cfg.Corpus == nil {
		return 0, fmt.Errorf("no corpus pack named %q is installed: %w", name, errNotFound)
	}
	// The delete writes to the collection, so it is made writable first,
	// and read-only again if the delete fails.
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
	s.clearReadCaches()
	slog.Info("corpus pack removed", "pack", name, "documents", n)
	return n, nil
}

// corpusList is every installed pack, by collection, for GET /v1/corpus.
func (s *Server) corpusList(inTenant func(string) bool) []corpusListEntry {
	out := []corpusListEntry{}
	if s.CollectionManager == nil {
		return out
	}
	for name, cfg := range s.CollectionManager.ListAll() {
		if cfg.Corpus != nil && inTenant(name) {
			out = append(out, corpusListEntry{Collection: name, ReadOnly: cfg.ReadOnly, Corpus: cfg.Corpus})
		}
	}
	return out
}

// corpusListEntry is one installed pack.
type corpusListEntry struct {
	Collection string      `json:"collection"`
	ReadOnly   bool        `json:"readOnly"`
	Corpus     *CorpusInfo `json:"corpus"`
}
