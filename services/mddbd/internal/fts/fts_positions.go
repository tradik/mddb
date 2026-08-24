package fts

import (
	"bytes"
	"encoding/binary"
	"sort"
	"strings"
	"unicode"

	bolt "go.etcd.io/bbolt"
)

var bucketFTSPos = []byte("ftsp") // positional index

// ftspKey builds the positional-index bucket key.
func ftspKey(collection, term, docID string) []byte {
	return []byte("ftsp|" + collection + "|" + term + "|" + docID)
}

// ftspRevKey builds the positional reverse lookup key.
func ftspRevKey(collection, docID string) []byte {
	return []byte("ftsprev|" + collection + "|" + docID)
}

// encodePositions encodes a slice of uint32 positions as little-endian bytes.
func encodePositions(positions []uint32) []byte {
	buf := make([]byte, 4+len(positions)*4)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(positions))) // #nosec G115 -- position index always positive and bounded
	for i, p := range positions {
		binary.LittleEndian.PutUint32(buf[4+i*4:8+i*4], p)
	}
	return buf
}

// decodePositions decodes a little-endian encoded position slice.
func decodePositions(data []byte) []uint32 {
	if len(data) < 4 {
		return nil
	}
	count := binary.LittleEndian.Uint32(data[0:4])
	if uint32(len(data)) < 4+count*4 { // #nosec G115 -- position index always positive and bounded
		return nil
	}
	positions := make([]uint32, count)
	for i := uint32(0); i < count; i++ {
		positions[i] = binary.LittleEndian.Uint32(data[4+i*4 : 8+i*4])
	}
	return positions
}

// TokenizePositions splits text into a map of term -> positions (word offsets).
func (f *FTSIndex) TokenizePositions(text string) map[string][]uint32 {
	positions := make(map[string][]uint32)
	text = strings.ToLower(text)

	var word strings.Builder
	var pos uint32
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word.WriteRune(r)
		} else {
			if word.Len() >= 2 {
				w := word.String()
				if !f.stopWords[w] {
					if f.stemmer != nil {
						w = f.stemmer.Stem(w)
					}
					positions[w] = append(positions[w], pos)
				}
				pos++
			}
			word.Reset()
		}
	}
	if word.Len() >= 2 {
		w := word.String()
		if !f.stopWords[w] {
			if f.stemmer != nil {
				w = f.stemmer.Stem(w)
			}
			positions[w] = append(positions[w], pos)
		}
	}
	return positions
}

// TokenizePositionsLang splits text into a map of term -> positions using language-specific processing.
func (f *FTSIndex) TokenizePositionsLang(text, lang string) map[string][]uint32 {
	stemmer, stopWords := f.resolveLang(lang)
	positions := make(map[string][]uint32)
	text = strings.ToLower(text)

	var word strings.Builder
	var pos uint32
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word.WriteRune(r)
		} else {
			if word.Len() >= 2 {
				w := word.String()
				if !stopWords[w] {
					if stemmer != nil {
						w = stemmer.Stem(w)
					}
					positions[w] = append(positions[w], pos)
				}
				pos++
			}
			word.Reset()
		}
	}
	if word.Len() >= 2 {
		w := word.String()
		if !stopWords[w] {
			if stemmer != nil {
				w = stemmer.Stem(w)
			}
			positions[w] = append(positions[w], pos)
		}
	}
	return positions
}

// IndexPositionsWithLang stores term positions using language-specific tokenization.
func (f *FTSIndex) IndexPositionsWithLang(collection, docID, content, lang string) error {
	positions := f.TokenizePositionsLang(content, lang)
	if len(positions) == 0 {
		return nil
	}

	return f.db.Update(func(tx *bolt.Tx) error {
		return f.indexPositionsInTx(tx, collection, docID, positions)
	})
}

