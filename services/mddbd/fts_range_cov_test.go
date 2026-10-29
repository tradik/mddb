package main

import (
	"testing"
	"time"

	"mddb/internal/fts"
	"mddb/internal/storage"
)

// covGRangeDocs stores numbered documents in "r", a corrupt and an expired
// one, and a document in "r~", whose keys sort straight after "r|"'s.
func covGRangeDocs(t *testing.T, s *Server) {
	t.Helper()
	for _, d := range []storage.Doc{
		{ID: "a", Key: "a", Lang: "en", Meta: map[string][]string{"n": {"5"}}},
		{ID: "b", Key: "b", Lang: "en", Meta: map[string][]string{"n": {"15"}}},
		{ID: "x", Key: "x", Lang: "en", Meta: map[string][]string{"n": {"7"}}, ExpiresAt: time.Now().Add(-time.Hour).Unix()},
	} {
		covGPutDoc(t, s, "r", &d)
	}
	covGPutRaw(t, s, "r", "c", []byte{0xff, 0xfe})
	covGPutDoc(t, s, "r~", &storage.Doc{ID: "z", Key: "z", Lang: "en", Meta: map[string][]string{"n": {"6"}}})
}

func TestCovGSearchRange(t *testing.T) {
	s := covGServer(t)
	covGRangeDocs(t, s)
	low := []RangeFilter{{Field: "n", Lte: "10"}}

	res, err := s.SearchRange("r", low, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Only a: c is corrupt, x expired, and r~'s document is another collection.
	if len(res) != 1 || res[0].DocID != "a" {
		t.Errorf("SearchRange = %+v", res)
	}

	closeStorage(t, s, "r")
	if _, err := s.SearchRange("r", low, 0); err == nil {
		t.Error("a store failure must be returned")
	}
}

func TestCovGSearchRangeWithoutDocsBucket(t *testing.T) {
	s := covGServer(t)
	s.BucketNames.Docs = []byte("no-such-bucket")
	res, err := s.SearchRange("r", []RangeFilter{{Field: "n", Gte: "1"}}, 5)
	if err != nil || len(res) != 0 {
		t.Errorf("no bucket: %+v %v", res, err)
	}
	in := []fts.FTSResult{{DocID: "a"}}
	out, err := s.FilterByRange("r", in, []RangeFilter{{Field: "n", Gte: "1"}})
	if err != nil || len(out) != 0 {
		t.Errorf("FilterByRange without a bucket keeps nothing: %+v %v", out, err)
	}
}

func TestCovGFilterByRange(t *testing.T) {
	s := covGServer(t)
	covGRangeDocs(t, s)
	in := []fts.FTSResult{{DocID: "a"}, {DocID: "b"}, {DocID: "c"}, {DocID: "ghost"}}

	// No ranges: the input passes through untouched.
	if out, err := s.FilterByRange("r", in, nil); err != nil || len(out) != 4 {
		t.Errorf("no ranges: %+v %v", out, err)
	}

	out, err := s.FilterByRange("r", in, []RangeFilter{{Field: "n", Gt: "10"}})
	if err != nil || len(out) != 1 || out[0].DocID != "b" {
		t.Errorf("filtered = %+v %v (missing and corrupt documents are dropped)", out, err)
	}
}
