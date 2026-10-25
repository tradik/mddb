package bbolt

import (
	"fmt"

	"go.etcd.io/bbolt/internal/common"
	fl "go.etcd.io/bbolt/internal/freelist"
)

// Deferred freelist loading — an mddb patch to bbolt v1.5.0.
//
// A writable open loads the freelist before it returns. Without a stored
// freelist (NoFreelistSync) that walks every page of the file, which on a
// 100 GB database is an hour or more during which nothing can be read.
// Reads do not need the freelist: only page allocation does. With
// Options.DeferFreelistLoad the open returns after reading the meta pages,
// read transactions run at once, and the freelist is built by LoadFreelist
// or by the first write transaction, which every writer waits for.
//
// A freelist tracks open read transactions so that pages they can still
// see are not reused. Readers that begin before it exists are counted in
// preFreelistReaders, under the meta lock, and registered with it when it
// is installed under the same lock.

// LoadFreelist loads the freelist now if Options.DeferFreelistLoad left it
// unloaded, blocking until it is, and returns why it could not be built. It
// is safe to call from several goroutines and more than once.
func (db *DB) LoadFreelist() error {
	db.loadFreelist()
	db.metalock.Lock()
	defer db.metalock.Unlock()
	return db.freelistErr
}

// FreelistLoaded reports whether the freelist has been loaded.
func (db *DB) FreelistLoaded() bool {
	db.metalock.Lock()
	defer db.metalock.Unlock()
	return db.freelist != nil
}

// buildFreelist reads or reconstructs the freelist. A damaged page tree
// panics inside the walk; that is returned as an error.
func (db *DB) buildFreelist() (f fl.Interface, err error) {
	defer func() {
		if r := recover(); r != nil {
			f, err = nil, fmt.Errorf("bbolt: loading the freelist: %v", r)
		}
	}()
	f = newFreelist(db.FreelistType)
	if !db.hasSyncedFreelist() {
		// Reconstruct free list by scanning the DB.
		f.Init(db.freepages())
	} else {
		// Read free list from freelist page.
		f.Read(db.page(db.meta().Freelist()))
	}
	return f, nil
}

// notePreFreelistReader counts a read transaction that begins (+1) or ends
// (-1) before the freelist exists. The caller holds the meta lock.
func (db *DB) notePreFreelistReader(txid common.Txid, delta int) {
	if db.preFreelistReaders == nil {
		db.preFreelistReaders = map[common.Txid]int{}
	}
	if db.preFreelistReaders[txid] += delta; db.preFreelistReaders[txid] <= 0 {
		delete(db.preFreelistReaders, txid)
	}
}
