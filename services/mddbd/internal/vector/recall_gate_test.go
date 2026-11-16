package vector

import "testing"

// TEST-005. Only HNSW and SQ4 had a recall test, so IVF, PQ, OPQ and BQ could
// lose most of their neighbours and every test would still pass: the others
// check that a search returns something, not that it returns the right thing.
// These gates hold each of the four to a recall@10 floor against exact cosine
// search, built and searched the way the server does it.
//
// The corpora are seeded, so every run searches the same vectors for the same
// queries. The k-means behind IVF, PQ and OPQ is not: it draws its starting
// centroids from the unseeded global source and walks a map, so a trained
// structure differs a little from run to run. Each floor is set under the
// lowest value seen over repeated runs, not under one measurement.

// The parameters newVectorSearchers gives each index in the server
// (services/mddbd/vector_indexing.go), and the default bqRerankFactor
// (services/mddbd/server_config.go). A gate measured with other values would
// describe an index nobody runs.
const (
	prodIVFNProbe      = 10
	prodKMeansMaxIter  = 20
	prodPQSubspaces    = 8
	prodPQCodebookSize = 256
	prodOPQIterations  = 5
	prodBQRerankFactor = 10
)

// wideGateCorpus is what IVF and BQ search: 4,000 vectors of 128 dimensions
// in 20 topics, 200 queries. A topic of 200 is several IVF clusters and twice
// BQ's 100 re-scored candidates, so neither can answer from the topic alone.
func wideGateCorpus() gateCorpus { return newGateCorpus(128, 20, 200, 10) }

// codebookGateCorpus is what PQ and OPQ search: 600 vectors of 64 dimensions
// in 6 topics, 120 queries. It is small because training is not: with 256
// codewords in each of 8 subspaces the server's k-means costs about 1.3 ms a
// vector, and OPQ runs it five times.
func codebookGateCorpus() gateCorpus { return newGateCorpus(64, 6, 100, 20) }

// The gates mean nothing on a corpus where any answer is right, or where the
// ground truth is wrong. The flat index must agree with the brute force
// exactly, and knowing a query's topic must not be enough: ten documents of
// the right topic, taken without ranking them, are mostly not the nearest ten.
func TestRecallGateCorporaAreNotTrivial(t *testing.T) {
	corpora := map[string]gateCorpus{"wide": wideGateCorpus(), "codebook": codebookGateCorpus()}
	for name, c := range corpora {
		if flat := c.recall(t, NewVectorIndex()); flat != 1 {
			t.Errorf("%s corpus: flat recall@%d = %.3f, want 1: the ground truth and the exact index disagree",
				name, recallGateTopK, flat)
		}
		unranked := c.unrankedTopicRecall()
		t.Logf("%s corpus: ten unranked documents of the query's topic reach recall@%d = %.3f",
			name, recallGateTopK, unranked)
		if unranked > 0.25 {
			t.Errorf("%s corpus: unranked documents of the right topic reach recall@%d = %.3f; too easy to gate on",
				name, recallGateTopK, unranked)
		}
	}
}

// IVF only looks inside the clusters nearest the query. This gate catches a
// change that sends vectors or queries to the wrong clusters, or probes fewer
// of them than nProbe says: on this corpus 3 probes measure about 0.95 and
// 1 probe about 0.6. Measured with the server's 10: 1.000.
func TestIVFRecallOnAClusteredCorpus(t *testing.T) {
	got := wideGateCorpus().recall(t, NewIVFIndex(prodIVFNProbe, prodKMeansMaxIter))
	requireRecall(t, "ivf", got, 0.97)
}

// BQ ranks by Hamming distance over one bit per dimension and re-scores the
// best topK × rerankFactor exactly. This gate catches a broken encoding, a
// wrong Hamming distance, and a re-scoring pass that shrinks or disappears:
// with no re-scoring this corpus measures about 0.23. Measured with the
// server's factor of 10: 0.871–0.880 (ties at the cut fall either way).
func TestBQRecallOnAClusteredCorpus(t *testing.T) {
	got := wideGateCorpus().recall(t, NewBQIndex(prodBQRerankFactor))
	requireRecall(t, "bq", got, 0.85)
}

// PQ and OPQ rank by distances read from codebooks and re-score the best
// 3 × topK exactly. The gates catch codebooks that stop describing the
// vectors, codes and distance tables that disagree about the subspaces, and,
// for OPQ, a rotation applied to the vectors and not to the query. OPQ exists
// to be at least as good as PQ, so it is also held to PQ's recall, within the
// run-to-run spread of the two. Measured: PQ 0.955–0.973, OPQ 0.944–0.969.
func TestPQAndOPQRecallOnAClusteredCorpus(t *testing.T) {
	// Training 8 subspaces × 256 centroids, then OPQ's rotations, is
	// single-threaded arithmetic: the race detector finds nothing in it and
	// made it run past ten minutes with coverage on CI. The gate runs in
	// every test job without -race.
	if raceEnabled {
		t.Skip("numeric recall gate; runs without -race")
	}
	c := codebookGateCorpus()
	pq := c.recall(t, NewPQIndex(prodPQSubspaces, prodPQCodebookSize, prodKMeansMaxIter))
	opq := c.recall(t, NewOPQIndex(prodPQSubspaces, prodPQCodebookSize, prodKMeansMaxIter, prodOPQIterations))
	requireRecall(t, "pq", pq, 0.93)
	requireRecall(t, "opq", opq, 0.92)
	if opq < pq-0.04 {
		t.Errorf("opq recall@%d = %.3f is below pq's %.3f by more than their spread", recallGateTopK, opq, pq)
	}
}
