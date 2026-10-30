package metrics

import (
	"strings"
	"sync/atomic"
	"time"
)

// The query-shape counters as a report (query-shape.json in the diagnose
// bundle) and as Prometheus series (mddb_query_shape_* in /metrics).

// Gate thresholds computed from the counters (OPS-025 part C). A gate is met
// on one instance when its share reaches the threshold; the audit decides
// across instances (CODE-006 asks for three).
const (
	// Code006ChainShare is the share of graph sessions whose chain reached
	// three calls or more.
	Code006ChainShare = 0.05
	// Code007MapShare is the share of code-graph requests shaped as "show me
	// the whole map": no start document, or depth and fan-out at maximum.
	Code007MapShare = 0.05
	// Code006MinSessions and Code007MinRequests are how many sessions or
	// requests a share needs before it counts: three of three is not 5%.
	Code006MinSessions = 20
	Code007MinRequests = 20
)

// SearchCounts are one surface and mode's searches.
type SearchCounts struct {
	Total       int64 `json:"total"`
	Empty       int64 `json:"empty"`
	LimitCapped int64 `json:"limitCapped"`
}

// FilterReport counts filter operators and conditions per search.
type FilterReport struct {
	Operators  map[string]int64 `json:"operators"`
	Conditions map[string]int64 `json:"conditions"`
}

// DecompositionReport counts decomposed queries (SRCH-020).
type DecompositionReport struct {
	Searches map[string]int64 `json:"searches"`
	Parts    int64            `json:"parts"`
}

// ChainReport describes graph sessions: chains of code-graph calls.
type ChainReport struct {
	Sessions  int64            `json:"sessions"`
	Length    map[string]int64 `json:"length"`
	AtLeast3  int64            `json:"atLeast3"`
	Open      int              `json:"open"`
	Untracked int64            `json:"untracked"`
}

// GraphReport counts graph requests.
type GraphReport struct {
	Requests     map[string]map[string]int64 `json:"requests"`     // surface → kind
	Depth        map[string]map[string]int64 `json:"depth"`        // kind → requested depth ("0" = default)
	DepthClamped map[string]map[string]int64 `json:"depthClamped"` // surface → kind
	Truncated    map[string]int64            `json:"truncated"`    // kind
	NoStart      map[string]int64            `json:"noStart"`      // surface
	WholeGraph   map[string]int64            `json:"wholeGraph"`   // surface
	Chains       ChainReport                 `json:"chains"`
}

// CollectionShape is one collection's counters.
type CollectionShape struct {
	Searches     map[string]int64 `json:"searches"`
	Empty        map[string]int64 `json:"empty"`
	LimitCapped  int64            `json:"limitCapped"`
	Graph        int64            `json:"graph"`
	DepthClamped int64            `json:"depthClamped"`
}

// Gate is one audit gate's measure on this instance.
type Gate struct {
	Measure     string  `json:"measure"`
	Numerator   int64   `json:"numerator"`
	Denominator int64   `json:"denominator"`
	Share       float64 `json:"share"`
	Threshold   float64 `json:"threshold"`
	MinSample   int64   `json:"minSample"`
	Met         bool    `json:"met"`
}

// GateReport is the gates computed from the counters.
type GateReport struct {
	Code006 Gate `json:"CODE-006"`
	Code007 Gate `json:"CODE-007"`
}

// QueryShapeReport is the counters since the process started.
type QueryShapeReport struct {
	Since         string                             `json:"since"`
	Searches      map[string]map[string]SearchCounts `json:"searches"` // surface → mode
	EmptyRatio    map[string]float64                 `json:"emptyRatio"`
	Algorithms    map[string]map[string]int64        `json:"algorithms"` // mode → algorithm
	AutoChoices   map[string]int64                   `json:"autoChoices"`
	Filters       FilterReport                       `json:"filters"`
	Decomposition DecompositionReport                `json:"decomposition"`
	Graph         GraphReport                        `json:"graph"`
	Collections   map[string]CollectionShape         `json:"collections"`
	Gates         GateReport                         `json:"gates"`
}

// put sets m[k] to v when v is not zero, so a report lists what happened.
func put(m map[string]int64, k string, v int64) {
	if v != 0 {
		m[k] += v
	}
}

