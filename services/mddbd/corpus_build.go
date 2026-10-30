package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"mddb/internal/boltx"
	"mddb/internal/migrate"
	"mddb/internal/vector"

	bolt "go.etcd.io/bbolt"
)

// Building a corpus pack (AGT-005): a consistent snapshot of one collection,
// described by a manifest and streamed as tar.zst.

// corpusBuildRequest is the body of POST /v1/corpus/build.
type corpusBuildRequest struct {
	Collection  string         `json:"collection"`
	Version     string         `json:"version,omitempty"`
	Description string         `json:"description,omitempty"`
	License     string         `json:"license"`
	Sources     []corpusSource `json:"sources,omitempty"`
}

// errCorpusInvalid marks a request or a pack the server refuses as it is:
// HTTP 400, never a server fault.
var errCorpusInvalid = errors.New("invalid corpus pack")

// corpusOpenRoot is os.OpenRoot; a test replaces it to reach its failure.
var corpusOpenRoot = os.OpenRoot

// corpusBatchBytes is how much a key copy holds before it commits.
var corpusBatchBytes = migrateBatchBytes

// corpusBuild is a snapshot ready to stream; close removes it.
type corpusBuild struct {
	manifest *corpusManifest
	work     string
	root     *os.Root
}

// corpusWorkDir is where packs are staged: beside the database, so an
// installed collection file is renamed into place on the same file system
// rather than copied across two.
func (s *Server) corpusWorkDir() string {
	if s.Path == "" {
		return os.TempDir()
	}
	return filepath.Dir(s.Path)
}

// newCorpusWork creates a private staging directory and an os.Root on it.
// The caller closes the root before removing the directory: Windows does not
// remove a directory a handle is open in.
func (s *Server) newCorpusWork() (string, *os.Root, error) {
	work, err := os.MkdirTemp(s.corpusWorkDir(), ".mddb-corpus-*")
	if err != nil {
		return "", nil, err
	}
	root, err := corpusOpenRoot(work) // NOSONAR gosecurity:S2083 -- a directory this function just created
	if err != nil {
		_ = os.RemoveAll(work)
		return "", nil, err
	}
	return work, root, nil
}

// prepareCorpus snapshots the collection and writes its manifest. Nothing
// is streamed yet, so every refusal can still be an HTTP status.
func (s *Server) prepareCorpus(req corpusBuildRequest) (*corpusBuild, error) {
	m := &corpusManifest{Format: corpusFormat, Name: req.Collection, Version: req.Version,
		Description: req.Description, MddbVersion: VERSION, CreatedAt: time.Now().UTC(),
		License: req.License, Sources: req.Sources}
	if err := errors.Join(validPackName(m.Name), m.validateLicenses(), s.packableCollection(m.Name)); err != nil {
		return nil, fmt.Errorf("%w: %w", errCorpusInvalid, err)
	}
	work, root, err := s.newCorpusWork()
	if err != nil {
		return nil, err
	}
	b := &corpusBuild{manifest: m, work: work, root: root}
	if err := s.fillCorpus(b); err != nil {
		b.close()
		return nil, err
	}
	return b, nil
}

// fillCorpus writes the snapshot into the build and describes it.
func (s *Server) fillCorpus(b *corpusBuild) error {
	m := b.manifest
	if err := s.snapshotCollection(m.Name, b.root, corpusDataName); err != nil {
		return err
	}
	if err := s.describeSnapshot(filepath.Join(b.work, corpusDataName), m); err != nil {
		return err
	}
	if m.DocCount == 0 {
		return fmt.Errorf("collection %q: %w: it has no documents", m.Name, errNotFound)
	}
	file, err := hashFile(b.root, corpusDataName)
	m.SHA256, m.Bytes = file.sum, file.size
	return err
}

// packableCollection refuses a collection whose data is not all in bbolt or
// cannot be read elsewhere.
func (s *Server) packableCollection(collection string) error {
	if s.CollectionManager == nil {
		return nil
	}
	cfg, found := s.CollectionManager.Get(collection)
	switch {
	case !found:
		return nil
	case cfg.StorageBackend != "" && cfg.StorageBackend != "boltdb":
		return fmt.Errorf("collection %q keeps its documents in %s, not in its bbolt file", collection, cfg.StorageBackend)
	case cfg.Encrypted:
		return fmt.Errorf("collection %q is encrypted with this server's key; no other server could read the pack", collection)
	}
	return nil
}

