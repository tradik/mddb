package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
)

// Asking every group (GO-052): a fan-out sends each group its part, marked
// with X-MDDB-Placement-Partial so the node answers from its own group
// only. Each part is a group leader's answer; a leader answers its own
// group's part here, by the handler itself.

// askPart sends a part of r to group and returns the answer's body; payload
// replaces the body when not nil.
func (p *placementRuntime) askPart(r *http.Request, group string, payload any, next http.Handler) ([]byte, error) {
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		if body, err = json.Marshal(payload); err != nil {
			return nil, err
		}
	}
	var resp *http.Response
	if group == p.cfg.self && p.leads() { // a group's part is its leader's answer
		resp = p.serveHere(r, body, next)
	} else {
		n, ok := p.leaderNode(group)
		if !ok {
			return nil, fmt.Errorf("node group %q: %w", group, errNoGroupLeader)
		}
		part := r.Clone(r.Context())
		part.Header.Set(hdrPlacementPartial, "1")
		if resp, err = p.send(part, n.HTTP, body, 1); err != nil {
			return nil, fmt.Errorf("node group %q: %w", group, err)
		}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err == nil && resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("node group %q answered %d: %s", group, resp.StatusCode, bytes.TrimSpace(raw))
	}
	return raw, err
}

// serveHere runs next on a copy of r with body, as a part.
func (p *placementRuntime) serveHere(r *http.Request, body []byte, next http.Handler) *http.Response {
	part := r.Clone(r.Context())
	part.Header.Set(hdrPlacementPartial, "1")
	part.Body, part.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
	rec := &partRecorder{header: http.Header{}, code: http.StatusOK}
	next.ServeHTTP(rec, part)
	return &http.Response{StatusCode: rec.code, Header: rec.header, Body: io.NopCloser(&rec.body)}
}

// partRecorder keeps a handler's answer in memory.
type partRecorder struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (pr *partRecorder) Header() http.Header         { return pr.header }
func (pr *partRecorder) Write(b []byte) (int, error) { return pr.body.Write(b) }
func (pr *partRecorder) WriteHeader(code int)        { pr.code = code }

// statsGroup is one group in a merged /v1/stats.
type statsGroup struct {
	Name      string `json:"name"`
	Documents int    `json:"totalDocuments"`
	Error     string `json:"error,omitempty"`
}

// fanoutStats answers /v1/stats for every group: each group's collections —
// only those the map places in it — and the totals over all of them.
func (p *placementRuntime) fanoutStats(w http.ResponseWriter, r *http.Request, next http.Handler) {
	answers := make([]map[string]any, len(p.cfg.names))
	errs := make([]error, len(p.cfg.names))
	var wg sync.WaitGroup
	for i, g := range p.cfg.names {
		wg.Go(func() {
			raw, err := p.askPart(r, g, nil, next)
			if err == nil {
				err = json.Unmarshal(raw, &answers[i])
			}
			errs[i] = err
		})
	}
	wg.Wait()
	out := map[string]any{}
	var collections []any
	var groups []statsGroup
	for i, g := range p.cfg.names {
		if errs[i] != nil {
			groups = append(groups, statsGroup{Name: g, Error: errs[i].Error()})
			continue
		}
		if g == p.cfg.self || len(out) == 0 {
			for k, v := range answers[i] {
				out[k] = v
			}
		}
		own, docs := p.ownStats(g, answers[i])
		collections = append(collections, own...)
		groups = append(groups, statsGroup{Name: g, Documents: docs})
		mergeStatsFlags(out, answers[i])
	}
	sort.Slice(collections, func(i, j int) bool { return statName(collections[i]) < statName(collections[j]) })
	out["collections"], out["groups"] = collections, groups
	totals := map[string]string{"totalDocuments": "documentCount", "totalRevisions": "revisionCount", "totalMetaIndices": "metaIndexCount"}
	for total, field := range totals {
		sum := 0.0
		for _, c := range collections {
			n, _ := c.(map[string]any)[field].(float64)
			sum += n
		}
		out[total] = sum
	}
	writeJSONStatus(w, http.StatusOK, out)
}

// ownStats keeps the collections of a group's stats that the map places in
// it, and counts their documents.
func (p *placementRuntime) ownStats(group string, answer map[string]any) ([]any, int) {
	list, _ := answer["collections"].([]any)
	var out []any
	docs := 0
	for _, c := range list {
		if g, _ := p.owner(statName(c)); g != group {
			continue // a copy a finished move has not dropped yet
		}
		n, _ := c.(map[string]any)["documentCount"].(float64)
		docs += int(n)
		out = append(out, c)
	}
	return out, docs
}

// mergeStatsFlags carries a group's pending flag and age into the merge.
func mergeStatsFlags(out, answer map[string]any) {
	if pending, _ := answer["statsPending"].(bool); pending {
		out["statsPending"] = true
	}
	age, _ := answer["statsAgeSeconds"].(float64)
	if cur, _ := out["statsAgeSeconds"].(float64); age > cur {
		out["statsAgeSeconds"] = age
	}
}

// statName is the name of a collection in a stats answer.
func statName(c any) string {
	m, _ := c.(map[string]any)
	name, _ := m["name"].(string)
	return name
}
