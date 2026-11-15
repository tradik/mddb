package main

import (
	"errors"
	"net/http"

	"mddb/internal/storage"
)

// Conditional create (#423) and conditional delete (#424).
//
// An empty expectedRevision has always meant "no condition", and a non-empty
// one fails for a document that does not exist — so a client could say
// "only if unchanged" but never "only if new". Two editors creating the same
// article at once overwrote each other; reading first left a round trip in
// which the other write could land. ifAbsent is that condition, checked in
// the write transaction like the revision, so nothing can land in between.

// errConflictingConditions is a write that asks for a document that does not
// exist and for one at a given revision at once: no document satisfies both.
var errConflictingConditions = errors.New("ifAbsent and expectedRevision cannot be combined")

// writeCondition is what a write requires of the stored document. The zero
// value is no condition.
type writeCondition struct {
	// ExpectedRevision: the document must be at this revision (INT-021).
	ExpectedRevision string
	// IfAbsent: the document must not exist (#423).
	IfAbsent bool
}

// validate refuses a condition no document can satisfy, before any
// transaction is opened.
func (c writeCondition) validate() error {
	if c.IfAbsent && c.ExpectedRevision != "" {
		return errConflictingConditions
	}
	return nil
}

// check fails the write unless existing satisfies the condition. existing is
// the stored document, the zero Doc when there is none; now decides whether
// it has expired. An expired document counts as absent: a read no longer
// returns it and an unconditional write replaces it, so "create only if new"
// must not be refused by it.
func (c writeCondition) check(existing *storage.Doc, now int64) error {
	if !c.IfAbsent {
		return checkRevision(existing, c.ExpectedRevision)
	}
	if existing.ExpiresAt > 0 && existing.ExpiresAt < now {
		return nil
	}
	if cur := docRevision(existing); cur != "" {
		return &RevisionConflictError{Current: cur}
	}
	return nil
}

// writeDeleteError answers a failed delete. A lost conditional delete is the
// same 409 as a lost update; every other failure keeps the answer a delete
// has always given, the 404 body of a missing document included.
func writeDeleteError(w http.ResponseWriter, err error) {
	var conflict *RevisionConflictError
	if errors.As(err, &conflict) {
		writeDocWriteError(w, err)
		return
	}
	bad(w, err)
}
