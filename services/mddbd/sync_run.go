package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"mddb/internal/automationlog"
)

// The run of a source sync (INT-022): list the source → per source, fetch
// what changed (ETag / Last-Modified, the object's ETag, a content hash when
// neither helps) → write it through the one write path (addDocument: schema,
// quotas, read-only) → prune what is gone. Each run is logged in the
// automation log, announced over the jobs SSE stream and fires source.synced.

// syncConcurrency is how many sources a run fetches at once.
const syncConcurrency = 2

// maxSyncErrors is how many failures a run reports by name.
const maxSyncErrors = 20

// errSyncRunning is why a run is skipped: the previous run of the same rule
// has not finished.
var errSyncRunning = errors.New("the previous run of this sync is still going")

// syncRuns holds the IDs of the rules whose sync is running now.
var syncRuns sync.Map

// syncOutcome is what a run did.
type syncOutcome struct {
	counts automationlog.SyncCounts
	errors []string // the first maxSyncErrors failures, "source: reason"
}

// syncRun is one run of one rule.
type syncRun struct {
	server *Server
	rule   AutomationRule
	action *SyncAction
	job    *BulkIngestJob
	mu     sync.Mutex
	out    syncOutcome
}

// runSourceSync syncs rule's source once. now names the run's job.
func (s *Server) runSourceSync(rule AutomationRule, now time.Time) (syncOutcome, error) {
	start := time.Now()
	if _, running := syncRuns.LoadOrStore(rule.ID, true); running {
		s.logSync(rule, start, syncOutcome{}, errSyncRunning)
		return syncOutcome{}, errSyncRunning
	}
	defer syncRuns.Delete(rule.ID)

	r := &syncRun{server: s, rule: rule, action: rule.Sync, job: &BulkIngestJob{
		ID:          "sync-" + rule.ID + "-" + now.UTC().Format(backupStampLayout),
		Collection:  rule.Sync.Collection,
		Status:      BulkJobProcessing,
		SubmittedAt: start.Unix(),
		StartedAt:   start.Unix(),
	}}
	s.announceJob(SSEJobStarted, r.job)
	err := r.execute(context.Background())

	r.job.CompletedAt = time.Now().Unix()
	r.job.Errors = r.out.errors
	event := SSEJobCompleted
	r.job.Status = BulkJobCompleted
	if err != nil {
		event, r.job.Status = SSEJobFailed, BulkJobFailed
	}
	s.announceJob(event, r.job)
	s.logSync(rule, start, r.out, err)
	s.fireSourceSynced(rule, r.out, err)
	return r.out, err
}

// execute lists the source, syncs every source in it and prunes. Its error
// is the run's: the listing failed, sources failed, or a prune was withheld.
func (r *syncRun) execute(ctx context.Context) error {
	a := r.action
	src, err := newSyncSource(a)
	if err != nil {
		return fmt.Errorf("connecting to the source: %w", err)
	}
	items, truncated, err := src.list(ctx)
	if err != nil {
		return fmt.Errorf("listing the source: %w", err)
	}
	r.job.Total = len(items)

	// Two sources that derive one key would overwrite each other on every
	// run: the first keeps the key, the rest fail.
	owner := map[string]string{}
	work := make(chan syncItem)
	var wg sync.WaitGroup
	for range syncConcurrency {
		wg.Go(func() {
			for it := range work {
				r.sync(ctx, src, it)
			}
		})
	}
	listed := make(map[string]bool, len(items))
	for _, it := range items {
		listed[it.ref] = true
		if first, taken := owner[it.key]; taken {
			r.failed(it.ref, fmt.Errorf("its key %q is already %s's", it.key, first))
			continue
		}
		owner[it.key] = it.ref
		work <- it
	}
	close(work)
	wg.Wait()

	var errs []error
	if f := r.out.counts.Failed; f > 0 {
		errs = append(errs, fmt.Errorf("%d of %d sources failed", f, len(items)))
	}
	if a.Prune {
		errs = append(errs, r.prune(listed, truncated))
	} else if truncated {
		errs = append(errs, fmt.Errorf("the source lists more than maxDocuments (%d): synced the first %d", a.MaxDocuments, len(items)))
	}
	return errors.Join(errs...)
}

