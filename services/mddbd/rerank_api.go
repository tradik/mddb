package main

import (
	"fmt"
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
	"mddb/internal/rerank"
)

// POST /v1/rerank (#450): the external reranker contract of Open WebUI —
// {"model", "query", "documents", "top_n"} in, {"results": [{"index",
// "relevance_score"}]} out — scored by the reranking model the search rerank
// stage uses (RAG-011), with its cache.
//
// The caller sorts the results by index and reads one score per document, so
// every document gets a result: one the model left out scores below all the
// others. MDDB runs one reranking model at a time, so the request's model
// name (Open WebUI sends "reranker" unless told otherwise) does not choose
// one; the answer names the model that scored.

// Limits on one call: the rerank ceiling of a search, and a total that keeps
// the request far under the body cap.
const (
	rerankAPIMaxDocuments = rerank.MaxCandidatesCeiling
	rerankAPIMaxBytes     = 2 << 20
)

// rerankAPIRequest is the body the caller sends.
type rerankAPIRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n"`
}

// rerankAPIResult is one document's score.
type rerankAPIResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// rerankAPIResponse is the answer, best first.
type rerankAPIResponse struct {
	Model   string            `json:"model"`
	Results []rerankAPIResult `json:"results"`
}

// handleRerankAPI answers POST /v1/rerank.
func (s *Server) handleRerankAPI(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var req rerankAPIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if msg := checkRerankAPI(&req); msg != "" {
		writeAPIError(w, http.StatusBadRequest, msg)
		return
	}
	setup := s.rerankConfigured()
	if setup == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "no reranking model is configured: store a default rerank "+
			"configuration (POST /v1/embedding-configs with kind rerank) or set MDDB_RERANK_PROVIDER and MDDB_RERANK_API_URL")
		return
	}
	texts := make([]string, len(req.Documents))
	for i, d := range req.Documents {
		texts[i], _ = truncateRunes(d, maxRerankChars) // what the search stage sends per candidate
	}
	scored, err := setup.provider.Rerank(r.Context(), req.Query, texts, 0)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "the reranking model failed: "+rerank.Reason(err))
		return
	}
	ok(w, rerankAPIResponse{Model: setup.provider.Model(), Results: rerankAPIResults(scored, len(texts), req.TopN)})
}

// checkRerankAPI returns why a request cannot be scored, or "".
func checkRerankAPI(req *rerankAPIRequest) string {
	if strings.TrimSpace(req.Query) == "" {
		return "query is required"
	}
	if len(req.Documents) > rerankAPIMaxDocuments {
		return fmt.Sprintf("too many documents: %d, at most %d", len(req.Documents), rerankAPIMaxDocuments)
	}
	total := 0
	for _, d := range req.Documents {
		total += len(d)
	}
	if total > rerankAPIMaxBytes {
		return fmt.Sprintf("documents too large: %d bytes, at most %d", total, rerankAPIMaxBytes)
	}
	if req.TopN < 0 {
		return "top_n must be 0 or more"
	}
	return ""
}

// rerankAPIResults is the model's answer, best first, followed by the
// documents it did not score in their order, each below the lowest score;
// cut to topN when that is smaller.
func rerankAPIResults(scored []rerank.Scored, n, topN int) []rerankAPIResult {
	out := make([]rerankAPIResult, 0, n)
	placed := make([]bool, n)
	floor := 0.0
	for _, sc := range scored {
		placed[sc.Index] = true
		out = append(out, rerankAPIResult{Index: sc.Index, RelevanceScore: sc.Score})
		floor = min(floor, sc.Score)
	}
	for i := 0; i < n; i++ {
		if !placed[i] {
			out = append(out, rerankAPIResult{Index: i, RelevanceScore: floor - 1})
		}
	}
	if topN > 0 && topN < len(out) {
		out = out[:topN]
	}
	return out
}
