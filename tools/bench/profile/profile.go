package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// A profile is how much of everything one run does. The numbers are fixed
// per profile and the corpus is drawn from the seed, so two runs of a profile
// do the same work on the same data.

// profile sizes every scenario of a run.
type profile struct {
	Name    string
	Vectors int // documents in the search corpus, one vector each
	Dims    int
	Topics  int // clusters the vectors are drawn around
	Queries int // timed queries per scenario and round
	Warmup  int // untimed queries before each scenario
	Rounds  int // sequential passes; the reported percentile is their median
	Clients int // concurrent clients of the QPS pass and of the mixed workload

	LoadBatch    int // documents per add-batch request when loading
	LoadParallel int // add-batch requests in flight when loading

	IngestSingle int // documents written one per request
	IngestBatch  int // documents written in batches of LoadBatch

	Mixed       time.Duration // how long the read/write mix runs
	KillAfter   time.Duration // how long writes run before kill -9
	KillWriters int
	BaseDocs    int // documents on the leader before the follower joins
	LagProbes   int // marker documents timed from leader to follower
	LagLoad     int // documents per second written to the leader while probing
	CatchupDocs int // documents written while the follower is down
	// CatchupLimit is how long the follower gets to reach the leader before
	// the scenario records it as out of sync.
	CatchupLimit time.Duration
	Timeout      time.Duration
}

// profiles are the named sizes. full takes -vectors 2000000 for the largest
// corpus the ticket names.
var profiles = map[string]profile{
	"small": {
		Name: "small", Vectors: 20_000, Dims: 128, Topics: 100,
		Queries: 1000, Warmup: 20, Rounds: 5, Clients: 4,
		LoadBatch: 1000, LoadParallel: 4,
		IngestSingle: 1000, IngestBatch: 10_000,
		Mixed: 10 * time.Second, KillAfter: 3 * time.Second, KillWriters: 4,
		BaseDocs: 1000, LagProbes: 50, LagLoad: 20, CatchupDocs: 5000, CatchupLimit: 3 * time.Minute,
		// OPQ trains single-threaded before it answers: about 5.5 minutes
		// for 20k vectors on a desktop CPU, and slower on a shared runner.
		Timeout: 30 * time.Minute,
	},
	"full": {
		Name: "full", Vectors: 200_000, Dims: 128, Topics: 1000,
		Queries: 1000, Warmup: 20, Rounds: 5, Clients: 8,
		LoadBatch: 1000, LoadParallel: 4,
		IngestSingle: 5000, IngestBatch: 100_000,
		Mixed: 30 * time.Second, KillAfter: 10 * time.Second, KillWriters: 8,
		BaseDocs: 10_000, LagProbes: 200, LagLoad: 50, CatchupDocs: 50_000, CatchupLimit: 15 * time.Minute,
		Timeout: 3 * time.Hour,
	},
}

// profileNamed looks a profile up, with vectors and dims overridden when
// they are not zero. Topics follow the corpus: 200 documents per topic.
func profileNamed(name string, vectors, dims int) (profile, error) {
	p, ok := profiles[name]
	if !ok {
		names := make([]string, 0, len(profiles))
		for n := range profiles {
			names = append(names, n)
		}
		sort.Strings(names)
		return p, fmt.Errorf("unknown profile %q: want one of %s", name, strings.Join(names, ", "))
	}
	if dims > 0 {
		p.Dims = dims
	}
	if vectors > 0 {
		if vectors < 200 {
			return p, fmt.Errorf("-vectors %d: want at least 200", vectors)
		}
		p.Vectors, p.Topics = vectors, vectors/200
	}
	return p, nil
}

// vectorAlgorithms are the indexes a search names, in report order.
var vectorAlgorithms = []string{"flat", "hnsw", "ivf", "pq", "opq", "sq", "sq4", "bq"}

// selectivities are the shares of the corpus, in percent, the filtered
// scenarios admit.
var selectivities = []int{1, 2, 10, 25, 50}

// sections are the parts of a run -only chooses between.
var sections = []string{"search", "ingest", "recovery", "replication"}

// parseSections reads -only: a comma separated subset of sections, or all of
// them when empty.
func parseSections(only string) (map[string]bool, error) {
	out := map[string]bool{}
	if strings.TrimSpace(only) == "" {
		for _, s := range sections {
			out[s] = true
		}
		return out, nil
	}
	for _, s := range strings.Split(only, ",") {
		s = strings.TrimSpace(s)
		if !slices.Contains(sections, s) {
			return nil, fmt.Errorf("-only %q: want any of %s", s, strings.Join(sections, ", "))
		}
		out[s] = true
	}
	return out, nil
}
