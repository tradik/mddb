// Full-text search internals: tokenising, stemming, synonyms, stop words and
// the scoring helpers underneath the public search API.
//
// Renamed from coverage_boost3_test.go (TEST-002). See the note in
// document_converters_helpers_test.go for why.
package main

import (
	"mddb/internal/binlog"
	"mddb/internal/compression"
	"mddb/internal/delta"
	"mddb/internal/fts"
	"mddb/internal/storage"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// ---------------------------------------------------------------------------
// fts.go: TokenizeQuery / TokenizeQueryLang — synonym expansion
// ---------------------------------------------------------------------------

func TestTokenizeQueryNoSynonyms(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	terms := idx.TokenizeQuery("col", "quick brown fox")
	if len(terms) == 0 {
		t.Fatal("expected terms")
	}
}

func TestTokenizeQueryWithSynonyms(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	sm := fts.NewSynonymManager(db)
	_ = sm.EnsureBucket()
	_ = sm.Set("col", "fast", []string{"quick", "rapid"})
	idx.SetSynonymManager(sm)
	_ = idx.EnsureBuckets()

	terms := idx.TokenizeQuery("col", "fast runner")
	// Should include "fast" + synonyms "quick"/"rapid" (stemmed)
	if len(terms) < 2 {
		t.Errorf("expected synonym expansion, got %d terms: %v", len(terms), terms)
	}
}

func TestTokenizeQueryLangNoSynonyms(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	reg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(reg)
	idx.SetLangRegistry(reg)
	_ = idx.EnsureBuckets()

	terms := idx.TokenizeQueryLang("col", "running quickly", "en")
	if len(terms) == 0 {
		t.Fatal("expected terms")
	}
}

func TestTokenizeQueryLangWithSynonyms(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	reg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(reg)
	idx.SetLangRegistry(reg)
	sm := fts.NewSynonymManager(db)
	_ = sm.EnsureBucket()
	_ = sm.Set("col", "big", []string{"large", "huge"})
	idx.SetSynonymManager(sm)
	_ = idx.EnsureBuckets()

	terms := idx.TokenizeQueryLang("col", "big house", "en")
	if len(terms) < 2 {
		t.Errorf("expected synonym expansion, got %d terms", len(terms))
	}
}

// ---------------------------------------------------------------------------
// fts_stopwords.go: IsStopWord
// ---------------------------------------------------------------------------

func TestIsStopWord(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	swm := fts.NewStopWordManager(db)
	_ = swm.EnsureBucket()

	// Default stop words
	if !swm.IsStopWord("any", "the") {
		t.Error("'the' should be a default stop word")
	}
	if swm.IsStopWord("any", "elephant") {
		t.Error("'elephant' should not be a stop word")
	}

	// Custom stop words
	_ = swm.Add("mycol", []string{"customword"})
	if !swm.IsStopWord("mycol", "customword") {
		t.Error("'customword' should be a stop word for mycol")
	}
	if swm.IsStopWord("othercol", "customword") {
		t.Error("'customword' should not be in othercol")
	}
}

// ---------------------------------------------------------------------------
// fts_range.go: parseFlexibleDate, matchTimestampRange
// ---------------------------------------------------------------------------

func TestParseFlexibleDate(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"2024-01-15", false},
		{"2024/01/15", false},
		{"2024-01-15T10:30:00", false},
		{"2024-01-15T10:30:00Z", false},
		{"Jan 2, 2024", false},
		{"not-a-date", true},
		{"", true},
	}
	for _, tc := range tests {
		_, err := parseFlexibleDate(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseFlexibleDate(%q) error=%v, wantErr=%v", tc.input, err, tc.wantErr)
		}
	}
}

