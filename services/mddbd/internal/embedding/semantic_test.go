package embedding

import (
	"testing"
	"time"
)

func TestIsSemanticDefaultsToTrueForModels(t *testing.T) {
	for _, p := range []Provider{
		NewOllamaProvider("http://localhost:11434", "nomic-embed-text", 768),
		NewOpenAIProvider("k", "https://api.openai.com/v1", "text-embedding-3-small", 1536),
	} {
		if !IsSemantic(p) {
			t.Errorf("%s reported as not semantic", p.Model())
		}
	}
}

func TestCachePassesSemanticThrough(t *testing.T) {
	lex := NewCachingProvider(NewLexicalProvider(256), 10, time.Minute)
	if IsSemantic(lex) {
		t.Error("the cache made the lexical provider claim meaning")
	}
	model := NewCachingProvider(NewOllamaProvider("http://localhost:11434", "m", 8), 10, time.Minute)
	if !IsSemantic(model) {
		t.Error("the cache hid that a model is semantic")
	}
}

func TestIsSemanticProvider(t *testing.T) {
	if IsSemanticProvider(ProviderLexical) {
		t.Error("lexical reported as semantic")
	}
	for _, name := range []string{"openai", "ollama", "cohere", "voyage"} {
		if !IsSemanticProvider(name) {
			t.Errorf("%s reported as not semantic", name)
		}
	}
}

// Lexical is chosen only by name, and the environment path builds it with the
// configured dimensions behind the usual cache.
func TestNewProviderBuildsLexicalOnlyWhenNamed(t *testing.T) {
	t.Setenv("MDDB_EMBEDDING_PROVIDER", ProviderLexical)
	t.Setenv("MDDB_EMBEDDING_DIMENSIONS", "1024")
	p := NewProvider()
	if p == nil || p.Model() != LexicalModel || p.Dimensions() != 1024 {
		t.Fatalf("NewProvider() = %v", p)
	}
	if IsSemantic(p) {
		t.Error("NewProvider's lexical provider reports semantic")
	}

	t.Setenv("MDDB_EMBEDDING_PROVIDER", "")
	if p := NewProvider(); p != nil {
		t.Errorf("no provider named, got %s", p.Model())
	}
}
