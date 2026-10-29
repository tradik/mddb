// Package sliceutil provides small, dependency-free slice helpers shared across
// the daemon and its internal packages.
package sliceutil

// Unique returns a new slice containing the elements of in with duplicates
// removed, preserving first-seen order. The input is not modified.
func Unique[T comparable](in []T) []T {
	seen := make(map[T]struct{}, len(in))
	out := make([]T, 0, len(in))
	for _, x := range in {
		if _, ok := seen[x]; !ok {
			seen[x] = struct{}{}
			out = append(out, x)
		}
	}
	return out
}

// GroupTop keeps the first perGroup items of each group, in input order.
//
// Items are expected best-first, so "first" is "best": the result is the input
// with each group's overflow removed. ranks[i] is out[i]'s 1-based position
// within its group. perGroup < 1 is treated as 1.
func GroupTop[T any](in []T, perGroup int, key func(T) string) (out []T, ranks []int) {
	if perGroup < 1 {
		perGroup = 1
	}
	seen := make(map[string]int, len(in))
	out = make([]T, 0, len(in))
	ranks = make([]int, 0, len(in))
	for _, x := range in {
		k := key(x)
		if seen[k] >= perGroup {
			continue
		}
		seen[k]++
		out = append(out, x)
		ranks = append(ranks, seen[k])
	}
	return out, ranks
}
