package main

import (
	"context"
	"errors"
	"mddb/internal/storage"
	"mddb/internal/vector"
	"net/http"
	"sort"
	"time"

	json "github.com/goccy/go-json"
	bolt "go.etcd.io/bbolt"
)

// ClassifyHTTPRequest is the HTTP JSON request for zero-shot classification.
type ClassifyHTTPRequest struct {
	Collection string   `json:"collection,omitempty"`
	Key        string   `json:"key,omitempty"`
	Lang       string   `json:"lang,omitempty"`
	Text       string   `json:"text,omitempty"`
	Labels     []string `json:"labels"`
	TopK       int      `json:"topK,omitempty"`
	Multi      bool     `json:"multi,omitempty"`
	Threshold  float64  `json:"threshold,omitempty"`
}

// ClassifyLabelScore represents a single label + similarity score.
type ClassifyLabelScore struct {
	Label string  `json:"label"`
	Score float64 `json:"score"`
}

// ClassifyHTTPResponse is the HTTP JSON response for classification.
type ClassifyHTTPResponse struct {
	Results    []ClassifyLabelScore `json:"results"`
	Model      string               `json:"model"`
	Dimensions int                  `json:"dimensions"`
}

// classifyDocument is the shared core logic for zero-shot classification.
// Both HTTP handler and gRPC call this method.
func (s *Server) classifyDocument(ctx context.Context, collection, key, lang, text string, labels []string, topK int, multi bool, threshold float64) (*ClassifyHTTPResponse, error) {
	if s.Embedding == nil {
		return nil, errors.New("no embedding provider configured")
	}
	if len(labels) == 0 {
		return nil, errors.New("labels are required")
	}
	if len(labels) > 100 {
		return nil, errors.New("maximum 100 labels allowed")
	}
	if text == "" && (collection == "" || key == "") {
		return nil, errors.New("provide either text or collection+key")
	}

	var docVector []float32

	if text != "" {
		vec, err := s.Embedding.Embed(ctx, text)
		if err != nil {
			return nil, errors.New("failed to embed text: " + err.Error())
		}
		docVector = vec
	} else {
		if lang == "" {
			lang = "en"
		}
		docID := genID(collection, key, lang)

		// Try to reuse existing embedding from VectorStore
		if s.VectorStore != nil {
			rec, err := s.VectorStore.Get(collection, docID)
			if err == nil && rec != nil && len(rec.Vector) > 0 {
				docVector = rec.Vector
			}
		}

		// Fallback: load doc and embed content
		if docVector == nil {
			doc, err := s.loadDocByRef(collection, key, lang)
			if err != nil {
				return nil, err
			}
			if doc.ContentMD == "" {
				return nil, errors.New("document has no content to classify")
			}
			vec, err := s.Embedding.Embed(ctx, doc.ContentMD)
			if err != nil {
				return nil, errors.New("failed to embed document: " + err.Error())
			}
			docVector = vec
		}
	}

	// Embed all labels in a single batch
	labelVectors, err := s.Embedding.EmbedBatch(ctx, labels)
	if err != nil {
		return nil, errors.New("failed to embed labels: " + err.Error())
	}

	// Compute cosine similarity for each label
	scored := make([]ClassifyLabelScore, len(labels))
	for i, labelVec := range labelVectors {
		sim := vector.CosineSimilarity(docVector, labelVec)
		scored[i] = ClassifyLabelScore{
			Label: labels[i],
			Score: float64(sim),
		}
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// Apply threshold filter
	var filtered []ClassifyLabelScore
	for _, s := range scored {
		if s.Score >= threshold {
			filtered = append(filtered, s)
		}
	}

	// Apply topK limit
	if topK > 0 && len(filtered) > topK {
		filtered = filtered[:topK]
	}

	return &ClassifyHTTPResponse{
		Results:    filtered,
		Model:      s.Embedding.Model(),
		Dimensions: s.Embedding.Dimensions(),
	}, nil
}

// loadDocByRef loads a document by collection, key, lang from BoltDB.
func (s *Server) loadDocByRef(collection, key, lang string) (*storage.Doc, error) {
	// GO-021: for a collection on an external backend the id is resolved from
	// the local index and the payload fetched afterwards — never inside the
	// transaction, where a network round trip would hold the view open.
	if s.usesExternalBackend(collection) {
		var docID string
		if err := s.DBView(func(tx *bolt.Tx) error {
			bByK := tx.Bucket([]byte("bykey"))
			if bByK == nil {
				return errors.New("not found")
			}
			if v := bByK.Get(storage.ByKeyKey(collection, key, lang)); v != nil {
				docID = string(v)
			}
			return nil
		}); err != nil {
			return nil, err
		}
		if docID == "" {
			return nil, errors.New("not found")
		}
		d, err := s.LoadDocFromBackend(collection, docID)
		if err != nil {
			return nil, err
		}
		if d == nil {
			return nil, errors.New("not found")
		}
		return d, nil
	}

	var doc storage.Doc
	err := s.DBView(func(tx *bolt.Tx) error {
		bByK := tx.Bucket([]byte("bykey"))
		bDocs := tx.Bucket([]byte("docs"))
		docID := bByK.Get(storage.ByKeyKey(collection, key, lang))
		if docID == nil {
			return errors.New("not found")
		}
		v := bDocs.Get(storage.DocKey(collection, string(docID)))
		if v == nil {
			return errors.New("not found")
		}
		d, err := loadDoc(v)
		if err != nil {
			return err
		}
		doc = *d
		return nil
	})
	if err != nil {
		return nil, err
	}
	if doc.ExpiresAt > 0 && doc.ExpiresAt < time.Now().Unix() {
		return nil, errors.New("not found")
	}
	return &doc, nil
}

// handleClassify handles POST /v1/classify — zero-shot classification.
func (s *Server) handleClassify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req ClassifyHTTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}

	if s.AuthManager != nil && req.Collection != "" {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	resp, err := s.classifyDocument(r.Context(), req.Collection, req.Key, req.Lang, req.Text, req.Labels, req.TopK, req.Multi, req.Threshold)
	if err != nil {
		if err.Error() == "not found" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"document not found"}`))
			return
		}
		bad(w, err)
		return
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("classify")
	}

	ok(w, resp)
}