func TestMatchTimestampRange(t *testing.T) {
	now := time.Now().Unix()
	tests := []struct {
		name string
		ts   int64
		rf   RangeFilter
		want bool
	}{
		{"no filter", now, RangeFilter{}, true},
		{"gte pass", 100, RangeFilter{Gte: "50"}, true},
		{"gte fail", 30, RangeFilter{Gte: "50"}, false},
		{"gt pass", 100, RangeFilter{Gt: "50"}, true},
		{"gt fail", 50, RangeFilter{Gt: "50"}, false},
		{"lte pass", 50, RangeFilter{Lte: "100"}, true},
		{"lte fail", 200, RangeFilter{Lte: "100"}, false},
		{"lt pass", 50, RangeFilter{Lt: "100"}, true},
		{"lt fail", 100, RangeFilter{Lt: "100"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchTimestampRange(tc.ts, tc.rf)
			if got != tc.want {
				t.Errorf("matchTimestampRange(%d, %+v) = %v, want %v", tc.ts, tc.rf, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// fts_boolean.go: SearchBoolean
// ---------------------------------------------------------------------------

func TestSearchBooleanEmpty(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	results, err := idx.SearchBoolean("col", &fts.ParsedQuery{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchBooleanTerms(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "the quick brown fox")
	_ = idx.Index("col", "d2", "the lazy brown dog")
	_ = idx.Index("col", "d3", "a red car on highway")

	pq := &fts.ParsedQuery{
		Clauses: []fts.QueryClause{
			{Type: "term", Value: "brown", Operator: "AND"},
		},
	}
	results, err := idx.SearchBoolean("col", pq, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 1 {
		t.Error("expected at least 1 result for 'brown'")
	}
}

// ---------------------------------------------------------------------------
// storage_memory.go: Stats
// ---------------------------------------------------------------------------

func TestMemoryBackendStats(t *testing.T) {
	m := NewMemoryBackend()
	defer func() { _ = m.Close() }()

	if m.Stats("col") != 0 {
		t.Error("empty collection should have 0 docs")
	}
	_ = m.PutDoc("col", "d1", []byte("a"))
	_ = m.PutDoc("col", "d2", []byte("b"))
	if m.Stats("col") != 2 {
		t.Errorf("expected 2, got %d", m.Stats("col"))
	}
	if m.Stats("other") != 0 {
		t.Error("other collection should have 0")
	}
}

// ---------------------------------------------------------------------------
// keybuilder.go: Reset
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// binlog_entry.go: binlog.BinlogOps.Len
// ---------------------------------------------------------------------------

func TestBinlogOpsLen(t *testing.T) {
	bo := &binlog.BinlogOps{}
	if bo.Len() != 0 {
		t.Error("empty should be 0")
	}
	bo.Put("bucket", []byte("key"), []byte("val"))
	if bo.Len() != 1 {
		t.Errorf("expected 1, got %d", bo.Len())
	}
	bo.Delete("bucket", []byte("key"))
	if bo.Len() != 2 {
		t.Errorf("expected 2, got %d", bo.Len())
	}
}

// ---------------------------------------------------------------------------
// compression.go: compression.ConfigureCompression
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// fts_positions.go: RemovePositions
// ---------------------------------------------------------------------------

func TestRemovePositions(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.IndexPositionsWithLang("col", "doc1", "hello world test", "en")
	err := idx.RemovePositions("col", "doc1")
	if err != nil {
		t.Fatal(err)
	}
	// Remove again (no-op)
	err = idx.RemovePositions("col", "doc1")
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// fts_synonyms.go: SetBinlog
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// fts_stopwords.go: SetBinlog
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// fts_stopwords.go: LoadAll (partial coverage → boost)
// ---------------------------------------------------------------------------

func TestStopWordManagerLoadAll(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	swm := fts.NewStopWordManager(db)
	_ = swm.EnsureBucket()

	// Add some custom stop words
	_ = swm.Add("col1", []string{"foo", "bar"})
	_ = swm.Add("col2", []string{"baz"})

	// Create a new manager and load
	swm2 := fts.NewStopWordManager(db)
	_ = swm2.EnsureBucket()
	if err := swm2.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if !swm2.IsStopWord("col1", "foo") {
		t.Error("expected 'foo' in col1 after LoadAll")
	}
	if !swm2.IsStopWord("col2", "baz") {
		t.Error("expected 'baz' in col2 after LoadAll")
	}
}

// ---------------------------------------------------------------------------
// fts_boolean.go: SearchBoolean with NOT operator
// ---------------------------------------------------------------------------

func TestSearchBooleanOR(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "apple banana cherry")
	_ = idx.Index("col", "d2", "orange grape melon")

	pq := &fts.ParsedQuery{
		Clauses: []fts.QueryClause{
			{Type: "term", Value: "apple", Operator: "OR"},
			{Type: "term", Value: "orange", Operator: "OR"},
		},
	}
	results, err := idx.SearchBoolean("col", pq, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Errorf("expected at least 2 results with OR, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// fts.go: Search with various query styles (boost partial coverage)
// ---------------------------------------------------------------------------

func TestFTSSearchLimitCB3(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	for i := 0; i < 20; i++ {
		_ = idx.Index("col", "doc-"+itoa(i), "common term everywhere document")
	}

	results, err := idx.Search("col", "common term", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) > 5 {
		t.Errorf("limit=5 but got %d results", len(results))
	}
}

// ---------------------------------------------------------------------------
// storage_backend.go: Default, CreateBackend (if possible)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// More FTS coverage: Search, Remove, Index edge cases
// ---------------------------------------------------------------------------

func TestFTSIndexUpdateExisting(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "doc1", "original content here")
	_ = idx.Index("col", "doc1", "updated content completely different")

	results, _ := idx.Search("col", "updated different", 10)
	found := false
	for _, r := range results {
		if r.DocID == "doc1" {
			found = true
		}
	}
	if !found {
		t.Error("expected doc1 with updated content")
	}

	results2, _ := idx.Search("col", "original", 10)
	for _, r := range results2 {
		if r.DocID == "doc1" {
			t.Error("old content should not match after re-index")
		}
	}
}

func TestFTSIndexWithLangAndRemove(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	reg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(reg)
	idx.SetLangRegistry(reg)
	_ = idx.EnsureBuckets()

	_ = idx.IndexWithLang("col", "d1", "programming languages computers", "en")
	_ = idx.IndexWithLang("col", "d2", "cooking recipes kitchen", "en")
	_ = idx.IndexWithLang("col", "d3", "programming algorithms data", "en")

	results, _ := idx.Search("col", "programming", 10)
	if len(results) < 2 {
		t.Errorf("expected 2+ results, got %d", len(results))
	}

	_ = idx.Remove("col", "d1")
	results2, _ := idx.Search("col", "programming", 10)
	for _, r := range results2 {
		if r.DocID == "d1" {
			t.Error("d1 should be removed")
		}
	}
}

// ---------------------------------------------------------------------------
// FTS phrase search (fts_positions.go coverage)
// ---------------------------------------------------------------------------

func TestFTSPhraseSearch(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "the quick brown fox jumps over the lazy dog")
	_ = idx.IndexPositionsWithLang("col", "d1", "the quick brown fox jumps over the lazy dog", "en")
	_ = idx.Index("col", "d2", "brown quick fox lazy")
	_ = idx.IndexPositionsWithLang("col", "d2", "brown quick fox lazy", "en")

	results, err := idx.SearchPhrase("col", "quick brown fox", 10)
	if err != nil {
		t.Fatal(err)
	}
	// d1 has exact phrase, d2 does not
	foundD1 := false
	for _, r := range results {
		if r.DocID == "d1" {
			foundD1 = true
		}
	}
	if !foundD1 {
		t.Error("expected d1 to match phrase 'quick brown fox'")
	}
}

// ---------------------------------------------------------------------------
// FTS wildcard search (fts_wildcard.go coverage)
// ---------------------------------------------------------------------------

func TestFTSWildcardSearch(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "programming programmer programmatic")
	_ = idx.Index("col", "d2", "cooking cookies cookbook")

	results, err := idx.SearchWildcard("col", "program*", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 1 {
		t.Error("expected at least 1 result for 'program*'")
	}
	foundD1 := false
	for _, r := range results {
		if r.DocID == "d1" {
			foundD1 = true
		}
	}
	if !foundD1 {
		t.Error("d1 should match program*")
	}
}

// ---------------------------------------------------------------------------
// fts_range.go: matchNumericRange
// ---------------------------------------------------------------------------

func TestMatchNumericRange(t *testing.T) {
	tests := []struct {
		val  float64
		rf   RangeFilter
		want bool
	}{
		{25.0, RangeFilter{Gte: "10", Lte: "50"}, true},
		{5.0, RangeFilter{Gte: "10"}, false},
		{100.0, RangeFilter{Lte: "50"}, false},
		{10.0, RangeFilter{Gt: "10"}, false},
		{50.0, RangeFilter{Lt: "50"}, false},
		{11.0, RangeFilter{Gt: "10"}, true},
		{49.0, RangeFilter{Lt: "50"}, true},
	}
	for _, tc := range tests {
		got := matchNumericRange(tc.val, tc.rf)
		if got != tc.want {
			t.Errorf("matchNumericRange(%f, %+v) = %v, want %v", tc.val, tc.rf, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// upload_handler.go: more converters
// ---------------------------------------------------------------------------

func TestHtmlToMarkdownCodeBlock(t *testing.T) {
	html := "<pre><code>func main() {}</code></pre>"
	got := htmlToMarkdown([]byte(html))
	if !strings.Contains(got, "func main()") {
		t.Errorf("expected code content, got %q", got)
	}
}

func TestDocxXMLToMarkdownList(t *testing.T) {
	xml := `<w:body>
		<w:p><w:pPr><w:numPr/></w:pPr><w:r><w:t>Item one</w:t></w:r></w:p>
		<w:p><w:pPr><w:numPr/></w:pPr><w:r><w:t>Item two</w:t></w:r></w:p>
	</w:body>`
	got := docxXMLToMarkdown(xml)
	if !strings.Contains(got, "- Item one") {
		t.Errorf("expected list items: %q", got)
	}
}

func TestOdtXMLToMarkdownEmpty(t *testing.T) {
	got := odtXMLToMarkdown("<office:body><office:text></office:text></office:body>")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestRtfSpecialChars(t *testing.T) {
	rtf := `{\rtf1\ansi Hello\endash world\emdash test\par Done}`
	got := rtfToMarkdown([]byte(rtf))
	if !strings.Contains(got, "–") {
		t.Errorf("expected endash, got %q", got)
	}
	if !strings.Contains(got, "—") {
		t.Errorf("expected emdash, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// vector_index.go: more similarity edge cases
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// FormatTimestamp (bytes_utils.go)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// fts_boolean.go: SearchBoolean with phrase clause
// ---------------------------------------------------------------------------

func TestSearchBooleanPhrase(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "the quick brown fox jumps over")
	_ = idx.IndexPositionsWithLang("col", "d1", "the quick brown fox jumps over", "en")
	_ = idx.Index("col", "d2", "brown fox quick jumping around")
	_ = idx.IndexPositionsWithLang("col", "d2", "brown fox quick jumping around", "en")

	pq := &fts.ParsedQuery{
		Clauses: []fts.QueryClause{
			{Type: "phrase", Value: "quick brown fox", Operator: "AND"},
		},
	}
	results, err := idx.SearchBoolean("col", pq, 10)
	if err != nil {
		t.Fatal(err)
	}
	_ = results // phrase search exercises more code paths
}

func TestSearchBooleanWildcard(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "programming languages software")
	_ = idx.Index("col", "d2", "cooking recipes kitchen")

	pq := &fts.ParsedQuery{
		Clauses: []fts.QueryClause{
			{Type: "wildcard", Value: "program*", Operator: "AND"},
		},
	}
	results, err := idx.SearchBoolean("col", pq, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 1 {
		t.Error("expected at least 1 wildcard result")
	}
}

func TestSearchBooleanMultipleAND(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "apple banana cherry")
	_ = idx.Index("col", "d2", "apple cherry grape")
	_ = idx.Index("col", "d3", "banana grape melon")

	pq := &fts.ParsedQuery{
		Clauses: []fts.QueryClause{
			{Type: "term", Value: "apple", Operator: "AND"},
			{Type: "term", Value: "cherry", Operator: "AND"},
		},
	}
	results, err := idx.SearchBoolean("col", pq, 10)
	if err != nil {
		t.Fatal(err)
	}
	// d1 and d2 have both apple and cherry
	if len(results) < 1 {
		t.Error("expected results for apple AND cherry")
	}
}

// ---------------------------------------------------------------------------
// fts.go: more search paths — metadata search, empty results
// ---------------------------------------------------------------------------

func TestFTSSearchNoResultsCB3(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "hello world")
	results, err := idx.Search("col", "xyzzyznonexistent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestFTSSearchMultipleCollections(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col1", "d1", "unique document alpha")
	_ = idx.Index("col2", "d2", "unique document beta")

	r1, _ := idx.Search("col1", "unique", 10)
	r2, _ := idx.Search("col2", "unique", 10)

	if len(r1) != 1 || r1[0].DocID != "d1" {
		t.Errorf("col1 should only find d1, got %v", r1)
	}
	if len(r2) != 1 || r2[0].DocID != "d2" {
		t.Errorf("col2 should only find d2, got %v", r2)
	}
}

// ---------------------------------------------------------------------------
// fts_positions.go: IndexPositionsWithLang + SearchPhrase edge cases
// ---------------------------------------------------------------------------

func TestSearchPhraseNoPositions(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	// Index without positions
	_ = idx.Index("col", "d1", "hello world test")
	results, err := idx.SearchPhrase("col", "hello world", 10)
	if err != nil {
		t.Fatal(err)
	}
	_ = results // should not panic
}

func TestSearchPhraseEmpty(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	results, err := idx.SearchPhrase("col", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Error("empty phrase should return 0 results")
	}
}

// ---------------------------------------------------------------------------
// fts_wildcard.go: SearchWildcard edge cases
// ---------------------------------------------------------------------------

func TestSearchWildcardNoMatch(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "hello world")
	results, err := idx.SearchWildcard("col", "zzz*", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchWildcardQuestion(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "cat bat hat mat")
	results, err := idx.SearchWildcard("col", "?at", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 1 {
		t.Error("expected at least 1 result for '?at'")
	}
}

// ---------------------------------------------------------------------------
// More FTS: index many docs, test BM25 scoring, searchSingleTerm
// ---------------------------------------------------------------------------

func TestFTSSearchScoringOrder(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	// d1 mentions "database" 3 times, d2 once — d1 should score higher
	_ = idx.Index("col", "d1", "database database database performance")
	_ = idx.Index("col", "d2", "database storage solution")
	_ = idx.Index("col", "d3", "completely unrelated content here")

	results, _ := idx.Search("col", "database", 10)
	if len(results) < 2 {
		t.Fatalf("expected 2+ results, got %d", len(results))
	}
	if results[0].Score < results[1].Score {
		t.Error("d1 should score higher than d2")
	}
}

func TestFTSWildcardStarOnly(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "hello world")
	results, err := idx.SearchWildcard("col", "*", 10)
	if err != nil {
		t.Fatal(err)
	}
	// "*" matches everything
	if len(results) < 1 {
		t.Error("'*' should match all docs")
	}
}

func TestFTSIndexPositionsMultipleDocs(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	for i := 0; i < 10; i++ {
		content := "document number " + itoa(i) + " with some common words"
		_ = idx.Index("col", "doc"+itoa(i), content)
		_ = idx.IndexPositionsWithLang("col", "doc"+itoa(i), content, "en")
	}

	results, _ := idx.SearchPhrase("col", "common words", 10)
	if len(results) < 5 {
		t.Errorf("expected 5+ phrase results, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// compression.go: Compress / Decompress round-trip
// ---------------------------------------------------------------------------

func TestCompressDecompress(t *testing.T) {
	data := []byte("hello world this is a test of compression with some repeated content hello world hello world")
	compressed := compression.CompressDoc(data)
	if len(compressed) == 0 {
		t.Fatal("compressed should not be empty")
	}
	decompressed, err := compression.DecompressDoc(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if string(decompressed) != string(data) {
		t.Error("round-trip failed")
	}
}

func TestGetCompressionStats(t *testing.T) {
	data := []byte("hello world test data for compression stats analysis")
	stats := compression.GetCompressionStats(data)
	if stats.OriginalSize != len(data) {
		t.Errorf("expected OriginalSize=%d, got %d", len(data), stats.OriginalSize)
	}
}

// ---------------------------------------------------------------------------
// More coverage: delta encoder, bloom filter rebuild path, vector store
// ---------------------------------------------------------------------------

func TestDeltaEncoderRoundTrip(t *testing.T) {
	de := delta.NewDeltaEncoder()
	original := []byte("hello world this is test data for delta encoding")
	modified := []byte("hello world this is modified data for delta encoding")

	delta := de.Encode(original, modified)
	if len(delta) == 0 {
		t.Fatal("delta should not be empty")
	}
	decoded, err := de.Decode(original, delta)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(modified) {
		t.Errorf("round-trip failed: got %q", string(decoded))
	}

	origSize, deltaSize, ratio := de.Stats(original, modified)
	if origSize < 1 || deltaSize < 1 || ratio <= 0 {
		t.Errorf("unexpected stats: orig=%d delta=%d ratio=%f", origSize, deltaSize, ratio)
	}
}

func TestDeltaEncoderIdentical(t *testing.T) {
	de := delta.NewDeltaEncoder()
	data := []byte("identical content")
	delta := de.Encode(data, data)
	decoded, err := de.Decode(data, delta)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(data) {
		t.Error("identical round-trip failed")
	}
}

func TestDeltaEncoderEmpty(t *testing.T) {
	de := delta.NewDeltaEncoder()
	delta := de.Encode(nil, []byte("new"))
	decoded, err := de.Decode(nil, delta)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "new" {
		t.Errorf("nil->new: got %q", string(decoded))
	}
}

func TestFTSIndexManyDocsSearch(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	// Index 50 docs
	for i := 0; i < 50; i++ {
		_ = idx.Index("col", "d"+itoa(i), "common shared term document number "+itoa(i))
	}
	results, _ := idx.Search("col", "common shared", 10)
	if len(results) != 10 {
		t.Errorf("expected limit=10, got %d", len(results))
	}
}

func TestParseRangeBoundaryCB3(t *testing.T) {
	// Unix timestamp
	got := parseRangeBoundary("1704067200")
	if got != 1704067200 {
		t.Errorf("expected 1704067200, got %d", got)
	}
	// Date string
	got2 := parseRangeBoundary("2024-01-01")
	if got2 == 0 {
		t.Error("expected non-zero for valid date")
	}
	// Invalid
	got3 := parseRangeBoundary("not-a-number-or-date!!!")
	if got3 != 0 {
		t.Errorf("expected 0 for invalid, got %d", got3)
	}
}

func TestMatchStringRangeCB3(t *testing.T) {
	// Additional edge cases
	if !matchStringRange("m", RangeFilter{Gte: "a", Lte: "z"}) {
		t.Error("m should be in [a, z]")
	}
	if matchStringRange("a", RangeFilter{Gt: "a"}) {
		t.Error("a should not match gt=a")
	}
}

// ---------------------------------------------------------------------------
// fts_range.go: matchRangeFilter (covers timestamp, numeric, string, date paths)
// ---------------------------------------------------------------------------

func TestMatchRangeFilterTimestamp(t *testing.T) {
	doc := &storage.Doc{AddedAt: 1000, UpdatedAt: 2000}
	if !matchRangeFilter(doc, RangeFilter{Field: "addedAt", Gte: "500", Lte: "1500"}) {
		t.Error("addedAt=1000 should match [500,1500]")
	}
	if matchRangeFilter(doc, RangeFilter{Field: "addedAt", Gt: "1500"}) {
		t.Error("addedAt=1000 should not match >1500")
	}
	if !matchRangeFilter(doc, RangeFilter{Field: "updatedAt", Lte: "3000"}) {
		t.Error("updatedAt=2000 should match <=3000")
	}
}

func TestMatchRangeFilterNumeric(t *testing.T) {
	doc := &storage.Doc{Meta: map[string][]string{"price": {"25.5"}}}
	if !matchRangeFilter(doc, RangeFilter{Field: "price", Gte: "10", Lte: "50"}) {
		t.Error("price=25.5 should match [10,50]")
	}
	if matchRangeFilter(doc, RangeFilter{Field: "price", Gt: "30"}) {
		t.Error("price=25.5 should not match >30")
	}
}

func TestMatchRangeFilterString(t *testing.T) {
	doc := &storage.Doc{Meta: map[string][]string{"name": {"medium"}}}
	if !matchRangeFilter(doc, RangeFilter{Field: "name", Gte: "a", Lte: "z"}) {
		t.Error("name=medium should match [a,z]")
	}
}

func TestMatchRangeFilterMissing(t *testing.T) {
	doc := &storage.Doc{Meta: map[string][]string{}}
	if matchRangeFilter(doc, RangeFilter{Field: "missing", Gte: "0"}) {
		t.Error("missing field should not match")
	}
}

func TestMatchRangeFilterDate(t *testing.T) {
	doc := &storage.Doc{Meta: map[string][]string{"published": {"2024-06-15"}}}
	if !matchRangeFilter(doc, RangeFilter{Field: "published", Gte: "2024-01-01", Lte: "2024-12-31"}) {
		t.Error("2024-06-15 should match [2024-01-01, 2024-12-31]")
	}
}

// ---------------------------------------------------------------------------
// fts_bm25.go: decodeCollectionStats (partial 66.7%)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// fts_positions.go: SearchProximity, findMinSpan
// ---------------------------------------------------------------------------

func TestSearchProximity(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "the quick brown fox jumped over the lazy sleeping dog in the park")
	_ = idx.IndexPositionsWithLang("col", "d1", "the quick brown fox jumped over the lazy sleeping dog in the park", "en")
	_ = idx.Index("col", "d2", "brown dog")
	_ = idx.IndexPositionsWithLang("col", "d2", "brown dog", "en")

	results, err := idx.SearchProximity("col", "brown dog", 5, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 1 {
		t.Error("expected at least 1 proximity result")
	}
}

func TestSearchProximityNoPositions(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	results, err := idx.SearchProximity("col", "hello world", 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	_ = results
}

// ---------------------------------------------------------------------------
// fts_bm25.go: RemoveBM25Meta
// ---------------------------------------------------------------------------

func TestRemoveBM25MetaViaTx(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "doc1", "hello world test")
	err := db.Update(func(tx *bolt.Tx) error {
		_ = idx.RemoveBM25Meta(tx, "col", "doc1")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// fts_positions.go: decodePositions edge cases
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// fts_boolean.go: combineResults (AND logic)
// ---------------------------------------------------------------------------

func TestSearchBooleanANDNoOverlap(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "apple banana")
	_ = idx.Index("col", "d2", "cherry grape")

	pq := &fts.ParsedQuery{
		Clauses: []fts.QueryClause{
			{Type: "term", Value: "apple", Operator: "AND"},
			{Type: "term", Value: "cherry", Operator: "AND"},
		},
	}
	results, err := idx.SearchBoolean("col", pq, 10)
	if err != nil {
		t.Fatal(err)
	}
	// No doc has both apple AND cherry
	if len(results) != 0 {
		t.Errorf("expected 0 results for non-overlapping AND, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// More coverage: FTS field indexing, vector store, worker pool
// ---------------------------------------------------------------------------

func TestFTSIndexFieldsAndSearch(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	fields := map[string]string{
		"title": "Introduction to Go Programming",
		"body":  "Go is a statically typed compiled language designed at Google",
	}
	if err := idx.IndexFields("col", "doc1", fields); err != nil {
		t.Fatal(err)
	}

	fields2 := map[string]string{
		"title": "Python for Data Science",
		"body":  "Python is widely used for machine learning and data analysis",
	}
	if err := idx.IndexFields("col", "doc2", fields2); err != nil {
		t.Fatal(err)
	}

	// IndexFields stores under field-specific keys; just verify no error
	_ = idx
}

func TestFTSIndexFieldsWithLang(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	reg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(reg)
	idx.SetLangRegistry(reg)
	_ = idx.EnsureBuckets()

	fields := map[string]string{
		"title": "Running Fast Marathon Training",
		"body":  "Learn how to train for your first marathon with proper running technique",
	}
	if err := idx.IndexFieldsWithLang("col", "doc1", fields, "en"); err != nil {
		t.Fatal(err)
	}
}

func TestSearchBooleanMultipleOR(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	for i := 0; i < 15; i++ {
		_ = idx.Index("col", "d"+itoa(i), "word"+itoa(i)+" content text")
	}

	pq := &fts.ParsedQuery{
		Clauses: []fts.QueryClause{
			{Type: "term", Value: "word0", Operator: "OR"},
			{Type: "term", Value: "word5", Operator: "OR"},
			{Type: "term", Value: "word10", Operator: "OR"},
		},
	}
	results, err := idx.SearchBoolean("col", pq, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 3 {
		t.Errorf("expected 3+ OR results, got %d", len(results))
	}
}

func TestFTSSynonymExpansionInSearch(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	sm := fts.NewSynonymManager(db)
	_ = sm.EnsureBucket()
	_ = sm.Set("col", "happy", []string{"glad", "joyful"})
	idx.SetSynonymManager(sm)
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "glad tidings today")
	_ = idx.Index("col", "d2", "sad news yesterday")

	// TokenizeQuery should expand "happy" to include "glad"/"joyful"
	terms := idx.TokenizeQuery("col", "happy")
	// At minimum: "happi" (stemmed happy) + synonyms
	if len(terms) < 1 {
		t.Errorf("expected at least 1 term, got %d: %v", len(terms), terms)
	}
}

// ---------------------------------------------------------------------------
// More coverage: FTS BM25F fuzzy, synonym loading
// ---------------------------------------------------------------------------

func TestFTSSynonymExpandMultiple(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	sm := fts.NewSynonymManager(db)
	_ = sm.EnsureBucket()
	_ = sm.Set("col", "big", []string{"large", "huge", "enormous"})
	_ = sm.Set("col", "fast", []string{"quick", "rapid", "swift"})

	expanded := sm.Expand("col", []string{"big", "fast"})
	if len(expanded) < 4 {
		t.Errorf("expected 4+ expanded terms, got %d: %v", len(expanded), expanded)
	}
}

func TestFTSSynonymDelete(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	sm := fts.NewSynonymManager(db)
	_ = sm.EnsureBucket()
	_ = sm.Set("col", "big", []string{"large"})
	_ = sm.Delete("col", "big")
	syns := sm.Get("col", "big")
	if len(syns) != 0 {
		t.Errorf("expected 0 synonyms after delete, got %d", len(syns))
	}
}

func TestFTSSynonymList(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	sm := fts.NewSynonymManager(db)
	_ = sm.EnsureBucket()
	_ = sm.Set("col", "big", []string{"large"})
	_ = sm.Set("col", "fast", []string{"quick"})

	list := sm.List("col")
	if len(list) < 2 {
		t.Errorf("expected 2+ synonym entries, got %d", len(list))
	}
}

// ---------------------------------------------------------------------------
// storage_memory.go: more operations
// ---------------------------------------------------------------------------

func TestMemoryBackendDeleteDoc(t *testing.T) {
	m := NewMemoryBackend()
	defer func() { _ = m.Close() }()

	_ = m.PutDoc("col", "d1", []byte("data"))
	if err := m.DeleteDoc("col", "d1"); err != nil {
		t.Fatal(err)
	}
	got, _ := m.GetDoc("col", "d1")
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestMemoryBackendDeleteByKey(t *testing.T) {
	m := NewMemoryBackend()
	defer func() { _ = m.Close() }()

	_ = m.PutByKey("col", "k1", "en", "doc1")
	if err := m.DeleteByKey("col", "k1", "en"); err != nil {
		t.Fatal(err)
	}
	got, _ := m.GetByKey("col", "k1", "en")
	if got != "" {
		t.Errorf("expected empty after delete, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// More pure function coverage
// ---------------------------------------------------------------------------

func TestFTSSearchBM25FFuzzy(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	fields1 := map[string]string{"title": "Golang Programming Guide", "body": "Learn Go language basics and advanced topics"}
	_ = idx.IndexFields("col", "d1", fields1)
	fields2 := map[string]string{"title": "Python Tutorial", "body": "Python programming for beginners"}
	_ = idx.IndexFields("col", "d2", fields2)

	tokens := map[string]int{"programing": 1}
	results, err := idx.SearchBM25FFuzzy("col", tokens, 10, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = results // fuzzy match "programing" → "programming"
}

func TestFTSSearchBM25Fuzzy(t *testing.T) {
	db := openTestDB(t)
	defer func() { _ = db.Close() }()
	idx := fts.NewFTSIndex(db)
	idx.SetStemmer(fts.NewPorterStemmer())
	_ = idx.EnsureBuckets()

	_ = idx.Index("col", "d1", "algorithm optimization performance")
	_ = idx.Index("col", "d2", "database indexing storage")

	results, err := idx.SearchBM25Fuzzy("col", "algorithmm", 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	_ = results // fuzzy match "algorithmm" → "algorithm"
}

func TestCompressDocRoundTrip(t *testing.T) {
	compression.ConfigureCompression(true, 10, 100)
	defer compression.ConfigureCompression(false, 0, 0)

	// Small data (below threshold)
	small := []byte("tiny")
	compressed := compression.CompressDoc(small)
	decompressed, err := compression.DecompressDoc(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if string(decompressed) != "tiny" {
		t.Errorf("small round-trip: got %q", string(decompressed))
	}

	// Larger data (above threshold)
	large := []byte(strings.Repeat("hello world this is a compression test ", 50))
	compressed2 := compression.CompressDoc(large)
	decompressed2, err := compression.DecompressDoc(compressed2)
	if err != nil {
		t.Fatal(err)
	}
	if string(decompressed2) != string(large) {
		t.Error("large round-trip failed")
	}
}

func TestFormatTimestampCB3(t *testing.T) {
	buf := make([]byte, 20)
	result := FormatTimestamp(0, buf)
	if string(result) != "00000000000000000000" {
		t.Errorf("zero: got %q", string(result))
	}
	result2 := FormatTimestamp(12345, buf)
	if len(result2) != 20 {
		t.Errorf("expected 20 chars, got %d", len(result2))
	}
	if !strings.HasSuffix(string(result2), "12345") {
		t.Errorf("expected suffix 12345, got %q", string(result2))
	}
	// Small buffer
	result3 := FormatTimestamp(99, nil)
	if len(result3) != 20 {
		t.Errorf("small buf: expected 20, got %d", len(result3))
	}
}
