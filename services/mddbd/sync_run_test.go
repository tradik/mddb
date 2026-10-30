package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mddb/internal/automationlog"
	"mddb/internal/storage"
	"mddb/internal/webhooks"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// INT-022's acceptance: a sitemap of three pages; a second run with nothing
// changed writes nothing; a changed page is one update; a page dropped from
// the sitemap is, with prune, one delete.
func TestSyncSitemapChangeDetectionAndPrune(t *testing.T) {
	s := newBackupTestServer(t)
	site := newFakeSite(t, map[string]string{
		"/a.html": page("A", "alpha"), "/blog/b/": page("B", "beta"), "/c": page("C", "gamma"),
	})
	synced := sourceSyncedReceiver(t, s)
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceSitemap, URL: site.URL + "/sitemap.xml",
		Collection: "web", Lang: "en", Prune: true})
	_, events := collectSSE(s.SSEHub, "", "")

	updatedAt := syncFirstRunAddsAll(t, s, site, rule, events)
	syncSecondRunWritesNothing(t, s, site, rule, updatedAt)
	syncChangedPageUpdates(t, s, site, rule)
	syncDroppedPageDeletes(t, s, site, rule)

	p := <-synced
	if p["event"] != webhooks.EventSourceSynced || detailOf(p)["ruleId"] != rule.ID || detailOf(p)["added"] != 3.0 {
		t.Errorf("source.synced: %v", p)
	}
	logs, _, err := s.AutomationLogStore.List(10, "", rule.ID, "")
	if err != nil || len(logs) != 4 || logs[0].Action != "sync" || logs[0].Sync == nil {
		t.Fatalf("the automation log: %+v, %v", logs, err)
	}
}

// syncFirstRunAddsAll runs the rule once: three pages added, the synced page
// carries its source, ETag and Last-Modified, and SSE reports the job. It
// returns the synced page's UpdatedAt.
func syncFirstRunAddsAll(t *testing.T, s *Server, site *fakeSite, rule AutomationRule, events func() []SSEJobEvent) int64 {
	t.Helper()
	out, err := runSync(t, s, rule)
	if err != nil || out.counts != (automationlog.SyncCounts{Added: 3}) {
		t.Fatalf("first run: %+v, %v", out, err)
	}
	doc, err := s.loadDocByRef("web", "blog-b", "en")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.ContentMD, "# B") || metaFirst(doc.Meta, syncMetaSource) != site.URL+"/blog/b/" ||
		metaFirst(doc.Meta, syncMetaETag) == "" || metaFirst(doc.Meta, syncMetaLastModified) != siteModified {
		t.Errorf("the synced page: %q %v", doc.ContentMD, doc.Meta)
	}
	if got := events(); len(got) == 0 || got[len(got)-1].Event != SSEJobCompleted || got[len(got)-1].Job.Added != 3 {
		t.Errorf("SSE: %+v", got)
	}
	return doc.UpdatedAt
}

// syncSecondRunWritesNothing checks that with nothing changed every page
// answers 304 and nothing is written.
func syncSecondRunWritesNothing(t *testing.T, s *Server, site *fakeSite, rule AutomationRule, updatedAt int64) {
	t.Helper()
	time.Sleep(1100 * time.Millisecond) // a write now would move UpdatedAt
	out, err := runSync(t, s, rule)
	if err != nil || out.counts != (automationlog.SyncCounts{Unchanged: 3}) {
		t.Fatalf("second run: %+v, %v", out, err)
	}
	if site.hits("/a.html") != 1 {
		t.Errorf("an unchanged page was downloaded again: %d", site.hits("/a.html"))
	}
	if d, _ := s.loadDocByRef("web", "blog-b", "en"); d.UpdatedAt != updatedAt {
		t.Error("an unchanged page was written")
	}
}

// syncChangedPageUpdates changes one page and checks the run is one update.
func syncChangedPageUpdates(t *testing.T, s *Server, site *fakeSite, rule AutomationRule) {
	t.Helper()
	site.set("/c", page("C", "gamma, revised"))
	out, err := runSync(t, s, rule)
	if err != nil || out.counts != (automationlog.SyncCounts{Updated: 1, Unchanged: 2}) {
		t.Fatalf("after a change: %+v, %v", out, err)
	}
	if d, _ := s.loadDocByRef("web", "c", "en"); !strings.Contains(d.ContentMD, "revised") {
		t.Errorf("the update did not land: %q", d.ContentMD)
	}
}

