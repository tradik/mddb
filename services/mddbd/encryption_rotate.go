package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mddb/internal/audit"
	"mddb/internal/encryption"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	json "mddb/internal/jsonx"
)

// Rotation job statuses.
const (
	RotationQueued    = "queued"
	RotationRunning   = "running"
	RotationCompleted = "completed"
	RotationFailed    = "failed"
)

// RotationJob is a single re-encryption pass. The struct is read by
// the HTTP status handler under RotationManager.mu, so its scalar
// fields are updated via atomics where the worker mutates them.
type RotationJob struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	PrimaryKey  byte   `json:"primaryKeyID"`
	StartedAt   int64  `json:"startedAt"`            // unix nanos
	FinishedAt  int64  `json:"finishedAt,omitempty"` // unix nanos
	Scanned     int64  `json:"scanned"`              // entries inspected (docs + rev)
	Reencrypted int64  `json:"reencrypted"`          // entries rewritten with primary key
	Skipped     int64  `json:"skipped"`              // already-primary or plaintext
	Errors      int64  `json:"errors"`
	LastError   string `json:"lastError,omitempty"`
}

// RotationManager coordinates background re-encryption. One job runs
// at a time — calling Start while a job is RotationRunning returns
// the existing job's ID.
type RotationManager struct {
	server    *Server
	encryptor *encryption.Encryptor
	mu        sync.Mutex
	jobs      map[string]*RotationJob
	current   *RotationJob // running job, if any
}

// NewRotationManager wires the manager. Encryptor must be non-nil
// because rotation is meaningless without it.
func NewRotationManager(s *Server, e *encryption.Encryptor) *RotationManager {
	return &RotationManager{
		server:    s,
		encryptor: e,
		jobs:      make(map[string]*RotationJob),
	}
}

// Status returns a snapshot of the encryption posture across the
// configured collections — useful for the admin UI dashboard.
type RotationStatus struct {
	Enabled      bool             `json:"enabled"`
	PrimaryKeyID byte             `json:"primaryKeyID"`
	PreviousIDs  []byte           `json:"previousKeyIDs,omitempty"`
	Collections  []CollectionStat `json:"collections"`
	CurrentJobID string           `json:"currentJobID,omitempty"`
}

// CollectionStat describes how a single collection is sealed today.
type CollectionStat struct {
	Collection  string `json:"collection"`
	Encrypted   bool   `json:"encrypted"`
	Total       int64  `json:"total"`
	WithPrimary int64  `json:"withPrimary"`
	WithLegacy  int64  `json:"withLegacy"`
	Plaintext   int64  `json:"plaintext"`
	UnknownKey  int64  `json:"unknownKey"`
}

// Status walks the docs bucket once and groups every entry by which
// key (if any) sealed it. Read-only; safe to call while the server
// serves traffic.
func (rm *RotationManager) Status() (*RotationStatus, error) {
	if rm == nil || rm.encryptor == nil {
		return &RotationStatus{Enabled: false}, nil
	}
	st := &RotationStatus{
		Enabled:      rm.encryptor.Enabled(),
		PrimaryKeyID: rm.encryptor.PrimaryKeyID(),
		PreviousIDs:  rm.encryptor.PreviousKeyIDs(),
	}

	rm.mu.Lock()
	if rm.current != nil {
		st.CurrentJobID = rm.current.ID
	}
	rm.mu.Unlock()

	per := make(map[string]*CollectionStat)
	err := rm.server.DBView(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("docs"))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			coll := collectionFromDocKey(k)
			if coll == "" {
				return nil
			}
			cs := per[coll]
			if cs == nil {
				cfg, _ := rm.server.CollectionManager.Get(coll)
				cs = &CollectionStat{Collection: coll, Encrypted: cfg != nil && cfg.Encrypted}
				per[coll] = cs
			}
			cs.Total++
			classifyEntry(v, rm.encryptor.PrimaryKeyID(), cs)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	for _, cs := range per {
		st.Collections = append(st.Collections, *cs)
	}
	return st, nil
}

