package ttl

import (
	"encoding/binary"
	"fmt"
	bolt "go.etcd.io/bbolt"
	"log/slog"
	"mddb/internal/binlog"
	"mddb/internal/storage"
	"strings"
	"time"
)

var (
	bucketTTL    = []byte("ttl")
	bucketTTLRev = []byte("ttlrev")
)

// Reaper is the document surface the cleanup loop needs (dependency inversion,
// GO-015): the manager owns the interface and the daemon implements it over its
// *Server, so TTL no longer holds a back-reference to the Server god-object.
type Reaper interface {
	// LoadDoc deserializes a stored document value (encryption/compression
	// aware) so the reaper can read its Key and Lang.
	LoadDoc(v []byte) (*storage.Doc, error)
	// DeleteDocument removes an expired document across all indexes.
	DeleteDocument(collection, key, lang string) error
	// GenID derives the deterministic document ID from its identity parts.
	GenID(collection, key, lang string) string
}

// TTLManager handles document time-to-live expiry.
type TTLManager struct {
	db     *bolt.DB
	reaper Reaper
	stopCh chan struct{}
	binlog *binlog.Binlog
}

// SetBinlog sets the binlog for replication logging.
func (t *TTLManager) SetBinlog(bl *binlog.Binlog) {
	t.binlog = bl
}

// NewTTLManager creates a new TTL manager.
func NewTTLManager(db *bolt.DB, reaper Reaper) *TTLManager {
	return &TTLManager{db: db, reaper: reaper, stopCh: make(chan struct{})}
}

// EnsureBuckets creates the TTL buckets if they don't exist.
func (t *TTLManager) EnsureBuckets() error {
	return t.db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketTTL); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists(bucketTTLRev)
		return err
	})
}

// Set stores a TTL entry for a document. Removes any previous TTL first.
func (t *TTLManager) Set(collection, docID string, expiresAt int64) error {
	var bo binlog.BinlogOps
	err := t.db.Update(func(tx *bolt.Tx) error {
		bTTL := tx.Bucket(bucketTTL)
		bRev := tx.Bucket(bucketTTLRev)

		revKey := ttlRevKey(collection, docID)

		// Remove old TTL entry if exists
		if old := bRev.Get(revKey); old != nil {
			oldExpiry := int64(binary.BigEndian.Uint64(old)) // #nosec G115 -- TTL timestamp within int64 range
			oldKey := ttlKey(oldExpiry, collection, docID)
			_ = bTTL.Delete(oldKey)
			bo.Delete("ttl", oldKey)
		}

		if expiresAt <= 0 {
			bo.Delete("ttlrev", revKey)
			return bRev.Delete(revKey)
		}

		// Store forward key: expiresAt|collection|docID -> empty
		fwdKey := ttlKey(expiresAt, collection, docID)
		if err := bTTL.Put(fwdKey, []byte{}); err != nil {
			return err
		}
		bo.Put("ttl", fwdKey, []byte{})

		// Store reverse key: collection|docID -> expiresAt (8 bytes)
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], uint64(expiresAt))
		bo.Put("ttlrev", revKey, buf[:])
		return bRev.Put(revKey, buf[:])
	})
	if err == nil {
		bo.FlushTo(t.binlog)
	}
	return err
}

// Remove deletes TTL entries for a document.
func (t *TTLManager) Remove(collection, docID string) error {
	var bo binlog.BinlogOps
	err := t.db.Update(func(tx *bolt.Tx) error {
		bTTL := tx.Bucket(bucketTTL)
		bRev := tx.Bucket(bucketTTLRev)

		revKey := ttlRevKey(collection, docID)
		if old := bRev.Get(revKey); old != nil {
			oldExpiry := int64(binary.BigEndian.Uint64(old)) // #nosec G115 -- TTL timestamp within int64 range
			oldKey := ttlKey(oldExpiry, collection, docID)
			_ = bTTL.Delete(oldKey)
			bo.Delete("ttl", oldKey)
		}
		bo.Delete("ttlrev", revKey)
		return bRev.Delete(revKey)
	})
	if err == nil {
		bo.FlushTo(t.binlog)
	}
	return err
}

// StartCleanup runs a background goroutine that reaps expired documents.
func (t *TTLManager) StartCleanup(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-t.stopCh:
				return
			case <-ticker.C:
				t.cleanup()
			}
		}
	}()
}

// Stop signals the cleanup goroutine to stop.
func (t *TTLManager) Stop() {
	close(t.stopCh)
}

func (t *TTLManager) cleanup() {
	now := time.Now().Unix()
	threshold := ttlKey(now, "\xff", "\xff") // scan everything <= now

	// Collect expired entries
	type expiredDoc struct {
		collection, key, lang string
	}
	var expired []expiredDoc

	_ = t.db.View(func(tx *bolt.Tx) error {
		bTTL := tx.Bucket(bucketTTL)
		if bTTL == nil {
			return nil
		}
		c := bTTL.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			// Key format: %020d|collection|docID
			if string(k) >= string(threshold) {
				break
			}
			// Parse collection and docID from key
			parts := strings.SplitN(string(k), "|", 3)
			if len(parts) < 3 {
				continue
			}
			coll := parts[1]
			docID := parts[2]

			// Look up key and lang from the document
			bDocs := tx.Bucket([]byte("docs"))
			if v := bDocs.Get(storage.DocKey(coll, docID)); v != nil {
				if doc, err := t.reaper.LoadDoc(v); err == nil {
					expired = append(expired, expiredDoc{coll, doc.Key, doc.Lang})
				}
			}
		}
		return nil
	})

	// Delete expired documents
	for _, ed := range expired {
		if err := t.reaper.DeleteDocument(ed.collection, ed.key, ed.lang); err != nil {
			slog.Warn("TTL cleanup failed to delete", "collection", ed.collection, "key", ed.key, "lang", ed.lang, "err", err)
			continue
		}
		// Also remove TTL entries
		docID := t.reaper.GenID(ed.collection, ed.key, ed.lang)
		_ = t.Remove(ed.collection, docID)
		slog.Info("TTL cleanup expired", "collection", ed.collection, "key", ed.key, "lang", ed.lang)
	}
}

// ttlKey builds the forward TTL bucket key.
func ttlKey(expiresAt int64, collection, docID string) []byte {
	return []byte(fmt.Sprintf("%020d|%s|%s", expiresAt, collection, docID))
}

// ttlRevKey builds the reverse TTL lookup key.
func ttlRevKey(collection, docID string) []byte {
	return []byte(collection + "|" + docID)
}
