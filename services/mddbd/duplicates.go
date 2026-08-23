package main

import (
	"bytes"
	"errors"
	"mddb/internal/storage"
	"mddb/internal/vector"
	"net/http"
	"sort"
	"time"

	json "mddb/internal/jsonx"
	bolt "go.etcd.io/bbolt"
)

// ---- Request/Response types ----

// FindDuplicatesRequest represents a duplicate detection request.
type FindDuplicatesRequest struct {
	Collection     string  `json:"collection"`
	Mode           string  `json:"mode"`           // "exact", "similar", "minhash", "both" (default)
	Threshold      float64 `json:"threshold"`      // similarity threshold 0-1 (default 0.9)
	MaxDocs        int     `json:"maxDocs"`        // max docs to process (default 5000)
	DistanceMetric string  `json:"distanceMetric"` // "cosine" (default), "dot_product", "euclidean"
	IncludeContent bool    `json:"includeContent"`
}

// DuplicateGroup represents a group of duplicate or similar documents.
type DuplicateGroup struct {
	GroupID   int                `json:"groupId"`
	Type      string             `json:"type"` // "exact" or "similar"
	Documents []DuplicateDocInfo `json:"documents"`
	Score     float32            `json:"score,omitempty"` // avg similarity for "similar" groups
}

// DuplicateDocInfo represents a document in a duplicate group.
type DuplicateDocInfo struct {
	DocID       string  `json:"docId"`
	Key         string  `json:"key,omitempty"`
	ContentHash string  `json:"contentHash,omitempty"`
	ContentMD   string  `json:"contentMd,omitempty"`
	Score       float32 `json:"score,omitempty"` // similarity to first doc in group
}

// FindDuplicatesResponse represents the result of duplicate detection.
type FindDuplicatesResponse struct {
	Collection     string           `json:"collection"`
	Mode           string           `json:"mode"`
	Threshold      float64          `json:"threshold"`
	DistanceMetric string           `json:"distanceMetric"`
	TotalDocuments int              `json:"totalDocuments"`
	TotalEmbedded  int              `json:"totalEmbedded"`
	ExactGroups    []DuplicateGroup `json:"exactGroups,omitempty"`
	SimilarGroups  []DuplicateGroup `json:"similarGroups,omitempty"`
	// MinHashGroups holds near-duplicates found by text overlap (SRCH-002) —
	// documents that share their words, as opposed to their topic.
	MinHashGroups   []DuplicateGroup `json:"minhashGroups,omitempty"`
	MinHashPairs    int              `json:"minhashPairs,omitempty"`
	ExactDuplicates int              `json:"exactDuplicates"`
	SimilarPairs    int              `json:"similarPairs"`
	Stats           *SearchStats     `json:"searchStats,omitempty"`
}

// ---- Union-Find for transitive clustering ----

type unionFind struct {
	parent []int
	rank   []int
}

func newUnionFind(n int) *unionFind {
	uf := &unionFind{parent: make([]int, n), rank: make([]int, n)}
	for i := range uf.parent {
		uf.parent[i] = i
	}
	return uf
}

func (uf *unionFind) find(x int) int {
	for uf.parent[x] != x {
		uf.parent[x] = uf.parent[uf.parent[x]]
		x = uf.parent[x]
	}
	return x
}

func (uf *unionFind) union(x, y int) {
	rx, ry := uf.find(x), uf.find(y)
	if rx == ry {
		return
	}
	if uf.rank[rx] < uf.rank[ry] {
		rx, ry = ry, rx
	}
	uf.parent[ry] = rx
	if uf.rank[rx] == uf.rank[ry] {
		uf.rank[rx]++
	}
}

// ---- Handler ----

// handleFindDuplicates handles POST /v1/find-duplicates
func (s *Server) handleFindDuplicates(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req FindDuplicatesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}

	// Check read permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	if req.Mode == "" {
		req.Mode = "both"
	}
	if req.Mode != "exact" && req.Mode != "similar" && req.Mode != "minhash" && req.Mode != "both" {
		bad(w, errors.New("mode must be 'exact', 'similar', or 'both'"))
		return
	}
	if req.Threshold <= 0 {
		req.Threshold = 0.9
	}
	if req.MaxDocs <= 0 {
		req.MaxDocs = 5000
	}

	resp, err := s.findDuplicates(req)
	if err != nil {
		bad(w, err)
		return
	}

	if searchStatsEnabled() {
		resp.Stats = &SearchStats{
			DurationMs: float64(time.Since(start).Microseconds()) / 1000.0,
			IndexSize:  resp.TotalEmbedded,
		}
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("find_duplicates", req.Mode)
	}

	ok(w, resp)
}

