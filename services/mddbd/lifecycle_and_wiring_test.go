package main

import (
	"fmt"
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

func TestKeyBuilderResetOnAZeroValue(t *testing.T) {
	kb := &KeyBuilder{}
	kb.Reset() // a fresh builder has nothing to reset

	// Reset must leave it usable rather than merely not panicking.
	got := kb.BuildMetaKeyPrefix("docs", "tag", "go")
	if len(got) == 0 {
		t.Error("a reset builder produced no key")
	}
}

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

// Removing a node must move only the keys that node owned. A hash that
// reshuffles everything turns one node leaving into a full cache miss.
func TestConsistentHashRemoveMovesOnlyTheAffectedKeys(t *testing.T) {
	ch := NewConsistentHash(150)
	for id := range 4 {
		ch.Add(id, 1)
	}

	const sample = 4000
	keys := make([]string, 0, sample)
	before := make([]int, 0, sample)
	for i := range sample {
		key := fmt.Sprintf("document-%d-key", i*7919)
		keys = append(keys, key)
		before = append(before, ch.Get(key))
	}

	ch.Remove(2)

	moved := 0
	for i, key := range keys {
		now := ch.Get(key)
		if now == 2 {
			t.Fatalf("key %q still maps to the removed node", key)
		}
		if now != before[i] {
			moved++
		}
	}

	// Only the keys that belonged to node 2 should move — about a quarter
	// with four equal nodes. Anything approaching all of them means the ring
	// is being rebuilt rather than repaired, which is the whole thing
	// consistent hashing exists to avoid.
	if moved > sample/2 {
		t.Errorf("removing one of four nodes moved %d of %d keys", moved, sample)
	}
	if moved == 0 {
		t.Error("removing a node moved nothing — the key set never used it")
	}
}

// The property that was broken: virtual nodes must interleave around the ring,
// or one shard owns a long arc and takes a disproportionate share of the keys.
//
// Measured before the fix at these parameters: one shard owned 50 consecutive
// positions and carried 160.8% of an even share while another carried 39.6% —
// four times the load on one shard versus another (TEST-002).
func TestConsistentHashDistributesEvenly(t *testing.T) {
	const shards, samples = 4, 8000

	ch := NewConsistentHash(150) // the production replica count
	for id := range shards {
		ch.Add(id, 1)
	}

	dist := make([]int, shards)
	for i := range samples {
		dist[ch.Get(fmt.Sprintf("document-%d-key", i*7919))]++
	}

	ideal := samples / shards
	for id, n := range dist {
		share := float64(n) / float64(ideal)
		if share < 0.75 || share > 1.25 {
			t.Errorf("shard %d holds %d keys, %.1f%% of an even share (want within 25%%): %v",
				id, n, share*100, dist)
		}
	}
}

// A shard added later must take its share rather than landing in a gap.
func TestConsistentHashBalancesAfterGrowth(t *testing.T) {
	const samples = 6000

	ch := NewConsistentHash(150)
	for id := range 3 {
		ch.Add(id, 1)
	}
	ch.Add(3, 1) // the fourth arrives after the ring exists

	dist := make([]int, 4)
	for i := range samples {
		dist[ch.Get(fmt.Sprintf("doc-%d", i*104729))]++
	}
	ideal := samples / 4
	if dist[3] < ideal/2 {
		t.Errorf("a shard added after the others took %d keys of an expected ~%d: %v",
			dist[3], ideal, dist)
	}
}
