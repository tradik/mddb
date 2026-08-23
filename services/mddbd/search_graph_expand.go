package main

import (
	"math"
	"sort"
)

// Graph-expanded retrieval (SRCH-006).
//
// `parent`, `chunk` and `window` all change the *shape* of what comes back.
// None of them changes how documents are *reached*: every one of them ranks by
// how well a document matches the query, on its own.
//
// That misses the relational answer. A query about a checkout bug matches
// checkout.js; the stylesheet whose selector it manipulates and the template
// that loads it match nothing, and an agent has to notice the gap and ask
// again. Two or three round trips to assemble what one traversal already knows.
//
// Graph expansion takes the results a search already produced and pulls in
// their neighbours from the code connection graph (CODE-005), scoring each
// neighbour as a fraction of the result that reached it. The edges are the
// same derived ones the /v1/code-graph endpoint walks — nothing is stored, and
// a reindex reproduces them exactly.
//
// Deliberately not an LLM: entity extraction here is the symbol index, not a
// model. MDDB stays the structural layer.

const (
	// GraphExpandDefaultDepth is how far expansion walks by default.
	//
	// One hop. A document's direct neighbours are the ones a person would
	// think to open next; two hops in a real theme reaches most of it, which
	// is the same as reaching nothing.
	GraphExpandDefaultDepth = 1

	// GraphExpandMaxDepth matches the traversal limit CODE-005 set, for the
	// same reason: a popular selector's neighbourhood at depth 3 is the whole
	// collection.
	GraphExpandMaxDepth = 3

	// GraphExpandDefaultDecay is how much of the source's score a neighbour
	// inherits per hop.
	//
	// 0.5 — a neighbour is worth reading, and worth reading after the document
	// that matched. Below that they never surface; above it they displace
	// direct matches, which is a different feature and a worse one.
	GraphExpandDefaultDecay = 0.5

	// GraphExpandDefaultMaxNeighbours caps how many neighbours one result may
	// contribute, so a hub document cannot flood the answer.
	GraphExpandDefaultMaxNeighbours = 10
)

// GraphExpandOptions configures expansion.
type GraphExpandOptions struct {
	// Depth is how many hops to follow (1..3).
	Depth int `json:"graphDepth,omitempty"`
	// Decay is the fraction of a source's score a neighbour inherits per hop.
	Decay float64 `json:"graphDecay,omitempty"`
	// MaxNeighbours caps neighbours contributed per source result.
	MaxNeighbours int `json:"graphMaxNeighbours,omitempty"`
	// Direction selects which edges to follow: "in" (what depends on this),
	// "out" (what this depends on) or "both". Defaults to "both".
	Direction string `json:"graphDirection,omitempty"`
}

// Defaults fills what the caller left unset and clamps what it set too high.
func (o GraphExpandOptions) Defaults() GraphExpandOptions {
	if o.Depth <= 0 {
		o.Depth = GraphExpandDefaultDepth
	}
	if o.Depth > GraphExpandMaxDepth {
		o.Depth = GraphExpandMaxDepth
	}
	if o.Decay <= 0 || o.Decay > 1 {
		o.Decay = GraphExpandDefaultDecay
	}
	if o.MaxNeighbours <= 0 {
		o.MaxNeighbours = GraphExpandDefaultMaxNeighbours
	}
	if o.Direction == "" {
		o.Direction = string(GraphBoth)
	}
	return o
}

// GraphExpansion explains why a document was added to the answer.
//
// Without this a caller sees a document that matched nothing and has no way to
// tell whether the search is working. "checkout.js matched; site.css is here
// because checkout.js applies .cart-total" is an answer; "here are eight
// documents" is not.
type GraphExpansion struct {
	// Key is the document the expansion added.
	Key string `json:"key"`
	// FromKey is the result whose neighbourhood it came from.
	FromKey string `json:"fromKey"`
	// Symbol is the selector, identifier or path that justifies the edge.
	Symbol string `json:"symbol"`
	// Kind is the edge type — "uses-selector" or "imports".
	Kind string `json:"kind"`
	// Depth is how many hops from the matching result.
	Depth int `json:"depth"`
	// Score is the inherited score after decay.
	Score float64 `json:"score"`
}

// graphSeed is one search result that expansion walks out from.
type graphSeed struct {
	Key   string
	Score float64
}

