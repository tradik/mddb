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
	if cacheKey(model, text, RoleQuery) != cacheKey(model, text, RoleQuery) {
		t.Error("the key is not stable for one role")
	}
}

func TestRoleString(t *testing.T) {
	if RoleDocument.String() != "document" || RoleQuery.String() != "query" {
		t.Errorf("roles render as %q and %q", RoleDocument, RoleQuery)
	}
}
