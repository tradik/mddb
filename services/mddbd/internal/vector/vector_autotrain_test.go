package vector

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestWhenACollectionIsDueForTraining(t *testing.T) {
	for _, tc := range []struct {
		name        string
		state       trainState
		n           int
		train, wait bool
	}{
		{"empty collection", trainState{}, 0, false, false},
		{"never trained", trainState{}, 1, true, true},
		{"grown by less than double", trainState{trained: true, trainedOn: 10}, 19, false, false},
		{"doubled", trainState{trained: true, trainedOn: 10}, 20, true, false},
		{"doubled, retrain already running", trainState{trained: true, trainedOn: 10, retraining: true}, 40, false, false},
		{"shrunk", trainState{trained: true, trainedOn: 10}, 3, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			train, wait := tc.state.due(tc.n)
			if train != tc.train || wait != tc.wait {
				t.Errorf("due(%d) = %v, %v; want %v, %v", tc.n, train, wait, tc.train, tc.wait)
			}
		})
	}
}

// trainables are the five indexes that search a trained structure, each with
// a probe for the state of one collection.
func trainables() map[string]struct {
	index VectorSearcher
	state func(collection string) trainState
} {
	ivf, pq, opq, sq, sq4 := NewIVFIndex(4, 5), NewPQIndex(2, 16, 5), NewOPQIndex(2, 16, 5, 1), NewSQIndex(), NewSQ4Index()
	type probe = struct {
		index VectorSearcher
		state func(string) trainState
	}
	return map[string]probe{
		"ivf": {ivf, func(c string) trainState { ivf.mu.RLock(); defer ivf.mu.RUnlock(); return ivf.data[c].trainState }},
		"pq":  {pq, func(c string) trainState { pq.mu.RLock(); defer pq.mu.RUnlock(); return pq.data[c].trainState }},
		"opq": {opq, func(c string) trainState { opq.mu.RLock(); defer opq.mu.RUnlock(); return opq.data[c].trainState }},
		"sq":  {sq, func(c string) trainState { sq.mu.RLock(); defer sq.mu.RUnlock(); return sq.data[c].trainState }},
		"sq4": {sq4, func(c string) trainState { sq4.mu.RLock(); defer sq4.mu.RUnlock(); return sq4.data[c].trainState }},
	}
}

func unitVec(i, dim int) []float32 {
	v := make([]float32, dim)
	for d := range v {
		v[d] = 0.01
	}
	v[i%dim] = 1
	return v
}

// A collection trained when it was small is retrained once it has doubled,
// without the search that notices it waiting for the training.
func TestACollectionThatDoublesIsRetrained(t *testing.T) {
	for name, tr := range trainables() {
		t.Run(name, func(t *testing.T) {
			for i := 0; i < 4; i++ {
				if err := tr.index.Add("c", fmt.Sprint(i), unitVec(i, 4)); err != nil {
					t.Fatal(err)
				}
			}
			tr.index.Search("c", unitVec(0, 4), 1, -1, nil)
			if got := tr.state("c"); !got.trained || got.trainedOn != 4 {
				t.Fatalf("after the first search: %+v, want trained on 4", got)
			}

			for i := 4; i < 8; i++ {
				if err := tr.index.Add("c", fmt.Sprint(i), unitVec(i, 4)); err != nil {
					t.Fatal(err)
				}
			}
			tr.index.Search("c", unitVec(0, 4), 1, -1, nil)

			deadline := time.Now().Add(5 * time.Second)
			for tr.state("c").trainedOn != 8 {
				if time.Now().After(deadline) {
					t.Fatalf("never retrained: %+v", tr.state("c"))
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}

// Training works on a snapshot. A vector added after the snapshot was taken
// is in the collection but not in what was trained; installing the new
// structure must not leave it out, or it is unsearchable until the next
// retrain.
func TestAVectorAddedDuringTrainingIsNotLeftOut(t *testing.T) {
	for name, tr := range trainables() {
		t.Run(name, func(t *testing.T) {
			snapshot := map[string][]float32{}
			for i := 0; i < 4; i++ {
				v := unitVec(i, 4)
				snapshot[fmt.Sprint(i)] = v
				if err := tr.index.Add("c", fmt.Sprint(i), v); err != nil {
					t.Fatal(err)
				}
			}
			late := []float32{0.01, 0.01, 0.01, 1}
			if err := tr.index.Add("c", "late", late); err != nil {
				t.Fatal(err)
			}

			tr.index.(Trainable).Train("c", snapshot)

			found := false
			for _, hit := range tr.index.Search("c", late, 5, -1, nil) {
				found = found || hit.DocID == "late"
			}
			if !found {
				t.Error("the vector added after the snapshot is not searchable")
			}
			if got := tr.state("c").trainedOn; got != 5 {
				t.Errorf("trainedOn = %d, want 5 (everything the collection holds)", got)
			}
		})
	}
}

// Searches and adds racing a background retrain. Run with -race.
func TestSearchingWhileTrainingIsSafe(t *testing.T) {
	for name, tr := range trainables() {
		t.Run(name, func(t *testing.T) {
			var wg sync.WaitGroup
			for w := 0; w < 4; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					for i := 0; i < 50; i++ {
						_ = tr.index.Add("c", fmt.Sprintf("%d-%d", w, i), unitVec(i+w, 4))
						tr.index.Search("c", unitVec(i, 4), 3, -1, nil)
						tr.index.SearchWithFilter("c", unitVec(i, 4), 3, -1, map[string]bool{"0-0": true}, nil)
					}
				}(w)
			}
			wg.Wait()
			if len(tr.index.Search("c", unitVec(0, 4), 3, -1, nil)) == 0 {
				t.Error("nothing found after concurrent adds")
			}
		})
	}
}

// A search on a collection the index has never seen trains nothing and finds
// nothing.
func TestAutoTrainIgnoresAnUnknownCollection(t *testing.T) {
	for name, tr := range trainables() {
		if hits := tr.index.Search("none", unitVec(0, 4), 3, -1, nil); len(hits) != 0 {
			t.Errorf("%s found %+v in a collection it never saw", name, hits)
		}
	}
}