// sync brings one source's document up to date.
func (r *syncRun) sync(ctx context.Context, src syncSource, it syncItem) {
	a := r.action
	prev, owned, err := r.state(it.key)
	if err != nil {
		r.failed(it.ref, err)
		return
	}
	got, err := src.fetch(ctx, it, prev)
	switch {
	case errors.Is(err, errSourceGone) && owned && a.Prune:
		r.remove(it.key, a.Lang, it.ref)
		return
	case err != nil:
		r.failed(it.ref, err)
		return
	case got.notModified:
		r.counted(func(c *automationlog.SyncCounts) { c.Unchanged++ })
		return
	}

	sum := sha256.Sum256(got.body)
	hash := hex.EncodeToString(sum[:])
	if owned && hash == prev.hash {
		r.counted(func(c *automationlog.SyncCounts) { c.Unchanged++ })
		return
	}
	conv, err := convertToMarkdown(syncFormat(got.contentType, it.ref), got.body, false)
	if err != nil {
		r.failed(it.ref, err)
		return
	}
	meta, body := map[string][]string{}, conv.Markdown
	if fm, rest := parseFrontmatter(body); fm != nil && !conv.Converted {
		meta, body = fm, rest
	}
	meta[syncMetaRule] = []string{r.rule.ID}
	meta[syncMetaSource] = []string{it.ref}
	meta[syncMetaHash] = []string{hash}
	for k, v := range map[string]string{syncMetaETag: got.etag, syncMetaLastModified: got.modified} {
		if v != "" {
			meta[k] = []string{v}
		}
	}
	_, isNew, err := r.server.addDocument(a.Collection, it.key, a.Lang, meta, body, 0, true)
	switch {
	case err != nil:
		r.failed(it.ref, err)
	case isNew:
		r.counted(func(c *automationlog.SyncCounts) { c.Added++; r.job.Added++ })
	default:
		r.counted(func(c *automationlog.SyncCounts) { c.Updated++; r.job.Updated++ })
	}
}

// state reads the validators the previous run left on key's document.
// owned is false when there is none; a document of the same key that this
// rule did not write is an error — a sync never overwrites another's.
func (r *syncRun) state(key string) (prev syncState, owned bool, err error) {
	doc, err := r.server.loadDocByRef(r.action.Collection, key, r.action.Lang)
	if errors.Is(err, errDocNotFound) {
		return prev, false, nil
	}
	if err != nil {
		return prev, false, err
	}
	if metaFirst(doc.Meta, syncMetaRule) != r.rule.ID {
		return prev, false, fmt.Errorf("document %q exists and was not written by this sync", key)
	}
	return syncState{
		etag:         metaFirst(doc.Meta, syncMetaETag),
		lastModified: metaFirst(doc.Meta, syncMetaLastModified),
		hash:         metaFirst(doc.Meta, syncMetaHash),
	}, true, nil
}

// prune deletes this rule's documents whose source the listing no longer
// has. A listing cut at maxDocuments, or one that lists nothing — a wrong
// prefix, an emptied sitemap, a site down for maintenance — prunes nothing:
// either would delete documents whose source still exists.
func (r *syncRun) prune(listed map[string]bool, truncated bool) error {
	a := r.action
	if truncated {
		return fmt.Errorf("the source lists more than maxDocuments (%d): nothing pruned", a.MaxDocuments)
	}
	if len(listed) == 0 {
		return errors.New("the source lists nothing: nothing pruned")
	}
	ids := r.server.getDocIDsByMeta(a.Collection, map[string][]string{syncMetaRule: {r.rule.ID}})
	for _, id := range sortedIDs(ids) {
		doc := r.server.loadDocByID(a.Collection, id, false)
		if doc == nil || listed[metaFirst(doc.Meta, syncMetaSource)] {
			continue
		}
		r.remove(doc.Key, doc.Lang, metaFirst(doc.Meta, syncMetaSource))
	}
	return nil
}

// remove deletes the document of a source that is gone.
func (r *syncRun) remove(key, lang, ref string) {
	if err := r.server.deleteDocumentInternal(r.action.Collection, key, lang); err != nil {
		r.failed(ref, err)
		return
	}
	r.counted(func(c *automationlog.SyncCounts) { c.Deleted++ })
}

// failed counts a source that could not be synced and keeps its reason.
func (r *syncRun) failed(ref string, err error) {
	r.counted(func(c *automationlog.SyncCounts) {
		c.Failed++
		r.job.Failed++
		if len(r.out.errors) < maxSyncErrors {
			r.out.errors = append(r.out.errors, ref+": "+err.Error())
		}
	})
}

// counted applies one source's result and announces the progress.
func (r *syncRun) counted(apply func(*automationlog.SyncCounts)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	apply(&r.out.counts)
	r.job.Processed++
	r.server.announceJob(SSEJobProgress, r.job)
}
