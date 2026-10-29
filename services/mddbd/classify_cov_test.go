package main

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mddb/internal/embedding"
	"mddb/internal/storage"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// covKEmbedding is an embedding provider whose vectors are chosen per text,
// so a test can know which label is closest, and whose calls can be made to
// fail one at a time.
type covKEmbedding struct {
	vectors   map[string][]float32
	failEmbed bool
	failBatch bool
}

func (e *covKEmbedding) vectorFor(text string) []float32 {
	if v, found := e.vectors[text]; found {
		return v
	}
	return []float32{0, 0, 1}
}

func (e *covKEmbedding) Embed(_ context.Context, text string, _ embedding.Role) ([]float32, error) {
	if e.failEmbed {
		return nil, errors.New("embed down")
	}
	return e.vectorFor(text), nil
}

func (e *covKEmbedding) EmbedBatch(_ context.Context, texts []string, _ embedding.Role) ([][]float32, error) {
	if e.failBatch {
		return nil, errors.New("batch down")
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = e.vectorFor(t)
	}
	return out, nil
}

func (e *covKEmbedding) Model() string   { return "covk-model" }
func (e *covKEmbedding) Dimensions() int { return 3 }

// covKLabelEmbedding knows three labels: one aligned with [1,0,0], one at 45
// degrees to it and one orthogonal.
func covKLabelEmbedding() *covKEmbedding {
	return &covKEmbedding{vectors: map[string][]float32{
		"sports":   {1, 0, 0},
		"mixed":    {1, 1, 0},
		"politics": {0, 1, 0},
		"a match":  {1, 0, 0},
		"football": {1, 0, 0},
	}}
}

var covKLabels = []string{"politics", "sports", "mixed"}

// covKPutRaw writes val under key in bucket of the file holding collection.
func covKPutRaw(t *testing.T, s *Server, collection, bucket string, key, val []byte) {
	t.Helper()
	if err := s.DBUpdateC(collection, func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return err
		}
		return b.Put(key, val)
	}); err != nil {
		t.Fatal(err)
	}
}

// covKDeleteRaw removes key from bucket of the file holding collection.
func covKDeleteRaw(t *testing.T, s *Server, collection, bucket string, key []byte) {
	t.Helper()
	if err := s.DBUpdateC(collection, func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucket)).Delete(key)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCovKClassify_TextSortedThresholdAndTopK(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = covKLabelEmbedding()

	resp, err := s.classifyDocument(context.Background(), "", "", "", "a match", covKLabels, 0, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 3 || resp.Results[0].Label != "sports" || resp.Results[1].Label != "mixed" || resp.Results[2].Label != "politics" {
		t.Fatalf("results not ordered by similarity: %+v", resp.Results)
	}
	if math.Abs(resp.Results[0].Score-1) > 1e-6 {
		t.Errorf("identical vectors scored %v, want 1", resp.Results[0].Score)
	}
	if resp.Model != "covk-model" || resp.Dimensions != 3 {
		t.Errorf("model/dimensions = %q/%d", resp.Model, resp.Dimensions)
	}

	// The orthogonal label falls below the threshold.
	resp, err = s.classifyDocument(context.Background(), "", "", "", "a match", covKLabels, 0, false, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("threshold 0.5 kept %d labels, want 2: %+v", len(resp.Results), resp.Results)
	}

	// topK cuts after the threshold.
	resp, err = s.classifyDocument(context.Background(), "", "", "", "a match", covKLabels, 1, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Label != "sports" {
		t.Fatalf("topK 1 = %+v, want only sports", resp.Results)
	}
}

func TestCovKClassify_InputErrors(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	if _, err := s.classifyDocument(context.Background(), "", "", "", "x", covKLabels, 0, false, 0); err == nil ||
		!strings.Contains(err.Error(), "no embedding provider") {
		t.Fatalf("no provider: err = %v", err)
	}

	s.Embedding = covKLabelEmbedding()
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = "l"
	}
	cases := map[string]struct {
		text, coll, key string
		labels          []string
		want            string
	}{
		"no labels":     {text: "x", want: "labels are required"},
		"too many":      {text: "x", labels: tooMany, want: "maximum 100 labels"},
		"nothing given": {labels: covKLabels, want: "provide either text"},
		"key only":      {coll: "c", labels: covKLabels, want: "provide either text"},
	}
	for name, c := range cases {
		_, err := s.classifyDocument(context.Background(), c.coll, c.key, "", c.text, c.labels, 0, false, 0)
		var inv invalidInput
		if !errors.As(err, &inv) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want invalidInput containing %q", name, err, c.want)
		}
	}
}