// nested returns m[k], creating it.
func nested[V any](m map[string]map[string]V, k string) map[string]V {
	if m[k] == nil {
		m[k] = map[string]V{}
	}
	return m[k]
}

// newGate computes a share against its threshold.
func newGate(measure string, num, den int64, threshold float64, minSample int64) Gate {
	g := Gate{Measure: measure, Numerator: num, Denominator: den, Threshold: threshold, MinSample: minSample}
	if den > 0 {
		g.Share = float64(num) / float64(den)
	}
	g.Met = den >= minSample && g.Share >= threshold
	return g
}

// Report returns the counters, collections named by name (the bundle passes
// its hash; nil keeps the names).
func (q *QueryShape) Report(name func(string) string) QueryShapeReport {
	r := QueryShapeReport{
		Searches: map[string]map[string]SearchCounts{}, EmptyRatio: map[string]float64{},
		Algorithms: map[string]map[string]int64{}, AutoChoices: map[string]int64{},
		Filters:       FilterReport{Operators: map[string]int64{}, Conditions: map[string]int64{}},
		Decomposition: DecompositionReport{Searches: map[string]int64{}},
		Graph: GraphReport{Requests: map[string]map[string]int64{}, Depth: map[string]map[string]int64{},
			DepthClamped: map[string]map[string]int64{}, Truncated: map[string]int64{},
			NoStart: map[string]int64{}, WholeGraph: map[string]int64{}, Chains: ChainReport{Length: map[string]int64{}}},
		Collections: map[string]CollectionShape{},
	}
	if q == nil {
		return r
	}
	r.Since = q.since.UTC().Format(time.RFC3339)
	var modeTotal, modeEmpty [modeCount]int64
	for s := Surface(0); s < surfaceCount; s++ {
		for m := SearchMode(0); m < modeCount; m++ {
			c := SearchCounts{Total: q.searches[s][m].Load(), Empty: q.empty[s][m].Load(), LimitCapped: q.limitCapped[s][m].Load()}
			if c.Total == 0 {
				continue
			}
			nested(r.Searches, s.String())[modeNames[m]] = c
			modeTotal[m] += c.Total
			modeEmpty[m] += c.Empty
		}
		put(r.Decomposition.Searches, s.String(), q.decomposed[s].Load())
	}
	for m := SearchMode(0); m < modeCount; m++ {
		if modeTotal[m] > 0 {
			r.EmptyRatio[modeNames[m]] = float64(modeEmpty[m]) / float64(modeTotal[m])
		}
		for a, name := range algorithmNames {
			if v := q.algorithms[m][a].Load(); v != 0 {
				nested(r.Algorithms, modeNames[m])[name] = v
			}
		}
	}
	for a, name := range algorithmNames {
		put(r.AutoChoices, name, q.autoChoices[a].Load())
	}
	for op, name := range filterOpNames {
		put(r.Filters.Operators, name, q.filterOps[op].Load())
	}
	for b, name := range smallBucketNames {
		put(r.Filters.Conditions, name, q.conditions[b].Load())
	}
	r.Decomposition.Parts = q.parts.Load()
	q.reportGraph(&r)
	q.reportCollections(&r, name)
	return r
}

// reportGraph fills the graph half of r and the gates.
func (q *QueryShape) reportGraph(r *QueryShapeReport) {
	g, gr := &q.graph, &r.Graph
	var codeGraph, mapShaped int64
	for s := Surface(0); s < surfaceCount; s++ {
		for k := GraphKind(0); k < graphKindCount; k++ {
			if v := g.requests[s][k].Load(); v != 0 {
				nested(gr.Requests, s.String())[graphKindNames[k]] = v
			}
			if v := g.clamped[s][k].Load(); v != 0 {
				nested(gr.DepthClamped, s.String())[graphKindNames[k]] = v
			}
		}
		codeGraph += g.requests[s][GraphCodeGraph].Load()
		noStart, whole := g.noStart[s].Load(), g.wholeGraph[s].Load()
		mapShaped += noStart + whole
		put(gr.NoStart, s.String(), noStart)
		put(gr.WholeGraph, s.String(), whole)
	}
	for k := GraphKind(0); k < graphKindCount; k++ {
		for b, name := range smallBucketNames {
			if v := g.depth[k][b].Load(); v != 0 {
				nested(gr.Depth, graphKindNames[k])[name] = v
			}
		}
		put(gr.Truncated, graphKindNames[k], g.truncated[k].Load())
	}
	cs := g.chains.stats()
	gr.Chains.Sessions, gr.Chains.Open, gr.Chains.Untracked = cs.sessions, cs.open, cs.untracked
	for b := 1; b < smallBuckets; b++ {
		put(gr.Chains.Length, smallBucketNames[b], cs.lengths[b])
		if b >= 3 {
			gr.Chains.AtLeast3 += cs.lengths[b]
		}
	}
	r.Gates.Code006 = newGate("graph sessions whose chain reached 3 or more code-graph calls",
		gr.Chains.AtLeast3, cs.sessions, Code006ChainShare, Code006MinSessions)
	r.Gates.Code007 = newGate("code-graph requests with no start document, or at maximum depth and fan-out",
		mapShaped, codeGraph, Code007MapShare, Code007MinRequests)
}

