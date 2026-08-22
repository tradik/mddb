package fts

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"mddb/internal/binlog"
	"mddb/internal/sliceutil"
	"sort"
	"strings"
	"unicode"

	json "github.com/goccy/go-json"
	bolt "go.etcd.io/bbolt"
)

var (
	bucketFTS      = []byte("fts")
	bucketFTSRev   = []byte("ftsrev")
	bucketFTSF     = []byte("ftsf")
	bucketFTSFMeta = []byte("ftsfmeta")
	bucketFTSFStat = []byte("ftsfstat")
	bucketFTSFRev  = []byte("ftsfrev")
)

// FTSIndex provides full-text search using an inverted index in BoltDB.
type FTSIndex struct {
	db              *bolt.DB
	stopWords       map[string]bool
	binlog          *binlog.Binlog
	stemmer         Stemmer
	langRegistry    *LangRegistry
	synonymManager  *SynonymManager
	stopWordManager *StopWordManager
	pmiData         *PMIData
}

// SetStemmer sets the stemmer for term normalization.
func (f *FTSIndex) SetStemmer(s Stemmer) { f.stemmer = s }

// SetLangRegistry sets the language registry for multi-language FTS support.
func (f *FTSIndex) SetLangRegistry(r *LangRegistry) { f.langRegistry = r }

// SetSynonymManager sets the synonym manager for query expansion.
func (f *FTSIndex) SetSynonymManager(sm *SynonymManager) { f.synonymManager = sm }

// SetStopWordManager sets the stop word manager for per-collection custom stop words.
func (f *FTSIndex) SetStopWordManager(swm *StopWordManager) { f.stopWordManager = swm }

// SetBinlog sets the binlog for replication logging.
func (f *FTSIndex) SetBinlog(bl *binlog.Binlog) {
	f.binlog = bl
}

// Stemmer returns the active stemmer (nil when stemming is disabled).
func (f *FTSIndex) Stemmer() Stemmer { return f.stemmer }

// SynonymManager returns the active synonym manager (nil when disabled).
func (f *FTSIndex) SynonymManager() *SynonymManager { return f.synonymManager }

// LangRegistry returns the language registry (nil when multi-language FTS is off).
func (f *FTSIndex) LangRegistry() *LangRegistry { return f.langRegistry }

// FTSResult represents a full-text search result.
type FTSResult struct {
	DocID        string   `json:"docId"`
	Score        float64  `json:"score"`
	MatchedTerms []string `json:"matchedTerms"`
}

// fieldTermEntry is used in the field-level reverse index for BM25F cleanup.
type fieldTermEntry struct {
	Field string `json:"f"`
	Term  string `json:"t"`
}

// NewFTSIndex creates a new full-text search index.
func NewFTSIndex(db *bolt.DB) *FTSIndex {
	return &FTSIndex{
		db:        db,
		stopWords: defaultStopWords,
	}
}

// EnsureBuckets creates the FTS buckets if they don't exist.
func (f *FTSIndex) EnsureBuckets() error {
	return f.db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{
			bucketFTS, bucketFTSRev,
			bucketFTSF, bucketFTSFMeta, bucketFTSFStat, bucketFTSFRev,
			bucketFTSPos,
		} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
}

// Tokenize splits text into a frequency map of normalized terms.
// If a stemmer is configured, terms are stemmed after stop word filtering.
func (f *FTSIndex) Tokenize(text string) map[string]int {
	terms := make(map[string]int)
	text = strings.ToLower(text)

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
					terms[w]++
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
			terms[w]++
		}
	}
	return terms
}

// TokenizeQuery tokenizes query text and expands with synonyms.
func (f *FTSIndex) TokenizeQuery(collection, text string) map[string]int {
	terms := f.Tokenize(text)
	if f.synonymManager == nil {
		return terms
	}
	// Expand with synonyms
	expanded := make(map[string]int, len(terms)*2)
	for term, count := range terms {
		expanded[term] = count
		synonyms := f.synonymManager.Expand(collection, []string{term})
		for _, syn := range synonyms {
			if syn == term {
				continue
			}
			// Stem the synonym too
			stemmed := syn
			if f.stemmer != nil {
				stemmed = f.stemmer.Stem(syn)
			}
			if _, exists := expanded[stemmed]; !exists {
				expanded[stemmed] = 1
			}
		}
	}
	return expanded
}

