package embedding

import "fmt"

// A provider's answer is checked before anyone else sees it (#252).
//
// Every path that builds a provider — environment, stored configuration,
// autodetection — ends at one of the four constructors in this package, and two
// of those paths bypass every decorator (the stored-configuration path gets no
// cache, and neither does autodetection). The provider itself is therefore the
// only place a check cannot be walked around.
//
// What it guards against was reaching production. OpenAI and Voyage built their
// result as `make([][]float32, len(result.Data))` and then assigned by each
// entry's `index`, trusting both: an answer with fewer entries than texts, or
// with an index skipped or repeated, left nil vectors in the slice and returned
// them with a nil error. Under rate limiting (HTTP 429) that is not
// hypothetical. A nil vector then reached the HNSW graph, where — if it arrived
// first — it made every later add in its collection fail.
//
// A nil or empty vector is not a result, it is the absence of one, and the
// caller already knows how to handle an error: the document is counted as
// failed and can be retried. What it could not handle was being told nothing
// had gone wrong.

// checkVectors verifies that a batch answer holds exactly want usable vectors,
// all of the same length.
func checkVectors(provider string, vectors [][]float32, want int) error {
	if len(vectors) != want {
		return fmt.Errorf("%s returned %d embeddings for %d texts", provider, len(vectors), want)
	}
	dim := 0
	for i, v := range vectors {
		if len(v) == 0 {
			return fmt.Errorf("%s returned no embedding for text %d of %d", provider, i+1, want)
		}
		if dim == 0 {
			dim = len(v)
			continue
		}
		if len(v) != dim {
			return fmt.Errorf("%s returned embeddings of different lengths in one batch: %d and %d",
				provider, dim, len(v))
		}
	}
	return nil
}

// placeByIndex builds the ordered result of an API that labels each embedding
// with the position of its input, refusing an index it cannot place rather
// than panicking on it or leaving a hole.
func placeByIndex(provider string, want int, entries []indexedEmbedding) ([][]float32, error) {
	vectors := make([][]float32, want)
	for _, e := range entries {
		if e.index < 0 || e.index >= want {
			return nil, fmt.Errorf("%s returned an embedding for text %d of %d", provider, e.index+1, want)
		}
		if vectors[e.index] != nil {
			return nil, fmt.Errorf("%s returned two embeddings for text %d", provider, e.index+1)
		}
		vectors[e.index] = e.vector
	}
	return vectors, checkVectors(provider, vectors, want)
}

// indexedEmbedding is one entry of an answer that says which input it belongs to.
type indexedEmbedding struct {
	index  int
	vector []float32
}
