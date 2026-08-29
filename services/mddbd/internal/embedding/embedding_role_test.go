package embedding

import "testing"

// RAG-006. Retrieval models are trained asymmetrically, and the whole point of
// the Role argument is that a query and a document reach the model differently.
// These tests hold that contract without needing a model to be running.

func TestTaskPrefixDistinguishesQueryFromDocument(t *testing.T) {
	const text = "how do I rotate a token"

	doc := applyTaskPrefix("nomic-embed-text:latest", text, RoleDocument)
	query := applyTaskPrefix("nomic-embed-text:latest", text, RoleQuery)

	if doc == query {
		t.Fatal("a document and a query reach the model as the same string; " +
			"the asymmetry the model was trained on is gone")
	}
	if doc != "search_document: "+text {
		t.Errorf("document prefix = %q", doc)
	}
	if query != "search_query: "+text {
		t.Errorf("query prefix = %q", query)
	}
}

// The tag is not part of the model's identity for this purpose: pulling
// nomic-embed-text:v1.5 instead of :latest must not silently drop the prefixes
// and leave retrieval quietly worse.
func TestTaskPrefixIgnoresTheModelTag(t *testing.T) {
	for _, model := range []string{
		"nomic-embed-text",
		"nomic-embed-text:latest",
		"nomic-embed-text:v1.5",
	} {
		if got := applyTaskPrefix(model, "x", RoleQuery); got != "search_query: x" {
			t.Errorf("%s: got %q, want the query prefix", model, got)
		}
	}
}

// A model nobody has measured is sent through untouched. An invented prefix
// moves every vector without improving the ranking, which is worse than none.
func TestUnknownModelIsNotPrefixed(t *testing.T) {
	for _, model := range []string{"all-minilm", "some-future-model:7b", ""} {
		for _, role := range []Role{RoleDocument, RoleQuery} {
			if got := applyTaskPrefix(model, "x", role); got != "x" {
				t.Errorf("%s/%s: got %q, want the text unchanged", model, role, got)
			}
		}
	}
}

// The cache key carries the role. Without it the two roles of one string share
// an entry, and whichever was embedded first answers for both — which surfaces
// as a ranking problem rather than a caching one.
func TestCacheKeyDependsOnRole(t *testing.T) {
	const model, text = "nomic-embed-text", "same words either way"

	if cacheKey(model, text, RoleDocument) == cacheKey(model, text, RoleQuery) {
		t.Fatal("a query and a document with the same text share a cache entry")
	}
	first := cacheKey(model, text, RoleQuery)
	second := cacheKey(model, text, RoleQuery)
	if first != second {
		t.Error("the key is not stable for one role")
	}
}

func TestRoleString(t *testing.T) {
	if RoleDocument.String() != "document" || RoleQuery.String() != "query" {
		t.Errorf("roles render as %q and %q", RoleDocument, RoleQuery)
	}
}

// RAG-007. The variant says how documents are embedded now, and an empty one
// means "as MDDB always did" — which is what records written before roles
// existed hold. That equivalence is what lets a mismatch be a plain string
// comparison instead of a special case.
func TestDocumentVariantDistinguishesProvidersThatChanged(t *testing.T) {
	prefixed := &OllamaProvider{model: "nomic-embed-text:latest"}
	if got := prefixed.DocumentVariant(); got != "search_document: " {
		t.Errorf("a prefixed model reports %q", got)
	}

	// A model with no measured prefix is embedded as it always was.
	plain := &OllamaProvider{model: "all-minilm"}
	if got := plain.DocumentVariant(); got != "" {
		t.Errorf("an unprefixed model reports %q, want empty", got)
	}

	// Cohere always sent search_document, so its document side is unchanged.
	if got := (&CohereProvider{}).DocumentVariant(); got != "" {
		t.Errorf("Cohere reports %q, want empty — its documents never changed", got)
	}

	// Voyage never sent input_type at all, so its documents did change.
	if got := (&VoyageProvider{}).DocumentVariant(); got == "" {
		t.Error("Voyage reports an empty variant, but its documents now carry " +
			"an input_type they were written without")
	}
}

// A provider that reports nothing must not be mistaken for one that reports
// something; the wrapper has to pass the answer through.
func TestCachingProviderPassesTheVariantThrough(t *testing.T) {
	inner := &OllamaProvider{model: "nomic-embed-text"}
	c := NewCachingProvider(inner, 10, 0)
	if got, want := DocumentVariantOf(c), inner.DocumentVariant(); got != want {
		t.Errorf("cache reports %q, wrapped provider reports %q", got, want)
	}
}
