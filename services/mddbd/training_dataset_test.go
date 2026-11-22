package main

import (
	"errors"
	"strings"
	"testing"
)

func TestDatasetRequestValidation(t *testing.T) {
	long := strings.Repeat("x", maxPairTemplate+1)
	for name, r := range map[string]datasetRequest{
		"no collection":  {Format: datasetText},
		"too many":       {Collections: strings.Split(strings.Repeat("c,", maxDatasetCollections)+"z", ","), Format: datasetText},
		"twice":          {Collections: []string{"a", "a"}, Format: datasetText},
		"empty name":     {Collections: []string{""}, Format: datasetText},
		"max records":    {Collections: []string{"a"}, Format: datasetText, MaxRecords: -1},
		"ratio":          {Collections: []string{"a"}, Format: datasetText, ValidationRatio: 0.9},
		"format":         {Collections: []string{"a"}, Format: "chat"},
		"pairs missing":  {Collections: []string{"a"}, Format: datasetPairs},
		"pairs output":   {Collections: []string{"a"}, Format: datasetPairs, Pairs: &pairMapping{Input: "{content}"}},
		"long template":  {Collections: []string{"a"}, Format: datasetPairs, Pairs: &pairMapping{Input: long, Output: "x"}},
		"bad field":      {Collections: []string{"a"}, Format: datasetPairs, Pairs: &pairMapping{Input: "{meta}", Output: "x"}},
		"unclosed brace": {Collections: []string{"a"}, Format: datasetPairs, Pairs: &pairMapping{Input: "{content", Output: "x"}},
	} {
		if err := r.validate(); !errors.Is(err, errDatasetInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	text := datasetRequest{Collections: []string{"a"}, Format: datasetText, Pairs: &pairMapping{}}
	if err := text.validate(); err != nil || text.Pairs != nil || !text.dedupe() {
		t.Errorf("text: %v %+v", err, text)
	}
}

func TestDatasetRecordsFromCollections(t *testing.T) {
	f := newTrainingFixture(t, "")
	f.seedRecipes(t)
	req := pairsRequest()
	req.Collections = []string{"recipes", "techniques"}
	req.FilterMeta = map[string][]string{"cuisine": {"french"}}
	req.Lang = "EN"
	recs, sums, err := f.s.datasetRecords(&req)
	if err != nil {
		t.Fatal(err)
	}
	// french + en: soup, stew, dup (a duplicate of soup); techniques has no cuisine.
	if len(recs) != 2 || sums[0].Documents != 3 || sums[0].Duplicates != 1 || sums[0].Records != 2 || sums[1].Documents != 0 {
		t.Fatalf("records %d, summaries %+v", len(recs), sums)
	}
	if r := recs[0]; r.Input != "A french dish, vegan" || r.System != "You write recipes." || r.Meta.License != "CC-BY-4.0" || r.Meta.Collection != "recipes" {
		t.Errorf("record: %+v", r)
	}
	// Without dedupe the duplicate stays; a template naming a missing field skips the document.
	off := false
	req = datasetRequest{Collections: []string{"recipes", "techniques"}, Format: datasetPairs, Dedupe: &off,
		Pairs: &pairMapping{Input: "{collection}/{key}/{lang}: {meta.diet}", Output: "{content}"}}
	recs, sums, err = f.s.datasetRecords(&req)
	if err != nil || len(recs) != 5 || sums[1].Skipped != 1 || recs[0].Input != "recipes/dup/en: vegan" {
		t.Fatalf("no dedupe: %d %+v %v", len(recs), sums, err)
	}
	for _, bad := range []pairMapping{{Input: "{content}", Output: "{meta.none}"}, {Input: "{content}", Output: "{content}", System: "{meta.none}"}} {
		req.Pairs = &bad
		if recs, _, _ = f.s.datasetRecords(&req); len(recs) != 0 {
			t.Errorf("%+v kept %d records", bad, len(recs))
		}
	}
	// A seeded shuffle is reproducible; the limit applies after it.
	shuffled := func(seed int64) string {
		r := datasetRequest{Collections: []string{"recipes"}, Format: datasetText, Shuffle: true, Seed: seed, MaxRecords: 3}
		recs, _, err := f.s.datasetRecords(&r)
		if err != nil || len(recs) != 3 {
			t.Fatalf("shuffle: %d %v", len(recs), err)
		}
		var keys []string
		for _, r := range recs {
			keys = append(keys, r.Meta.Key)
		}
		return strings.Join(keys, ",")
	}
	if a, b := shuffled(7), shuffled(7); a != b {
		t.Errorf("seed 7 gave %s then %s", a, b)
	}
	closeStorage(t, f.s, "recipes")
	if _, _, err := f.s.datasetRecords(&req); err == nil {
		t.Error("a closed store gave records")
	}
}

func TestDatasetHelpers(t *testing.T) {
	recs := make([]datasetRecord, 5)
	if tr, va := splitRecords(recs, 0.1); len(tr) != 4 || len(va) != 1 {
		t.Errorf("at least one validation record: %d %d", len(tr), len(va))
	}
	if tr, va := splitRecords(recs[:1], 0.5); len(tr) != 1 || len(va) != 0 {
		t.Errorf("one record: %d %d", len(tr), len(va))
	}
	long := strings.Repeat("é", datasetPreviewChars+1)
	pv := previewRecords([]datasetRecord{{Text: long}, {}, {}, {}})
	if len(pv) != datasetPreviewRecords || !strings.HasSuffix(pv[0].Text, "…") {
		t.Errorf("preview: %d", len(pv))
	}
	s := &Server{}
	if s.collectionLicence("x") != "" {
		t.Error("licence without a collection manager")
	}
	if _, err := s.checkLicences(&datasetRequest{Collections: []string{"x"}}); !errors.Is(err, errDatasetLicence) {
		t.Errorf("unlicensed: %v", err)
	}
}