// classifyEntry tallies one stored value into a CollectionStat.
func classifyEntry(v []byte, primary byte, cs *CollectionStat) {
	switch encryption.CiphertextVersion(v) {
	case 0:
		cs.Plaintext++
	case 1:
		cs.WithLegacy++ // V1 always treated as "needs migration"
	case 2:
		id, ok := encryption.CiphertextKeyID(v)
		if !ok {
			cs.UnknownKey++
			return
		}
		if id == primary {
			cs.WithPrimary++
		} else {
			cs.WithLegacy++
		}
	default:
		cs.UnknownKey++
	}
}

// collectionFromDocKey extracts the collection name from a key in the
// docs bucket. Document keys are formatted "doc|{collection}|{id}".
// Returns "" when the key does not match the pattern.
func collectionFromDocKey(k []byte) string {
	s := string(k)
	if len(s) < 5 || s[:4] != "doc|" {
		return ""
	}
	rest := s[4:]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '|' {
			return rest[:i]
		}
	}
	return ""
}

// Start launches a re-encryption job for one collection (or all
// encrypted collections when collection == "").
//
// Returns the job ID immediately; progress is observable via Get(id).
// Re-entrant call while a job is running returns the running job's ID.
func (rm *RotationManager) Start(ctx context.Context, collection string) (*RotationJob, error) {
	if rm == nil || rm.encryptor == nil || !rm.encryptor.Enabled() {
		return nil, errors.New("encryption not configured")
	}
	rm.mu.Lock()
	if rm.current != nil && rm.current.Status == RotationRunning {
		cp := *rm.current
		rm.mu.Unlock()
		return &cp, nil
	}
	job := &RotationJob{
		ID:         newRotationID(),
		Status:     RotationQueued,
		PrimaryKey: rm.encryptor.PrimaryKeyID(),
		StartedAt:  time.Now().UnixNano(),
	}
	rm.jobs[job.ID] = job
	rm.current = job
	cp := *job
	rm.mu.Unlock()

	go rm.run(ctx, job, collection)
	// Return a defensive copy — the worker mutates `job` concurrently
	// (Status, FinishedAt, LastError, counters), so handing the live
	// pointer to the HTTP handler would race with its JSON marshal.
	return &cp, nil
}

// Get returns the job with the given ID, or nil.
func (rm *RotationManager) Get(id string) *RotationJob {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	j := rm.jobs[id]
	if j == nil {
		return nil
	}
	cp := *j
	return &cp
}

// List returns every job ever scheduled in this process — newest first.
func (rm *RotationManager) List() []*RotationJob {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	out := make([]*RotationJob, 0, len(rm.jobs))
	for _, j := range rm.jobs {
		cp := *j
		out = append(out, &cp)
	}
	// newest first by StartedAt
	for i := 0; i < len(out)-1; i++ {
		for k := i + 1; k < len(out); k++ {
			if out[k].StartedAt > out[i].StartedAt {
				out[i], out[k] = out[k], out[i]
			}
		}
	}
	return out
}

func (rm *RotationManager) run(ctx context.Context, job *RotationJob, collection string) {
	rm.audit("encryption.rotation_started", job)
	rm.setStatus(job, RotationRunning)

	err := rm.processBuckets(ctx, job, collection)
	finished := time.Now().UnixNano()
	if err != nil {
		rm.finalize(job, RotationFailed, finished, err.Error())
		rm.audit("encryption.rotation_failed", job)
	} else {
		rm.finalize(job, RotationCompleted, finished, "")
		rm.audit("encryption.rotation_completed", job)
	}
	rm.mu.Lock()
	rm.current = nil
	rm.mu.Unlock()
}

func (rm *RotationManager) setStatus(job *RotationJob, st string) {
	rm.mu.Lock()
	job.Status = st
	rm.mu.Unlock()
}

// finalize updates Status, FinishedAt, and LastError under the manager
// mutex so concurrent Get/List/Status calls observe a consistent
// snapshot once the worker exits.
func (rm *RotationManager) finalize(job *RotationJob, status string, finishedAt int64, lastErr string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	job.Status = status
	job.FinishedAt = finishedAt
	if lastErr != "" {
		job.LastError = lastErr
	}
}

// processBuckets walks docs and rev once each. Re-encryption uses
// short-lived bolt.Update transactions — one per entry — so a long
// rotation does not block writers behind a single giant tx.
func (rm *RotationManager) processBuckets(ctx context.Context, job *RotationJob, collection string) error {
	for _, bucket := range []string{"docs", "rev"} {
		if err := rm.processBucket(ctx, job, bucket, collection); err != nil {
			return err
		}
	}
	return nil
}

