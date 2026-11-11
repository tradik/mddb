package main

import (
	"context"
	"net/http"
	"testing"

	json "mddb/internal/jsonx"
	proto "mddb/proto"
)

// Every document in a duplicate group names its language: the panel used to
// parse it off the end of docId, which only works while the ID scheme stays
// what it is today.
func TestFindDuplicatesGroupsCarryLang(t *testing.T) {
	srv, cleanup := newHandlerTestServer(t)
	defer cleanup()

	batch := []*proto.BatchDocument{
		makeBatchDoc("restart-staging", "en_GB", runbook+" on staging", nil, false),
		makeBatchDoc("restart-production", "pl", runbook+" on production", nil, false),
	}
	if _, err := NewBatchProcessor(srv, 2).ProcessBatch(context.Background(), "langs", batch); err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, srv.handleFindDuplicates, FindDuplicatesRequest{Collection: "langs", Mode: "minhash", Threshold: 0.7})
	if rec.Code != http.StatusOK {
		t.Fatalf("find-duplicates: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		MinhashGroups []struct {
			Documents []map[string]any `json:"documents"`
		} `json:"minhashGroups"`
		MinhashPairs int `json:"minhashPairs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.MinhashGroups) != 1 || resp.MinhashPairs != 1 {
		t.Fatalf("want one minhash group of one pair, got %s", rec.Body.String())
	}
	langs := map[string]string{}
	for _, d := range resp.MinhashGroups[0].Documents {
		key, _ := d["key"].(string)
		lang, _ := d["lang"].(string)
		langs[key] = lang
	}
	if langs["restart-staging"] != "en_GB" || langs["restart-production"] != "pl" {
		t.Errorf("documents carry lang %v, want en_GB and pl", langs)
	}
}