// indexPositionsInTx writes one document's term positions inside a caller's
// transaction — the shared body behind IndexPositionsWithLang and the bulk
// path (GO-027).
func (f *FTSIndex) indexPositionsInTx(tx *bolt.Tx, collection, docID string, positions map[string][]uint32) error {
	bPos := tx.Bucket(bucketFTSPos)
	if bPos == nil {
		return nil
	}

	revKey := ftspRevKey(collection, docID)
	if old := bPos.Get(revKey); old != nil {
		oldTerms := strings.Split(string(old), ",")
		for _, term := range oldTerms {
			if term != "" {
				_ = bPos.Delete(ftspKey(collection, term, docID))
			}
		}
	}

	termList := make([]string, 0, len(positions))
	for term, posSlice := range positions {
		k := ftspKey(collection, term, docID)
		if err := bPos.Put(k, encodePositions(posSlice)); err != nil {
			return err
		}
		termList = append(termList, term)
	}

	// Sorted so identical content yields identical bytes — see the note in
	// indexTermsInTx.
	sort.Strings(termList)
	return bPos.Put(revKey, []byte(strings.Join(termList, ",")))
}

// IndexPositions stores term positions for a document (used for phrase/proximity search).
func (f *FTSIndex) IndexPositions(collection, docID, content string) error {
	positions := f.TokenizePositions(content)
	if len(positions) == 0 {
		return nil
	}

	return f.db.Update(func(tx *bolt.Tx) error {
		bPos := tx.Bucket(bucketFTSPos)
		if bPos == nil {
			return nil
		}

		// Remove old entries via reverse index
		revKey := ftspRevKey(collection, docID)
		if old := bPos.Get(revKey); old != nil {
			oldTerms := strings.Split(string(old), ",")
			for _, term := range oldTerms {
				if term != "" {
					_ = bPos.Delete(ftspKey(collection, term, docID))
				}
			}
		}

		// Store new entries
		termList := make([]string, 0, len(positions))
		for term, posSlice := range positions {
			k := ftspKey(collection, term, docID)
			if err := bPos.Put(k, encodePositions(posSlice)); err != nil {
				return err
			}
			termList = append(termList, term)
		}

		// Store reverse index
		return bPos.Put(revKey, []byte(strings.Join(termList, ",")))
	})
}

// RemovePositions removes positional index entries for a document.
func (f *FTSIndex) RemovePositions(collection, docID string) error {
	return f.db.Update(func(tx *bolt.Tx) error {
		f.removePositionsInTx(tx, collection, docID)
		return nil
	})
}

// removePositionsInTx removes positional index entries within an existing transaction.
func (f *FTSIndex) removePositionsInTx(tx *bolt.Tx, collection, docID string) {
	bPos := tx.Bucket(bucketFTSPos)
	if bPos == nil {
		return
	}

	revKey := ftspRevKey(collection, docID)
	if old := bPos.Get(revKey); old != nil {
		oldTerms := strings.Split(string(old), ",")
		for _, term := range oldTerms {
			if term != "" {
				_ = bPos.Delete(ftspKey(collection, term, docID))
			}
		}
	}
	_ = bPos.Delete(revKey)
}

