package main

import (
	"testing"

	"mddb/internal/rerank"
)

func TestRerankPoolSizeIsBounded(t *testing.T) {
	ceiling := rerank.MaxCandidatesCeiling
	for _, c := range []struct{ available, requested, want int }{
		{10, 5, 5},
		{3, 5, 3},
		{ceiling + 50, ceiling + 10, ceiling},
		{10, -1, 0},
	} {
		if got := rerankPoolSize(c.available, c.requested); got != c.want {
			t.Errorf("rerankPoolSize(%d, %d) = %d, want %d", c.available, c.requested, got, c.want)
		}
	}
}
