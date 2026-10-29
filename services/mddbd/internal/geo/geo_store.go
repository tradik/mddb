package geo

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"mddb/internal/binlog"
	"mddb/internal/boltx"

	bolt "go.etcd.io/bbolt"
)

// geoBucketName is the single BoltDB bucket that persists geo points.
// Key format: "geo|<collection>|<docID>" → 16-byte value (lat float64 + lng
// float64, big-endian). Keeping this bucket independent of "docs" means an
// index rebuild can stream-scan lat/lng without unmarshaling documents.
var geoBucketName = []byte("geo")

// GeoStore persists per-document lat/lng pairs and knows how to rebuild the
// in-memory R-tree from BoltDB at startup. Mirrors VectorStore.
type GeoStore struct {
	db     *bolt.DB
	router boltx.RouterSlot // #336: which file holds a collection; nil means db
	binlog *binlog.Binlog
}

// SetRouter makes it read and write each collection in the file the router
// names (#336). Without one, everything goes to the db it was built on.
func (gs *GeoStore) SetRouter(r boltx.Router) { gs.router.Set(r) }

func (gs *GeoStore) rt() boltx.Router { return gs.router.Get(gs.db) }

// NewGeoStore creates a new geo store backed by BoltDB.
func NewGeoStore(db *bolt.DB) *GeoStore {
	return &GeoStore{db: db}
}

// SetBinlog wires the geo store into the replication log so follower nodes
// receive geo upserts/deletes for free, identical to VectorStore.SetBinlog.
func (gs *GeoStore) SetBinlog(bl *binlog.Binlog) { gs.binlog = bl }

// EnsureBucket creates the "geo" bucket if it does not exist.
func (gs *GeoStore) EnsureBucket() error {
	return boltx.EnsureBuckets(gs.db, geoBucketName)
}

// Put upserts a (collection, docID) point.
func (gs *GeoStore) Put(collection, docID string, lat, lng float64) error {
	key := buildGeoKey(collection, docID)
	value := encodeGeoValue(lat, lng)
	err := boltx.Update(gs.rt(), collection, func(tx *bolt.Tx) error {
		b := tx.Bucket(geoBucketName)
		if b == nil {
			return fmt.Errorf("geo bucket not found")
		}
		return b.Put(key, value)
	})
	if err == nil && gs.binlog != nil {
		_ = gs.binlog.Append(&binlog.BinlogEntry{
			Type:       binlog.BinlogPut,
			BucketName: "geo",
			Key:        bytes.Clone(key),
			Value:      bytes.Clone(value),
		})
	}
	return err
}

// Delete removes a (collection, docID) point. No-op if absent.
func (gs *GeoStore) Delete(collection, docID string) error {
	key := buildGeoKey(collection, docID)
	err := boltx.Update(gs.rt(), collection, func(tx *bolt.Tx) error {
		b := tx.Bucket(geoBucketName)
		if b == nil {
			return nil
		}
		return b.Delete(key)
	})
	if err == nil && gs.binlog != nil {
		_ = gs.binlog.Append(&binlog.BinlogEntry{
			Type:       binlog.BinlogDelete,
			BucketName: "geo",
			Key:        bytes.Clone(key),
		})
	}
	return err
}

// DeleteCollection drops all geo points for a collection. Called from the
// regular /v1/delete-collection path alongside the FTS/vector cleanup.
func (gs *GeoStore) DeleteCollection(collection string) error {
	prefix := buildGeoPrefix(collection)
	return boltx.Update(gs.rt(), collection, func(tx *bolt.Tx) error {
		b := tx.Bucket(geoBucketName)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		var toDelete [][]byte
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			toDelete = append(toDelete, bytes.Clone(k))
		}
		for _, k := range toDelete {
			if err := b.Delete(k); err != nil {
				return err
			}
			if gs.binlog != nil {
				_ = gs.binlog.Append(&binlog.BinlogEntry{
					Type:       binlog.BinlogDelete,
					BucketName: "geo",
					Key:        bytes.Clone(k),
				})
			}
		}
		return nil
	})
}