// SearchPhrase finds documents containing an exact phrase (consecutive terms).
func (f *FTSIndex) SearchPhrase(collection string, phrase string, limit int) ([]FTSResult, error) {
	phraseTerms := f.tokenizeOrdered(phrase)
	if len(phraseTerms) == 0 {
		return nil, nil
	}

	type docScore struct {
		id    string
		count int // number of phrase occurrences
	}
	scores := make(map[string]*docScore)

	err := f.db.View(func(tx *bolt.Tx) error {
		bPos := tx.Bucket(bucketFTSPos)
		if bPos == nil {
			return nil
		}

		// Get document IDs that contain the first term
		firstPrefix := ftspKey(collection, phraseTerms[0], "")
		c := bPos.Cursor()
		for k, v := c.Seek(firstPrefix); k != nil && bytes.HasPrefix(k, firstPrefix); k, v = c.Next() {
			docID := string(k[len(firstPrefix):])
			if docID == "" {
				continue
			}

			firstPositions := decodePositions(v)
			if len(firstPositions) == 0 {
				continue
			}

			// For each starting position, check if remaining terms follow consecutively
			matchCount := 0
			for _, startPos := range firstPositions {
				match := true
				for offset := 1; offset < len(phraseTerms); offset++ {
					termKey := ftspKey(collection, phraseTerms[offset], docID)
					posData := bPos.Get(termKey)
					termPositions := decodePositions(posData)
					expected := startPos + uint32(offset)
					if !containsPos(termPositions, expected) {
						match = false
						break
					}
				}
				if match {
					matchCount++
				}
			}

			if matchCount > 0 {
				scores[docID] = &docScore{id: docID, count: matchCount}
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
			Score:        float64(ds.count),
			MatchedTerms: phraseTerms,
		})
	}

	sortByScore(results)
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// SearchProximity finds documents where phrase terms appear within N words of each other.
func (f *FTSIndex) SearchProximity(collection string, phrase string, distance int, limit int) ([]FTSResult, error) {
	phraseTerms := f.tokenizeOrdered(phrase)
	if len(phraseTerms) < 2 {
		return nil, nil
	}

	type docScore struct {
		id       string
		minSpan  int // closest match span
		matchCnt int
	}
	scores := make(map[string]*docScore)

	err := f.db.View(func(tx *bolt.Tx) error {
		bPos := tx.Bucket(bucketFTSPos)
		if bPos == nil {
			return nil
		}

		// Get all docs that contain ALL terms
		docTermPositions := make(map[string]map[int][]uint32) // docID -> termIdx -> positions

		for termIdx, term := range phraseTerms {
			prefix := ftspKey(collection, term, "")
			c := bPos.Cursor()
			for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
				docID := string(k[len(prefix):])
				if docID == "" {
					continue
				}
				positions := decodePositions(v)
				if len(positions) == 0 {
					continue
				}
				if docTermPositions[docID] == nil {
					docTermPositions[docID] = make(map[int][]uint32)
				}
				docTermPositions[docID][termIdx] = positions
			}
		}

		// Check each doc: all terms must be present and within distance
		for docID, termPosMap := range docTermPositions {
			if len(termPosMap) != len(phraseTerms) {
				continue // not all terms present
			}

			// Find minimum window span containing all terms
			minSpan := findMinSpan(phraseTerms, termPosMap, distance)
			if minSpan >= 0 {
				scores[docID] = &docScore{
					id:       docID,
					minSpan:  minSpan,
					matchCnt: 1,
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	results := make([]FTSResult, 0, len(scores))
	for _, ds := range scores {
		// Score inversely proportional to span: closer = higher score
		score := float64(distance+1-ds.minSpan) / float64(distance+1)
		if score < 0.1 {
			score = 0.1
		}
		results = append(results, FTSResult{
			DocID:        ds.id,
			Score:        score,
			MatchedTerms: phraseTerms,
		})
	}

	sortByScore(results)
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// tokenizeOrdered returns terms in order (preserving sequence for phrase matching).
func (f *FTSIndex) tokenizeOrdered(text string) []string {
	text = strings.ToLower(text)
	var terms []string
	var word strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word.WriteRune(r)
		} else {
			if word.Len() >= 2 {
				w := word.String()
				if !f.stopWords[w] {
					if f.stemmer != nil {
						w = f.stemmer.Stem(w)
					}
					terms = append(terms, w)
				}
			}
			word.Reset()
		}
	}
	if word.Len() >= 2 {
		w := word.String()
		if !f.stopWords[w] {
			if f.stemmer != nil {
				w = f.stemmer.Stem(w)
			}
			terms = append(terms, w)
		}
	}
	return terms
}

// containsPos checks if a position slice contains a specific value.
func containsPos(positions []uint32, target uint32) bool {
	for _, p := range positions {
		if p == target {
			return true
		}
	}
	return false
}

// findMinSpan finds the minimum window containing positions from all terms, within maxDist.
func findMinSpan(terms []string, termPosMap map[int][]uint32, maxDist int) int {
	if len(terms) < 2 {
		return 0
	}

	// Simple approach: check all combinations of first and last term positions
	firstPositions := termPosMap[0]
	lastPositions := termPosMap[len(terms)-1]

	bestSpan := -1
	for _, fp := range firstPositions {
		for _, lp := range lastPositions {
			var span int
			if lp >= fp {
				span = int(lp - fp)
			} else {
				span = int(fp - lp)
			}
			if span <= maxDist {
				// Verify all middle terms have positions within this window
				minPos := fp
				maxPos := lp
				if minPos > maxPos {
					minPos, maxPos = maxPos, minPos
				}
				allFound := true
				for termIdx := 1; termIdx < len(terms)-1; termIdx++ {
					found := false
					for _, p := range termPosMap[termIdx] {
						if p >= minPos && p <= maxPos {
							found = true
							break
						}
					}
					if !found {
						allFound = false
						break
					}
				}
				if allFound && (bestSpan < 0 || span < bestSpan) {
					bestSpan = span
				}
			}
		}
	}
	return bestSpan
}
