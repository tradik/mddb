package vector

import (
	"errors"
	"fmt"
)

// What an index will refuse, and why it has to refuse it (#252).
//
// A vector the index cannot compare with its neighbours is not a slow vector,
// it is a trap. `coder/hnsw` computes the distance between the incoming node
// and the graph's entry point, and a length mismatch panics inside the library:
//
//	embedding dimension mismatch: 0 != 3
//
// The old code caught that panic and carried on, which looks defensive and is
// not. Three things went wrong at once, and only the first was visible:
//
//  1. the chunk never entered the graph, and the reindex still reported
//     `"failed": 0`;
//  2. the vector was already in the flat fallback map by then, so
//     CollectionSize counted a chunk that nothing could find;
//  3. worst, if the unusable vector arrived FIRST it became the graph's entry
//     point — and then every later add to that collection panicked against it.
//     A single empty vector cost a whole collection its index, and every
//     search on it silently fell back to brute force.
//
// Measured: four vectors added to an empty collection, the first of them
// empty, left three panics and zero usable graph nodes.
//
// So the rule is enforced before anything is stored, and the caller is told.
var (
	// ErrEmptyVector is a vector with no components. A provider that answers a
	// rate-limited request with an empty array produces one, which is how this
	// reached production.
	ErrEmptyVector = errors.New("vector is empty")
	// ErrDimensionMismatch is a vector that cannot be compared with the ones
	// already in its collection.
	ErrDimensionMismatch = errors.New("vector dimension differs from the collection")
)

// CheckVector reports whether a vector can be indexed alongside a collection
// whose established dimension is want. A want of zero means the collection has
// nothing to compare against yet, so any non-empty vector is acceptable and
// becomes the dimension the rest must match.
func CheckVector(v []float32, want int) error {
	if len(v) == 0 {
		return ErrEmptyVector
	}
	if want > 0 && len(v) != want {
		return fmt.Errorf("%w: %d != %d", ErrDimensionMismatch, len(v), want)
	}
	return nil
}
