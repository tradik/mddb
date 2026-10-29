package main

import (
	"log/slog"
	"mddb/internal/storage"

	"mddb/internal/boltx"

	bolt "go.etcd.io/bbolt"
)

// Routing collection data to its file (#336). In the single-file layout
// every function here answers with s.DB, so the code that calls them works
// unchanged; with MDDB_STORAGE_LAYOUT=per-collection they answer with the
// collection's own file.

// collectionStore returns the per-collection files, opening them on first
// use, or nil in the single-file layout.
func (s *Server) collectionStore() *collectionFiles {
	if !perCollectionLayout() || s.Path == "" {
		return nil
	}
	s.collMu.Lock()
	defer s.collMu.Unlock()
	if s.collFiles == nil {
		cf, err := openCollectionFiles(collectionDir(s.Path), collectionFileOptions)
		if err == nil {
			// DOC-017: every file is at this build's schema before any
			// of them is handed out.
			if err = migrateCollectionFiles(cf); err != nil {
				cf.closeAll()
			}
		}
		if err != nil {
			slog.Error("opening per-collection files failed", "err", err)
			return nil
		}
		s.collFiles = cf
		s.wireRouters()
	}
	return s.collFiles
}

// wireRouters points every collection-data component that exists at the
// server's router. main sets them as it builds them; this covers a server
// assembled another way, so no component keeps writing to the main file.
func (s *Server) wireRouters() {
	r := s.router()
	if s.FTSIndex != nil {
		s.FTSIndex.SetRouter(r)
	}
	if s.VectorStore != nil {
		s.VectorStore.SetRouter(r)
	}
	if s.GeoStore != nil {
		s.GeoStore.SetRouter(r)
	}
	if s.TTLManager != nil {
		s.TTLManager.SetRouter(r)
	}
	if s.TemporalManager != nil {
		s.TemporalManager.SetRouter(r)
	}
}

// collectionDB is the file to read a collection from.
func (s *Server) collectionDB(collection string) *bolt.DB {
	if cf := s.collectionStore(); cf != nil {
		if db := cf.get(collection); db != nil {
			return db
		}
	}
	return s.DB
}

// collectionDBForWrite is the file to write a collection to.
func (s *Server) collectionDBForWrite(collection string) (*bolt.DB, error) {
	if cf := s.collectionStore(); cf != nil {
		return cf.getOrCreate(collection)
	}
	return s.DB, nil
}

// collectionDBs is every file holding collection data.
func (s *Server) collectionDBs() []*bolt.DB {
	if cf := s.collectionStore(); cf != nil {
		all := cf.all()
		out := make([]*bolt.DB, 0, len(all))
		for _, c := range cf.names() {
			out = append(out, all[c])
		}
		return out
	}
	return []*bolt.DB{s.DB}
}

// DBViewC runs fn in a read transaction on the file holding collection.
func (s *Server) DBViewC(collection string, fn func(*bolt.Tx) error) error {
	s.restoreMu.RLock()
	defer s.restoreMu.RUnlock()
	return s.collectionDB(collection).View(fn)
}

// DBScanC is boltx.Scan of bucket in the file holding collection (#350).
func (s *Server) DBScanC(collection string, bucket, prefix []byte, visit func(k, v []byte) bool) error {
	s.restoreMu.RLock()
	defer s.restoreMu.RUnlock()
	return boltx.Scan(s.collectionDB(collection), bucket, prefix, visit)
}

// DBUpdateC runs fn in a write transaction on the file holding collection.
func (s *Server) DBUpdateC(collection string, fn func(*bolt.Tx) error) error {
	s.restoreMu.RLock()
	defer s.restoreMu.RUnlock()
	db, err := s.collectionDBForWrite(collection)
	if err != nil {
		return err
	}
	return db.Update(fn)
}

// DBViewAll runs fn in a read transaction on every file holding collection
// data, one after another. fn sees each file's buckets in turn, so code that
// scans a whole bucket and groups by collection works in both layouts.
func (s *Server) DBViewAll(fn func(*bolt.Tx) error) error {
	s.restoreMu.RLock()
	defer s.restoreMu.RUnlock()
	for _, db := range s.collectionDBs() {
		if err := db.View(fn); err != nil {
			return err
		}
	}
	return nil
}

// DBScanAll is boltx.Scan of bucket over every file holding collection data:
// a whole-bucket walk in short read transactions, so a write that grows a
// file is not stalled behind it (#350).
func (s *Server) DBScanAll(bucket, prefix []byte, visit func(k, v []byte) bool) error {
	s.restoreMu.RLock()
	defer s.restoreMu.RUnlock()
	for _, db := range s.collectionDBs() {
		if err := boltx.Scan(db, bucket, prefix, visit); err != nil {
			return err
		}
	}
	return nil
}

// router is the Router the collection-data components use.
func (s *Server) router() boltx.Router { return serverRouter{s} }

type serverRouter struct{ s *Server }

func (r serverRouter) ForRead(c string) *bolt.DB           { return r.s.collectionDB(c) }
func (r serverRouter) ForWrite(c string) (*bolt.DB, error) { return r.s.collectionDBForWrite(c) }
func (r serverRouter) All() []*bolt.DB                     { return r.s.collectionDBs() }

// docBytes is a copy of a document's stored record, read from the file of
// its collection, or nil. For code that loads documents of several
// collections, such as cross-collection search.
func (s *Server) docBytes(collection, docID string) []byte {
	var out []byte
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		if b := tx.Bucket(s.BucketNames.Docs); b != nil {
			if v := b.Get(storage.DocKey(collection, docID)); v != nil {
				out = append([]byte(nil), v...)
			}
		}
		return nil
	})
	return out
}

// collectionFileDBs is the open collection files, none in the single-file
// layout.
func (s *Server) collectionFileDBs() []*bolt.DB {
	s.collMu.Lock()
	cf := s.collFiles
	s.collMu.Unlock()
	if cf == nil {
		return nil
	}
	all := cf.all()
	out := make([]*bolt.DB, 0, len(all))
	for _, db := range all {
		out = append(out, db)
	}
	return out
}

// closeCollectionFiles closes the collection files at exit.
func (s *Server) closeCollectionFiles() {
	s.collMu.Lock()
	defer s.collMu.Unlock()
	if s.collFiles != nil {
		s.collFiles.closeAll()
	}
}