// ---- Core algorithm ----

// docVec holds a document's primary vector and content hash.
type docVec struct {
	docID       string
	vector      []float32
	contentHash string
}

func (s *Server) findDuplicates(req FindDuplicatesRequest) (*FindDuplicatesResponse, error) {
	metricName := req.DistanceMetric
	if metricName == "" {
		metricName = "cosine"
	}

	// Count total documents in collection
	totalDocs := 0
	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		prefix := []byte("doc|" + req.Collection + "|")
		c := bDocs.Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			totalDocs++
		}
		return nil
	})

	// Load all embedding records for collection
	records, err := s.VectorStore.LoadCollection(req.Collection)
	if err != nil {
		return nil, err
	}

	// Deduplicate to primary vectors only (chunk #0 or legacy single)
	primaryMap := make(map[string]*docVec)
	for suffix, rec := range records {
		base := vector.BaseDocID(suffix)
		// Keep chunk #0 or legacy (no #) — skip higher chunks
		if suffix != base && suffix != base+"#0" {
			continue
		}
		if _, exists := primaryMap[base]; !exists {
			primaryMap[base] = &docVec{
				docID:       base,
				vector:      rec.Vector,
				contentHash: rec.ContentHash,
			}
		}
	}

	// Build ordered slice (deterministic)
	docs := make([]docVec, 0, len(primaryMap))
	for _, dv := range primaryMap {
		docs = append(docs, *dv)
	}
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].docID < docs[j].docID
	})

	// Truncate if exceeding maxDocs
	if len(docs) > req.MaxDocs {
		docs = docs[:req.MaxDocs]
	}

	resp := &FindDuplicatesResponse{
		Collection:     req.Collection,
		Mode:           req.Mode,
		Threshold:      req.Threshold,
		DistanceMetric: metricName,
		TotalDocuments: totalDocs,
		TotalEmbedded:  len(docs),
	}

	// ---- Exact duplicates ----
	if req.Mode == "exact" || req.Mode == "both" {
		resp.ExactGroups = s.findExactDuplicates(req.Collection, docs, req.IncludeContent)
		for _, g := range resp.ExactGroups {
			resp.ExactDuplicates += len(g.Documents)
		}
	}

	// ---- Near-duplicates by text overlap (SRCH-002) ----
	//
	// Not part of "both": it reads every document body, which the other two
	// modes do not, so it is opt-in rather than something "both" quietly
	// starts doing to a large collection.
	if req.Mode == "minhash" {
		groups, scanned, err := s.findMinHashDuplicates(req.Collection, req.Threshold, req.IncludeContent)
		if err != nil {
			return nil, err
		}
		resp.MinHashGroups = groups
		resp.TotalDocuments = scanned
		for _, g := range groups {
			n := len(g.Documents)
			resp.MinHashPairs += n * (n - 1) / 2
		}
		return resp, nil
	}

	// ---- Similar duplicates ----
	if req.Mode == "similar" || req.Mode == "both" {
		metric := vector.ResolveSimilarity(req.DistanceMetric)
		resp.SimilarGroups = s.findSimilarDuplicates(req.Collection, docs, req.Threshold, metric, req.IncludeContent)
		// Count pairs: for a group of size n, there are n*(n-1)/2 pairs
		for _, g := range resp.SimilarGroups {
			n := len(g.Documents)
			resp.SimilarPairs += n * (n - 1) / 2
		}
	}

	return resp, nil
}

// findExactDuplicates groups documents by ContentHash.
func (s *Server) findExactDuplicates(collection string, docs []docVec, includeContent bool) []DuplicateGroup {
	// Group by content hash
	hashGroups := make(map[string][]int) // hash -> indices
	for i, d := range docs {
		if d.contentHash == "" {
			continue
		}
		hashGroups[d.contentHash] = append(hashGroups[d.contentHash], i)
	}

	// Collect groups with 2+ members
	var groups []DuplicateGroup
	groupID := 1
	for hash, indices := range hashGroups {
		if len(indices) < 2 {
			continue
		}
		g := DuplicateGroup{
			GroupID: groupID,
			Type:    "exact",
		}
		for _, idx := range indices {
			info := DuplicateDocInfo{
				DocID:       docs[idx].docID,
				ContentHash: hash,
				Score:       1.0,
			}
			g.Documents = append(g.Documents, info)
		}
		g.Score = 1.0
		groups = append(groups, g)
		groupID++
	}

	// Enrich with doc metadata
	s.enrichDuplicateGroups(collection, groups, includeContent)

	// Sort groups by size descending
	sort.Slice(groups, func(i, j int) bool {
		return len(groups[i].Documents) > len(groups[j].Documents)
	})

	return groups
}

