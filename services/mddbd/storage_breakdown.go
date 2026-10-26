package main

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Where the bytes are (#335).
//
// /v1/stats counted documents but no bytes, so a 101 GB file holding 3 GB of
// content gave no clue where the other 98 GB were. Every collection-scoped
// key in mddb has the form "<tag>|<collection>|...", in every bucket, which
// lets one pass attribute each key and value to its bucket and collection.
//
// The full breakdown reads the whole database — on a large file that is
// minutes, and it holds a read transaction that long, during which freed
// pages cannot be reused. With ?collection=NAME only that collection's keys
// are read: within each bucket the scan jumps from one tag to the next.

// storageBreakdown is the response of GET /v1/storage.
type storageBreakdown struct {
	FileBytes   int64                       `json:"fileBytes"`
	PageSize    int                         `json:"pageSize"`
	FreePages   int                         `json:"freePages"`
	FreeBytes   int64                       `json:"freeBytes"`
	Buckets     map[string]bucketUsage      `json:"buckets,omitempty"`
	Collections map[string]*collectionUsage `json:"collections"`
	ElapsedMs   int64                       `json:"elapsedMs"`
	// #349: when this breakdown was computed.
	StatsComputedAt string `json:"statsComputedAt,omitempty"`
	StatsAgeSeconds int    `json:"statsAgeSeconds"`
}

// bucketUsage is one top-level bucket as bbolt allocates it.
type bucketUsage struct {
	AllocBytes int `json:"allocBytes"` // pages the bucket occupies
	InUseBytes int `json:"inUseBytes"` // bytes used within them
	Keys       int `json:"keys"`
}

// collectionUsage is the key and value bytes a collection holds, per bucket.
type collectionUsage struct {
	FileBytes  int64            `json:"fileBytes,omitempty"` // its own file, in the per-collection layout (#336)
	TotalBytes int64            `json:"totalBytes"`
	ByBucket   map[string]int64 `json:"byBucket"`
	ByCategory map[string]int64 `json:"byCategory"`
}

// storageCategory groups buckets into what an operator can act on.
func storageCategory(bucket string) string {
	switch {
	case bucket == "docs" || bucket == "rev":
		return "content"
	case bucket == "idxmeta" || bucket == "bykey":
		return "metadata"
	case strings.HasPrefix(bucket, "fts") || bucket == "synonyms" || bucket == "stopwords":
		return "fullText"
	case strings.HasPrefix(bucket, "vector"):
		return "vectors"
	default:
		return "other"
	}
}

func (u *collectionUsage) add(bucket string, n int64) {
	u.TotalBytes += n
	u.ByBucket[bucket] += n
	u.ByCategory[storageCategory(bucket)] += n
}

// collectionOf returns the collection segment of "<tag>|<collection>|...".
func collectionOf(key []byte) (string, bool) {
	i := bytes.IndexByte(key, '|')
	if i < 0 {
		return "", false
	}
	rest := key[i+1:]
	j := bytes.IndexByte(rest, '|')
	if j <= 0 {
		return "", false
	}
	return string(rest[:j]), true
}

// handleStorage answers from the walk cache (#349): the breakdown reads every
// page, and on a large database a request waiting for it never returned.
func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	only := r.URL.Query().Get("collection")
	val, at, pending := s.cachedWalk("storage:"+only, statsWait(), func() (any, error) { return s.storageUsage(only) })
	if pending {
		ok(w, map[string]any{"statsPending": true})
		return
	}
	out := *val.(*storageBreakdown)
	out.StatsComputedAt = at.UTC().Format(time.RFC3339)
	out.StatsAgeSeconds = int(time.Since(at).Seconds())
	ok(w, out)
}

// storageUsage computes the breakdown, for one collection or for all. With
// one file per collection (#336) it covers the main file and every
// collection file, and each collection also reports its file's size.
func (s *Server) storageUsage(only string) (*storageBreakdown, error) {
	start := time.Now()
	out := &storageBreakdown{Collections: map[string]*collectionUsage{}}
	if only == "" {
		out.Buckets = map[string]bucketUsage{}
	}
	usage := func(c string) *collectionUsage {
		u := out.Collections[c]
		if u == nil {
			u = &collectionUsage{ByBucket: map[string]int64{}, ByCategory: map[string]int64{}}
			out.Collections[c] = u
		}
		return u
	}

	scan := func(db *bolt.DB) error {
		if fi, err := os.Stat(db.Path()); err == nil {
			out.FileBytes += fi.Size()
		}
		return db.View(func(tx *bolt.Tx) error {
			out.PageSize = tx.DB().Info().PageSize
			free := tx.DB().Stats().FreePageN
			out.FreePages += free
			out.FreeBytes += int64(free) * int64(out.PageSize)
			return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
				bucket := string(name)
				if only != "" {
					scanCollectionKeys(b, only, func(n int64) { usage(only).add(bucket, n) })
					return nil
				}
				st := b.Stats()
				u := out.Buckets[bucket]
				u.AllocBytes += st.BranchAlloc + st.LeafAlloc
				u.InUseBytes += st.BranchInuse + st.LeafInuse
				u.Keys += st.KeyN
				out.Buckets[bucket] = u
				return b.ForEach(func(k, v []byte) error {
					if c, found := keyCollection(bucket, k); found && isCollectionBucket(name) {
						usage(c).add(bucket, int64(len(k)+len(v)))
					} else if c, found := collectionOf(k); found {
						usage(c).add(bucket, int64(len(k)+len(v)))
					}
					return nil
				})
			})
		})
	}

	s.restoreMu.RLock()
	defer s.restoreMu.RUnlock()
	dbs := []*bolt.DB{s.DB}
	if cf := s.collectionStore(); cf != nil {
		if only != "" {
			if db := cf.get(only); db != nil {
				dbs = append(dbs, db)
			}
		} else {
			for _, c := range cf.names() {
				if db := cf.get(c); db != nil {
					dbs = append(dbs, db)
				}
			}
		}
		for c, db := range cf.all() {
			if fi, err := os.Stat(db.Path()); err == nil && (only == "" || only == c) {
				usage(c).FileBytes = fi.Size()
			}
		}
	}
	var err error
	for _, db := range dbs {
		if err = scan(db); err != nil {
			break
		}
	}
	out.ElapsedMs = time.Since(start).Milliseconds()
	return out, err
}

// scanCollectionKeys reports the size of every "<tag>|collection|..." key in
// b, whatever the tag, seeking past the keys of other collections.
func scanCollectionKeys(b *bolt.Bucket, collection string, add func(int64)) {
	c := b.Cursor()
	for k, _ := c.First(); k != nil; {
		i := bytes.IndexByte(k, '|')
		if i < 0 {
			k, _ = c.Next() // not a tagged key; step over it
			continue
		}
		tag := append([]byte(nil), k[:i]...)
		// "<tag>|<collection>" on its own: a per-collection total.
		exact := append(append(append([]byte(nil), tag...), '|'), collection...)
		if ek, ev := c.Seek(exact); bytes.Equal(ek, exact) {
			add(int64(len(ek) + len(ev)))
		}
		prefix := append(exact, '|')
		var v []byte
		for k, v = c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			add(int64(len(k) + len(v)))
		}
		// Next tag: the first key after every "<tag>|..." key. '}' sorts
		// right after '|'.
		k, _ = c.Seek(append(tag, '}'))
	}
}
