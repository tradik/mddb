package benchkit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The same seed draws the same corpus; documents are unit vectors grouped by
// topic, each nearer its own centroid than any other.
func TestGen_Clustered(t *testing.T) {
	docs, centroids := NewGen(7, 16).Clustered(3, 5)
	again, _ := NewGen(7, 16).Clustered(3, 5)
	if len(docs) != 15 || len(centroids) != 3 || Dot(docs[4], again[4]) < 0.9999 {
		t.Fatalf("%d docs, %d centroids", len(docs), len(centroids))
	}
	for i, d := range docs {
		if n := Dot(d, d); n < 0.999 || n > 1.001 {
			t.Fatalf("doc %d has length² %f", i, n)
		}
		own := Dot(d, centroids[i/5])
		for t2, c := range centroids {
			if t2 != i/5 && Dot(d, c) > own {
				t.Fatalf("doc %d is nearer topic %d than its own", i, t2)
			}
		}
	}
}

// Nearest is the exact top k, best first, among what allow admits.
func TestNearest(t *testing.T) {
	docs := [][]float32{{1, 0}, {0.8, 0.6}, {0, 1}, {-1, 0}, {0.6, 0.8}}
	q := []float32{1, 0}
	if got := Nearest(docs, q, 3, nil); len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 4 {
		t.Fatalf("%v", got)
	}
	even := func(i int) bool { return i%2 == 0 }
	if got := Nearest(docs, q, 2, even); len(got) != 2 || got[0] != 0 || got[1] != 4 {
		t.Fatalf("filtered: %v", got)
	}
	if got := Nearest(docs, q, 0, nil); len(got) != 0 {
		t.Fatalf("k 0: %v", got)
	}
	if got := Nearest(docs, q, 9, nil); len(got) != 5 || got[4] != 3 {
		t.Fatalf("k past the corpus: %v", got)
	}
}

// The embedder answers a text naming a document with its vector, and
// anything else with a fixed one.
func TestEmbedder(t *testing.T) {
	e := &Embedder{Docs: [][]float32{{0, 1}, {1, 0}}, Dims: 2, Ref: regexp.MustCompile(`doc-(\d+)`)}
	srv := httptest.NewServer(e)
	defer srv.Close()
	resp, err := srv.Client().Post(srv.URL+"/v1/embeddings", "application/json",
		strings.NewReader(`{"input": ["see doc-0", "unrelated", "doc-9", "doc-99999999999999999999"]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || len(got.Data) != 4 {
		t.Fatalf("%v %+v", err, got)
	}
	if got.Data[0].Embedding[1] != 1 || got.Data[3].Index != 3 {
		t.Fatalf("%+v", got.Data)
	}
	for _, d := range got.Data[1:] {
		if d.Embedding[0] != 1 || d.Embedding[1] != 0 {
			t.Fatalf("fallback vector: %+v", d)
		}
	}
	bad, err := srv.Client().Post(srv.URL+"/v1/embeddings", "application/json", strings.NewReader("{"))
	if err != nil || bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed request: %v", err)
	}
	_ = bad.Body.Close()
}

func TestPost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			http.Error(w, "no", http.StatusTeapot)
			return
		}
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer srv.Close()
	if raw, err := Post(srv.Client(), srv.URL+"/good", map[string]int{"a": 1}); err != nil || !strings.Contains(string(raw), "ok") {
		t.Fatalf("%v %s", err, raw)
	}
	if _, err := Post(srv.Client(), srv.URL+"/bad", nil); err == nil || !strings.Contains(err.Error(), "418") {
		t.Fatalf("a refusal: %v", err)
	}
	if _, err := Post(srv.Client(), "http://127.0.0.1:1", nil); err == nil {
		t.Fatal("no server")
	}
}

func TestStatistics(t *testing.T) {
	xs := []float64{5, 1, 4, 2, 3}
	if Mean(xs) != 3 || Percentile(xs, 50) != 3 || Percentile(xs, 99) != 5 || xs[0] != 5 {
		t.Fatal("statistics, and the input left unsorted")
	}
	if Mean(nil) != 0 || Percentile(nil, 50) != 0 {
		t.Fatal("empty")
	}
	if v := Unit(Scale([]float32{3, 4}, 2)); v[0] < 0.5999 || v[0] > 0.6001 || v[1] < 0.7999 || v[1] > 0.8001 {
		t.Fatalf("unit: %v", v)
	}
}