// resolveLang returns the stemmer and stop words for the given language code.
// If no lang registry or language is configured, falls back to defaults.
func (f *FTSIndex) resolveLang(lang string) (Stemmer, map[string]bool) {
	if f.langRegistry != nil && lang != "" {
		cfg := f.langRegistry.Resolve(lang)
		if cfg != nil {
			return cfg.Stemmer, cfg.StopWords
		}
	}
	return f.stemmer, f.stopWords
}

// TokenizeLang tokenizes text using the stemmer and stop words for the given language.
func (f *FTSIndex) TokenizeLang(text, lang string) map[string]int {
	stemmer, stopWords := f.resolveLang(lang)
	terms := make(map[string]int)
	text = strings.ToLower(text)

	var word strings.Builder
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
					terms[w]++
				}
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
			terms[w]++
		}
	}
	return terms
}

// TokenizeQueryLang tokenizes query text with synonym expansion, language-aware.
func (f *FTSIndex) TokenizeQueryLang(collection, text, lang string) map[string]int {
	terms := f.TokenizeLang(text, lang)
	if f.synonymManager == nil {
		return terms
	}
	stemmer, _ := f.resolveLang(lang)
	expanded := make(map[string]int, len(terms)*2)
	for term, count := range terms {
		expanded[term] = count
		synonyms := f.synonymManager.Expand(collection, []string{term})
		for _, syn := range synonyms {
			if syn == term {
				continue
			}
			stemmed := syn
			if stemmer != nil {
				stemmed = stemmer.Stem(syn)
			}
			if _, exists := expanded[stemmed]; !exists {
				expanded[stemmed] = 1
			}
		}
	}
	return expanded
}

// IndexWithLang adds or updates the FTS index for a document using language-specific tokenization.
func (f *FTSIndex) IndexWithLang(collection, docID, content, lang string) error {
	terms := f.TokenizeLang(content, lang)
	if len(terms) == 0 {
		return nil
	}

	var bo binlog.BinlogOps
	err := f.db.Update(func(tx *bolt.Tx) error {
		return f.indexTermsInTx(tx, &bo, collection, docID, terms)
	})
	if err == nil {
		bo.FlushTo(f.binlog)
		f.InvalidatePMI(collection)
	}
	return err
}

// indexTermsInTx writes one document's inverted-index entries inside a caller's
// transaction. Split out of IndexWithLang so a bulk import can index many
// documents in a single transaction (GO-027) while running exactly the same
// logic — the batch path cannot drift from the single-document one because
// there is only one implementation.
func (f *FTSIndex) indexTermsInTx(tx *bolt.Tx, bo *binlog.BinlogOps, collection, docID string, terms map[string]int) error {
	bFTS := tx.Bucket(bucketFTS)
	bRev := tx.Bucket(bucketFTSRev)

	// Remove old entries via reverse index
	revKey := ftsRevKey(collection, docID)
	if old := bRev.Get(revKey); old != nil {
		oldTerms := strings.Split(string(old), ",")
		for _, term := range oldTerms {
			if term != "" {
				k := ftsKey(collection, term, docID)
				_ = bFTS.Delete(k)
				bo.Delete("fts", k)
			}
		}
	}

	// Store new entries
	termList := make([]string, 0, len(terms))
	for term, count := range terms {
		var buf [4]byte
		binary.LittleEndian.PutUint32(buf[:], uint32(count)) // #nosec G115 -- value always positive and bounded
		k := ftsKey(collection, term, docID)
		if err := bFTS.Put(k, buf[:]); err != nil {
			return err
		}
		bo.Put("fts", k, buf[:])
		termList = append(termList, term)
	}

	// Store reverse index. Sorted because the list comes from a Go map, whose
	// iteration order is randomised: without this, indexing the same document
	// twice writes different bytes for the same content, which defeats content
	// hashing and makes two replicas' indexes impossible to compare. Order
	// carries no meaning here — the list is only ever split to delete a
	// document's previous terms.
	sort.Strings(termList)
	revVal := []byte(strings.Join(termList, ","))
	bo.Put("ftsrev", revKey, revVal)
	if err := bRev.Put(revKey, revVal); err != nil {
		return err
	}

	// Store BM25 metadata
	var docLength uint32
	for _, count := range terms {
		docLength += uint32(count) // #nosec G115 -- value always positive and bounded
	}
	return f.IndexBM25Meta(tx, collection, docID, docLength)
}

