package main

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"sort"

	"mddb-bench/internal/benchkit"
)

// The regression half: facts in meta must not move a single search result.
// Every query runs against both collections and the answers are compared
// after normalize, which removes only what differs by construction — the
// collection's name, the sym* meta keys, timings.

// loadQueries reads the queries of evaluation files: SRCH-019's
// {"queries":[{"query"}]} and AGT-004's {"tasks":[{"question","grep"}]}.
func loadQueries(files []string) ([]string, error) {
	var out []string
	for _, f := range files {
		b, err := os.ReadFile(f) // #nosec G304 -- the operator names the file
		if err != nil {
			return nil, err
		}
		var set struct {
			Queries []struct {
				Query string `json:"query"`
			} `json:"queries"`
			Tasks []struct {
				Question string `json:"question"`
				Grep     string `json:"grep"`
			} `json:"tasks"`
		}
		if err := json.Unmarshal(b, &set); err != nil {
			return nil, err
		}
		for _, q := range set.Queries {
			out = append(out, q.Query)
		}
		for _, t := range set.Tasks {
			out = append(out, t.Question, t.Grep)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no queries")
	}
	return out, nil
}

// volatile are response fields that differ between two identical searches.
var volatile = map[string]bool{"took": true, "tookMs": true, "durationMs": true, "elapsedMs": true, "timing": true}

// factKeys are the meta keys facts are stored under.
var factKeys = map[string]bool{"symIndex": true, "symDefs": true, "symRefs": true, "symImpls": true}

// labels are what facts add to a passage on purpose (CODE-012): the
// definition holding it by its qualified name, Conn.Close for Close.
var labels = map[string]bool{"symbol": true, "kind": true, "container": true}

// normalize makes two collections' answers comparable: the collection's name
// becomes "<c>", sym* keys, volatile fields and the keys in drop go, scores
// are rounded to nine decimals (the same search twice can differ in the last
// bit: floating-point sums in no fixed order), and scored lists are put in a
// fixed order (sortTies; cut is the requested result count).
func normalize(v any, collection string, drop map[string]bool, cut int) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range t {
			if !volatile[k] && !drop[k] && !factKeys[k] {
				out[k] = normalize(x, collection, drop, cut)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = normalize(t[i], collection, drop, cut)
		}
		return sortTies(out, cut)
	case float64:
		return math.Round(t*1e9) / 1e9
	case string:
		if t == collection {
			return "<c>"
		}
	}
	return v
}

// sortTies orders results of equal score by their content. The server
// returns ties in no fixed order — the same request twice can swap them, and
// at the cut it can keep one or the other — so in a full list (cut results)
// the lowest score's results are left out, and only the order of different
// scores is compared.
func sortTies(list []any, cut int) []any {
	scores := make([]float64, len(list))
	for i, x := range list {
		m, ok := x.(map[string]any)
		if !ok {
			return list
		}
		if scores[i], ok = m["score"].(float64); !ok {
			return list
		}
	}
	keys := make([]string, len(list))
	for i, x := range list {
		b, _ := json.Marshal(x)
		keys[i] = string(b)
	}
	idx := make([]int, len(list))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		if scores[idx[a]] != scores[idx[b]] {
			return scores[idx[a]] > scores[idx[b]]
		}
		return keys[idx[a]] < keys[idx[b]]
	})
	sorted := make([]any, 0, len(list))
	for _, j := range idx {
		if cut == 0 || len(list) < cut || scores[j] != scores[idx[len(idx)-1]] {
			sorted = append(sorted, list[j])
		}
	}
	return sorted
}

// searchDiff is the regression's outcome for one endpoint.
type searchDiff struct {
	Endpoint string `json:"endpoint"`
	Compared string `json:"compared"` // what was compared with what
	Queries  int    `json:"queries"`
	// Identical answers are equal byte for byte once normalized.
	Identical int `json:"identical"`
	// SameResults answers are equal but for the passage labels facts add:
	// same documents, lines, scores and order.
	SameResults int      `json:"sameResults"`
	Differ      []string `json:"differ,omitempty"` // the queries whose results differ
	Errors      []string `json:"errors,omitempty"` // endpoint failures (both sides failing alike count as identical)
}

// side is a collection on a server.
type side struct{ url, collection string }

func (s side) String() string { return s.collection + "@" + s.url }

// compareSearch runs every query on both sides and compares.
func compareSearch(client *http.Client, a, b side, endpoint string, queries []string, topK int) searchDiff {
	d := searchDiff{Endpoint: endpoint, Compared: a.String() + " vs " + b.String(), Queries: len(queries)}
	for _, q := range queries {
		ra, ea := searchOnce(client, a, endpoint, q, topK)
		rb, eb := searchOnce(client, b, endpoint, q, topK)
		switch {
		case ea != nil || eb != nil:
			if (ea == nil) != (eb == nil) {
				d.Differ = append(d.Differ, q)
			} else {
				d.Identical++
				d.SameResults++
			}
			for _, e := range []error{ea, eb} {
				if e != nil && len(d.Errors) < 3 {
					d.Errors = append(d.Errors, e.Error())
				}
			}
		case ra[0] == rb[0]:
			d.Identical++
			d.SameResults++
		case ra[1] == rb[1]:
			d.SameResults++
		default:
			d.Differ = append(d.Differ, q)
		}
	}
	return d
}

// searchOnce returns one answer as canonical JSON, normalized, and again
// without the passage labels.
func searchOnce(client *http.Client, at side, endpoint, query string, topK int) ([2]string, error) {
	collection := at.collection
	raw, err := benchkit.Post(client, at.url+endpoint, map[string]any{"collection": collection, "query": query,
		"topK": topK, "limit": topK})
	if err != nil {
		return [2]string{}, err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return [2]string{}, err
	}
	full, _ := json.Marshal(normalize(v, collection, nil, topK)) // map keys come out sorted
	bare, _ := json.Marshal(normalize(v, collection, labels, topK))
	return [2]string{string(full), string(bare)}, nil
}
