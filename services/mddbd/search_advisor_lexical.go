package main

// What the search advisor says about lexical vectors (SRCH-014).
//
// The lexical provider hashes words and character n-grams; its vectors know
// spelling, not meaning. Whether blending them with BM25 is worth it is a
// question for a measurement, and the measurement on the MDDB docs set
// (tools/bench/evalsets/docs.json, 20 queries, 60 pages, 2026-10-29) said:
//
//	fts/bm25              recall@10 0.825  nDCG@10 0.719  MRR 0.698
//	vector (lexical 512)  recall@10 0.775  nDCG@10 0.649  MRR 0.644
//	hybrid (lexical 512)  recall@10 0.875  nDCG@10 0.726  MRR 0.686
//	hybrid (nomic-embed)  recall@10 0.875  nDCG@10 0.782  MRR 0.764
//
// Hybrid with lexical vectors found one more answer in twenty and ranked the
// answers no better (nDCG +0.007, MRR −0.012). One query is inside the noise
// of a twenty-query set, so the default advice is keyword search, with the
// reason. A collection with its own evaluation set is measured instead
// (SRCH-019), and the measurement overrides this when hybrid wins there.

// lexicalAdvice is the reason given for keyword search when the collection's
// vectors are lexical.
const lexicalAdvice = "Keyword search: this collection's vectors come from the lexical provider, which hashes words and word fragments and encodes no meaning. " +
	"Measured on the MDDB docs set, lexical vectors added no ranking signal over bm25 (hybrid nDCG@10 0.726 against 0.719, MRR lower). " +
	"Add an evaluation set to measure your own collection, or configure an embedding model for vectors that match by meaning."