// IndexFieldsWithLang indexes a document's fields using language-specific tokenization.
func (f *FTSIndex) IndexFieldsWithLang(collection, docID string, fields map[string]string, lang string) error {
	fieldTokens := make(map[string]map[string]int, len(fields))
	for field, text := range fields {
		tokens := f.TokenizeLang(text, lang)
		if len(tokens) > 0 {
			fieldTokens[field] = tokens
		}
	}
	if len(fieldTokens) == 0 {
		return nil
	}

	return f.db.Update(func(tx *bolt.Tx) error {
		return f.indexFieldTokensInTx(tx, collection, docID, fieldTokens)
	})
}

// indexFieldTokensInTx writes one document's per-field index entries inside a
// caller's transaction — the shared body behind IndexFieldsWithLang and the
// bulk path (GO-027).
func (f *FTSIndex) indexFieldTokensInTx(tx *bolt.Tx, collection, docID string, fieldTokens map[string]map[string]int) error {
	{
		if err := f.removeFieldData(tx, collection, docID); err != nil {
			return err
		}

		bF := tx.Bucket(bucketFTSF)
		bMeta := tx.Bucket(bucketFTSFMeta)
		bStat := tx.Bucket(bucketFTSFStat)
		bRev := tx.Bucket(bucketFTSFRev)
		if bF == nil || bMeta == nil || bStat == nil || bRev == nil {
			return nil
		}

		var allEntries []fieldTermEntry
		for field, tokens := range fieldTokens {
			var docLength uint32
			for term, count := range tokens {
				var buf [4]byte
				binary.LittleEndian.PutUint32(buf[:], uint32(count)) // #nosec G115 -- value always positive and bounded
				if err := bF.Put(ftsfKey(collection, field, term, docID), buf[:]); err != nil {
					return err
				}
				allEntries = append(allEntries, fieldTermEntry{Field: field, Term: term})
				docLength += uint32(count) // #nosec G115 -- value always positive and bounded
			}

			var buf [4]byte
			binary.LittleEndian.PutUint32(buf[:], docLength)
			if err := bMeta.Put(ftsfMetaKey(collection, field, docID), buf[:]); err != nil {
				return err
			}

			sk := ftsfStatKey(collection, field)
			stats := collectionStats{}
			if sraw := bStat.Get(sk); sraw != nil {
				stats = decodeCollectionStats(sraw)
			}
			stats.TotalDocs++
			stats.TotalTerms += uint64(docLength)
			if err := bStat.Put(sk, encodeCollectionStats(stats)); err != nil {
				return err
			}
		}

		// Sorted for the same reason as the term lists: identical input must
		// produce identical bytes.
		sort.Slice(allEntries, func(i, j int) bool {
			if allEntries[i].Field != allEntries[j].Field {
				return allEntries[i].Field < allEntries[j].Field
			}
			return allEntries[i].Term < allEntries[j].Term
		})
		revData, err := json.Marshal(allEntries)
		if err != nil {
			return err
		}
		return bRev.Put(ftsfRevKey(collection, docID), revData)
	}
}

