package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"mddb/internal/boltx"
	"mddb/internal/envconf"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

// One storage file per collection (#336).
//
// Everything lived in one bbolt file. A file of 101 GB opened slowly, never
// gave back the space of a deleted collection, and was backed up and checked
// as one piece. With MDDB_STORAGE_LAYOUT=per-collection each collection's
// keys — documents, metadata index, revisions, full-text postings, vectors,
// geo points — live in a file of their own under "<database>.d/", and the
// main file keeps what belongs to the server: configuration, users, keys,
// automations, webhooks, audit.
//
// The keys and buckets are the same in both layouts; only the file differs.
// Every access to collection data asks collectionDB or collectionDBForWrite
// which file holds it. A collection without a file reads from the main
// file, where its buckets exist and are empty for it, so a read of a
// collection never written is the same empty answer as before. The first
// write creates the file. Deleting a collection closes and removes its
// file, so the space comes back at once.
//
// Mixing is not allowed: in this layout the main file must hold no
// collection keys. `mddbd -split-collections` moves them out, and
// `-merge-collections` moves them back.

// collectionBuckets hold one collection's data per key; they move with the
// collection. Everything else stays in the main file, including the small
// per-collection settings kept with the server's configuration: synonyms,
// stop words, spelling dictionaries, schemas, collection configs.
var collectionBuckets = [][]byte{
	[]byte("docs"), []byte("idxmeta"), []byte("rev"), []byte("bykey"),
	[]byte("fts"), []byte("ftsrev"), []byte("ftsf"), []byte("ftsfmeta"),
	[]byte("ftsfstat"), []byte("ftsfrev"), []byte("ftsp"),
	[]byte("vectors"), []byte("vector_provenance"),
	[]byte("geo"), []byte("ttl"), []byte("ttlrev"),
	[]byte("temporal"), []byte("temporal_hot"),
	bucketImages, // RAG-010: image bytes, img|collection|docID
}

// keyCollection returns the collection a key of a collection bucket belongs
// to. Most keys are "<tag>|<collection>|..." or, for per-collection totals
// such as "ftsstat|<collection>", "<tag>|<collection>"; the exceptions are
// ttlrev ("<collection>|<docID>") and vector_provenance (the bare name).
func keyCollection(bucket string, key []byte) (string, bool) {
	switch bucket {
	case "ttlrev":
		c, _, ok := strings.Cut(string(key), "|")
		return c, ok && c != ""
	case "vector_provenance":
		return string(key), len(key) > 0
	}
	if c, ok := collectionOf(key); ok {
		return c, true
	}
	_, rest, ok := strings.Cut(string(key), "|")
	return rest, ok && rest != ""
}

// The file operations of opening and restoring collection files, variables
// only so a test can make each of them fail.
var (
	collReadDir  = os.ReadDir
	collRename   = os.Rename
	collMkdirAll = os.MkdirAll
	collCopyFile = copyFile
)

func isCollectionBucket(name []byte) bool {
	for _, b := range collectionBuckets {
		if string(b) == string(name) {
			return true
		}
	}
	return false
}

// perCollectionLayout reports whether MDDB_STORAGE_LAYOUT asks for one file
// per collection.
func perCollectionLayout() bool {
	return envconf.String("MDDB_STORAGE_LAYOUT", "single") == "per-collection"
}

// collectionFiles holds the open per-collection files.
type collectionFiles struct {
	dir  string
	opts func(path string) *bolt.Options
	mu   sync.RWMutex
	dbs  map[string]*bolt.DB
}

// collectionDir is where the per-collection files of a database live.
func collectionDir(mainPath string) string { return mainPath + ".d" }

