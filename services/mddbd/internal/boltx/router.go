package boltx

import bolt "go.etcd.io/bbolt"

// Router says which bbolt file holds a collection's data (#336). With one
// file per collection, the components that store collection data — full-text
// index, vectors, geo points, TTL, temporal events, document bodies — ask it
// instead of keeping one handle.
type Router interface {
	// ForRead returns the file to read a collection from. A collection that
	// has no file of its own reads from a file where it has no keys, which
	// answers every read with nothing.
	ForRead(collection string) *bolt.DB
	// ForWrite returns the file to write a collection to, creating it on
	// first use.
	ForWrite(collection string) (*bolt.DB, error)
	// All returns every file that can hold collection data, for work that
	// covers every collection.
	All() []*bolt.DB
}

// Single routes everything to one file: the layout mddb has always had.
type Single struct{ DB *bolt.DB }

// ForRead returns the file.
func (s Single) ForRead(string) *bolt.DB { return s.DB }

// ForWrite returns the file.
func (s Single) ForWrite(string) (*bolt.DB, error) { return s.DB, nil }

// All returns the file.
func (s Single) All() []*bolt.DB { return []*bolt.DB{s.DB} }

// View runs fn in a read transaction on the file holding collection.
func View(r Router, collection string, fn func(*bolt.Tx) error) error {
	return r.ForRead(collection).View(fn)
}

// Update runs fn in a write transaction on the file holding collection.
func Update(r Router, collection string, fn func(*bolt.Tx) error) error {
	db, err := r.ForWrite(collection)
	if err != nil {
		return err
	}
	return db.Update(fn)
}

// ViewAll runs fn in a read transaction on every file, in turn, stopping at
// the first error.
func ViewAll(r Router, fn func(*bolt.Tx) error) error {
	for _, db := range r.All() {
		if err := db.View(fn); err != nil {
			return err
		}
	}
	return nil
}

// Or is r, or a Single on db when r is nil: what a component that was given
// a router uses, and what one built on a plain handle falls back to.
func Or(r Router, db *bolt.DB) Router {
	if r != nil {
		return r
	}
	return Single{DB: db}
}

// ViewOneOrAll is View for a named collection and ViewAll for "", the
// convention of the rebuild functions that cover one collection or all.
func ViewOneOrAll(r Router, collection string, fn func(*bolt.Tx) error) error {
	if collection == "" {
		return ViewAll(r, fn)
	}
	return View(r, collection, fn)
}

// Current is the database a long-lived component should use: what follow
// returns when it is set and gives one, else db. A restore or replication
// snapshot replaces the server's handle; components built on the old one
// used it, closed, until restart. Following the server's handle fixes that.
func Current(db *bolt.DB, follow func() *bolt.DB) *bolt.DB {
	if follow != nil {
		if d := follow(); d != nil {
			return d
		}
	}
	return db
}
