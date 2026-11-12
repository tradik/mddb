package embedding

import (
	"testing"
	"time"
)

// #415: the name of the provider that is actually called, never the model's.
func TestNameOf(t *testing.T) {
	cases := []struct {
		want string
		p    Provider
	}{
		{"openai", NewOpenAIProvider("k", "https://api.openai.com/v1", "text-embedding-3-small", 1536)},
		{"ollama", NewOllamaProvider("http://localhost:11434", "text-embedding-3-small", 1536)},
		{"cohere", NewCohereProvider("k", "https://api.cohere.ai/v1", "embed-english-v3.0", 1024)},
		{"voyage", NewVoyageProvider("k", "https://api.voyageai.com/v1", "voyage-3", 1024)},
		{ProviderLexical, NewLexicalProvider(512)},
	}
	for _, c := range cases {
		if got := NameOf(c.p); got != c.want {
			t.Errorf("NameOf(%T) = %q, want %q", c.p, got, c.want)
		}
		cached := NewCachingProvider(c.p, 10, time.Minute)
		if got := NameOf(cached); got != c.want {
			t.Errorf("NameOf(cached %T) = %q, want %q", c.p, got, c.want)
		}
	}
}

// A provider that does not report a name stays unnamed instead of guessed.
func TestNameOfUnknownProvider(t *testing.T) {
	if got := NameOf(nil); got != "" {
		t.Errorf("NameOf(nil) = %q, want empty", got)
	}
	if got := NameOf(unnamedProvider{}); got != "" {
		t.Errorf("NameOf(unnamed) = %q, want empty", got)
	}
}

type unnamedProvider struct{ Provider }
