package main

import (
	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// updateLogged runs fn in a write transaction on the file holding
// collection and writes what it records to the binlog as the transaction's
// last step, so the log takes writes in the order they commit (GO-051,
// binlog.Logged).
func (s *Server) updateLogged(collection string, fn func(*bolt.Tx, *binlog.BinlogOps) error) error {
	return binlog.Logged(func(f func(*bolt.Tx) error) error { return s.DBUpdateC(collection, f) }, s.Binlog, fn)
}