// collectionFileName is a collection's file name: the name with anything
// but lower-case letters, digits, '-' and '_' percent-encoded, so it maps
// back to the collection and is a valid, distinct file name everywhere.
// Upper case is encoded because macOS and Windows file systems fold case —
// "Blog" and "blog" would share a file — and a name Windows reserves (CON,
// NUL, COM1...) has its first character encoded.
func collectionFileName(collection string) string {
	var b strings.Builder
	for i, r := range collection {
		switch {
		case i == 0 && windowsReservedName(collection):
			fmt.Fprintf(&b, "%%%02X", r)
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			for _, c := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
	}
	return b.String() + ".db"
}

func windowsReservedName(name string) bool {
	switch strings.ToLower(name) {
	case "con", "prn", "aux", "nul",
		"com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
		"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9":
		return true
	}
	return false
}

// collectionFromFileName reverses collectionFileName.
func collectionFromFileName(name string) (string, bool) {
	base, ok := strings.CutSuffix(name, ".db")
	if !ok || base == "" {
		return "", false
	}
	c, err := url.PathUnescape(base)
	return c, err == nil && collectionFileName(c) == name
}

// openCollectionFiles opens every collection file in dir, in parallel.
func openCollectionFiles(dir string, opts func(string) *bolt.Options) (*collectionFiles, error) {
	cf := &collectionFiles{dir: dir, opts: opts, dbs: map[string]*bolt.DB{}}
	if err := os.MkdirAll(dir, 0o700); err != nil { // #nosec G703 -- derived from the operator's database path
		return nil, err
	}
	entries, err := collReadDir(dir)
	if err != nil {
		return nil, err
	}
	type opened struct {
		collection string
		db         *bolt.DB
		err        error
	}
	results := make(chan opened)
	n := 0
	for _, e := range entries {
		c, ok := collectionFromFileName(e.Name())
		if !ok || e.IsDir() {
			continue
		}
		n++
		go func(c, path string) {
			db, err := openCollectionFile(path, opts(path))
			results <- opened{c, db, err}
		}(c, filepath.Join(dir, e.Name()))
	}
	var errs []error
	for i := 0; i < n; i++ {
		r := <-results
		if r.err != nil {
			errs = append(errs, fmt.Errorf("collection %q: %w", r.collection, r.err))
			continue
		}
		cf.dbs[r.collection] = r.db
	}
	if len(errs) > 0 {
		cf.closeAll()
		return nil, errors.Join(errs...)
	}
	slog.Info("per-collection storage opened", "dir", dir, "collections", len(cf.dbs))
	return cf, nil
}

// get returns a collection's file, or nil when it has none.
func (cf *collectionFiles) get(collection string) *bolt.DB {
	cf.mu.RLock()
	defer cf.mu.RUnlock()
	return cf.dbs[collection]
}

// getOrCreate returns a collection's file, creating it with every
// collection bucket on first use.
func (cf *collectionFiles) getOrCreate(collection string) (*bolt.DB, error) {
	if db := cf.get(collection); db != nil {
		return db, nil
	}
	if collection == "" {
		return nil, errors.New("a collection file needs a collection name")
	}
	cf.mu.Lock()
	defer cf.mu.Unlock()
	if db := cf.dbs[collection]; db != nil {
		return db, nil
	}
	path := filepath.Join(cf.dir, collectionFileName(collection))
	db, err := openBolt(path, cf.opts(path))
	if err != nil {
		return nil, err
	}
	if err := boltx.EnsureBuckets(db, collectionBuckets...); err != nil {
		_ = db.Close()
		return nil, err
	}
	cf.dbs[collection] = db
	return db, nil
}

// attach makes the bbolt file at src a collection's file and opens it,
// without a restart (AGT-005: installing a corpus pack). src is renamed into
// place, so it must be on the file system of the collection directory. The
// collection must not have a file yet; detaching is remove. The caller has
// checked the file and given it every collection bucket.
func (cf *collectionFiles) attach(collection, src string) error {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	path := filepath.Join(cf.dir, collectionFileName(collection))
	if _, err := os.Lstat(path); cf.dbs[collection] != nil || err == nil {
		return fmt.Errorf("collection %q already has a file", collection)
	}
	if err := collRename(src, path); err != nil {
		return err
	}
	db, err := openCollectionFile(path, cf.opts(path))
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	cf.dbs[collection] = db
	return nil
}

// remove closes a collection's file and deletes it.
func (cf *collectionFiles) remove(collection string) error {
	cf.mu.Lock()
	db := cf.dbs[collection]
	delete(cf.dbs, collection)
	cf.mu.Unlock()
	if db == nil {
		return nil
	}
	path := db.Path()
	if err := db.Close(); err != nil {
		return err
	}
	return os.Remove(path)
}

// all returns the open files by collection, for operations that cover every
// collection.
func (cf *collectionFiles) all() map[string]*bolt.DB {
	cf.mu.RLock()
	defer cf.mu.RUnlock()
	out := make(map[string]*bolt.DB, len(cf.dbs))
	for c, db := range cf.dbs {
		out[c] = db
	}
	return out
}

// names returns the collections with a file, sorted.
func (cf *collectionFiles) names() []string {
	cf.mu.RLock()
	defer cf.mu.RUnlock()
	out := make([]string, 0, len(cf.dbs))
	for c := range cf.dbs {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// closeAll closes every file.
func (cf *collectionFiles) closeAll() {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	for c, db := range cf.dbs {
		if err := db.Close(); err != nil {
			slog.Error("closing a collection file", "collection", c, "err", err)
		}
	}
	cf.dbs = map[string]*bolt.DB{}
}

// mainFileHoldsCollections reports whether the main file still holds
// collection keys, which the per-collection layout does not allow.
func mainFileHoldsCollections(db *bolt.DB) (bool, error) {
	found := false
	err := db.View(func(tx *bolt.Tx) error {
		for _, name := range collectionBuckets {
			if b := tx.Bucket(name); b != nil {
				if k, _ := b.Cursor().First(); k != nil {
					found = true
					return nil
				}
			}
		}
		return nil
	})
	return found, err
}

// swapCollectionFiles replaces the live collection files with the backup's,
// for a restore in the per-collection layout, and returns how to put the old
// ones back. It does nothing in the single-file layout. The caller holds the
// restore write lock.
func (s *Server) swapCollectionFiles(srcDir string) (undo func(), err error) {
	noop := func() {}
	if !perCollectionLayout() {
		return noop, nil
	}
	// srcDir is derived from a path safeBackupPath accepted; checked again so
	// it cannot climb out of the backup directory.
	if strings.Contains(srcDir, "..") {
		return noop, fmt.Errorf("invalid backup path %q", srcDir)
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return noop, fmt.Errorf("this backup has no collection files (%s): it was taken in the single-file layout; "+
			"restore it with MDDB_STORAGE_LAYOUT=single, then run mddbd -split-collections", filepath.Base(srcDir))
	}
	for _, e := range entries {
		if _, ok := collectionFromFileName(e.Name()); ok {
			if err := verifyDatabase(filepath.Join(srcDir, e.Name())); err != nil {
				return noop, fmt.Errorf("%s is not a usable database: %w", e.Name(), err)
			}
		}
	}

	s.collMu.Lock()
	if s.collFiles != nil {
		s.collFiles.closeAll()
		s.collFiles = nil
	}
	s.collMu.Unlock()

	live := collectionDir(s.Path)
	aside := live + ".pre-restore"
	_ = os.RemoveAll(aside)
	if err := collRename(live, aside); err != nil && !os.IsNotExist(err) {
		return noop, fmt.Errorf("moving the collection files aside: %w", err)
	}
	// putBack is a local, not the named result: a returned closure calling
	// the result would call itself once the result is set, and the first
	// restore that had to undo a swap overflowed the stack (RAG-010).
	// The files opened from the swapped-in directory are closed before it is
	// removed: Windows refuses to delete or rename a directory with open
	// files, so closing them afterwards left a failed restore with no
	// collection files at all there.
	putBack := func() {
		s.collMu.Lock()
		if s.collFiles != nil {
			s.collFiles.closeAll()
		}
		s.collFiles = nil
		s.collMu.Unlock()
		_ = os.RemoveAll(live)
		_ = os.Rename(aside, live)
	}
	if err := collMkdirAll(live, 0o700); err != nil {
		putBack()
		return noop, err
	}
	for _, e := range entries {
		if _, ok := collectionFromFileName(e.Name()); !ok {
			continue
		}
		if err := collCopyFile(filepath.Join(srcDir, e.Name()), filepath.Join(live, e.Name())); err != nil {
			putBack()
			return noop, fmt.Errorf("copying %s: %w", e.Name(), err)
		}
	}
	return putBack, nil
}

// errSnapshotPerCollection is why replication snapshots are refused in the
// per-collection layout: a snapshot streams one file.
var errSnapshotPerCollection = errors.New("replication snapshots are not available with MDDB_STORAGE_LAYOUT=per-collection: " +
	"a snapshot streams one file; start the follower from a backup of the leader (main file plus its .d directory) and let it catch up from the binlog")

// openCollectionFile opens one collection file. When its freelist is not
// stored (after a crash), it opens without it and rebuilds it in the
// background, as the main file does (#333): reads of the collection work at
// once, and its writes wait for the rebuild.
func openCollectionFile(path string, opts *bolt.Options) (*bolt.DB, error) {
	meta, err := readBoltMeta(path)
	if err != nil || !meta.needsWalk() || !backgroundFreelistEnabled() {
		return openBolt(path, opts)
	}
	o := *opts
	o.DeferFreelistLoad = true
	db, err := openBolt(path, &o)
	if err != nil {
		return nil, err
	}
	go func() {
		if err := db.LoadFreelist(); err != nil {
			if !errors.Is(err, bolterrors.ErrDatabaseNotOpen) { // closed meanwhile: nothing to rebuild
				slog.Error("rebuilding a collection file's freelist failed; its writes fail", "file", path, "err", err)
			}
			return
		}
		slog.Info("collection file freelist rebuilt", "file", filepath.Base(path))
	}()
	return db, nil
}

// collectionFileOptions are a collection file's bbolt options: the main
// file's, without the 100 MB initial map. With it, bbolt grows a file in
// 16 MB steps from the first write, so every collection, however small,
// took 16 MB of disk; without it a file grows by doubling from 32 KB. A file
// past 1 GB gets room to grow before a remap (#350).
func collectionFileOptions(path string) *bolt.Options {
	o := boltOptionsFor(path)
	o.InitialMmapSize = mmapHeadroom(path)
	return o
}
