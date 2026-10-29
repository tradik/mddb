package embedding

// SemanticReporter is implemented by a provider that knows whether its
// vectors encode meaning (SRCH-014).
//
// Every model-backed provider does — that is what an embedding model is for —
// so they do not implement it and IsSemantic answers true for them. The
// lexical provider implements it to answer false, because its vectors encode
// shared spelling and nothing else, and every place that describes the
// vector configuration to a person or an agent must be able to say so.
type SemanticReporter interface {
	// Semantic reports whether the provider's vectors encode meaning.
	Semantic() bool
}

// IsSemantic reports whether p's vectors encode meaning. A provider that does
// not say is a model, and models do.
func IsSemantic(p Provider) bool {
	if s, ok := p.(SemanticReporter); ok {
		return s.Semantic()
	}
	return true
}

// IsSemanticProvider reports whether the provider named name produces
// vectors that encode meaning — false only for "lexical".
func IsSemanticProvider(name string) bool {
	return name != ProviderLexical
}
