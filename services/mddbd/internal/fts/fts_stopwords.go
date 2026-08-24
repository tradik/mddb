package fts

import (
	"fmt"
	"mddb/internal/binlog"
	"sort"
	"strings"
	"sync"

	bolt "go.etcd.io/bbolt"
)

var bucketStopWords = []byte("stopwords")

// StopWordManager manages per-collection custom stop words on top of defaults.
type StopWordManager struct {
	db           *bolt.DB
	mu           sync.RWMutex
	cache        map[string]map[string]bool // collection -> word -> true (custom only)
	binlog       *binlog.Binlog
	langRegistry *LangRegistry
}

// NewStopWordManager creates a new stop word manager.
func NewStopWordManager(db *bolt.DB) *StopWordManager {
	return &StopWordManager{
		db:    db,
		cache: make(map[string]map[string]bool),
	}
}

// SetBinlog sets the binlog for replication logging.
func (swm *StopWordManager) SetBinlog(bl *binlog.Binlog) {
	swm.binlog = bl
}

// SetLangRegistry sets the language registry for multi-language stop word support.
func (swm *StopWordManager) SetLangRegistry(r *LangRegistry) {
	swm.langRegistry = r
}

// EnsureBucket creates the stopwords bucket if it doesn't exist.
// Reload points the manager at a freshly swapped database handle after a
// restore and rebuilds its cache from it (SEC-015/SEC-016).
func (swm *StopWordManager) Reload(db *bolt.DB) error {
	swm.mu.Lock()
	swm.db = db
	swm.mu.Unlock()
	return swm.LoadAll()
}

func (swm *StopWordManager) EnsureBucket() error {
	return swm.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketStopWords)
		return err
	})
}

// LoadAll loads all custom stop word entries from BoltDB into the in-memory cache.
func (swm *StopWordManager) LoadAll() error {
	swm.mu.Lock()
	defer swm.mu.Unlock()

	swm.cache = make(map[string]map[string]bool)

	return swm.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketStopWords)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			key := string(k)
			parts := strings.SplitN(key, "|", 3)
			if len(parts) != 3 || parts[0] != "sw" {
				return nil
			}
			collection := parts[1]
			word := parts[2]

			if swm.cache[collection] == nil {
				swm.cache[collection] = make(map[string]bool)
			}
			swm.cache[collection][word] = true
			return nil
		})
	})
}

// Add adds custom stop words for a collection.
func (swm *StopWordManager) Add(collection string, words []string) error {
	if collection == "" {
		return fmt.Errorf("collection is required")
	}

	normalized := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		if w != "" {
			normalized = append(normalized, w)
		}
	}
	if len(normalized) == 0 {
		return fmt.Errorf("words list cannot be empty")
	}

	var bo binlog.BinlogOps
	err := swm.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketStopWords)
		for _, w := range normalized {
			key := swKey(collection, w)
			bo.Put("stopwords", key, []byte("1"))
			if err := b.Put(key, []byte("1")); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	bo.FlushTo(swm.binlog)

	swm.mu.Lock()
	if swm.cache[collection] == nil {
		swm.cache[collection] = make(map[string]bool)
	}
	for _, w := range normalized {
		swm.cache[collection][w] = true
	}
	swm.mu.Unlock()

	return nil
}

// Delete removes a custom stop word for a collection.
func (swm *StopWordManager) Delete(collection, word string) error {
	word = strings.ToLower(strings.TrimSpace(word))
	if collection == "" || word == "" {
		return fmt.Errorf("collection and word are required")
	}

	// Reject deleting default stop words
	if defaultStopWords[word] {
		return fmt.Errorf("cannot delete default stop word: %s", word)
	}

	key := swKey(collection, word)
	var bo binlog.BinlogOps
	err := swm.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketStopWords)
		bo.Delete("stopwords", key)
		return b.Delete(key)
	})
	if err != nil {
		return err
	}
	bo.FlushTo(swm.binlog)

	swm.mu.Lock()
	if coll, ok := swm.cache[collection]; ok {
		delete(coll, word)
	}
	swm.mu.Unlock()

	return nil
}

// IsStopWord checks if a word is a stop word (default or custom) for a collection.
func (swm *StopWordManager) IsStopWord(collection, word string) bool {
	if defaultStopWords[word] {
		return true
	}
	swm.mu.RLock()
	defer swm.mu.RUnlock()
	if coll, ok := swm.cache[collection]; ok {
		return coll[word]
	}
	return false
}

// List returns all stop words for a collection (defaults + custom).
func (swm *StopWordManager) List(collection string) (defaults []string, custom []string) {
	// Defaults
	defaults = make([]string, 0, len(defaultStopWords))
	for w := range defaultStopWords {
		defaults = append(defaults, w)
	}
	sort.Strings(defaults)

	// Custom
	swm.mu.RLock()
	defer swm.mu.RUnlock()
	if coll, ok := swm.cache[collection]; ok {
		custom = make([]string, 0, len(coll))
		for w := range coll {
			custom = append(custom, w)
		}
		sort.Strings(custom)
	}
	return
}

// ListLang returns stop words for a collection using language-specific defaults.
func (swm *StopWordManager) ListLang(collection, lang string) (defaults []string, custom []string, resolvedLang string) {
	// Determine default stop words based on language
	defaultSW := defaultStopWords
	resolvedLang = "en"
	if swm.langRegistry != nil && lang != "" {
		cfg := swm.langRegistry.Resolve(lang)
		if cfg != nil && cfg.StopWords != nil {
			defaultSW = cfg.StopWords
			resolvedLang = cfg.Code
		}
	}

	defaults = make([]string, 0, len(defaultSW))
	for w := range defaultSW {
		defaults = append(defaults, w)
	}
	sort.Strings(defaults)

	// Custom
	swm.mu.RLock()
	defer swm.mu.RUnlock()
	if coll, ok := swm.cache[collection]; ok {
		custom = make([]string, 0, len(coll))
		for w := range coll {
			custom = append(custom, w)
		}
		sort.Strings(custom)
	}
	return
}

// swKey builds the stop word BoltDB key.
func swKey(collection, word string) []byte {
	return []byte(fmt.Sprintf("sw|%s|%s", collection, word))
}

// --- HTTP handlers ---
