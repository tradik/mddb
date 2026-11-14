package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchErrors(t *testing.T) {
	base := newFake(t, &fakeMDDB{hybridFails: true})
	d := compareSearch(http.DefaultClient, side{base, "heur"}, side{base, "sem"}, "/v1/hybrid-search", []string{"q"}, 5)
	if d.Identical != 1 || len(d.Errors) != 2 {
		t.Fatalf("%+v", d)
	}
	// One side failing is a difference.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["collection"] == "sem" {
			_, _ = w.Write([]byte("not json"))
			return
		}
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	d = compareSearch(http.DefaultClient, side{srv.URL, "heur"}, side{srv.URL, "sem"}, "/v1/fts", []string{"q"}, 5)
	if len(d.Differ) != 1 || len(d.Errors) != 1 {
		t.Fatalf("%+v", d)
	}
}

func TestNormalize(t *testing.T) {
	var v any
	_ = json.Unmarshal([]byte(`{"took":1,"hits":[{"collection":"c","symbol":"T.f","meta":{"symRefs":["x"],"path":["c"]}}]}`), &v)
	got, _ := json.Marshal(normalize(v, "c", labels, 0))
	if string(got) != `{"hits":[{"collection":"\u003cc\u003e","meta":{"path":["\u003cc\u003e"]}}]}` {
		t.Fatal(string(got))
	}
}

func TestSortTies(t *testing.T) {
	var v any
	_ = json.Unmarshal([]byte(`[{"key":"b","score":1},{"key":"c","score":2},{"key":"a","score":1.0000000000000002}]`), &v)
	got, _ := json.Marshal(normalize(v, "x", nil, 0))
	if string(got) != `[{"key":"c","score":2},{"key":"a","score":1},{"key":"b","score":1}]` {
		t.Fatal(string(got))
	}
	// A full list leaves out the tie at the cut: which of them made it is chance.
	if got, _ := json.Marshal(normalize(v, "x", nil, 3)); string(got) != `[{"key":"c","score":2}]` {
		t.Fatal(string(got))
	}
	for _, in := range []string{`[1,2]`, `[{"key":"b"},{"key":"a"}]`} {
		_ = json.Unmarshal([]byte(in), &v)
		if got, _ := json.Marshal(normalize(v, "x", nil, 2)); string(got) != in {
			t.Fatalf("%s became %s", in, got)
		}
	}
}
