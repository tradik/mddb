package main

import (
	"bytes"
	"sort"
	"time"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// The tenant side of the write gate (GO-054). checkWriteQuota asks it when
// the collection being written belongs to a tenant with a quota: the write is
// refused when the tenant's collections together would pass a limit, with the
// same QuotaError — and so the same 507 on every surface — as a collection
// quota, naming the tenant.
//
// The collection being written is counted exactly, inside the write's own
// transaction. The tenant's other collections are counted as of at most
// tenantUsageRefresh ago: recounting all of them on every write would make a
// tenant's writes cost as much as its size. Two writers adding to two
// different collections of one tenant at the same moment can therefore pass a
// tenant limit by what they add in that window.

// tenantUsageRefresh is how old the count of a tenant's other collections may
// be. A variable so tests need not wait.
var tenantUsageRefresh = 5 * time.Second

// quotaCollections is the tenant resource "collections holding documents".
const quotaCollections = "collections"

// recentUsage is a collection's usage and when it was counted.
type recentUsage struct {
	at    time.Time
	usage quotaUsage
}

// tenantUsage is what a set of a tenant's collections holds.
type tenantUsage struct {
	docs        int64
	bytes       int64
	collections int64 // collections holding documents, or configured
}

// tenantQuotaExceeded compares a write to collection with its tenant's quota.
// base is the collection's usage before the write and delta what it adds.
func (s *Server) tenantQuotaExceeded(tx *bolt.Tx, tenant, collection string, tq *TenantQuota, base, delta quotaUsage) *QuotaError {
	others := s.tenantOthers(tx, tenant, collection)
	docs, size := others.docs+base.docs, others.bytes+base.bytes
	qe := &QuotaError{Tenant: tenant, Collection: collection}
	switch {
	case tq.MaxCollections > 0 && base.docs == 0 && delta.docs > 0 && others.collections >= tq.MaxCollections:
		qe.Resource, qe.Limit, qe.Used = quotaCollections, tq.MaxCollections, others.collections
	case tq.MaxDocuments > 0 && delta.docs > 0 && docs+delta.docs > tq.MaxDocuments:
		qe.Resource, qe.Limit, qe.Used = quotaDocuments, tq.MaxDocuments, docs
	case tq.MaxBytes > 0 && delta.bytes > 0 && size+delta.bytes > tq.MaxBytes:
		qe.Resource, qe.Limit, qe.Used = quotaBytes, tq.MaxBytes, size
	case tq.MaxVectors > 0:
		n := s.tenantVectorCount(tx, tenant)
		if n < tq.MaxVectors {
			return nil
		}
		qe.Resource, qe.Limit, qe.Used = quotaVectors, tq.MaxVectors, n
	default:
		return nil
	}
	return qe
}

// tenantOthers is what the tenant's collections other than exclude hold.
func (s *Server) tenantOthers(tx *bolt.Tx, tenant, exclude string) tenantUsage {
	var t tenantUsage
	for _, c := range s.tenantCollections(tx, tenant) {
		if c == exclude {
			continue
		}
		u := s.usageNear(tx, c)
		t.docs += u.docs
		t.bytes += u.bytes
		if u.docs > 0 || s.collectionConfigured(c) {
			t.collections++
		}
	}
	return t
}

// collectionConfigured reports whether collection has a stored config.
func (s *Server) collectionConfigured(collection string) bool {
	if s.CollectionManager == nil {
		return false
	}
	_, ok := s.CollectionManager.Get(collection)
	return ok
}

// usageNear is a collection's usage, counted at most tenantUsageRefresh ago.
// A count is made with tx when the collection lives in tx's file, and with a
// read of its own file otherwise — never through DBViewC, whose lock the
// caller's write already holds.
func (s *Server) usageNear(tx *bolt.Tx, collection string) quotaUsage {
	qs := s.quotas()
	qs.mu.Lock()
	r, ok := qs.recent[collection]
	qs.mu.Unlock()
	if ok && time.Since(r.at) < tenantUsageRefresh {
		return r.usage
	}
	var u quotaUsage
	if db := s.collectionDB(collection); tx != nil && db == tx.DB() {
		u = scanUsage(tx, collection)
	} else {
		_ = db.View(func(rtx *bolt.Tx) error {
			u = scanUsage(rtx, collection)
			return nil
		})
	}
	qs.noteRecent(collection, u)
	return u
}

// noteRecent records a collection's usage as counted now.
func (qs *quotaState) noteRecent(collection string, u quotaUsage) {
	qs.mu.Lock()
	qs.recent[collection] = recentUsage{at: time.Now(), usage: u}
	qs.mu.Unlock()
}

// tenantCollections is every collection of the tenant's namespace the server
// knows: configured, in a file of its own, or holding documents in the main
// file (found by seeking from one collection's keys to the next, not by
// reading every document).
func (s *Server) tenantCollections(tx *bolt.Tx, tenant string) []string {
	seen := map[string]bool{}
	add := func(c string) {
		if CollectionInTenant(tenant, c) {
			seen[c] = true
		}
	}
	if s.CollectionManager != nil {
		for c := range s.CollectionManager.ListAll() {
			add(c)
		}
	}
	if cf := s.collectionStore(); cf != nil {
		for _, c := range cf.names() {
			add(c)
		}
	}
	collect := func(t *bolt.Tx) error {
		for _, c := range namespaceCollections(t, tenant) {
			add(c)
		}
		return nil
	}
	if tx != nil && tx.DB() == s.DB {
		_ = collect(tx)
	} else if s.DB != nil {
		_ = s.DB.View(collect)
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// namespaceCollections lists the collections of a tenant holding documents in
// tx's file, one seek per collection.
func namespaceCollections(tx *bolt.Tx, tenant string) []string {
	b := tx.Bucket([]byte("docs"))
	if b == nil {
		return nil
	}
	prefix := storage.DocKey(tenant+TenantSeparator, "")
	prefix = prefix[:len(prefix)-1] // "doc|acme/": every collection of acme
	var out []string
	c := b.Cursor()
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); {
		name, ok := statsCollection(k)
		if !ok {
			k, _ = c.Next()
			continue
		}
		out = append(out, string(name))
		// Past every key of this collection: "doc|<name>}" sorts after
		// "doc|<name>|<any id>".
		next := storage.DocKey(string(name), "")
		next[len(next)-1] = '|' + 1
		k, _ = c.Seek(next)
	}
	return out
}

// tenantVectorCount is the chunk vectors of all the tenant's collections.
func (s *Server) tenantVectorCount(tx *bolt.Tx, tenant string) int64 {
	var n int64
	for _, c := range s.tenantCollections(tx, tenant) {
		n += s.collectionVectorCount(c)
	}
	return n
}

// tenantVectorRAM estimates the bytes of the in-memory index the tenant's
// vectors take: its share of the vectors times the index's measured size.
// used is VectorIndex.MemoryBytes, passed in so it is read once per write.
func (s *Server) tenantVectorRAM(tenant string, used int64) int64 {
	var all, mine int64
	for _, c := range s.VectorIndex.Collections() {
		n := int64(s.VectorIndex.CollectionSize(c))
		all += n
		if CollectionInTenant(tenant, c) {
			mine += n
		}
	}
	if all == 0 {
		return 0
	}
	return int64(float64(used) * float64(mine) / float64(all))
}

// vectorRAMExceeded is the server's vector RAM limit, then the tenant's share
// of it (GO-054), for a write that adds vectors to RAM.
func (s *Server) vectorRAMExceeded(tenant string, tq *TenantQuota, limit int64) *QuotaError {
	used := s.VectorIndex.MemoryBytes()
	if used >= limit {
		return &QuotaError{Resource: quotaVectorRAM, Limit: limit, Used: used}
	}
	share := s.tenantDefaults().ramShareFor(tq)
	if tenant == "" || share <= 0 {
		return nil
	}
	tenantLimit := int64(share * float64(limit))
	if mine := s.tenantVectorRAM(tenant, used); mine >= tenantLimit {
		return &QuotaError{Tenant: tenant, Resource: quotaVectorRAM, Limit: tenantLimit, Used: mine}
	}
	return nil
}
