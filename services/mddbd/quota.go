package main

import (
	"context"
	"errors"
	"fmt"
)

// Per-collection quotas (GO-043).
//
// A collection with no quota behaves exactly as before: nothing here is
// consulted on its writes beyond one map lookup. A quota caps what one
// collection may hold — documents, stored bytes, vectors — and how much
// embedding work it may cause per UTC day. Storage limits refuse the write
// (507 Insufficient Storage); the embedding budget never refuses a document,
// it parks the embedding work until the budget resets (see
// embedding_budget.go). The write gate itself is in quota_gate.go.

// CollectionQuota caps what a collection may use. A zero field is no limit.
type CollectionQuota struct {
	// MaxDocuments is the number of documents the collection may hold.
	MaxDocuments int64 `json:"maxDocuments,omitempty"`
	// MaxBytes is the stored size of the collection's documents, as written
	// to the docs bucket (after compression and encryption); revisions and
	// indexes are not counted.
	MaxBytes int64 `json:"maxBytes,omitempty"`
	// MaxVectors is the number of chunk vectors the collection may hold. New
	// documents are refused once it is reached.
	MaxVectors int64 `json:"maxVectors,omitempty"`
	// EmbeddingCallsPerDay is the number of embedding provider calls the
	// collection's documents may cause per UTC day.
	EmbeddingCallsPerDay int64 `json:"embeddingCallsPerDay,omitempty"`
	// EmbeddingTokensPerDay is the estimated tokens (characters / 4) sent to
	// the embedding provider for the collection per UTC day.
	EmbeddingTokensPerDay int64 `json:"embeddingTokensPerDay,omitempty"`
}

// errQuotaAdminOnly refuses a quota change by anyone but a global admin: a
// tenant able to raise its own quota would not have one.
var errQuotaAdminOnly = errors.New("forbidden: only a global admin may change a collection quota")

// Validate refuses negative limits.
func (q *CollectionQuota) Validate() error {
	if q == nil {
		return nil
	}
	for name, v := range map[string]int64{
		"maxDocuments": q.MaxDocuments, "maxBytes": q.MaxBytes, "maxVectors": q.MaxVectors,
		"embeddingCallsPerDay": q.EmbeddingCallsPerDay, "embeddingTokensPerDay": q.EmbeddingTokensPerDay,
	} {
		if v < 0 {
			return fmt.Errorf("invalid quota.%s: must be >= 0 (0 = unlimited)", name)
		}
	}
	return nil
}

// isZero reports a quota that limits nothing (nil included).
func (q *CollectionQuota) isZero() bool {
	return !q.limitsStorage() && !q.limitsEmbedding()
}

// limitsStorage reports whether any limit is checked on a document write.
func (q *CollectionQuota) limitsStorage() bool {
	return q != nil && (q.MaxDocuments > 0 || q.MaxBytes > 0 || q.MaxVectors > 0)
}

// limitsEmbedding reports whether the collection has an embedding budget.
func (q *CollectionQuota) limitsEmbedding() bool {
	return q != nil && (q.EmbeddingCallsPerDay > 0 || q.EmbeddingTokensPerDay > 0)
}

// equal compares two quotas, nil and zero being the same.
func (q *CollectionQuota) equal(o *CollectionQuota) bool {
	if q.isZero() || o.isZero() {
		return q.isZero() && o.isZero()
	}
	return *q == *o
}

// collectionQuota is the stored quota of a collection, nil when it has none.
func (s *Server) collectionQuota(collection string) *CollectionQuota {
	if s.CollectionManager == nil {
		return nil
	}
	if cfg, ok := s.CollectionManager.Get(collection); ok && cfg != nil {
		return cfg.Quota
	}
	return nil
}

// checkQuotaChange validates a requested quota and refuses a change by anyone
// but a global admin when authentication is on. next == nil means "not sent"
// and is always allowed: the stored quota is kept (CollectionManager.Set).
func (s *Server) checkQuotaChange(ctx context.Context, collection string, next *CollectionQuota) error {
	if next == nil {
		return nil
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if s.AuthManager == nil || !s.AuthManager.IsEnabled() || next.equal(s.collectionQuota(collection)) {
		return nil
	}
	if claims, ok := GetClaimsFromContext(ctx); ok && claims.Admin {
		return nil
	}
	return errQuotaAdminOnly
}
