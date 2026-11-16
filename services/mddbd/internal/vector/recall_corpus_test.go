package vector

import (
	"fmt"
	"testing"
)

// The corpora and the measuring behind the recall gates of TEST-005
// (recall_gate_test.go).

const (
	recallGateTopK = 10
	recallGateSeed = 5
)

// gateCorpus is a seeded corpus, its ids, and the exact top 10 of each query
// as ids.
type gateCorpus struct {
	docs, queries     [][]float32
	ids               []string
	truth             [][]string
	perTopic, heldOut int
}

// newGateCorpus builds topics × perTopic unit vectors stored topic by topic
// and the brute-force cosine ground truth. The queries are heldOut further
// documents of each topic that the corpus does not keep: a query then sits
// among its topic's documents as a document would, not at the centre of them,
// and the corpus has topics × heldOut of them to average over.
func newGateCorpus(dims, topics, perTopic, heldOut int) gateCorpus {
	all, _ := topicCorpus(recallGateSeed, dims, topics, perTopic+heldOut)
	c := gateCorpus{perTopic: perTopic, heldOut: heldOut}
	for i, v := range all {
		if i%(perTopic+heldOut) < perTopic {
			c.docs = append(c.docs, v)
		} else {
			c.queries = append(c.queries, v)
		}
	}
	c.ids = make([]string, len(c.docs))
	for i := range c.ids {
		c.ids[i] = fmt.Sprintf("d%05d", i)
	}
	c.truth = make([][]string, len(c.queries))
	for qi, q := range c.queries {
		for _, i := range exactTop(c.docs, q, recallGateTopK) {
			c.truth[qi] = append(c.truth[qi], c.ids[i])
		}
	}
	return c
}

// recall is recall@10 of idx over the corpus. The index is filled as the
// server fills it: every vector through Add, then one training pass over the
// collection through TrainSafely for an index that trains, as trainCollection
// does after a load.
func (c gateCorpus) recall(t *testing.T, idx VectorSearcher) float64 {
	t.Helper()
	vectors := make(map[string][]float32, len(c.docs))
	for i, d := range c.docs {
		if err := idx.Add("c", c.ids[i], d); err != nil {
			t.Fatal(err)
		}
		vectors[c.ids[i]] = d
	}
	if trainable, ok := idx.(Trainable); ok {
		TrainSafely(trainable, "c", vectors)
	}
	idx.SetReady()

	var total float64
	for qi, q := range c.queries {
		total += recallAt(idx.Search("c", q, recallGateTopK, -1, nil), c.truth[qi])
	}
	return total / float64(len(c.queries))
}

// unrankedTopicRecall is the recall@10 of answering each query with the first
// ten documents of its own topic: what knowing the topic is worth without
// ranking anything inside it.
func (c gateCorpus) unrankedTopicRecall() float64 {
	var total float64
	for qi := range c.queries {
		first := (qi / c.heldOut) * c.perTopic
		answer := make([]VectorResult, recallGateTopK)
		for i := range answer {
			answer[i] = VectorResult{DocID: c.ids[first+i]}
		}
		total += recallAt(answer, c.truth[qi])
	}
	return total / float64(len(c.queries))
}

// requireRecall fails the test when an index's recall@10 is under its floor.
func requireRecall(t *testing.T, name string, got, floor float64) {
	t.Helper()
	t.Logf("%s recall@%d = %.3f (floor %.2f)", name, recallGateTopK, got, floor)
	if got < floor {
		t.Errorf("%s recall@%d = %.3f, want >= %.2f", name, recallGateTopK, got, floor)
	}
}