// expandByGraph walks the connection graph out from each seed and returns the
// neighbours it reached, best score first.
//
// Neighbours already among the seeds are skipped: a document that matched the
// query on its own does not also need to be added as somebody's neighbour, and
// double-counting it would push it above documents that matched better.
func (s *Server) expandByGraph(collection string, seeds []graphSeed, opts GraphExpandOptions) []GraphExpansion {
	opts = opts.Defaults()
	if len(seeds) == 0 {
		return nil
	}

	direction, err := ParseGraphDirection(opts.Direction)
	if err != nil {
		direction = GraphBoth
	}

	seedKeys := make(map[string]bool, len(seeds))
	for _, seed := range seeds {
		seedKeys[seed.Key] = true
	}

	// Best expansion per document. A neighbour reachable from two different
	// results keeps the stronger claim rather than accumulating both — an
	// inherited score is evidence, and two weak pieces of evidence are not
	// one strong one.
	best := make(map[string]GraphExpansion)

	for _, seed := range seeds {
		result, err := s.CodeGraph(GraphRequest{
			Collection: collection,
			Key:        seed.Key,
			Direction:  direction,
			Depth:      opts.Depth,
			MaxDegree:  opts.MaxNeighbours,
		})
		if err != nil || result == nil {
			// A seed with no graph entry is not an error: most collections are
			// prose and have no edges at all, and expansion should then simply
			// add nothing.
			continue
		}

		depthOf := make(map[string]int, len(result.Nodes))
		for _, n := range result.Nodes {
			depthOf[n.Key] = n.Depth
		}

		added := 0
		for _, edge := range result.Edges {
			neighbour := edge.To
			if neighbour == seed.Key {
				neighbour = edge.From
			}
			if neighbour == seed.Key || seedKeys[neighbour] {
				continue
			}

			depth := depthOf[neighbour]
			if depth <= 0 {
				depth = 1
			}
			if depth > opts.Depth {
				continue
			}

			score := seed.Score * math.Pow(opts.Decay, float64(depth))
			candidate := GraphExpansion{
				Key:     neighbour,
				FromKey: seed.Key,
				Symbol:  edge.Symbol,
				Kind:    string(edge.Kind),
				Depth:   depth,
				Score:   score,
			}

			if existing, seen := best[neighbour]; seen && existing.Score >= score {
				continue
			}
			if !seenBefore(best, neighbour) {
				added++
				if added > opts.MaxNeighbours {
					break
				}
			}
			best[neighbour] = candidate
		}
	}

	out := make([]GraphExpansion, 0, len(best))
	for _, e := range best {
		out = append(out, e)
	}
	// Deterministic: score first, then key, so two identical runs return the
	// same order and a diff between runs means something changed.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func seenBefore(m map[string]GraphExpansion, key string) bool {
	_, ok := m[key]
	return ok
}

// appendGraphNeighbours expands a result set and returns both the explanations
// and the extended results.
//
// Neighbours are appended after the direct matches rather than merged into the
// ranking. A document that matched the query and one that is merely adjacent
// to a match are different kinds of answer, and sorting them together would
// hide which is which — the caller can re-sort by score if it wants to, and
// then it has chosen to.
func (s *Server) appendGraphNeighbours(collection string, items []VectorSearchResultItem, opts GraphExpandOptions, includeContent bool) ([]GraphExpansion, []VectorSearchResultItem) {
	if len(items) == 0 {
		return nil, items
	}

	seeds := make([]graphSeed, 0, len(items))
	for _, it := range items {
		seeds = append(seeds, graphSeed{Key: it.Document.Key, Score: float64(it.Score)})
	}

	expansions := s.expandByGraph(collection, seeds, opts)
	if len(expansions) == 0 {
		return nil, items
	}

	rank := len(items)
	for _, e := range expansions {
		// The graph works in keys; the store is addressed by document ID. The
		// by-key index the graph already uses is what bridges them.
		ref, err := s.loadCodeDocByKey(collection, e.Key)
		if err != nil || ref == nil {
			continue
		}
		doc, err := s.LoadDocByID(collection, ref.DocID)
		if err != nil || doc == nil {
			// The graph names a document the store no longer has. Reporting
			// the edge without the document would hand the caller a key it
			// cannot fetch.
			continue
		}
		if !includeContent {
			doc.ContentMD = ""
		}
		rank++
		items = append(items, VectorSearchResultItem{
			Document: *doc,
			Score:    float32(e.Score),
			Rank:     rank,
		})
	}
	return expansions, items
}
