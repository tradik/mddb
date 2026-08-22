package main

// Context token budget (RAG-001).
//
// A RAG caller asks for topK results and gets whatever those documents happen
// to contain. Ten long documents overflow the model's context, and the caller
// finds out at generation time — after paying for the retrieval.
//
// A collection can therefore cap the total context its searches return. The cap
// truncates the result list rather than the documents in it: half a document is
// worse than one document fewer, because a passage cut mid-sentence still costs
// tokens and no longer says anything reliable.

// approxTokens estimates the token count of a string.
//
// Four bytes per token, the usual English rule of thumb. Deliberately not a
// real tokeniser: one would tie the budget to a single model family and make
// the answer depend on which tokeniser MDDB happened to link. This is a guard
// rail, not accounting — it stops a caller being handed ten times its context,
// and does not pretend to know the exact number.
func approxTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

// budgetCut reports how many results fit inside a token budget.
//
// sizes must be in result order, most relevant first: the budget keeps the best
// results and drops the tail, never the other way round.
//
// The first result is always kept even when it alone exceeds the budget.
// Returning nothing would turn a too-small budget into "no matches", which
// reads as an empty corpus rather than a configuration problem.
func budgetCut(sizes []int, budget int) (keep int, truncated bool) {
	if budget <= 0 || len(sizes) == 0 {
		return len(sizes), false
	}

	total := 0
	for i, size := range sizes {
		if i > 0 && total+size > budget {
			return i, true
		}
		total += size
	}
	return len(sizes), false
}

// applyContextBudget truncates results to a collection's context budget.
//
// tokensOf reports one result's token cost; the returned slice length is how
// many results fit. Generic over the result type because every search path has
// its own, and copying this loop four times is how they drift apart.
func applyContextBudget[T any](s *Server, collection string, results []T, tokensOf func(T) int) ([]T, bool) {
	budget := s.ContextTokenBudget(collection)
	if budget <= 0 || len(results) == 0 {
		return results, false
	}

	sizes := make([]int, len(results))
	for i, r := range results {
		sizes[i] = tokensOf(r)
	}

	keep, truncated := budgetCut(sizes, budget)
	return results[:keep], truncated
}
