package main

import (
	"log/slog"
	"time"

	"mddb/internal/binlog"
)

// Dropping a copy of a collection another group holds (GO-052): the source
// of a finished move, or the target of one given up. Every key goes through
// the log, so the group's followers drop it too, and the file goes. Nothing
// else is touched: a document body kept in an external backend belongs to
// the collection wherever it is, and stays.

// dropBatch is how many keys one transaction of a drop removes.
const dropBatch = 1000

// dropCopy removes this node's copy of c: its file's keys and its settings.
func (p *placementRuntime) dropCopy(c string) error {
	p.local.mark(p.local.dropping, c, true)
	defer p.local.mark(p.local.dropping, c, false)
	var file, main []*binlog.BinlogEntry
	collect := func(into *[]*binlog.BinlogEntry) func(string, []byte, []byte) error {
		return func(b string, k, _ []byte) error {
			*into = append(*into, &binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: b, Key: append([]byte(nil), k...)})
			return nil
		}
	}
	cf := p.s.collectionStore()
	db := cf.get(c)
	var err error
	if db != nil {
		err = scanFile(db, collect(&file))
	}
	if err == nil {
		err = scanBuckets(p.s.DB, settingsBucketNames(), ownedBy(c), collect(&main))
	}
	if err != nil {
		return err
	}
	if db == nil && len(main) == 0 {
		return nil
	}
	for start := 0; start < len(file); start += dropBatch {
		if err := logEntries(db.Update, p.s.Binlog, file[start:min(start+dropBatch, len(file))]); err != nil {
			return err
		}
	}
	if err := logEntries(p.s.DBUpdate, p.s.Binlog, main); err != nil {
		return err
	}
	p.forgetInMemory(c)
	if err := cf.remove(c); err != nil {
		slog.Warn("placement: removing a dropped collection's file failed", "collection", c, "err", err)
	}
	slog.Info("placement: dropped this group's copy of a collection another group holds",
		"collection", c, "keys", len(file)+len(main))
	return nil
}

// forgetInMemory clears what the node holds of c in memory.
func (p *placementRuntime) forgetInMemory(c string) {
	s := p.s
	s.dropCollectionVectors(c)
	if s.GeoIndex != nil {
		s.GeoIndex.Clear(c)
	}
	if s.GeoHashIndex != nil {
		s.GeoHashIndex.Clear(c)
	}
	if s.Cache != nil {
		s.Cache.Clear()
	}
	if s.LockFreeCache != nil {
		s.LockFreeCache.Clear()
	}
	if s.SearchCache != nil {
		s.SearchCache.Clear()
	}
	if s.CollectionManager != nil {
		_ = s.CollectionManager.LoadAll()
	}
	if s.SchemaManager != nil {
		_ = s.SchemaManager.LoadAll()
	}
}

func keepAll(string, []byte, []byte) bool { return true }

// ownedBy keeps the settings keys of collection c.
func ownedBy(c string) func(string, []byte, []byte) bool {
	return func(b string, k, v []byte) bool {
		owner, _ := settingsCollection(b, k, v)
		return owner == c
	}
}

// settingsBucketNames lists settingsBuckets.
func settingsBucketNames() []string {
	out := make([]string, 0, len(settingsBuckets))
	for b := range settingsBuckets {
		out = append(out, b)
	}
	return out
}

// dropStale drops, on a group's leader, the copies of collections the map
// places in another group — once they have been elsewhere for the drop
// grace, so a node whose map is a little behind does not lose reads.
func (p *placementRuntime) dropStale(seen map[string]time.Time) {
	t := p.current()
	cf := p.s.collectionStore()
	if t == nil || cf == nil {
		return
	}
	now := time.Now()
	for _, c := range cf.names() {
		e, placed := t.Entries[c]
		if !placed || e.Group == p.cfg.self || e.MovingTo == p.cfg.self || p.local.isImporting(c) {
			delete(seen, c)
			continue
		}
		first, ok := seen[c]
		if !ok {
			seen[c] = now
			continue
		}
		if now.Sub(first) < p.cfg.dropGrace {
			continue
		}
		if err := p.dropCopy(c); err != nil {
			slog.Warn("placement: dropping a collection another group holds failed", "collection", c, "err", err)
			continue
		}
		delete(seen, c)
	}
}
