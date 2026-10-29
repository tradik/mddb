package migrate

import (
	"fmt"
	"reflect"
	"testing"
)

// The index as 2.18.8 left it: entries in every spelling ever written.
func TestByKeyIndexIsRewrittenToTheIdentifiersCase(t *testing.T) {
	db, _ := openTemp(t)
	put(t, db, "docs",
		"doc|docs|docs|readme.md|en", "{}",
		"doc|docs|docs|guide/intro.md|en", "{}",
		"doc|Notes|notes|todo.md|pl", "{}",
		"doc|docs|docs|live.md|en", "{}",
	)
	put(t, db, "bykey",
		// Two spellings of one document: one entry survives.
		"bykey|docs|README.md|en", "docs|readme.md|en",
		"bykey|docs|readme.md|en", "docs|readme.md|en",
		// Key and language both normalised; the collection keeps its case.
		"bykey|docs|Guide/Intro.MD|EN", "docs|guide/intro.md|en",
		"bykey|Notes|TODO.md|pl", "notes|todo.md|pl",
		// A collision the rule decides: the entry pointing at a document
		// that exists wins over one that does not.
		"bykey|docs|live.md|en", "docs|gone|en",
		"bykey|docs|LIVE.md|en", "docs|live.md|en",
		// An entry whose document is gone, with nothing to merge with,
		// moves to its normal form rather than vanishing.
		"bykey|docs|Gone.md|en", "docs|gone.md|en",
		// Not shaped like an entry: left alone.
		"bykey|nopipe", "x",
	)

	if err := Run(db); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"bykey|docs|readme.md|en":      "docs|readme.md|en",
		"bykey|docs|guide/intro.md|en": "docs|guide/intro.md|en",
		"bykey|Notes|todo.md|pl":       "notes|todo.md|pl",
		"bykey|docs|live.md|en":        "docs|live.md|en",
		"bykey|docs|gone.md|en":        "docs|gone.md|en",
		"bykey|nopipe":                 "x",
	}
	if got := dump(t, db, "bykey"); !reflect.DeepEqual(got, want) {
		t.Fatalf("index after the migration:\n got %v\nwant %v", got, want)
	}
	if got := dump(t, db, "docs"); len(got) != 4 {
		t.Fatalf("the migration touched documents: %v", got)
	}
}

func TestCollisionKeepsTheEntryAlreadyThereWhenBothOrNeitherExist(t *testing.T) {
	db, _ := openTemp(t)
	put(t, db, "docs", "doc|c|c|a|en", "{}", "doc|c|c|b|en", "{}")
	put(t, db, "bykey",
		"bykey|c|K|en", "c|b|en", // both exist: the normal one stays
		"bykey|c|k|en", "c|a|en",
		"bykey|c|N|en", "c|y|en", // neither exists: the normal one stays
		"bykey|c|n|en", "c|x|en",
	)
	if err := Run(db); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"bykey|c|k|en": "c|a|en", "bykey|c|n|en": "c|x|en"}
	if got := dump(t, db, "bykey"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestByKeyRewriteSpansChunksAndSurvivesAnInterruption(t *testing.T) {
	db, _ := openTemp(t)
	defer func(n int) { chunkSize = n }(chunkSize)
	chunkSize = 3

	const n = 10
	for i := 0; i < n; i++ {
		put(t, db, "bykey", fmt.Sprintf("bykey|c|Doc%02d|en", i), fmt.Sprintf("c|doc%02d|en", i))
	}

	// One chunk commits, then the process "dies": the version is still 0
	// and part of the index is rewritten.
	next, err := normalizeByKeyChunk(db, nil, 3)
	if err != nil || next == nil {
		t.Fatalf("first chunk: next=%q err=%v", next, err)
	}
	if got := mustVersion(t, db); got != 0 {
		t.Fatalf("an interrupted step recorded version %d", got)
	}

	// The next start runs the step from the beginning.
	if err := Run(db); err != nil {
		t.Fatal(err)
	}
	got := dump(t, db, "bykey")
	if len(got) != n {
		t.Fatalf("%d entries after the rerun, want %d: %v", len(got), n, got)
	}
	for i := 0; i < n; i++ {
		if v := got[fmt.Sprintf("bykey|c|doc%02d|en", i)]; v != fmt.Sprintf("c|doc%02d|en", i) {
			t.Errorf("entry %d = %q", i, v)
		}
	}
}

func TestFileWithoutAKeyIndexMigratesToNothing(t *testing.T) {
	db, _ := openTemp(t)
	put(t, db, "docs", "doc|c|a|en", "{}")
	if err := Run(db); err != nil {
		t.Fatal(err)
	}
	if got := dump(t, db, "bykey"); len(got) != 0 {
		t.Fatalf("a file with no index gained one: %v", got)
	}
}