// syncDroppedPageDeletes drops one page from the sitemap and checks the run
// (with prune) is one delete.
func syncDroppedPageDeletes(t *testing.T, s *Server, site *fakeSite, rule AutomationRule) {
	t.Helper()
	site.del("/a.html")
	out, err := runSync(t, s, rule)
	if err != nil || out.counts != (automationlog.SyncCounts{Unchanged: 2, Deleted: 1}) {
		t.Fatalf("after a removal: %+v, %v", out, err)
	}
	if _, err := s.loadDocByRef("web", "a", "en"); !errors.Is(err, errDocNotFound) {
		t.Errorf("the dropped page's document: %v", err)
	}
	if countDocs(t, s, "web") != 2 {
		t.Errorf("documents left: %d", countDocs(t, s, "web"))
	}
}

// Without ETag or Last-Modified, the content hash tells an unchanged page:
// it is downloaded again, and not written.
func TestSyncContentHashWhenNoValidators(t *testing.T) {
	s := newBackupTestServer(t)
	site := newFakeSite(t, map[string]string{"/x": page("X", "x")})
	site.noValidators = true
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceURLs, URLs: []string{site.URL + "/x", site.URL + "/x"},
		Collection: "web", Lang: "en"})
	if out, err := runSync(t, s, rule); err != nil || out.counts.Added != 1 {
		t.Fatalf("first run: %+v, %v", out, err)
	}
	out, err := runSync(t, s, rule)
	if err != nil || out.counts != (automationlog.SyncCounts{Unchanged: 1}) || site.hits("/x") != 2 {
		t.Fatalf("second run: %+v, %v, %d downloads", out, err, site.hits("/x"))
	}
}

// A URL to an internal address is refused by the SSRF guard (SEC-004).
func TestSyncRefusesInternalAddresses(t *testing.T) {
	s := newBackupTestServer(t)
	site := newFakeSite(t, map[string]string{"/x": page("X", "x")})
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "false")
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceURLs, URLs: []string{site.URL + "/x"}, Collection: "web", Lang: "en"})
	out, err := runSync(t, s, rule)
	if err == nil || out.counts.Failed != 1 || !strings.Contains(out.errors[0], "not allowed") {
		t.Fatalf("an internal URL: %+v, %v", out, err)
	}
	if site.hits("/x") != 0 {
		t.Error("the internal address was fetched")
	}

	// A sitemap there is refused before anything is listed.
	rule = syncRule(t, s, &SyncAction{Source: SyncSourceSitemap, URL: site.URL + "/sitemap.xml", Collection: "web", Lang: "en"})
	if _, err := runSync(t, s, rule); err == nil || !strings.Contains(err.Error(), "listing the source") {
		t.Errorf("an internal sitemap: %v", err)
	}
}

// What a page answers decides what happens to its document: 404/410 deletes
// it only with prune, other failures are reported and change nothing.
func TestSyncPageFailures(t *testing.T) {
	s := newBackupTestServer(t)
	site := newFakeSite(t, map[string]string{"/ok": page("OK", "ok"), "/big": strings.Repeat("x", 2048),
		"/gone": page("G", "g"), "/err.html": page("E", "e"), "/kept": page("K", "k")})
	urls := []string{site.URL + "/ok", site.URL + "/big", site.URL + "/gone", site.URL + "/err.html", site.URL + "/kept"}
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceURLs, URLs: urls, Collection: "web", Lang: "en", MaxBytes: 1024, Prune: true})
	if out, _ := runSync(t, s, rule); out.counts.Added != 4 || out.counts.Failed != 1 || !strings.Contains(out.errors[0], "maxBytes") {
		t.Fatalf("first run: %+v", out)
	}

	site.mu.Lock()
	site.status["/gone"] = http.StatusGone
	site.status["/err.html"] = http.StatusInternalServerError
	site.status["/never"] = http.StatusNotFound
	site.mu.Unlock()
	rule.Sync.URLs = append(urls, site.URL+"/never")
	out, err := runSync(t, s, rule)
	if err == nil || out.counts.Deleted != 1 || out.counts.Failed != 3 {
		t.Fatalf("second run: %+v, %v", out, err)
	}
	if _, err := s.loadDocByRef("web", "gone", "en"); !errors.Is(err, errDocNotFound) {
		t.Error("the 410 page's document is still there")
	}
	if _, err := s.loadDocByRef("web", "err", "en"); err != nil {
		t.Error("a 500 deleted the document")
	}

	// Without prune a page that is gone is a failure, and stays.
	rule.Sync.Prune = false
	site.mu.Lock()
	site.status["/kept"] = http.StatusNotFound
	site.mu.Unlock()
	if out, _ := runSync(t, s, rule); out.counts.Deleted != 0 {
		t.Errorf("deleted without prune: %+v", out)
	}
	if _, err := s.loadDocByRef("web", "kept", "en"); err != nil {
		t.Error("a 404 without prune deleted the document")
	}
}