// snapshotCollection writes a consistent copy of one collection's data to
// name in dir. In the per-collection layout that is the collection's own
// file, copied as a backup copies it (backupFile); in the single layout it is
// the collection's keys, read from the shared file in one transaction.
func (s *Server) snapshotCollection(collection string, dir *os.Root, name string) error {
	if cf := s.collectionStore(); cf != nil {
		db := cf.get(collection)
		if db == nil {
			return fmt.Errorf("collection %q: %w", collection, errNotFound)
		}
		return backupFile(db, &s.restoreMu, dir, name)
	}
	dst, err := openBolt(filepath.Join(dir.Name(), name), &bolt.Options{Timeout: time.Second})
	if err != nil {
		return err
	}
	err = s.extractCollection(collection, dst)
	if err == nil {
		err = migrate.Run(dst) // stamps the format the keys are in
	}
	return errors.Join(err, dst.Close())
}

// extractCollection copies the collection's keys of every collection bucket
// from the shared file into dst, in batches, from one read transaction.
func (s *Server) extractCollection(collection string, dst *bolt.DB) error {
	if err := boltx.EnsureBuckets(dst, collectionBuckets...); err != nil {
		return err
	}
	s.restoreMu.RLock()
	defer s.restoreMu.RUnlock()
	return s.DB.View(func(tx *bolt.Tx) error {
		for _, name := range collectionBuckets {
			b := tx.Bucket(name)
			if b == nil {
				continue
			}
			batch := &keyBatch{db: dst, bucket: name}
			err := b.ForEach(func(k, v []byte) error {
				if c, ok := keyCollection(string(name), k); !ok || c != collection || v == nil {
					return nil
				}
				return batch.put(k, v)
			})
			if err := errors.Join(err, batch.flush()); err != nil {
				return err
			}
		}
		return nil
	})
}

// keyBatch collects puts to one bucket of db and commits them in
// transactions of about corpusBatchBytes.
type keyBatch struct {
	db     *bolt.DB
	bucket []byte
	items  [][2][]byte
	size   int
}

func (kb *keyBatch) put(k, v []byte) error {
	kb.items = append(kb.items, [2][]byte{bytes.Clone(k), bytes.Clone(v)})
	if kb.size += len(k) + len(v); kb.size >= corpusBatchBytes {
		return kb.flush()
	}
	return nil
}

func (kb *keyBatch) flush() error {
	if len(kb.items) == 0 {
		return nil
	}
	items := kb.items
	kb.items, kb.size = nil, 0
	return kb.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(kb.bucket)
		for _, kv := range items {
			if err := b.Put(kv[0], kv[1]); err != nil {
				return err
			}
		}
		return nil
	})
}

// describeSnapshot fills in what the manifest says about the snapshot at
// path: its documents, its storage format and how its vectors were made.
func (s *Server) describeSnapshot(path string, m *corpusManifest) error {
	snap, err := openBolt(path, &bolt.Options{Timeout: time.Second, ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = snap.Close() }()
	if m.SchemaVersion, err = migrate.Version(snap); err != nil {
		return err
	}
	m.DocCount = countPrefix(snap, s.BucketNames.Docs, []byte("doc|"+m.Name+"|"))
	m.Embedding = s.snapshotEmbedding(snap, m.Name)
	return nil
}

// snapshotEmbedding is how a snapshot's vectors were made, nil when it has
// none: the model its provenance records and the length of a stored vector.
func (s *Server) snapshotEmbedding(snap *bolt.DB, collection string) *corpusEmbedding {
	prefix := "vec|" + collection + "|"
	var first string
	_ = boltx.Scan(snap, []byte("vectors"), []byte(prefix), func(k, _ []byte) bool {
		first = string(k[len(prefix):])
		return false
	})
	if first == "" {
		return nil
	}
	store := vector.NewVectorStore(snap)
	emb := &corpusEmbedding{Dims: len(store.GetVectors(collection, []string{first})[first])}
	if prov, found, err := store.Provenance(collection); err == nil && found {
		emb.Model = prov.Model
	}
	if vc := s.vectorConfig(); vc != nil && vc.Model == emb.Model {
		emb.Provider = vc.Provider
	}
	return emb
}

// countPrefix counts the keys of bucket that start with prefix.
func countPrefix(db *bolt.DB, bucket, prefix []byte) int {
	n := 0
	_ = boltx.Scan(db, bucket, prefix, func(_, _ []byte) bool { n++; return true })
	return n
}

// writeTo streams the pack.
func (b *corpusBuild) writeTo(w io.Writer) error {
	return writeCorpusArchive(w, b.manifest, b.root)
}

// close removes the snapshot, closing the root first (see newCorpusWork).
func (b *corpusBuild) close() {
	_ = b.root.Close()
	_ = os.RemoveAll(b.work)
}