// reportCollections fills r's per-collection rows.
func (q *QueryShape) reportCollections(r *QueryShapeReport, name func(string) string) {
	q.collMu.RLock()
	defer q.collMu.RUnlock()
	for coll, c := range q.colls {
		row := CollectionShape{Searches: map[string]int64{}, Empty: map[string]int64{},
			LimitCapped: c.limitCapped.Load(), Graph: c.graph.Load(), DepthClamped: c.clamped.Load()}
		for m := SearchMode(0); m < modeCount; m++ {
			put(row.Searches, modeNames[m], c.searches[m].Load())
			put(row.Empty, modeNames[m], c.empty[m].Load())
		}
		if name != nil && coll != otherCollection {
			coll = name(coll)
		}
		r.Collections[coll] = row
	}
}

// QueryShape returns the collector's query-shape counters; nil for a nil
// collector.
func (m *Metrics) QueryShape() *QueryShape {
	if m == nil {
		return nil
	}
	return m.shape
}

// promFamily writes one counter family's header.
func promFamily(buf *strings.Builder, name, help, kind string) {
	writef(buf, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
}

// promCounter writes the non-zero series of a counter family, and the family
// itself only when it has one, unless always is set (then its first series
// stands for the family at zero).
func promCounter(buf *strings.Builder, name, help string, always bool, series [][3]string, values []int64) {
	var n int
	for _, v := range values {
		if v != 0 {
			n++
		}
	}
	if n == 0 && !always {
		return
	}
	promFamily(buf, name, help, "counter")
	for i, v := range values {
		if v != 0 || (n == 0 && i == 0) {
			writef(buf, "%s{%s} %d\n", name, labelPairs(series[i]), v)
		}
	}
	buf.WriteString("\n")
}

// labelPairs renders `a="x",b="y"` from up to three "name=value" strings;
// empty ones are skipped. Values come from fixed vocabularies, never from a
// request, so they need no escaping.
func labelPairs(l [3]string) string {
	parts := make([]string, 0, 3)
	for _, kv := range l {
		if kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		parts = append(parts, k+`="`+v+`"`)
	}
	return strings.Join(parts, ",")
}

// family collects a counter family's series before writing it.
type family struct {
	series [][3]string
	values []int64
}

func (f *family) add(v *atomic.Int64, labels ...string) {
	var l [3]string
	copy(l[:], labels)
	f.series = append(f.series, l)
	f.values = append(f.values, v.Load())
}

// writeQueryShape renders the counters as mddb_query_shape_* series.
// Families with nothing to report are left out, except the two the CODE-006
// dashboard reads, which are always present.
func (q *QueryShape) writeQueryShape(buf *strings.Builder) {
	if q == nil {
		return
	}
	var searches, empty, capped, decomposed, requests, clamped, noStart, whole family
	for s := Surface(0); s < surfaceCount; s++ {
		sl := "surface=" + s.String()
		for m := SearchMode(0); m < modeCount; m++ {
			ml := "mode=" + modeNames[m]
			searches.add(&q.searches[s][m], sl, ml)
			empty.add(&q.empty[s][m], sl, ml)
			capped.add(&q.limitCapped[s][m], sl, ml)
		}
		decomposed.add(&q.decomposed[s], sl)
		for k := GraphKind(0); k < graphKindCount; k++ {
			kl := "kind=" + graphKindNames[k]
			requests.add(&q.graph.requests[s][k], sl, kl)
			clamped.add(&q.graph.clamped[s][k], sl, kl)
		}
		noStart.add(&q.graph.noStart[s], sl)
		whole.add(&q.graph.wholeGraph[s], sl)
	}
	var algos, autos, ops, conds, depth, truncated family
	for m := SearchMode(0); m < modeCount; m++ {
		for a, name := range algorithmNames {
			algos.add(&q.algorithms[m][a], "mode="+modeNames[m], "algorithm="+name)
		}
	}
	for a, name := range algorithmNames {
		autos.add(&q.autoChoices[a], "algorithm="+name)
	}
	for op, name := range filterOpNames {
		ops.add(&q.filterOps[op], "operator="+name)
	}
	for b, name := range smallBucketNames {
		conds.add(&q.conditions[b], "conditions="+name)
	}
	for k := GraphKind(0); k < graphKindCount; k++ {
		for b, name := range smallBucketNames {
			depth.add(&q.graph.depth[k][b], "kind="+graphKindNames[k], "depth="+name)
		}
		truncated.add(&q.graph.truncated[k], "kind="+graphKindNames[k])
	}
	const p = "mddb_query_shape_"
	promCounter(buf, p+"searches_total", "Searches by surface and mode.", false, searches.series, searches.values)
	promCounter(buf, p+"empty_results_total", "Searches that returned nothing, by surface and mode.", false, empty.series, empty.values)
	promCounter(buf, p+"limit_capped_total", "Searches whose result count the server's cap lowered.", false, capped.series, capped.values)
	promCounter(buf, p+"algorithm_total", "Searches by mode and the algorithm that ran.", false, algos.series, algos.values)
	promCounter(buf, p+"auto_choice_total", "The planner's choice for algorithm auto.", false, autos.series, autos.values)
	promCounter(buf, p+"filter_operator_total", "Searches using each metadata filter operator.", false, ops.series, ops.values)
	promCounter(buf, p+"filter_conditions_total", "Searches by number of filtered metadata keys.", false, conds.series, conds.values)
	promCounter(buf, p+"decomposed_total", "Searches whose query was decomposed into parts.", false, decomposed.series, decomposed.values)
	promCounter(buf, p+"graph_requests_total", "Graph requests by surface and kind.", false, requests.series, requests.values)
	promCounter(buf, p+"graph_depth_total", "Graph requests by requested depth (0 = default).", false, depth.series, depth.values)
	promCounter(buf, p+"graph_depth_clamped_total", "Graph requests whose depth was clamped to the server's maximum.", true, clamped.series, clamped.values)
	promCounter(buf, p+"graph_truncated_total", "Graph walks a degree or depth limit cut short.", false, truncated.series, truncated.values)
	promCounter(buf, p+"graph_no_start_total", "Code-graph requests refused for want of a start document.", false, noStart.series, noStart.values)
	promCounter(buf, p+"graph_whole_graph_total", "Code-graph requests at maximum depth and fan-out.", false, whole.series, whole.values)
	if v := q.parts.Load(); v != 0 {
		promFamily(buf, p+"decomposition_parts_total", "Sub-queries run by decomposed searches.", "counter")
		writef(buf, "%sdecomposition_parts_total %d\n\n", p, v)
	}
	cs := q.graph.chains.stats()
	promFamily(buf, p+"graph_chain_length", "Length of ended chains of code-graph calls (one client, each starting from the previous answer, within 30 s).", "histogram")
	var cum int64
	for b := 1; b < smallBuckets-1; b++ {
		cum += cs.lengths[b]
		writef(buf, "%sgraph_chain_length_bucket{le=\"%d\"} %d\n", p, b, cum)
	}
	writef(buf, "%sgraph_chain_length_bucket{le=\"+Inf\"} %d\n", p, cs.sessions)
	writef(buf, "%sgraph_chain_length_sum %d\n%sgraph_chain_length_count %d\n\n", p, cs.sum, p, cs.sessions)
	promFamily(buf, p+"graph_chains_open", "Chains of code-graph calls still within their window.", "gauge")
	writef(buf, "%sgraph_chains_open %d\n\n", p, cs.open)
}
