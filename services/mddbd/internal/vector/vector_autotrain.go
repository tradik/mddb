package vector

import (
	"maps"
	"sync"
)

// When a trainable index builds its structure.
//
// IVF, PQ, OPQ, SQ and SQ4 search a structure — clusters, codebooks, scales —
// trained from a collection's vectors. Until 2.15.3 that training happened at
// startup and on a reindex, nowhere else, and an index that had never been
// trained answered every query with nothing. So a collection created while the
// server was running was invisible to all five until the next restart: 2.15.2
// made sure each of them received the vectors, and measured on the released
// image they still found none. A collection that grew after startup was found,
// but through a structure trained on whatever it held at startup.
//
// The index now decides for itself. A collection that has vectors and was
// never trained is trained before the first search, which waits for it — the
// alternative is an empty answer. One that has doubled since it was last
// trained is retrained in the background while searches go on against the
// current structure, so the cost of training stays proportional to growth:
// a collection that grows to N vectors is trained about log2(N) times.

// trainState is what each trainable index keeps per collection to decide when
// to train it. Embedded in the collection, it supplies the `trained` field the
// search paths already check.
type trainState struct {
	trained    bool
	trainedOn  int  // vectors the collection held when training finished
	retraining bool // a background retrain is running; do not start another
}

// markTrained records a finished training over n vectors.
func (t *trainState) markTrained(n int) {
	t.trained = true
	t.trainedOn = n
	t.retraining = false
}

// due reports whether a collection holding n vectors should be trained, and
// whether the search asking must wait for it.
func (t *trainState) due(n int) (train, wait bool) {
	switch {
	case n == 0 || t.retraining:
		return false, false
	case !t.trained:
		return true, true
	case n >= 2*t.trainedOn:
		return true, false
	}
	return false, false
}

// autoTrain trains a collection when due. state is called under mu and returns
// the collection's train state and its vectors, or nil when the collection
// does not exist; train is called outside mu with a copy of the vectors.
//
// The common case — nothing to do — costs one read lock, so searches do not
// serialise on it.
func autoTrain(mu *sync.RWMutex, state func() (*trainState, map[string][]float32), train func(map[string][]float32)) {
	mu.RLock()
	t, vecs := state()
	run := false
	if t != nil {
		run, _ = t.due(len(vecs))
	}
	mu.RUnlock()
	if !run {
		return
	}

	mu.Lock()
	t, vecs = state() // re-checked: another search may have got here first
	if t == nil {
		mu.Unlock()
		return
	}
	run, wait := t.due(len(vecs))
	if !run {
		mu.Unlock()
		return
	}
	snapshot := maps.Clone(vecs)
	if !wait {
		t.retraining = true
	}
	mu.Unlock()

	if wait {
		train(snapshot)
		return
	}
	go train(snapshot)
}

// The five indexes' hooks: which vectors each keeps, and its Train.

func (idx *IVFIndex) autoTrain(collection string) {
	autoTrain(&idx.mu, func() (*trainState, map[string][]float32) {
		if c, ok := idx.data[collection]; ok {
			return &c.trainState, c.allVecs
		}
		return nil, nil
	}, func(v map[string][]float32) { idx.Train(collection, v) })
}

func (p *PQIndex) autoTrain(collection string) {
	autoTrain(&p.mu, func() (*trainState, map[string][]float32) {
		if c, ok := p.data[collection]; ok {
			return &c.trainState, c.origVecs
		}
		return nil, nil
	}, func(v map[string][]float32) { p.Train(collection, v) })
}

func (o *OPQIndex) autoTrain(collection string) {
	autoTrain(&o.mu, func() (*trainState, map[string][]float32) {
		if c, ok := o.data[collection]; ok {
			return &c.trainState, c.origVecs
		}
		return nil, nil
	}, func(v map[string][]float32) { o.Train(collection, v) })
}

func (s *SQIndex) autoTrain(collection string) {
	autoTrain(&s.mu, func() (*trainState, map[string][]float32) {
		if c, ok := s.data[collection]; ok {
			return &c.trainState, c.origVecs
		}
		return nil, nil
	}, func(v map[string][]float32) { s.Train(collection, v) })
}

func (s *SQ4Index) autoTrain(collection string) {
	autoTrain(&s.mu, func() (*trainState, map[string][]float32) {
		if c, ok := s.data[collection]; ok {
			return &c.trainState, c.origVecs
		}
		return nil, nil
	}, func(v map[string][]float32) { s.Train(collection, v) })
}
