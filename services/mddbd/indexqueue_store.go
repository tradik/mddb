package main

import (
	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// serverIndexStore adapts *Server to indexqueue.Store, the dependency-inversion
// seam introduced in GO-015. It lets internal/indexqueue write the metadata
// index without importing the Server god-object.
type serverIndexStore struct {
	s *Server
}

func (a serverIndexStore) DBUpdate(collection string, fn func(*bolt.Tx) error) error {
	return a.s.DBUpdateC(collection, fn)
}
func (a serverIndexStore) IdxMetaBucket() []byte  { return a.s.BucketNames.IdxMeta }
func (a serverIndexStore) Binlog() *binlog.Binlog { return a.s.Binlog }