// findSimilarDuplicates does pairwise comparison and clusters with Union-Find.
func (s *Server) findSimilarDuplicates(collection string, docs []docVec, threshold float64, metric vector.SimilarityFunc, includeContent bool) []DuplicateGroup {
	n := len(docs)
	if n < 2 {
		return nil
	}

	if metric == nil {
		metric = vector.CosineSimilarity
	}

	// Pairwise comparison (upper triangle) + Union-Find
	uf := newUnionFind(n)
	// Store best scores for each pair in the same group
	type scorePair struct {
		i, j  int
		score float32
	}
	var pairs []scorePair

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			score := metric(docs[i].vector, docs[j].vector)
			if float64(score) >= threshold {
				uf.union(i, j)
				pairs = append(pairs, scorePair{i, j, score})
			}
		}
	}

	// Build groups from Union-Find components
	components := make(map[int][]int) // root -> indices
	for i := 0; i < n; i++ {
		root := uf.find(i)
		components[root] = append(components[root], i)
	}

	// Calculate per-document scores within each group (max similarity to any other member)
	docMaxScore := make(map[int]float32)
	groupScoreSum := make(map[int]float64)
	groupScoreCount := make(map[int]int)
	for _, p := range pairs {
		root := uf.find(p.i)
		groupScoreSum[root] += float64(p.score)
		groupScoreCount[root]++
		if p.score > docMaxScore[p.i] {
			docMaxScore[p.i] = p.score
		}
		if p.score > docMaxScore[p.j] {
			docMaxScore[p.j] = p.score
		}
	}

	// Collect groups with 2+ members
	var groups []DuplicateGroup
	groupID := 1
	for root, indices := range components {
		if len(indices) < 2 {
			continue
		}
		g := DuplicateGroup{
			GroupID: groupID,
			Type:    "similar",
		}

		// Average score for the group
		if groupScoreCount[root] > 0 {
			g.Score = float32(groupScoreSum[root] / float64(groupScoreCount[root]))
		}

		for _, idx := range indices {
			info := DuplicateDocInfo{
				DocID:       docs[idx].docID,
				ContentHash: docs[idx].contentHash,
				Score:       docMaxScore[idx],
			}
			g.Documents = append(g.Documents, info)
		}

		// Sort docs within group by score descending
		sort.Slice(g.Documents, func(i, j int) bool {
			return g.Documents[i].Score > g.Documents[j].Score
		})

		groups = append(groups, g)
		groupID++
	}

	// Enrich with doc metadata
	s.enrichDuplicateGroups(collection, groups, includeContent)

	// Sort groups by score descending
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Score > groups[j].Score
	})

	return groups
}

// enrichDuplicateGroups loads doc metadata (key, content) from BoltDB.
func (s *Server) enrichDuplicateGroups(collection string, groups []DuplicateGroup, includeContent bool) {
	if len(groups) == 0 {
		return
	}

	// Collect all docIDs we need
	docIDs := make(map[string]bool)
	for _, g := range groups {
		for _, d := range g.Documents {
			docIDs[d.DocID] = true
		}
	}

	// Load docs in single transaction
	docMap := make(map[string]*storage.Doc)
	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		for docID := range docIDs {
			v := bDocs.Get(storage.DocKey(collection, docID))
			if v == nil {
				continue
			}
			d, err := loadDoc(v)
			if err != nil {
				continue
			}
			docMap[docID] = d
		}
		return nil
	})

	// Enrich group documents
	for gi := range groups {
		for di := range groups[gi].Documents {
			docID := groups[gi].Documents[di].DocID
			if d, found := docMap[docID]; found {
				groups[gi].Documents[di].Key = d.Key
				if includeContent {
					groups[gi].Documents[di].ContentMD = d.ContentMD
				}
			}
		}
	}
}
