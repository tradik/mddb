package benchkit

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
)

// Embedder is an OpenAI-compatible embeddings endpoint (POST /v1/embeddings)
// that answers each text naming a corpus document with that document's
// vector. Start MDDB with MDDB_EMBEDDING_PROVIDER=openai pointing at it and
// the vectors MDDB indexes are exactly the corpus.
type Embedder struct {
	Docs [][]float32
	Dims int
	// Ref finds the document number in a text: its first group is the index
	// into Docs.
	Ref *regexp.Regexp
}

// ServeHTTP answers one embeddings request.
func (e *Embedder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input []string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	type datum struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	}
	resp := struct {
		Data []datum `json:"data"`
	}{}
	for i, text := range req.Input {
		resp.Data = append(resp.Data, datum{Embedding: e.Embed(text), Index: i})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// Embed is the corpus vector of the document text names, or a fixed vector
// for anything else (the benches send query vectors, so the server never
// needs a query embedded).
func (e *Embedder) Embed(text string) []float32 {
	if m := e.Ref.FindStringSubmatch(text); m != nil {
		if i, err := strconv.Atoi(m[1]); err == nil && i < len(e.Docs) {
			return e.Docs[i]
		}
	}
	v := make([]float32, e.Dims)
	v[0] = 1
	return v
}
