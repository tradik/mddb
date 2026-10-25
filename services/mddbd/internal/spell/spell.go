package spell

import (
	"encoding/binary"
	"mddb/internal/binlog"
	"mddb/internal/boltx"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"

	bolt "go.etcd.io/bbolt"

	"github.com/agnivade/levenshtein"
)

var bucketSpellDicts = []byte("spelldicts")

// SpellSuggestion is a correction candidate for a single token.
type SpellSuggestion struct {
	Original   string  `json:"original"`
	Corrected  string  `json:"corrected"`
	Confidence float64 `json:"confidence"`
}

// SpellCorrectionInfo carries spell-correction metadata in FTS responses.
type SpellCorrectionInfo struct {
	Original  string `json:"original"`
	Corrected string `json:"corrected"`
}

// spellModel is an in-memory frequency dictionary for one language+collection key.
type spellModel struct {
	mu    sync.RWMutex
	words map[string]uint32 // word -> frequency
}

func newSpellModel() *spellModel {
	return &spellModel{words: make(map[string]uint32)}
}

// train adds or updates a word's frequency count.
func (m *spellModel) train(word string, freq uint32) {
	m.mu.Lock()
	m.words[word] += freq
	m.mu.Unlock()
}

// suggest returns the best correction for a single token (max edit distance 2).
// Returns the original token unchanged if no better candidate is found.
func (m *spellModel) suggest(token string) (SpellSuggestion, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	lower := strings.ToLower(token)
	if _, ok := m.words[lower]; ok {
		return SpellSuggestion{}, false // already a known word
	}

	type candidate struct {
		word string
		dist int
		freq uint32
	}
	var best candidate
	found := false

	for w, freq := range m.words {
		dist := levenshtein.ComputeDistance(lower, w)
		if dist > 2 {
			continue
		}
		if !found || dist < best.dist || (dist == best.dist && freq > best.freq) {
			best = candidate{word: w, dist: dist, freq: freq}
			found = true
		}
	}

	if !found || best.dist == 0 {
		return SpellSuggestion{}, false
	}

	// Confidence: inversely proportional to edit distance, weighted by freq
	confidence := 1.0 - float64(best.dist)*0.35
	if confidence < 0.1 {
		confidence = 0.1
	}

	return SpellSuggestion{
		Original:   token,
		Corrected:  best.word,
		Confidence: confidence,
	}, true
}

// SpellManager manages per-language and per-collection spell-check dictionaries.
type SpellManager struct {
	db     *bolt.DB
	follow func() *bolt.DB // set by FollowDB: the server's current handle after a restore
	mu     sync.RWMutex
	models map[string]*spellModel // key: lang or "col:collection:lang"
	ready  atomic.Bool
	binlog *binlog.Binlog
}

// FollowDB makes it use the database src returns — the server's current
// handle — so a restore or replication snapshot does not leave it on the
// closed old one.
func (sm *SpellManager) FollowDB(src func() *bolt.DB) { sm.follow = src }

func (sm *SpellManager) handle() *bolt.DB { return boltx.Current(sm.db, sm.follow) }

// NewSpellManager creates a SpellManager backed by BoltDB.
func NewSpellManager(db *bolt.DB) *SpellManager {
	return &SpellManager{
		db:     db,
		models: make(map[string]*spellModel),
	}
}

// SetBinlog attaches a binlog for replication.
func (sm *SpellManager) SetBinlog(bl *binlog.Binlog) {
	sm.binlog = bl
}

// EnsureBucket creates the spelldicts bucket if needed.
func (sm *SpellManager) EnsureBucket() error {
	return boltx.EnsureBuckets(sm.handle(), bucketSpellDicts)
}

// Ready reports whether the initial dictionary load has completed.
func (sm *SpellManager) Ready() bool {
	return sm.ready.Load()
}

// LoadAll reads all stored words from BoltDB into memory asynchronously.
func (sm *SpellManager) LoadAll() {
	go func() {
		_ = sm.handle().View(func(tx *bolt.Tx) error {
			b := tx.Bucket(bucketSpellDicts)
			if b == nil {
				return nil
			}
			return b.ForEach(func(k, v []byte) error {
				ks := string(k)
				parts := strings.SplitN(ks, "|", 3) // lang|word  or  col|collection|lang|word
				if len(parts) < 2 {
					return nil
				}
				var modelKey, word string
				if parts[0] == "col" && len(parts) == 3 {
					// collection-specific: "col|{collection}|{lang}|{word}" but SplitN(3) gives ["col", "collection", "lang|word"]
					sub := strings.SplitN(parts[2], "|", 2)
					if len(sub) != 2 {
						return nil
					}
					modelKey = "col:" + parts[1] + ":" + sub[0]
					word = sub[1]
				} else {
					// global language dict: "{lang}|{word}"
					modelKey = parts[0]
					word = parts[1]
				}
				var freq uint32
				if len(v) == 4 {
					freq = binary.LittleEndian.Uint32(v)
				}
				sm.getOrCreate(modelKey).train(word, freq)
				return nil
			})
		})
		sm.ready.Store(true)
	}()
}

