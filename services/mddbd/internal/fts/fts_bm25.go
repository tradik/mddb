package fts

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"mddb/internal/sliceutil"
	"sort"

	bolt "go.etcd.io/bbolt"
)

// ftsMetaKey builds the key for storing document length metadata.
func ftsMetaKey(collection, docID string) []byte {
	return []byte(fmt.Sprintf("ftsmeta|%s|%s", collection, docID))
}

// ftsStatKey builds the key for storing collection-level statistics.
func ftsStatKey(collection string) []byte {
	return []byte(fmt.Sprintf("ftsstat|%s", collection))
}

// collectionStats holds per-collection BM25 statistics.
type collectionStats struct {
	TotalDocs  uint32
	TotalTerms uint64
}

func encodeCollectionStats(s collectionStats) []byte {
	buf := make([]byte, 12)
	binary.LittleEndian.PutUint32(buf[0:4], s.TotalDocs)
	binary.LittleEndian.PutUint64(buf[4:12], s.TotalTerms)
	return buf
}

func decodeCollectionStats(b []byte) collectionStats {
	if len(b) < 12 {
		return collectionStats{}
	}
	return collectionStats{
		TotalDocs:  binary.LittleEndian.Uint32(b[0:4]),
		TotalTerms: binary.LittleEndian.Uint64(b[4:12]),
	}
}

// IndexBM25Meta stores document length and updates collection stats.
// Called from Index() after tokenizing.
func (f *FTSIndex) IndexBM25Meta(tx *bolt.Tx, collection, docID string, docLength uint32) error {
	bRev := tx.Bucket(bucketFTSRev)
	if bRev == nil {
		return nil
	}

	metaKey := ftsMetaKey(collection, docID)
	statKey := ftsStatKey(collection)

	// Read old doc length (if updating)
	var oldDocLength uint32
	var isUpdate bool
	if old := bRev.Get(metaKey); len(old) >= 4 {
		oldDocLength = binary.LittleEndian.Uint32(old)
		isUpdate = true
	}

	// Store new doc length
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], docLength)
	if err := bRev.Put(metaKey, buf[:]); err != nil {
		return err
	}

	// Update collection stats
	stats := collectionStats{}
	if raw := bRev.Get(statKey); raw != nil {
		stats = decodeCollectionStats(raw)
	}
	if isUpdate {
		stats.TotalTerms = stats.TotalTerms - uint64(oldDocLength) + uint64(docLength)
	} else {
		stats.TotalDocs++
		stats.TotalTerms += uint64(docLength)
	}
	return bRev.Put(statKey, encodeCollectionStats(stats))
}

// RemoveBM25Meta decrements collection stats when a document is removed.
func (f *FTSIndex) RemoveBM25Meta(tx *bolt.Tx, collection, docID string) error {
	bRev := tx.Bucket(bucketFTSRev)
	if bRev == nil {
		return nil
	}

	metaKey := ftsMetaKey(collection, docID)
	statKey := ftsStatKey(collection)

	// Read doc length
	old := bRev.Get(metaKey)
	if len(old) < 4 {
		return nil
	}
	docLength := binary.LittleEndian.Uint32(old)

	// Delete doc length entry
	if err := bRev.Delete(metaKey); err != nil {
		return err
	}

	// Update collection stats
	stats := collectionStats{}
	if raw := bRev.Get(statKey); raw != nil {
		stats = decodeCollectionStats(raw)
	}
	if stats.TotalDocs > 0 {
		stats.TotalDocs--
	}
	if stats.TotalTerms >= uint64(docLength) {
		stats.TotalTerms -= uint64(docLength)
	} else {
		stats.TotalTerms = 0
	}
	// No document with content left: no statistics, as in an index built
	// from these documents, which never wrote them (GO-048).
	if stats.TotalDocs == 0 {
		return bRev.Delete(statKey)
	}
	return bRev.Put(statKey, encodeCollectionStats(stats))
}

// SearchBM25 performs full-text search using Okapi BM25 scoring.
func (f *FTSIndex) SearchBM25(collection, query string, limit int, p BM25Params) ([]FTSResult, error) {
	queryTerms := f.TokenizeQuery(collection, query)
	if len(queryTerms) == 0 {
		return nil, nil
	}

	type docScore struct {
		id           string
		score        float64
		matchedTerms []string
	}

	scores := make(map[string]*docScore)

	err := f.view(collection, func(tx *bolt.Tx) error {
		bFTS := tx.Bucket(bucketFTS)
		if bFTS == nil {
			return nil
		}
		bRev := tx.Bucket(bucketFTSRev)
		if bRev == nil {
			return nil
		}

		// Load collection stats for avgdl
		statKey := ftsStatKey(collection)
		stats := collectionStats{}
		if raw := bRev.Get(statKey); raw != nil {
			stats = decodeCollectionStats(raw)
		}
		totalDocs := float64(stats.TotalDocs)
		avgdl := float64(0)
		if stats.TotalDocs > 0 {
			avgdl = float64(stats.TotalTerms) / totalDocs
		}
		if avgdl == 0 {
			avgdl = 1 // avoid division by zero
		}

		for term := range queryTerms {
			// Count document frequency for this term
			var df int
			prefix := ftsKey(collection, term, "")
			c := bFTS.Cursor()
			for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
				docID := string(k[len(prefix):])
				if docID != "" {
					df++
				}
			}

			// IDF = ln((N - n + 0.5) / (n + 0.5) + 1)
			idf := math.Log((totalDocs-float64(df)+0.5)/(float64(df)+0.5) + 1)

			// Scan again to score each doc
			c = bFTS.Cursor()
			for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
				docID := string(k[len(prefix):])
				if docID == "" {
					continue
				}

				tf := float64(1)
				if len(v) >= 4 {
					tf = float64(binary.LittleEndian.Uint32(v))
				}

				// Get document length
				dl := avgdl // fallback to avgdl if metadata missing
				if meta := bRev.Get(ftsMetaKey(collection, docID)); len(meta) >= 4 {
					dl = float64(binary.LittleEndian.Uint32(meta))
				}

				// BM25 score: IDF * (tf * (k1 + 1)) / (tf + k1 * (1 - b + b * dl/avgdl))
				bm25Score := p.termScore(idf, tf, dl, avgdl)

				ds, ok := scores[docID]
				if !ok {
					ds = &docScore{id: docID}
					scores[docID] = ds
				}
				ds.score += bm25Score
				ds.matchedTerms = append(ds.matchedTerms, term)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	results := make([]FTSResult, 0, len(scores))
	for _, ds := range scores {
		results = append(results, FTSResult{
			DocID:        ds.id,
			Score:        ds.score,
			MatchedTerms: sliceutil.Unique(ds.matchedTerms),
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}
