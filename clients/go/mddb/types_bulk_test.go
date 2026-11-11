package mddb

import (
	"encoding/json"
	"testing"
)

// The server answers notFound since 2.19.0 and not_found (deprecated) before
// and alongside it; the client reads whichever it gets.
func TestDeleteBatchResponseReadsBothSpellings(t *testing.T) {
	for body, want := range map[string]int{
		`{"deleted":1,"notFound":3,"not_found":3,"failed":0}`: 3, // 2.19.0+
		`{"deleted":1,"notFound":4}`:                          4, // after the alias is removed
		`{"deleted":1,"not_found":2,"failed":0}`:              2, // before 2.19.0
		`{"deleted":1}`:                                       0,
	} {
		var r DeleteBatchResponse
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if r.NotFound != want || r.Deleted != 1 {
			t.Errorf("%s: %+v, want notFound %d", body, r, want)
		}
	}
	var r DeleteBatchResponse
	if err := json.Unmarshal([]byte(`{"notFound":"x"}`), &r); err == nil {
		t.Error("a malformed count was accepted")
	}
}