// Sources whose keys collide, a key another document already has, a format
// the converter refuses, a quota (GO-043): each fails alone.
func TestSyncWritePathRefusals(t *testing.T) {
	s := newBackupTestServer(t)
	site := newFakeSite(t, map[string]string{"/a/b": page("1", "1"), "/a-b": page("2", "2"),
		"/mine": page("M", "m"), "/blob.bin": "\x00\x01binary", "/q1": page("Q", "1"), "/q2": page("Q", "2")})
	site.types["/blob.bin"] = "application/octet-stream"
	site.types["/note.md"] = "text/markdown"
	site.pages["/note.md"] = "---\ntitle: Note\n---\nThe body."
	addTestDoc(t, s, "web", "mine", "en", "written by hand", nil)
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceURLs, Collection: "web", Lang: "en",
		URLs: []string{site.URL + "/a/b", site.URL + "/a-b", site.URL + "/mine", site.URL + "/blob.bin", site.URL + "/note.md"}})
	out, err := runSync(t, s, rule)
	if err == nil || out.counts.Added != 2 || out.counts.Failed != 3 {
		t.Fatalf("%+v, %v", out, err)
	}
	joined := strings.Join(out.errors, "\n")
	for _, want := range []string{"already", "not written by this sync", "unsupported format"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no %q among %v", want, out.errors)
		}
	}
	if d, _ := s.loadDocByRef("web", "mine", "en"); d.ContentMD != "written by hand" {
		t.Error("the sync overwrote a document it does not own")
	}
	// Markdown keeps its frontmatter as meta, next to the sync's own.
	if d, _ := s.loadDocByRef("web", "note", "en"); d.ContentMD != "The body." || metaFirst(d.Meta, "title") != "Note" ||
		metaFirst(d.Meta, syncMetaRule) != rule.ID {
		t.Errorf("the Markdown page: %q %v", d.ContentMD, d.Meta)
	}

	if err := s.CollectionManager.Set("small", &CollectionConfig{Quota: &CollectionQuota{MaxDocuments: 1}}); err != nil {
		t.Fatal(err)
	}
	rule = syncRule(t, s, &SyncAction{Source: SyncSourceURLs, Collection: "small", Lang: "en",
		URLs: []string{site.URL + "/q1", site.URL + "/q2"}})
	out, _ = runSync(t, s, rule)
	if out.counts.Added != 1 || out.counts.Failed != 1 || !strings.Contains(strings.Join(out.errors, ""), "quota") {
		t.Errorf("over the quota: %+v", out)
	}
}

// A listing cut at maxDocuments, or one that lists nothing, prunes nothing.
func TestSyncPruneSafety(t *testing.T) {
	s := newBackupTestServer(t)
	site := newFakeSite(t, map[string]string{"/1": page("1", "1"), "/2": page("2", "2"), "/3": page("3", "3")})
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceSitemap, URL: site.URL + "/sitemap.xml",
		Collection: "web", Lang: "en", Prune: true})
	if _, err := runSync(t, s, rule); err != nil {
		t.Fatal(err)
	}

	rule.Sync.MaxDocuments = 2
	out, err := runSync(t, s, rule)
	if err == nil || !strings.Contains(err.Error(), "nothing pruned") || out.counts.Deleted != 0 {
		t.Errorf("a cut listing: %+v, %v", out, err)
	}
	rule.Sync.Prune = false
	if _, err := runSync(t, s, rule); err == nil || !strings.Contains(err.Error(), "synced the first 2") {
		t.Errorf("a cut listing without prune: %v", err)
	}

	rule.Sync.MaxDocuments, rule.Sync.Prune = 10, true
	site.mu.Lock()
	site.sitemap = urlset(site.URL)
	site.mu.Unlock()
	if _, err := runSync(t, s, rule); err == nil || !strings.Contains(err.Error(), "lists nothing") {
		t.Errorf("an empty listing: %v", err)
	}
	if countDocs(t, s, "web") != 3 {
		t.Errorf("documents left: %d", countDocs(t, s, "web"))
	}
}