func TestCovKClassify_EmbeddingFailures(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "news", "story", "en", "football", nil)

	s.Embedding = &covKEmbedding{failEmbed: true}
	if _, err := s.classifyDocument(context.Background(), "", "", "", "x", covKLabels, 0, false, 0); err == nil ||
		!strings.Contains(err.Error(), "failed to embed text") {
		t.Errorf("text embed failure: err = %v", err)
	}
	if _, err := s.classifyDocument(context.Background(), "news", "story", "", "", covKLabels, 0, false, 0); err == nil ||
		!strings.Contains(err.Error(), "failed to embed document") {
		t.Errorf("document embed failure: err = %v", err)
	}

	s.Embedding = &covKEmbedding{failBatch: true}
	if _, err := s.classifyDocument(context.Background(), "", "", "", "x", covKLabels, 0, false, 0); err == nil ||
		!strings.Contains(err.Error(), "failed to embed labels") {
		t.Errorf("label embed failure: err = %v", err)
	}
}

func TestCovKClassify_StoredVectorIsReused(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	emb := covKLabelEmbedding()
	s.Embedding = emb
	doc := addTestDoc(t, s, "news", "story", "en", "unrelated words", nil)

	// The stored vector points at "politics"; the content would embed to the
	// default [0,0,1], orthogonal to every label.
	if err := s.VectorStore.Put("news", doc.ID, []float32{0, 1, 0}, "covk-model", "h"); err != nil {
		t.Fatal(err)
	}
	resp, err := s.classifyDocument(context.Background(), "news", "story", "", "", covKLabels, 1, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Results[0].Label != "politics" {
		t.Fatalf("top label = %q, want politics from the stored vector", resp.Results[0].Label)
	}
}

func TestCovKClassify_DocumentContentFallback(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = covKLabelEmbedding()
	addTestDoc(t, s, "news", "story", "en", "football", nil)

	// lang defaults to en.
	resp, err := s.classifyDocument(context.Background(), "news", "story", "", "", covKLabels, 1, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Results[0].Label != "sports" {
		t.Fatalf("top label = %q, want sports", resp.Results[0].Label)
	}

	if _, err := s.classifyDocument(context.Background(), "news", "absent", "en", "", covKLabels, 0, false, 0); !errors.Is(err, errDocNotFound) {
		t.Fatalf("missing doc: err = %v, want errDocNotFound", err)
	}
}

func TestCovKClassify_EmptyContent(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = covKLabelEmbedding()
	doc := addTestDoc(t, s, "news", "empty", "en", "x", nil)
	doc.ContentMD = ""
	data, err := marshalDoc(&doc)
	if err != nil {
		t.Fatal(err)
	}
	covKPutRaw(t, s, "news", "docs", storage.DocKey("news", doc.ID), data)

	if _, err := s.classifyDocument(context.Background(), "news", "empty", "en", "", covKLabels, 0, false, 0); err == nil ||
		!strings.Contains(err.Error(), "no content") {
		t.Fatalf("err = %v, want no content", err)
	}
}

func TestCovKLoadDocByRef_Bolt(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	doc := addTestDoc(t, s, "news", "gone", "en", "x", nil)
	covKDeleteRaw(t, s, "news", "docs", storage.DocKey("news", doc.ID))
	if _, err := s.loadDocByRef("news", "gone", "en"); !errors.Is(err, errDocNotFound) {
		t.Errorf("index entry without document: err = %v, want errDocNotFound", err)
	}

	doc = addTestDoc(t, s, "news", "corrupt", "en", "x", nil)
	covKPutRaw(t, s, "news", "docs", storage.DocKey("news", doc.ID), []byte{0xff, 0x01})
	if _, err := s.loadDocByRef("news", "corrupt", "en"); err == nil || errors.Is(err, errNotFound) {
		t.Errorf("corrupt document: err = %v, want a decode error", err)
	}

	doc = addTestDoc(t, s, "news", "expired", "en", "x", nil)
	doc.ExpiresAt = time.Now().Add(-time.Hour).Unix()
	data, err := marshalDoc(&doc)
	if err != nil {
		t.Fatal(err)
	}
	covKPutRaw(t, s, "news", "docs", storage.DocKey("news", doc.ID), data)
	if _, err := s.loadDocByRef("news", "expired", "en"); !errors.Is(err, errDocNotFound) {
		t.Errorf("expired document: err = %v, want errDocNotFound", err)
	}
}

func TestCovKLoadDocByRef_ExternalBackend(t *testing.T) {
	srv, cleanup := backendServer(t)
	defer cleanup()
	configureBackend(t, srv, "ext", "memory")

	saved, _, err := srv.addDocument("ext", "a", "en", nil, "remote body", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := srv.loadDocByRef("ext", "a", "en")
	if err != nil || got.ContentMD != "remote body" {
		t.Fatalf("loadDocByRef = %+v, %v", got, err)
	}

	if _, err := srv.loadDocByRef("ext", "missing", "en"); !errors.Is(err, errDocNotFound) {
		t.Errorf("unknown key: err = %v", err)
	}

	// Indexed locally but the backend has no payload.
	covKPutRaw(t, srv, "ext", "bykey", storage.ByKeyKey("ext", "orphan", "en"), []byte("ext|orphan|en"))
	if _, err := srv.loadDocByRef("ext", "orphan", "en"); !errors.Is(err, errDocNotFound) {
		t.Errorf("payload missing from backend: err = %v", err)
	}

	// A payload the backend holds but nobody can decode.
	if err := srv.Backends.Get("ext").PutDoc("ext", saved.ID, []byte{0xff, 0x02}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.loadDocByRef("ext", "a", "en"); err == nil || errors.Is(err, errNotFound) {
		t.Errorf("corrupt payload: err = %v, want a decode error", err)
	}

	// Without the key index there is nothing to resolve.
	if err := srv.DBUpdateC("ext", func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("bykey")) }); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.loadDocByRef("ext", "a", "en"); !errors.Is(err, errDocNotFound) {
		t.Errorf("no bykey bucket: err = %v", err)
	}
}

func covKClassifyPost(s *Server, body []byte) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/classify", bytes.NewReader(body))
	s.handleClassify(rec, req)
	return rec
}

func TestCovKHandleClassify(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = covKLabelEmbedding()
	addTestDoc(t, s, "news", "story", "en", "football", nil)

	rec := httptest.NewRecorder()
	s.handleClassify(rec, httptest.NewRequest(http.MethodGet, "/v1/classify", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d, want 405", rec.Code)
	}

	if rec := covKClassifyPost(s, []byte("{")); rec.Code != http.StatusBadRequest {
		t.Errorf("bad JSON: %d, want 400", rec.Code)
	}

	if rec := doRequest(t, s.handleClassify, ClassifyHTTPRequest{Text: "x"}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "labels are required") {
		t.Errorf("no labels: %d %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, s.handleClassify, ClassifyHTTPRequest{Collection: "news", Key: "nope", Labels: covKLabels})
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "document not found") {
		t.Errorf("missing doc: %d %s, want 404", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, s.handleClassify, ClassifyHTTPRequest{Collection: "news", Key: "story", Labels: covKLabels, TopK: 2})
	if rec.Code != http.StatusOK {
		t.Fatalf("classify: %d %s", rec.Code, rec.Body.String())
	}
	var resp ClassifyHTTPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 2 || resp.Results[0].Label != "sports" {
		t.Errorf("response = %+v", resp)
	}

	// Without metrics the handler still answers.
	s.Metrics = nil
	if rec := doRequest(t, s.handleClassify, ClassifyHTTPRequest{Text: "a match", Labels: covKLabels}); rec.Code != http.StatusOK {
		t.Errorf("no metrics: %d", rec.Code)
	}
}

func TestCovKHandleClassify_Forbidden(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = covKLabelEmbedding()
	am, amCleanup := authMwSetup(t)
	defer amCleanup()
	s.AuthManager = am

	// No claims on the request: a collection read is refused.
	rec := doRequest(t, s.handleClassify, ClassifyHTTPRequest{Collection: "news", Key: "story", Labels: covKLabels})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no claims: %d, want 403", rec.Code)
	}

	// Free text names no collection, so there is nothing to check.
	rec = doRequest(t, s.handleClassify, ClassifyHTTPRequest{Text: "a match", Labels: covKLabels})
	if rec.Code != http.StatusOK {
		t.Fatalf("text only: %d, want 200", rec.Code)
	}
}
