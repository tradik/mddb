package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
// A pack of several collections (FE-022) installs each the same way, after
// every one of them has been checked and found free; a failure part-way
// removes the ones already installed.

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
	// Collections are the collections the pack installed (FE-022).
	Collections []string `json:"collections"`
}

// corpusMaxBytes is MDDB_CORPUS_MAX_BYTES: the largest collection.db an
// install unpacks.
func corpusMaxBytes() int64 {
	return envconf.Int64("MDDB_CORPUS_MAX_BYTES", corpusMaxBytesDefault)
}

// installCorpus reads a pack from r and installs its collections read-only.
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
	parts := m.parts()
	paths := make([]string, len(parts))
	for i, p := range parts {
		paths[i] = filepath.Join(work, corpusMemberName(m.Format, i))
		if err := verifyDatabase(paths[i]); err != nil {
			return nil, fmt.Errorf("%w: %s is not a usable database: %w", errCorpusInvalid, corpusMemberName(m.Format, i), err)
		}
		if err := checkPackFile(paths[i], p, s.BucketNames.Docs); err != nil {
			return nil, err
		}
	}
	if conflicts := s.corpusConflicts(m); len(conflicts) > 0 {
		return nil, fmt.Errorf("%w: %s", errCorpusConflict, strings.Join(conflicts, "; "))
	}

	res := &corpusInstallResult{Name: m.Name, Version: m.Version, License: m.License, DocCount: m.DocCount, Warnings: []string{}, Collections: []string{}}
	disabled := make([]bool, len(parts))
	for i, p := range parts {
		if why := p.Embedding.mismatch(s.embeddingIdentity()); why != "" {
			disabled[i], res.VectorsDisabled = true, true
			res.Warnings = append(res.Warnings, corpusWarning(m, p, why))
			slog.Warn("corpus pack installed with its vectors disabled", "pack", m.Name, "collection", p.Name, "why", why)
		}
		if err := s.attachCorpus(m, p, paths[i], disabled[i]); err != nil {
			s.undoCorpusInstall(res.Collections)
			return nil, err
		}
		res.Collections = append(res.Collections, p.Name)
	}
	for i, p := range parts {
		s.loadInstalledCorpus(p.Name, disabled[i])
	}
	slog.Info("corpus pack installed", "pack", m.Name, "version", m.Version, "collections", len(parts), "documents", m.DocCount, "license", m.License)
	return res, nil
}

// corpusWarning words why a collection's vectors are not loaded; in a pack
// of several collections it names the collection.
func corpusWarning(m *corpusManifest, p corpusPart, why string) string {
	if m.Format == corpusFormatMulti {
		why = "collection " + p.Name + ": " + why
	}
	return why + ": its vectors are not loaded, and hybrid search on it uses full text only"
}

// corpusConflicts is why the pack cannot be installed beside what the
// server holds: a pack of that name already installed, or a collection of
// the pack that already holds data. Empty when it can.
func (s *Server) corpusConflicts(m *corpusManifest) []string {
	if len(s.packCollections(m.Name)) > 0 {
		return []string{fmt.Sprintf("a pack named %q is already installed: remove it first (mddb-cli corpus remove)", m.Name)}
	}
	out := []string{}
	for _, p := range m.parts() {
		if s.collectionHasData(p.Name) {
			out = append(out, fmt.Sprintf("collection %q already holds data: remove the pack installed under that name (mddb-cli corpus remove) or delete the collection first", p.Name))
		}
	}
	return out
}

// undoCorpusInstall removes the collections of a pack installed before a
// later one failed.
func (s *Server) undoCorpusInstall(collections []string) {
	for _, name := range collections {
		if _, err := s.removeCorpusCollection(name); err != nil {
			slog.Error("removing a partly installed corpus pack failed", "collection", name, "err", err)
		}
	}
}

// embeddingIdentity is the model and dimensions of the server's embedding
// provider, "" and 0 without one.
func (s *Server) embeddingIdentity() (string, int) {
	if s.Embedding == nil {
		return "", 0
	}
	return s.Embedding.Model(), s.Embedding.Dimensions()
}

// checkPackFile opens an unpacked collection file and refuses it unless
// every bucket is a collection bucket holding only the keys of p's
// collection, and it holds the documents the manifest says. A pack that carried
// keys of another collection would write into that collection on install.
// The file is then brought to this build's format (DOC-017) and given every
// collection bucket.
func checkPackFile(path string, p corpusPart, docsBucket []byte) error {
	db, err := openBolt(path, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return fmt.Errorf("%w: the file of %q: %w", errCorpusInvalid, p.Name, err)
	}
	defer func() { _ = db.Close() }()
	if v, verr := migrate.Version(db); verr != nil || v > migrate.Latest() {
		return fmt.Errorf("%w: the file of %q has storage format %d; this server knows %d", errCorpusInvalid, p.Name, v, migrate.Latest())
	}
	if err := db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error { return checkPackBucket(name, b, p.Name) })
	}); err != nil {
		return fmt.Errorf("%w: %w", errCorpusInvalid, err)
	}
	if n := countPrefix(db, docsBucket, []byte("doc|"+p.Name+"|")); n != p.DocCount || n == 0 {
		return fmt.Errorf("%w: the file of %q holds %d documents of it, the manifest says %d", errCorpusInvalid, p.Name, n, p.DocCount)
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
		return fmt.Errorf("the file of %q holds bucket %q, which is not collection data", collection, name)
	}
	return b.ForEach(func(k, v []byte) error {
		if c, ok := keyCollection(string(name), k); v == nil || !ok || c != collection {
			return fmt.Errorf("the file of %q holds key %q of bucket %s, which is not that collection's", collection, k, name)
		}
		return nil
	})
}

// attachCorpus records the pack in the config of p's collection, read-only,
// then puts its file at path in place; a failure takes the config back.
func (s *Server) attachCorpus(m *corpusManifest, p corpusPart, path string, vectorsDisabled bool) error {
	if s.CollectionManager == nil {
		return errors.New("collection configs are not available on this server")
	}
	prev, hadPrev := s.CollectionManager.Get(p.Name)
	cfg := &CollectionConfig{Type: "default", Description: m.Description, ReadOnly: true, Corpus: m.info(p, vectorsDisabled, time.Now())}
	if err := s.CollectionManager.put(p.Name, cfg); err != nil {
		return err
	}
	// Checked with writes already refused, so none can land in between.
	err := s.fillCorpusCollection(p.Name, path)
	if err != nil {
		if hadPrev {
			_ = s.CollectionManager.put(p.Name, prev)
		} else {
			_ = s.CollectionManager.Delete(p.Name)
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
			if vectors, loaded := s.loadCollectionVectors(name, newVectorLoadState(map[string]int{name: counts[name]})); loaded {
				s.trainCollection(name, vectors)
			}
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