// A run while the previous one of the same rule goes on is skipped and
// logged; a source that cannot be reached fails the run.
func TestSyncSkippedAndUnreachable(t *testing.T) {
	s := newBackupTestServer(t)
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceS3, Bucket: "b", Collection: "web", Lang: "en",
		StorageConfig: &StorageConfigDef{Endpoint: "127.0.0.1:1"}})
	syncRuns.Store(rule.ID, true)
	if _, err := runSync(t, s, rule); !errors.Is(err, errSyncRunning) {
		t.Errorf("a concurrent run: %v", err)
	}
	syncRuns.Delete(rule.ID)
	if _, err := runSync(t, s, rule); err == nil || !strings.Contains(err.Error(), "connecting to the source") {
		t.Errorf("an unreachable bucket: %v", err)
	}
	logs, _, _ := s.AutomationLogStore.List(10, "", rule.ID, "")
	statuses := map[string]bool{}
	for _, e := range logs {
		statuses[e.Status] = true
	}
	if !statuses["skipped"] || !statuses["error"] {
		t.Errorf("logged: %+v", logs)
	}

	// Without a log store, an SSE hub or webhooks, a run still runs.
	s.AutomationLogStore, s.SSEHub, s.WebhookManager = nil, nil, nil
	if _, err := runSync(t, s, rule); err == nil {
		t.Error("an unreachable bucket succeeded")
	}
}

// sourceSyncedReceiver registers a webhook for source.synced and returns the
// payloads it receives.
func sourceSyncedReceiver(t *testing.T, s *Server) chan map[string]any {
	t.Helper()
	got := make(chan map[string]any, 16)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		got <- p
	}))
	t.Cleanup(ts.Close)
	if _, err := s.WebhookManager.Register(ts.URL, []string{webhooks.EventSourceSynced}, ""); err != nil {
		t.Fatal(err)
	}
	return got
}

// A document that cannot be read fails its source rather than being
// overwritten blind.
func TestSyncUnreadableDocument(t *testing.T) {
	s := newBackupTestServer(t)
	site := newFakeSite(t, map[string]string{"/bad": page("B", "b")})
	id := genID("web", "bad", "en")
	if err := s.DBUpdateC("web", func(tx *bolt.Tx) error {
		if err := tx.Bucket([]byte("bykey")).Put(storage.ByKeyKey("web", "bad", "en"), []byte(id)); err != nil {
			return err
		}
		return tx.Bucket([]byte("docs")).Put(storage.DocKey("web", id), []byte{0x01})
	}); err != nil {
		t.Fatal(err)
	}
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceURLs, URLs: []string{site.URL + "/bad"}, Collection: "web", Lang: "en"})
	if out, err := runSync(t, s, rule); err == nil || out.counts.Failed != 1 || site.hits("/bad") != 0 {
		t.Errorf("an unreadable document: %+v, %v", out, err)
	}
}

// A sync into a read-only collection (an installed corpus pack, AGT-005) is
// refused by the same write gate as every other write: each page fails and
// nothing is stored.
func TestSyncIntoReadOnlyCollectionStoresNothing(t *testing.T) {
	s := newBackupTestServer(t)
	if err := s.CollectionManager.put("web", &CollectionConfig{ReadOnly: true, Corpus: &CorpusInfo{Name: "web"}}); err != nil {
		t.Fatal(err)
	}
	site := newFakeSite(t, map[string]string{"/a.html": page("A", "alpha"), "/c": page("C", "gamma")})
	rule := syncRule(t, s, &SyncAction{Source: SyncSourceSitemap, URL: site.URL + "/sitemap.xml",
		Collection: "web", Lang: "en"})

	out, _ := runSync(t, s, rule)
	if out.counts != (automationlog.SyncCounts{Failed: 2}) {
		t.Fatalf("counts = %+v, want only failures", out.counts)
	}
	if _, err := s.loadDocByRef("web", "a", "en"); err == nil {
		t.Error("a page was stored in the read-only collection")
	}
}
