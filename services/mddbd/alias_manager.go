package main

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"mddb/internal/binlog"
	"mddb/internal/boltx"

	bolt "go.etcd.io/bbolt"
)

// Collection aliases (GO-044): a stable name that points at a collection
// and can be moved to another one at once — build docs-v2 beside docs-v1,
// then flip "docs" to it, and no client changes a name.
//
// An alias is resolved where a request enters (REST, gRPC, MCP, GraphQL),
// before anything reads the collection name, so a request resolves it once:
// it runs entirely against the old collection or entirely against the new
// one, never both. Permissions are checked on the resolved name, so an
// alias carries its target's permissions.

var bucketAliases = []byte("aliases")

// maxAliasLen bounds an alias name.
const maxAliasLen = 128

// AliasManager holds alias → collection, cached in memory, stored in the
// main database (and in the binlog, so followers get the same aliases).
type AliasManager struct {
	db     *bolt.DB
	follow func() *bolt.DB
	binlog *binlog.Binlog
	mu     sync.RWMutex
	m      map[string]string
}

// NewAliasManager creates the manager; call LoadAll before use.
func NewAliasManager(db *bolt.DB) *AliasManager {
	return &AliasManager{db: db, m: map[string]string{}}
}

// FollowDB makes it use the server's current handle after a restore.
func (a *AliasManager) FollowDB(src func() *bolt.DB) { a.follow = src }

// SetBinlog records alias writes for replication.
func (a *AliasManager) SetBinlog(bl *binlog.Binlog) { a.binlog = bl }

func (a *AliasManager) handle() *bolt.DB { return boltx.Current(a.db, a.follow) }

// LoadAll reads every alias into memory.
func (a *AliasManager) LoadAll() error {
	m := map[string]string{}
	err := a.handle().View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAliases)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			m[string(k)] = string(v)
			return nil
		})
	})
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.m = m
	a.mu.Unlock()
	return nil
}

// Resolve returns the collection a name points at: the alias target, or the
// name itself when it is not an alias. Safe on a nil manager.
func (a *AliasManager) Resolve(name string) string {
	if a == nil || name == "" {
		return name
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if target, ok := a.m[name]; ok {
		return target
	}
	return name
}

// active reports whether any alias exists, so request paths skip the work
// of looking when there are none.
func (a *AliasManager) active() bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.m) > 0
}

// List returns every alias, sorted by name.
func (a *AliasManager) List() []AliasEntry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]AliasEntry, 0, len(a.m))
	for name, target := range a.m {
		out = append(out, AliasEntry{Alias: name, Collection: target})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out
}

// AliasEntry is one alias and the collection it points at.
type AliasEntry struct {
	Alias      string `json:"alias"`
	Collection string `json:"collection"`
}

// errAliasNotFound: deleting an alias that does not exist.
var errAliasNotFound = errors.New("alias not found")

// validAliasName rejects names that would collide with key syntax or with
// the role: and * target selectors.
func validAliasName(name string) error {
	switch {
	case name == "" || len(name) > maxAliasLen:
		return fmt.Errorf("alias name must be 1 to %d characters", maxAliasLen)
	case strings.ContainsAny(name, "|/ "), name == "*", strings.HasPrefix(name, rolePrefix):
		return fmt.Errorf("alias name %q may not contain '|', '/' or spaces, be '*' or start with %q", name, rolePrefix)
	}
	return nil
}

// set stores or moves an alias. exists reports whether a name is a
// collection; the flip is one write under the lock, so a reader sees the old
// target or the new one.
func (a *AliasManager) set(alias, collection string, exists func(string) bool) error {
	if err := validAliasName(alias); err != nil {
		return err
	}
	if collection == "" {
		return errors.New("collection is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, isAlias := a.m[collection]; isAlias {
		return fmt.Errorf("%q is an alias; point an alias at a collection", collection)
	}
	if _, isAlias := a.m[alias]; !isAlias && exists(alias) {
		return fmt.Errorf("%q is a collection; an alias cannot take its name", alias)
	}
	if err := a.handle().Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketAliases)
		if err != nil {
			return err
		}
		return b.Put([]byte(alias), []byte(collection))
	}); err != nil {
		return err
	}
	if a.binlog != nil {
		_ = a.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: string(bucketAliases),
			Key: []byte(alias), Value: []byte(collection)})
	}
	a.m[alias] = collection
	return nil
}

// remove deletes an alias; the collection it pointed at is untouched.
func (a *AliasManager) remove(alias string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.m[alias]; !ok {
		return errAliasNotFound
	}
	if err := a.handle().Update(func(tx *bolt.Tx) error {
		if b := tx.Bucket(bucketAliases); b != nil {
			return b.Delete([]byte(alias))
		}
		return nil
	}); err != nil {
		return err
	}
	if a.binlog != nil {
		_ = a.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: string(bucketAliases), Key: []byte(alias)})
	}
	delete(a.m, alias)
	return nil
}

// collectionExists reports whether a collection has a configuration or at
// least one document.
func (s *Server) collectionExists(name string) bool {
	if s.CollectionManager != nil {
		if _, ok := s.CollectionManager.Get(name); ok {
			return true
		}
	}
	found := false
	_ = s.DBViewC(name, func(tx *bolt.Tx) error {
		if b := tx.Bucket([]byte("docs")); b != nil {
			prefix := []byte("doc|" + name + "|")
			k, _ := b.Cursor().Seek(prefix)
			found = k != nil && bytes.HasPrefix(k, prefix)
		}
		return nil
	})
	return found
}

// SetAlias points alias at collection (GO-044).
func (s *Server) SetAlias(alias, collection string) error {
	if s.Aliases == nil {
		return errors.New("aliases are not available")
	}
	return s.Aliases.set(alias, collection, s.collectionExists)
}

// DeleteAlias removes an alias.
func (s *Server) DeleteAlias(alias string) error {
	if s.Aliases == nil {
		return errors.New("aliases are not available")
	}
	return s.Aliases.remove(alias)
}