// Index adds or updates the FTS index for a document.
func (f *FTSIndex) Index(collection, docID, content string) error {
	terms := f.Tokenize(content)
	if len(terms) == 0 {
		return nil
	}

	var bo binlog.BinlogOps
	err := f.db.Update(func(tx *bolt.Tx) error {
		bFTS := tx.Bucket(bucketFTS)
		bRev := tx.Bucket(bucketFTSRev)

		// Remove old entries via reverse index
		revKey := ftsRevKey(collection, docID)
		if old := bRev.Get(revKey); old != nil {
			oldTerms := strings.Split(string(old), ",")
			for _, term := range oldTerms {
				if term != "" {
					k := ftsKey(collection, term, docID)
					_ = bFTS.Delete(k)
					bo.Delete("fts", k)
				}
			}
		}

		// Store new entries
		termList := make([]string, 0, len(terms))
		for term, count := range terms {
			var buf [4]byte
			binary.LittleEndian.PutUint32(buf[:], uint32(count)) // #nosec G115 -- value always positive and bounded
			k := ftsKey(collection, term, docID)
			if err := bFTS.Put(k, buf[:]); err != nil {
				return err
			}
			bo.Put("fts", k, buf[:])
			termList = append(termList, term)
		}

		// Store reverse index, sorted for a stable byte representation —
		// see the note in indexTermsInTx.
		sort.Strings(termList)
		revVal := []byte(strings.Join(termList, ","))
		bo.Put("ftsrev", revKey, revVal)
		if err := bRev.Put(revKey, revVal); err != nil {
			return err
		}

		// Store BM25 metadata (document length = sum of term frequencies)
		var docLength uint32
		for _, count := range terms {
			docLength += uint32(count) // #nosec G115 -- value always positive and bounded
		}
		return f.IndexBM25Meta(tx, collection, docID, docLength)
	})
	if err == nil {
		bo.FlushTo(f.binlog)
		f.InvalidatePMI(collection)
	}
	return err
}

// Remove deletes all FTS entries for a document.
func (f *FTSIndex) Remove(collection, docID string) error {
	var bo binlog.BinlogOps
	err := f.db.Update(func(tx *bolt.Tx) error {
		bFTS := tx.Bucket(bucketFTS)
		bRev := tx.Bucket(bucketFTSRev)

		revKey := ftsRevKey(collection, docID)
		if old := bRev.Get(revKey); old != nil {
			oldTerms := strings.Split(string(old), ",")
			for _, term := range oldTerms {
				if term != "" {
					k := ftsKey(collection, term, docID)
					_ = bFTS.Delete(k)
					bo.Delete("fts", k)
				}
			}
		}
		// Clean up BM25 metadata
		if err := f.RemoveBM25Meta(tx, collection, docID); err != nil {
			return err
		}
		// Clean up field-level FTS data (BM25F)
		if err := f.removeFieldData(tx, collection, docID); err != nil {
			return err
		}
		// Clean up positional index
		f.removePositionsInTx(tx, collection, docID)
		bo.Delete("ftsrev", revKey)
		return bRev.Delete(revKey)
	})
	if err == nil {
		bo.FlushTo(f.binlog)
		f.InvalidatePMI(collection)
	}
	return err
}