// getOrCreate returns (creating if needed) the model for the given key.
func (sm *SpellManager) getOrCreate(key string) *spellModel {
	sm.mu.RLock()
	m, ok := sm.models[key]
	sm.mu.RUnlock()
	if ok {
		return m
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if m, ok = sm.models[key]; ok {
		return m
	}
	m = newSpellModel()
	sm.models[key] = m
	return m
}

// modelKey returns the lookup key for a language, optionally scoped to a collection.
func modelKey(collection, lang string) string {
	if collection != "" {
		return "col:" + collection + ":" + lang
	}
	return lang
}

// AddWord persists and trains a word. Pass collection="" for a global lang dict.
func (sm *SpellManager) AddWord(collection, lang, word string, freq uint32) error {
	lower := strings.ToLower(strings.TrimSpace(word))
	if lower == "" || lang == "" {
		return nil
	}
	var dbKey string
	if collection != "" {
		dbKey = "col|" + collection + "|" + lang + "|" + lower
	} else {
		dbKey = lang + "|" + lower
	}
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], freq)

	err := sm.handle().Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSpellDicts)
		if b == nil {
			return nil
		}
		existing := b.Get([]byte(dbKey))
		if len(existing) == 4 {
			old := binary.LittleEndian.Uint32(existing)
			binary.LittleEndian.PutUint32(buf[:], old+freq)
		}
		return b.Put([]byte(dbKey), buf[:])
	})
	if err != nil {
		return err
	}
	key := modelKey(collection, lang)
	sm.getOrCreate(key).train(lower, freq)
	return nil
}

// RemoveWord removes a word from the dictionary.
func (sm *SpellManager) RemoveWord(collection, lang, word string) error {
	lower := strings.ToLower(strings.TrimSpace(word))
	if lower == "" || lang == "" {
		return nil
	}
	var dbKey string
	if collection != "" {
		dbKey = "col|" + collection + "|" + lang + "|" + lower
	} else {
		dbKey = lang + "|" + lower
	}
	err := sm.handle().Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSpellDicts)
		if b == nil {
			return nil
		}
		return b.Delete([]byte(dbKey))
	})
	if err != nil {
		return err
	}
	key := modelKey(collection, lang)
	sm.mu.RLock()
	m, ok := sm.models[key]
	sm.mu.RUnlock()
	if ok {
		m.mu.Lock()
		delete(m.words, lower)
		m.mu.Unlock()
	}
	return nil
}

// ListWords returns all words stored for a collection+lang combination.
func (sm *SpellManager) ListWords(collection, lang string) ([]string, error) {
	var words []string
	err := sm.handle().View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSpellDicts)
		if b == nil {
			return nil
		}
		var prefix string
		if collection != "" {
			prefix = "col|" + collection + "|" + lang + "|"
		} else {
			prefix = lang + "|"
		}
		c := b.Cursor()
		for k, _ := c.Seek([]byte(prefix)); k != nil; k, _ = c.Next() {
			ks := string(k)
			if len(ks) < len(prefix) || ks[:len(prefix)] != prefix {
				break
			}
			words = append(words, ks[len(prefix):])
		}
		return nil
	})
	return words, err
}

// Suggest returns token-level correction suggestions for a text string.
// It looks up the collection-specific model first, then falls back to the
// global language model.
func (sm *SpellManager) Suggest(collection, lang, text string, maxSuggestions int) (string, []SpellSuggestion) {
	if maxSuggestions <= 0 {
		maxSuggestions = 3
	}
	tokens := tokenizeForSpell(text)
	var suggestions []SpellSuggestion
	correctedParts := make([]string, len(tokens))

	colModel := sm.getOrCreate(modelKey(collection, lang))
	globalModel := sm.getOrCreate(modelKey("", lang))

	for i, tok := range tokens {
		correctedParts[i] = tok
		if !isSpellableToken(tok) {
			continue
		}
		// Prefer collection-specific model, fall back to global
		if sug, ok := colModel.suggest(tok); ok {
			correctedParts[i] = sug.Corrected
			suggestions = append(suggestions, sug)
			if len(suggestions) >= maxSuggestions {
				break
			}
			continue
		}
		if sug, ok := globalModel.suggest(tok); ok {
			correctedParts[i] = sug.Corrected
			suggestions = append(suggestions, sug)
			if len(suggestions) >= maxSuggestions {
				break
			}
		}
	}

	return strings.Join(correctedParts, " "), suggestions
}

// Cleanup applies the best correction to each token and returns the cleaned text.
func (sm *SpellManager) Cleanup(collection, lang, text string) string {
	corrected, _ := sm.Suggest(collection, lang, text, 50)
	return corrected
}

// tokenizeForSpell splits text into word tokens, preserving order.
func tokenizeForSpell(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r)
	})
}

// isSpellableToken returns true for tokens that look like natural-language words
// (not numbers, not URLs, not short abbreviations).
func isSpellableToken(tok string) bool {
	if len(tok) < 3 {
		return false
	}
	for _, r := range tok {
		if unicode.IsDigit(r) || r == '/' || r == ':' || r == '.' || r == '@' {
			return false
		}
	}
	return true
}
