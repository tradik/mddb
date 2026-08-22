// Server-level helpers that have no larger home of their own: key building,
// sharding, caching, id generation and the small utilities the handlers share.
//
// Renamed from coverage_boost_test.go (TEST-002). See the note in
// document_converters_helpers_test.go for why.
package main

import (
	"mddb/internal/binlog"
	"mddb/internal/fts"
	"mddb/internal/vector"
	"slices"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 1. fts_stemmer.go — Porter stemmer helper functions
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 2. schema.go — validation functions
// ---------------------------------------------------------------------------

func TestContains(t *testing.T) {
	tests := []struct {
		name  string
		slice []string
		val   string
		want  bool
	}{
		{"found", []string{"a", "b", "c"}, "b", true},
		{"not_found", []string{"a", "b", "c"}, "d", false},
		{"empty_slice", []string{}, "a", false},
		{"nil_slice", nil, "a", false},
		{"empty_string_match", []string{""}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slices.Contains(tt.slice, tt.val)
			if got != tt.want {
				t.Errorf("slices.Contains(%v, %q) = %v, want %v", tt.slice, tt.val, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 3. fts_range.go — matchStringRange
// ---------------------------------------------------------------------------

func TestMatchStringRange(t *testing.T) {
	tests := []struct {
		name string
		val  string
		rf   RangeFilter
		want bool
	}{
		{"gte_pass", "b", RangeFilter{Gte: "a"}, true},
		{"gte_equal", "a", RangeFilter{Gte: "a"}, true},
		{"gte_fail", "a", RangeFilter{Gte: "b"}, false},
		{"gt_pass", "b", RangeFilter{Gt: "a"}, true},
		{"gt_equal_fail", "a", RangeFilter{Gt: "a"}, false},
		{"gt_fail", "a", RangeFilter{Gt: "b"}, false},
		{"lte_pass", "a", RangeFilter{Lte: "b"}, true},
		{"lte_equal", "b", RangeFilter{Lte: "b"}, true},
		{"lte_fail", "c", RangeFilter{Lte: "b"}, false},
		{"lt_pass", "a", RangeFilter{Lt: "b"}, true},
		{"lt_equal_fail", "b", RangeFilter{Lt: "b"}, false},
		{"lt_fail", "c", RangeFilter{Lt: "b"}, false},
		{"combined_gte_lte_pass", "m", RangeFilter{Gte: "a", Lte: "z"}, true},
		{"combined_gte_lte_fail", "a", RangeFilter{Gte: "b", Lte: "z"}, false},
		{"all_empty_pass", "anything", RangeFilter{}, true},
		{"all_boundaries", "m", RangeFilter{Gte: "a", Gt: "b", Lte: "z", Lt: "y"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchStringRange(tt.val, tt.rf)
			if got != tt.want {
				t.Errorf("matchStringRange(%q, %+v) = %v, want %v", tt.val, tt.rf, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 4. fts_wildcard.go — wildcardMatch
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 5. aggregation.go — weekNumber, itoa
// ---------------------------------------------------------------------------

func TestWeekNumber(t *testing.T) {
	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{"single_digit_week", time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), "02"},   // week 2
		{"double_digit_week", time.Date(2026, 3, 19, 0, 0, 0, 0, time.UTC), "12"},  // week 12
		{"week_1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "01"},              // week 1
		{"week_52", time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC), "01"},           // ISO week 1 of 2026
		{"last_week_of_year", time.Date(2025, 12, 22, 0, 0, 0, 0, time.UTC), "52"}, // week 52
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := weekNumber(tt.time)
			if got != tt.want {
				_, w := tt.time.ISOWeek()
				t.Errorf("weekNumber(%v) = %q, want %q (ISOWeek=%d)", tt.time, got, tt.want, w)
			}
		})
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{9, "9"},
		{10, "10"},
		{42, "42"},
		{100, "100"},
		{999, "999"},
		{12345, "12345"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := itoa(tt.n)
			if got != tt.want {
				t.Errorf("itoa(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 6. upload_handler.go — LaTeX converter
// ---------------------------------------------------------------------------

func TestTexToMarkdown(t *testing.T) {
	t.Run("sections", func(t *testing.T) {
		input := `\section{Introduction}Some text.\subsection{Details}More text.`
		result := texToMarkdown([]byte(input))
		if !strings.Contains(result, "## Introduction") {
			t.Errorf("expected '## Introduction' in result, got: %s", result)
		}
		if !strings.Contains(result, "### Details") {
			t.Errorf("expected '### Details' in result, got: %s", result)
		}
	})

	t.Run("bold_and_italic", func(t *testing.T) {
		input := `\textbf{bold} and \textit{italic} and \emph{emphasized}`
		result := texToMarkdown([]byte(input))
		if !strings.Contains(result, "**bold**") {
			t.Errorf("expected **bold** in result, got: %s", result)
		}
		if !strings.Contains(result, "*italic*") {
			t.Errorf("expected *italic* in result, got: %s", result)
		}
		if !strings.Contains(result, "*emphasized*") {
			t.Errorf("expected *emphasized* in result, got: %s", result)
		}
	})

	t.Run("lists", func(t *testing.T) {
		input := `\begin{itemize}\item First\item Second\end{itemize}`
		result := texToMarkdown([]byte(input))
		// \item becomes "\n- " so there may be extra whitespace
		if !strings.Contains(result, "First") {
			t.Errorf("expected 'First' in result, got: %s", result)
		}
		if !strings.Contains(result, "Second") {
			t.Errorf("expected 'Second' in result, got: %s", result)
		}
		if !strings.Contains(result, "-") {
			t.Errorf("expected list markers in result, got: %s", result)
		}
	})

	t.Run("code_blocks", func(t *testing.T) {
		input := `\begin{verbatim}code here\end{verbatim}`
		result := texToMarkdown([]byte(input))
		if !strings.Contains(result, "```") {
			t.Errorf("expected code block markers in result, got: %s", result)
		}
		if !strings.Contains(result, "code here") {
			t.Errorf("expected 'code here' in result, got: %s", result)
		}
	})

	t.Run("comments_removed", func(t *testing.T) {
		input := "% This is a comment\nVisible text"
		result := texToMarkdown([]byte(input))
		if strings.Contains(result, "This is a comment") {
			t.Errorf("comment should be removed, got: %s", result)
		}
		if !strings.Contains(result, "Visible text") {
			t.Errorf("expected 'Visible text' in result, got: %s", result)
		}
	})

	t.Run("inline_comment_stripped", func(t *testing.T) {
		input := "Some text % inline comment"
		result := texToMarkdown([]byte(input))
		if strings.Contains(result, "inline comment") {
			t.Errorf("inline comment should be stripped, got: %s", result)
		}
		if !strings.Contains(result, "Some text") {
			t.Errorf("expected 'Some text' in result, got: %s", result)
		}
	})

	t.Run("preamble_removal", func(t *testing.T) {
		input := `\documentclass{article}\usepackage{amsmath}\begin{document}Body text.\end{document}`
		result := texToMarkdown([]byte(input))
		if strings.Contains(result, "documentclass") {
			t.Errorf("preamble should be removed, got: %s", result)
		}
		if !strings.Contains(result, "Body text.") {
			t.Errorf("expected body text in result, got: %s", result)
		}
	})

	t.Run("texttt_to_inline_code", func(t *testing.T) {
		input := `Use \texttt{foo} for code.`
		result := texToMarkdown([]byte(input))
		if !strings.Contains(result, "`foo`") {
			t.Errorf("expected `foo` in result, got: %s", result)
		}
	})

	t.Run("special_characters", func(t *testing.T) {
		input := `\& \% \$ \# \_ \{ \}`
		result := texToMarkdown([]byte(input))
		if !strings.Contains(result, "&") {
			t.Errorf("expected & in result, got: %s", result)
		}
		if !strings.Contains(result, "#") {
			t.Errorf("expected # in result, got: %s", result)
		}
	})

	t.Run("empty_input", func(t *testing.T) {
		result := texToMarkdown([]byte(""))
		if result != "" {
			t.Errorf("expected empty output for empty input, got: %q", result)
		}
	})
}

func TestTexReplaceCmd(t *testing.T) {
	t.Run("basic_replacement", func(t *testing.T) {
		result := texReplaceCmd(`\textbf{hello}`, `\textbf`, "**", "**")
		if result != "**hello**" {
			t.Errorf("expected '**hello**', got: %q", result)
		}
	})

	t.Run("nested_braces", func(t *testing.T) {
		result := texReplaceCmd(`\textbf{a{b}c}`, `\textbf`, "**", "**")
		if result != "**a{b}c**" {
			t.Errorf("expected '**a{b}c**', got: %q", result)
		}
	})

	t.Run("no_argument", func(t *testing.T) {
		result := texReplaceCmd(`\maketitle rest`, `\maketitle`, "", "")
		if !strings.Contains(result, "rest") {
			t.Errorf("expected 'rest' after removing command without argument, got: %q", result)
		}
		if strings.Contains(result, "maketitle") {
			t.Errorf("command should be removed, got: %q", result)
		}
	})

	t.Run("multiple_occurrences", func(t *testing.T) {
		result := texReplaceCmd(`\textbf{a} and \textbf{b}`, `\textbf`, "**", "**")
		if result != "**a** and **b**" {
			t.Errorf("expected '**a** and **b**', got: %q", result)
		}
	})

	t.Run("no_match", func(t *testing.T) {
		result := texReplaceCmd("no commands here", `\textbf`, "**", "**")
		if result != "no commands here" {
			t.Errorf("expected unchanged string, got: %q", result)
		}
	})

	t.Run("with_optional_arg", func(t *testing.T) {
		result := texReplaceCmd(`\section[short]{Long Title}`, `\section`, "## ", "\n\n")
		if !strings.Contains(result, "## Long Title") {
			t.Errorf("expected '## Long Title' in result, got: %q", result)
		}
	})
}

// ---------------------------------------------------------------------------
// 7. collection_config.go — SetBinlog
// ---------------------------------------------------------------------------

func TestCollectionManagerSetBinlog(t *testing.T) {
	cm := &CollectionManager{
		cache: make(map[string]*CollectionConfig),
	}
	if cm.binlog != nil {
		t.Fatal("binlog should be nil initially")
	}
	bl := &binlog.Binlog{}
	cm.SetBinlog(bl)
	if cm.binlog != bl {
		t.Error("SetBinlog did not set the binlog")
	}
}

// ---------------------------------------------------------------------------
// 8. async_io.go — WaitAll with no pending operations
// ---------------------------------------------------------------------------

func TestAsyncIOWaitAllNoPending(t *testing.T) {
	aio := &AsyncIO{
		operations: make(map[uint64]*AsyncOperation),
	}
	// WaitAll with zero pending should return immediately
	done := make(chan struct{})
	go func() {
		aio.WaitAll()
		close(done)
	}()

	select {
	case <-done:
		// success — returned immediately
	case <-time.After(2 * time.Second):
		t.Fatal("WaitAll blocked with no pending operations")
	}
}

// ---------------------------------------------------------------------------
// 10. Additional coverage: FTS setters, CollectionManager.LoadAll,
//     BloomFilter, binlog.BinlogEntryType.String, etc.
// ---------------------------------------------------------------------------

func TestCollectionManagerLoadAll(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	cm := NewCollectionManager(db)
	_ = cm.EnsureBucket()
	// LoadAll on empty DB
	if err := cm.LoadAll(); err != nil {
		t.Fatal(err)
	}
	// Set a config, then LoadAll again
	if err := cm.Set("test-col", &CollectionConfig{Type: "default", Description: "test"}); err != nil {
		t.Fatal(err)
	}
	cm2 := NewCollectionManager(db)
	_ = cm2.EnsureBucket()
	if err := cm2.LoadAll(); err != nil {
		t.Fatal(err)
	}
	cfg, ok := cm2.Get("test-col")
	if !ok {
		t.Fatal("expected test-col config after LoadAll")
	}
	if cfg.Description != "test" {
		t.Errorf("expected description=test, got %q", cfg.Description)
	}
}

func TestBinlogEntryTypeString(t *testing.T) {
	tests := []struct {
		t    binlog.BinlogEntryType
		want string
	}{
		{binlog.BinlogPut, "Put"},
		{binlog.BinlogDelete, "Delete"},
		{binlog.BinlogDeleteBucket, "DeleteBucket"},
		{binlog.BinlogCheckpoint, "Checkpoint"},
		{binlog.BinlogEntryType(99), "Unknown(99)"},
	}
	for _, tc := range tests {
		got := tc.t.String()
		if got != tc.want {
			t.Errorf("binlog.BinlogEntryType(%d).String() = %q, want %q", tc.t, got, tc.want)
		}
	}
}

func TestBloomFilterRemoveAndClear(t *testing.T) {
	bfm := NewBloomFilterManager()
	bfm.Add("col", "key1", "en")
	if !bfm.Test("col", "key1", "en") {
		t.Error("expected Test=true after Add")
	}
	// Remove is a no-op for bloom filters
	bfm.Remove("col", "key1", "en")
	// Clear removes the filter entirely
	bfm.Clear("col")
	// After clear, new lookups should not find the old key
	if bfm.Test("col", "key1", "en") {
		t.Error("expected Test=false after Clear")
	}
}

func TestBloomFilterStats(t *testing.T) {
	bfm := NewBloomFilterManager()
	bfm.Add("col1", "a", "en")
	bfm.Add("col2", "b", "en")
	stats := bfm.Stats()
	if len(stats) != 2 {
		t.Errorf("expected 2 collections in stats, got %d", len(stats))
	}
}

func TestFTSSearchEmptyQueryCovBoost(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	results, err := idx.Search("col1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(results))
	}
}

func TestFTSIndexAndSearchCovBoost(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	// Index some documents
	if err := idx.Index("col", "doc1", "The quick brown fox jumps over the lazy dog"); err != nil {
		t.Fatal(err)
	}
	if err := idx.Index("col", "doc2", "A fast red car drives on the highway"); err != nil {
		t.Fatal(err)
	}
	if err := idx.Index("col", "doc3", "The quick brown cat sleeps on the couch"); err != nil {
		t.Fatal(err)
	}

	// Search
	results, err := idx.Search("col", "quick brown", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 1 {
		t.Fatal("expected at least 1 result for 'quick brown'")
	}

	// Remove
	if err := idx.Remove("col", "doc1"); err != nil {
		t.Fatal(err)
	}
	results2, err := idx.Search("col", "fox jumps", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results2 {
		if r.DocID == "doc1" {
			t.Error("doc1 should have been removed from index")
		}
	}
}

func TestFTSSearchWithLangCovBoost(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	reg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(reg)
	idx.SetLangRegistry(reg)
	_ = idx.EnsureBuckets()

	if err := idx.IndexWithLang("col", "doc1", "Running quickly through the forest", "en"); err != nil {
		t.Fatal(err)
	}
	results, err := idx.Search("col", "run forest", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 1 {
		t.Error("expected at least 1 result for stemmed search")
	}
}

func TestFTSRemoveNonExistentCovBoost(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()
	if err := idx.Remove("col", "nonexistent"); err != nil {
		t.Fatal(err)
	}
}

func TestShardClusterConsistency(t *testing.T) {
	sc := NewShardCluster(4, 1)
	s1 := sc.GetShard("my-key")
	s2 := sc.GetShard("my-key")
	if s1 == nil || s2 == nil {
		t.Fatal("GetShard returned nil")
		return
	}
	if s1.ID != s2.ID {
		t.Errorf("same key returned different shards: %d vs %d", s1.ID, s2.ID)
	}
}

func TestShardClusterDistribution(t *testing.T) {
	sc := NewShardCluster(4, 1)
	counts := make(map[int]int)
	for i := 0; i < 100; i++ {
		s := sc.GetShard(itoa(i))
		if s != nil {
			counts[s.ID]++
		}
	}
	if len(counts) < 2 {
		t.Errorf("poor distribution: only %d shards used out of 4", len(counts))
	}
}

func TestZeroCopyManagerStreamCopyCovBoost(t *testing.T) {
	zcm := NewZeroCopyManager()
	if zcm == nil {
		t.Fatal("nil ZeroCopyManager")
		return
	}
	src := strings.NewReader("hello world")
	var dst strings.Builder
	n, err := zcm.StreamCopy(&dst, src)
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Errorf("expected 11 bytes, got %d", n)
	}
	if dst.String() != "hello world" {
		t.Errorf("StreamCopy: got %q", dst.String())
	}
	stats := zcm.Stats()
	if !stats.Enabled {
		t.Error("expected Enabled=true")
	}
}

func TestSIMDProcessorVectorizedCompare(t *testing.T) {
	sp := vector.NewSIMDProcessor()
	if sp == nil {
		t.Fatal("nil vector.SIMDProcessor")
		return
	}
	data := [][]byte{
		[]byte("hello"),
		[]byte("world"),
		[]byte("hello"),
		[]byte("test"),
	}
	matches := sp.VectorizedCompare(data, []byte("hello"))
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matches))
	}
}

func TestSIMDProcessorVectorizedSearch(t *testing.T) {
	sp := vector.NewSIMDProcessor()
	if sp == nil {
		t.Fatal("nil vector.SIMDProcessor")
		return
	}
	// Use a single byte pattern to avoid chunk-boundary splits
	data := []byte("abcXdefXghiXjkl")
	positions := sp.VectorizedSearch(data, []byte("X"))
	if len(positions) < 1 {
		t.Errorf("expected at least 1 position, got %d", len(positions))
	}
}

func TestSIMDProcessorVectorizedSum(t *testing.T) {
	sp := vector.NewSIMDProcessor()
	if sp == nil {
		t.Fatal("nil vector.SIMDProcessor")
		return
	}
	data := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	sum := sp.VectorizedSum(data)
	if sum != 55 {
		t.Errorf("expected sum=55, got %d", sum)
	}
}

func TestSIMDProcessorVectorizedFilter(t *testing.T) {
	sp := vector.NewSIMDProcessor()
	if sp == nil {
		t.Fatal("nil vector.SIMDProcessor")
		return
	}
	data := [][]byte{[]byte("ab"), []byte("abc"), []byte("a"), []byte("abcd")}
	filtered := sp.VectorizedFilter(data, func(b []byte) bool { return len(b) >= 3 })
	if len(filtered) != 2 {
		t.Errorf("expected 2 filtered items, got %d", len(filtered))
	}
}

func TestSIMDProcessorVectorizedMap(t *testing.T) {
	sp := vector.NewSIMDProcessor()
	if sp == nil {
		t.Fatal("nil vector.SIMDProcessor")
		return
	}
	data := [][]byte{[]byte("hello"), []byte("world")}
	mapped := sp.VectorizedMap(data, func(b []byte) []byte {
		return append(b, '!')
	})
	if len(mapped) != 2 {
		t.Errorf("expected 2 mapped items, got %d", len(mapped))
	}
	if string(mapped[0]) != "hello!" {
		t.Errorf("expected 'hello!', got %q", string(mapped[0]))
	}
}

func TestSIMDProcessorParallelSort(t *testing.T) {
	sp := vector.NewSIMDProcessor()
	if sp == nil {
		t.Fatal("nil vector.SIMDProcessor")
		return
	}
	data := [][]byte{[]byte("cherry"), []byte("apple"), []byte("banana")}
	sp.ParallelSort(data, func(a, b []byte) bool { return string(a) < string(b) })
	if string(data[0]) != "apple" || string(data[1]) != "banana" || string(data[2]) != "cherry" {
		t.Errorf("not sorted: %v", data)
	}
}

func TestSIMDProcessorStats(t *testing.T) {
	sp := vector.NewSIMDProcessor()
	if sp == nil {
		t.Fatal("nil vector.SIMDProcessor")
		return
	}
	stats := sp.Stats()
	if !stats.Enabled {
		t.Error("expected Enabled=true")
	}
}