// Search performs a full-text search and returns matching document IDs with scores.
func (f *FTSIndex) Search(collection, query string, limit int) ([]FTSResult, error) {
	queryTerms := f.TokenizeQuery(collection, query)
	if len(queryTerms) == 0 {
		return nil, nil
	}

	type docScore struct {
		id           string
		totalTF      float64
		matchedTerms []string
	}

	scores := make(map[string]*docScore)

	err := f.db.View(func(tx *bolt.Tx) error {
		bFTS := tx.Bucket(bucketFTS)
		if bFTS == nil {
			return nil
		}

		for term := range queryTerms {
			prefix := ftsKey(collection, term, "")
			c := bFTS.Cursor()
			for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
				// Extract docID from key
				docID := string(k[len(prefix):])
				if docID == "" {
					continue
				}

				tf := float64(1)
				if len(v) >= 4 {
					tf = float64(binary.LittleEndian.Uint32(v))
				}
				// Use log(1+tf) to dampen high frequency terms
				logTF := math.Log1p(tf)

				ds, ok := scores[docID]
				if !ok {
					ds = &docScore{id: docID}
					scores[docID] = ds
				}
				ds.totalTF += logTF
				ds.matchedTerms = append(ds.matchedTerms, term)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Score = (matched terms / total query terms) * average log TF
	queryTermCount := float64(len(queryTerms))
	results := make([]FTSResult, 0, len(scores))
	for _, ds := range scores {
		matchRatio := float64(len(ds.matchedTerms)) / queryTermCount
		avgTF := ds.totalTF / float64(len(ds.matchedTerms))
		score := matchRatio * (0.5 + 0.5*math.Min(avgTF/5.0, 1.0)) // normalize

		results = append(results, FTSResult{
			DocID:        ds.id,
			Score:        score,
			MatchedTerms: sliceutil.Unique(ds.matchedTerms),
		})
	}

	// Sort by score desc
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// ftsKey builds the FTS bucket key.
func ftsKey(collection, term, docID string) []byte {
	return []byte(fmt.Sprintf("fts|%s|%s|%s", collection, term, docID))
}

// ftsRevKey builds the FTS reverse lookup key.
func ftsRevKey(collection, docID string) []byte {
	return []byte(fmt.Sprintf("ftsrev|%s|%s", collection, docID))
}

// --- Field-level FTS key builders (BM25F) ---

func ftsfKey(collection, field, term, docID string) []byte {
	return []byte(fmt.Sprintf("ftsf|%s|%s|%s|%s", collection, field, term, docID))
}

func ftsfMetaKey(collection, field, docID string) []byte {
	return []byte(fmt.Sprintf("ftsfmeta|%s|%s|%s", collection, field, docID))
}

func ftsfStatKey(collection, field string) []byte {
	return []byte(fmt.Sprintf("ftsfstat|%s|%s", collection, field))
}

func ftsfRevKey(collection, docID string) []byte {
	return []byte(fmt.Sprintf("ftsfrev|%s|%s", collection, docID))
}

// removeFieldData removes all field-level FTS data for a document within a transaction.
func (f *FTSIndex) removeFieldData(tx *bolt.Tx, collection, docID string) error {
	bF := tx.Bucket(bucketFTSF)
	bRev := tx.Bucket(bucketFTSFRev)
	if bF == nil || bRev == nil {
		return nil
	}

	revKey := ftsfRevKey(collection, docID)
	old := bRev.Get(revKey)
	if old == nil {
		return nil
	}

	var entries []fieldTermEntry
	if err := json.Unmarshal(old, &entries); err != nil {
		return bRev.Delete(revKey)
	}

	// Collect unique fields and delete term entries
	fields := make(map[string]bool)
	for _, e := range entries {
		_ = bF.Delete(ftsfKey(collection, e.Field, e.Term, docID))
		fields[e.Field] = true
	}

	// Update per-field stats and delete metadata
	bMeta := tx.Bucket(bucketFTSFMeta)
	bStat := tx.Bucket(bucketFTSFStat)
	if bMeta != nil && bStat != nil {
		for field := range fields {
			mk := ftsfMetaKey(collection, field, docID)
			if raw := bMeta.Get(mk); len(raw) >= 4 {
				oldLen := binary.LittleEndian.Uint32(raw)
				sk := ftsfStatKey(collection, field)
				stats := collectionStats{}
				if sraw := bStat.Get(sk); sraw != nil {
					stats = decodeCollectionStats(sraw)
				}
				if stats.TotalDocs > 0 {
					stats.TotalDocs--
				}
				if stats.TotalTerms >= uint64(oldLen) {
					stats.TotalTerms -= uint64(oldLen)
				} else {
					stats.TotalTerms = 0
				}
				_ = bStat.Put(sk, encodeCollectionStats(stats))
			}
			_ = bMeta.Delete(mk)
		}
	}

	return bRev.Delete(revKey)
}

// IndexFields indexes a document's fields separately for BM25F scoring.
// Each field (e.g. "content", "meta.title") is tokenized and stored independently.
func (f *FTSIndex) IndexFields(collection, docID string, fields map[string]string) error {
	fieldTokens := make(map[string]map[string]int, len(fields))
	for field, text := range fields {
		tokens := f.Tokenize(text)
		if len(tokens) > 0 {
			fieldTokens[field] = tokens
		}
	}
	if len(fieldTokens) == 0 {
		return nil
	}

	return f.db.Update(func(tx *bolt.Tx) error {
		// Remove old field data first (handles re-indexing)
		if err := f.removeFieldData(tx, collection, docID); err != nil {
			return err
		}

		bF := tx.Bucket(bucketFTSF)
		bMeta := tx.Bucket(bucketFTSFMeta)
		bStat := tx.Bucket(bucketFTSFStat)
		bRev := tx.Bucket(bucketFTSFRev)
		if bF == nil || bMeta == nil || bStat == nil || bRev == nil {
			return nil
		}

		var allEntries []fieldTermEntry
		for field, tokens := range fieldTokens {
			var docLength uint32
			for term, count := range tokens {
				var buf [4]byte
				binary.LittleEndian.PutUint32(buf[:], uint32(count)) // #nosec G115 -- value always positive and bounded
				if err := bF.Put(ftsfKey(collection, field, term, docID), buf[:]); err != nil {
					return err
				}
				allEntries = append(allEntries, fieldTermEntry{Field: field, Term: term})
				docLength += uint32(count) // #nosec G115 -- value always positive and bounded
			}

			// Store per-field doc length
			var buf [4]byte
			binary.LittleEndian.PutUint32(buf[:], docLength)
			if err := bMeta.Put(ftsfMetaKey(collection, field, docID), buf[:]); err != nil {
				return err
			}

			// Update per-field stats
			sk := ftsfStatKey(collection, field)
			stats := collectionStats{}
			if sraw := bStat.Get(sk); sraw != nil {
				stats = decodeCollectionStats(sraw)
			}
			stats.TotalDocs++
			stats.TotalTerms += uint64(docLength)
			if err := bStat.Put(sk, encodeCollectionStats(stats)); err != nil {
				return err
			}
		}

		// Store reverse index for cleanup
		// Sorted for the same reason as the term lists: identical input must
		// produce identical bytes.
		sort.Slice(allEntries, func(i, j int) bool {
			if allEntries[i].Field != allEntries[j].Field {
				return allEntries[i].Field < allEntries[j].Field
			}
			return allEntries[i].Term < allEntries[j].Term
		})
		revData, err := json.Marshal(allEntries)
		if err != nil {
			return err
		}
		return bRev.Put(ftsfRevKey(collection, docID), revData)
	})
}

// Default English stop words
var defaultStopWords = map[string]bool{
	"the": true, "be": true, "to": true, "of": true, "and": true,
	"in": true, "that": true, "have": true, "it": true, "for": true,
	"not": true, "on": true, "with": true, "he": true, "as": true,
	"you": true, "do": true, "at": true, "this": true, "but": true,
	"his": true, "by": true, "from": true, "they": true, "we": true,
	"say": true, "her": true, "she": true, "or": true, "an": true,
	"will": true, "my": true, "one": true, "all": true, "would": true,
	"there": true, "their": true, "what": true, "so": true, "up": true,
	"out": true, "if": true, "about": true, "who": true, "get": true,
	"which": true, "go": true, "me": true, "when": true, "make": true,
	"can": true, "like": true, "no": true, "just": true, "him": true,
	"know": true, "take": true, "come": true, "could": true, "than": true,
	"look": true, "use": true, "into": true, "some": true, "them": true,
	"see": true, "other": true, "then": true, "now": true, "only": true,
	"its": true, "also": true, "after": true, "way": true, "our": true,
	"how": true, "where": true, "most": true, "been": true, "is": true,
	"was": true, "are": true, "were": true, "had": true, "has": true,
	"did": true, "does": true, "am": true,
}
