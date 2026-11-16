package main

import (
	"testing"
	"time"

	"mddb/internal/automationlog"
	"mddb/internal/compression"
	"mddb/internal/fts"
)

// TEST-002. These eleven cases used to live in coverage_boost*_test.go with no
// assertion at all: they called something and dropped the result, which raises
// a coverage number without answering a question. Each exercised something
// worth pinning, so they were given the assertion they were missing rather
// than deleted.

// Setters called with nil are how a subsystem is wired before its dependency
// exists — during startup, in a restore, in a test. They must be no-ops rather
// than a crash, and the wiring must actually take effect.

func TestConfigureCompressionAcceptsBothStates(t *testing.T) {
	// Restore whatever the process was using, since this is global state.
	defer compression.ConfigureCompression(true, 256, 4096)

	compression.ConfigureCompression(false, 0, 0)
	if got := compression.CompressDoc([]byte("some text that would compress well")); len(got) == 0 {
		t.Error("compression disabled produced no output at all")
	}

	compression.ConfigureCompression(true, 256, 4096)
	round, err := compression.DecompressDoc(compression.CompressDoc([]byte("some text")))
	if err != nil || string(round) != "some text" {
		t.Errorf("re-enabling compression broke the round trip: %q, %v", round, err)
	}
}

func TestSynonymManagerAcceptsANilBinlog(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()

	sm := fts.NewSynonymManager(db)
	sm.SetBinlog(nil) // replication not configured

	if err := sm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	// The manager must still work — a nil binlog means "do not replicate",
	// not "do not function".
	if err := sm.Set("col", "car", []string{"automobile"}); err != nil {
		t.Fatalf("adding a synonym without a binlog failed: %v", err)
	}
	if got := sm.Get("col", "car"); len(got) == 0 {
		t.Error("the synonym was not stored")
	}
}

func TestStopWordManagerAcceptsANilBinlog(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()

	swm := fts.NewStopWordManager(db)
	swm.SetBinlog(nil)

	if err := swm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := swm.Add("col", []string{"the"}); err != nil {
		t.Fatalf("adding a stop word without a binlog failed: %v", err)
	}
	if !swm.IsStopWord("col", "the") {
		t.Error("the stop word was not stored")
	}
}

// Wiring a synonym manager into the index must change what the index finds —
// otherwise the setter is decorative.
func TestSynonymManagerWiredIntoTheIndexAffectsSearch(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()

	idx := fts.NewFTSIndex(db)
	if err := idx.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	sm := fts.NewSynonymManager(db)
	if err := sm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := sm.Set("col", "car", []string{"automobile"}); err != nil {
		t.Fatal(err)
	}

	if err := idx.Index("col", "d1", "the automobile is red"); err != nil {
		t.Fatal(err)
	}

	// Without the manager, the synonym is just another word.
	before, _ := idx.Search("col", "car", 10)

	idx.SetSynonymManager(sm)
	after, _ := idx.Search("col", "car", 10)

	if len(after) <= len(before) {
		t.Errorf("wiring the synonym manager changed nothing: %d results before, %d after",
			len(before), len(after))
	}
}

func TestStopWordManagerWiredIntoTheIndex(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()

	idx := fts.NewFTSIndex(db)
	if err := idx.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	swm := fts.NewStopWordManager(db)
	if err := swm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}

	idx.SetStopWordManager(swm)
	if err := idx.Index("col", "d1", "a document about databases"); err != nil {
		t.Fatalf("indexing with a stop-word manager wired in failed: %v", err)
	}
	if got, _ := idx.Search("col", "databases", 10); len(got) == 0 {
		t.Error("a real term stopped matching once stop words were wired in")
	}
}

func TestAutomationManagerAcceptsALogStore(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()

	am := NewAutomationManager(db)
	if err := am.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	am.SetLogStore(automationlog.NewStore(db, 24*time.Hour))

	// The manager must keep working with a log store attached.
	if _, err := am.Create(AutomationRule{
		Name: "hook", Type: "webhook", Enabled: true, URL: "http://example.com",
	}); err != nil {
		t.Errorf("creating a rule with a log store attached failed: %v", err)
	}
}

// A single-character query must return an answer rather than an error or a
// panic — users type one letter constantly.
func TestSingleCharacterQueryIsHandled(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()

	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	if err := idx.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	if err := idx.Index("col", "d1", "a short document"); err != nil {
		t.Fatal(err)
	}

	if _, err := idx.Search("col", "a", 10); err != nil {
		t.Errorf("a one-character query returned an error: %v", err)
	}
	// An empty query is the degenerate case of the same thing.
	if _, err := idx.Search("col", "", 10); err != nil {
		t.Errorf("an empty query returned an error: %v", err)
	}
}

// LoadDefaults either seeds synonyms or does not; what must not happen is a
// half-loaded state where Get returns something for a word that was never
// seeded.
func TestSynonymDefaultsLoadConsistently(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()

	sm := fts.NewSynonymManager(db)
	if err := sm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := sm.LoadDefaults("col"); err != nil {
		t.Fatalf("loading defaults failed: %v", err)
	}

	// Loading twice must be idempotent — startup can repeat it.
	first := len(sm.Get("col", "happy"))
	if err := sm.LoadDefaults("col"); err != nil {
		t.Fatalf("loading defaults twice failed: %v", err)
	}
	if second := len(sm.Get("col", "happy")); second != first {
		t.Errorf("loading defaults twice changed the set: %d then %d", first, second)
	}

	// A word nobody seeded must have no synonyms.
	if got := sm.Get("col", "zzzz-not-a-word"); len(got) != 0 {
		t.Errorf("an unseeded word has synonyms: %v", got)
	}
}

func TestBackendRegistryDefaultOnAnEmptyRegistry(t *testing.T) {
	registry := &BackendRegistry{}

	// An empty registry has no default. What matters is that asking does not
	// panic and the answer is honest rather than a zero-valued backend that
	// silently accepts writes.
	if def := registry.Default(); def != nil {
		t.Errorf("an empty registry produced a default backend: %#v", def)
	}
}