// Rebuild streams every geo point from BoltDB back into the in-memory index.
// Called at startup and by POST /v1/geo-reindex. When collection is empty,
// all collections are rebuilt; otherwise only the named one.
func (gs *GeoStore) Rebuild(idx *GeoIndex, collection string) (int, error) {
	if idx == nil {
		return 0, fmt.Errorf("nil index")
	}
	var prefix []byte
	if collection != "" {
		prefix = buildGeoPrefix(collection)
		idx.Clear(collection)
	}
	count := 0
	err := boltx.ViewOneOrAll(gs.rt(), collection, func(tx *bolt.Tx) error {
		b := tx.Bucket(geoBucketName)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		var k, v []byte
		if prefix != nil {
			k, v = c.Seek(prefix)
		} else {
			k, v = c.First()
		}
		for ; k != nil; k, v = c.Next() {
			if prefix != nil && !bytes.HasPrefix(k, prefix) {
				break
			}
			coll, docID, ok := parseGeoKey(k)
			if !ok {
				continue
			}
			lat, lng, ok := decodeGeoValue(v)
			if !ok {
				continue
			}
			idx.Add(coll, docID, lat, lng)
			count++
		}
		return nil
	})
	if err != nil {
		return count, err
	}
	if collection != "" {
		idx.markRebuilt(collection)
	} else {
		for _, c := range idx.Collections() {
			idx.markRebuilt(c)
		}
	}
	return count, nil
}

// RebuildHash streams every geo point from BoltDB into the geohash index.
// Same shape as Rebuild but targets GeoHashIndex; used at startup and by
// /v1/geo-reindex when the geohash algorithm is active.
func (gs *GeoStore) RebuildHash(idx *GeoHashIndex, collection string) (int, error) {
	if idx == nil {
		return 0, fmt.Errorf("nil index")
	}
	var prefix []byte
	if collection != "" {
		prefix = buildGeoPrefix(collection)
		idx.Clear(collection)
	}
	count := 0
	err := boltx.ViewOneOrAll(gs.rt(), collection, func(tx *bolt.Tx) error {
		b := tx.Bucket(geoBucketName)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		var k, v []byte
		if prefix != nil {
			k, v = c.Seek(prefix)
		} else {
			k, v = c.First()
		}
		for ; k != nil; k, v = c.Next() {
			if prefix != nil && !bytes.HasPrefix(k, prefix) {
				break
			}
			coll, docID, ok := parseGeoKey(k)
			if !ok {
				continue
			}
			lat, lng, ok := decodeGeoValue(v)
			if !ok {
				continue
			}
			idx.Add(coll, docID, lat, lng)
			count++
		}
		return nil
	})
	return count, err
}

// Get looks up a single point (used by tests and introspection).
func (gs *GeoStore) Get(collection, docID string) (float64, float64, bool, error) {
	key := buildGeoKey(collection, docID)
	var lat, lng float64
	var found bool
	err := boltx.View(gs.rt(), collection, func(tx *bolt.Tx) error {
		b := tx.Bucket(geoBucketName)
		if b == nil {
			return nil
		}
		v := b.Get(key)
		if v == nil {
			return nil
		}
		var ok bool
		lat, lng, ok = decodeGeoValue(v)
		found = ok
		return nil
	})
	return lat, lng, found, err
}

// buildGeoKey builds a key of the form "geo|<collection>|<docID>".
func buildGeoKey(collection, docID string) []byte {
	return []byte("geo|" + collection + "|" + docID)
}

// buildGeoPrefix builds a prefix for scanning all docs in a collection.
func buildGeoPrefix(collection string) []byte {
	return []byte("geo|" + collection + "|")
}

// parseGeoKey splits a BoltDB key back into (collection, docID). Returns
// ("", "", false) on malformed input.
func parseGeoKey(key []byte) (string, string, bool) {
	if !bytes.HasPrefix(key, []byte("geo|")) {
		return "", "", false
	}
	rest := key[4:]
	sep := bytes.IndexByte(rest, '|')
	if sep < 0 {
		return "", "", false
	}
	return string(rest[:sep]), string(rest[sep+1:]), true
}

// encodeGeoValue packs (lat, lng) into 16 bytes, big-endian.
func encodeGeoValue(lat, lng float64) []byte {
	buf := make([]byte, 16)
	binary.BigEndian.PutUint64(buf[0:8], math.Float64bits(lat))
	binary.BigEndian.PutUint64(buf[8:16], math.Float64bits(lng))
	return buf
}

// decodeGeoValue unpacks the 16-byte representation back into (lat, lng).
func decodeGeoValue(v []byte) (float64, float64, bool) {
	if len(v) != 16 {
		return 0, 0, false
	}
	lat := math.Float64frombits(binary.BigEndian.Uint64(v[0:8]))
	lng := math.Float64frombits(binary.BigEndian.Uint64(v[8:16]))
	return lat, lng, true
}
