package embedding

// Provider names as configured (MDDB_EMBEDDING_PROVIDER, the stored
// embedding configuration). ProviderLexical lives next to its provider.
const (
	ProviderOpenAI = "openai"
	ProviderOllama = "ollama"
	ProviderCohere = "cohere"
	ProviderVoyage = "voyage"
)

// NameReporter is implemented by every built-in provider: it names the
// service the provider calls (#415). The model name cannot stand in for it:
// an Ollama provider can be given an OpenAI model name, and then only the
// provider name shows where the requests go.
type NameReporter interface {
	// ProviderName is the provider's configured name, such as "ollama".
	ProviderName() string
}

// NameOf is the name of the provider p calls, "" when p does not say.
func NameOf(p Provider) string {
	if n, ok := p.(NameReporter); ok {
		return n.ProviderName()
	}
	return ""
}

// ProviderName implements NameReporter.
func (p *OpenAIProvider) ProviderName() string { return ProviderOpenAI }

// ProviderName implements NameReporter.
func (p *OllamaProvider) ProviderName() string { return ProviderOllama }

// ProviderName implements NameReporter.
func (p *CohereProvider) ProviderName() string { return ProviderCohere }

// ProviderName implements NameReporter.
func (p *VoyageProvider) ProviderName() string { return ProviderVoyage }

// ProviderName implements NameReporter.
func (p *LexicalProvider) ProviderName() string { return ProviderLexical }

// ProviderName passes the wrapped provider's name through, like
// DocumentVariant: the cache is not a provider of its own.
func (c *CachingProvider) ProviderName() string { return NameOf(c.inner) }