func (rm *RotationManager) processBucket(ctx context.Context, job *RotationJob, bucket, only string) error {
	// Snapshot the keys first so we can release the read tx and rewrite
	// each entry in its own transaction. This keeps memory bounded by
	// the number of keys (small per-key overhead) instead of holding
	// every value in RAM.
	var keys [][]byte
	if err := rm.server.DBView(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, _ []byte) error {
			if only != "" && !keyBelongsToCollection(k, only) {
				return nil
			}
			keys = append(keys, append([]byte(nil), k...))
			return nil
		})
	}); err != nil {
		return err
	}
	for _, k := range keys {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rewrote, err := rm.processOne(bucket, k)
		rm.mu.Lock()
		job.Scanned++
		switch {
		case err != nil:
			job.Errors++
			job.LastError = err.Error()
		case rewrote:
			job.Reencrypted++
		default:
			job.Skipped++
		}
		rm.mu.Unlock()
	}
	return nil
}

// processOne reads a single key, decides whether re-encryption is
// needed, and rewrites the value when so. The first return is true
// when the entry was rewritten; false when skipped (plaintext, or
// already sealed with the primary key).
func (rm *RotationManager) processOne(bucket string, key []byte) (bool, error) {
	var val []byte
	if err := rm.server.DBView(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		raw := b.Get(key)
		if raw == nil {
			return nil
		}
		val = append([]byte(nil), raw...)
		return nil
	}); err != nil {
		return false, err
	}
	if val == nil {
		return false, nil
	}

	var coll string
	switch bucket {
	case "docs":
		coll = collectionFromDocKey(key)
	case "rev":
		coll = collectionFromRevKey(key)
	}
	if coll == "" {
		return false, nil
	}

	var needs bool
	switch encryption.CiphertextVersion(val) {
	case 0:
		return false, nil // plaintext — leave alone (collection may not be encrypted)
	case 1:
		needs = true
	case 2:
		needs = !rm.encryptor.IsEncryptedWithPrimary(val)
	default:
		return false, nil
	}
	if !needs {
		return false, nil
	}

	pt, err := rm.encryptor.Decrypt(val)
	if err != nil {
		return false, fmt.Errorf("decrypt %s/%s: %w", bucket, string(key), err)
	}
	sealed, err := rm.encryptor.EncryptAlways(pt)
	if err != nil {
		return false, fmt.Errorf("seal %s/%s: %w", bucket, string(key), err)
	}
	if err := rm.server.DBUpdate(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return errors.New("bucket missing")
		}
		return b.Put(key, sealed)
	}); err != nil {
		return false, err
	}
	return true, nil
}

// collectionFromRevKey extracts the collection from a revision key.
// Revision keys are formatted "rev|{collection}|{id}|{ts}".
func collectionFromRevKey(k []byte) string {
	s := string(k)
	if len(s) < 5 || s[:4] != "rev|" {
		return ""
	}
	rest := s[4:]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '|' {
			return rest[:i]
		}
	}
	return ""
}

// keyBelongsToCollection returns true for keys in the docs or rev
// bucket whose embedded collection name matches the wanted value.
func keyBelongsToCollection(k []byte, want string) bool {
	if c := collectionFromDocKey(k); c != "" {
		return c == want
	}
	if c := collectionFromRevKey(k); c != "" {
		return c == want
	}
	return false
}

func (rm *RotationManager) audit(action string, job *RotationJob) {
	if rm.server == nil || rm.server.AuditManager == nil {
		return
	}
	rm.mu.Lock()
	det, _ := json.Marshal(map[string]interface{}{
		"jobID":       job.ID,
		"primaryKey":  job.PrimaryKey,
		"scanned":     job.Scanned,
		"reencrypted": job.Reencrypted,
		"errors":      job.Errors,
	})
	rm.mu.Unlock()
	rm.server.AuditManager.Record(audit.AuditEvent{
		Action:   action,
		Resource: "encryption",
		Result:   "ok",
		Detail:   string(det),
	})
}

func newRotationID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "rot-" + hex.EncodeToString(b[:])
}
