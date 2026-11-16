// Package rerank orders search candidates with a relevance model (RAG-011).
//
// Every ordering stage MDDB had before this — fusion, boosts, MMR, feedback
// rescoring, the exact pass after binary quantization — scores a document
// against the query from vectors or term statistics computed separately for
// each. A reranking model reads the query and a candidate's text together and
// answers one number: how well this text answers this question. It is slower
// and costs a provider call, so it runs on a short list a search has already
// found, never on the collection.
//
// The package mirrors internal/embedding: a Provider interface, one HTTP
// transport, a cache decorator, and a factory reading MDDB_RERANK_*. Nothing
// here knows about collections, budgets or result types; the server's rerank
// stage owns those.
package rerank

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"time"

	"mddb/internal/envconf"
	"mddb/internal/httpclient"
)

// Scored is one candidate's place in a provider's answer: its index in the
// documents sent and the relevance the model gave it.
type Scored struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

// Provider orders documents by their relevance to a query.
type Provider interface {
	// Rerank scores documents against query and returns them best first.
	// topN > 0 asks for at most that many; 0 asks for all of them.
	Rerank(ctx context.Context, query string, documents []string, topN int) ([]Scored, error)

	// Model names the model doing the scoring.
	Model() string
}

// Provider names.
const (
	// ProviderHTTP is the one transport: POST {apiUrl}/rerank with a query
	// and documents, answered with an index and a score per document.
	ProviderHTTP = "http"
	// ProviderNone turns reranking off; so does an empty name.
	ProviderNone = "none"
)

// Defaults for the settings an operator may leave out.
const (
	DefaultTimeout       = 2 * time.Second
	DefaultMaxCandidates = 50
	// MaxCandidatesCeiling bounds what one search may send, whatever the
	// configuration says: a model call over thousands of texts is a batch
	// job, not a search stage.
	MaxCandidatesCeiling = 200
	DefaultCacheSize     = 4096
	DefaultCacheTTL      = time.Hour
)

// ErrInvalidResponse marks an answer that could not be used: unparseable,
// an index out of range or repeated, a score that is not a number.
var ErrInvalidResponse = errors.New("rerank provider returned an invalid response")

// Config is a reranking provider's settings, from the environment or from a
// stored configuration.
type Config struct {
	Provider      string
	APIURL        string
	APIKey        string // #nosec G117 -- operator credential, never logged or returned
	Model         string
	Timeout       time.Duration
	MaxCandidates int
}

// ConfigFromEnv reads MDDB_RERANK_*.
func ConfigFromEnv() Config {
	return Config{
		Provider:      os.Getenv("MDDB_RERANK_PROVIDER"),
		APIURL:        os.Getenv("MDDB_RERANK_API_URL"),
		APIKey:        os.Getenv("MDDB_RERANK_API_KEY"),
		Model:         os.Getenv("MDDB_RERANK_MODEL"),
		Timeout:       time.Duration(envconf.Int("MDDB_RERANK_TIMEOUT_MS", int(DefaultTimeout/time.Millisecond))) * time.Millisecond,
		MaxCandidates: envconf.Int("MDDB_RERANK_MAX_CANDIDATES", DefaultMaxCandidates),
	}
}

// Normalized fills defaults and bounds the candidate count.
func (c Config) Normalized() Config {
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	if c.MaxCandidates <= 0 {
		c.MaxCandidates = DefaultMaxCandidates
	}
	c.MaxCandidates = min(c.MaxCandidates, MaxCandidatesCeiling)
	return c
}

// Enabled reports whether the configuration names a provider at all.
func (c Config) Enabled() bool {
	return c.Provider != "" && c.Provider != ProviderNone
}

// New builds the provider a configuration describes, wrapped in a cache of
// cacheSize (query, text) scores; nil when reranking is off or the
// configuration cannot work. The reason is logged, never the key.
func New(c Config, cacheSize int) Provider {
	if !c.Enabled() {
		return nil
	}
	if c.Provider != ProviderHTTP {
		slog.Warn("unknown MDDB_RERANK_PROVIDER, reranking disabled", "provider", c.Provider) // #nosec G706 -- operator config
		return nil
	}
	safeURL, err := httpclient.ValidateServiceURL(c.APIURL)
	if err != nil || safeURL == "" {
		slog.Warn("reranking disabled: the provider needs a usable apiUrl", "error", err)
		return nil
	}
	c = c.Normalized()
	c.APIURL = safeURL
	return NewCachingProvider(NewHTTPProvider(c), cacheSize, DefaultCacheTTL)
}

// FromEnv is New over the environment, with MDDB_RERANK_CACHE_SIZE.
func FromEnv() Provider {
	return New(ConfigFromEnv(), envconf.Int("MDDB_RERANK_CACHE_SIZE", DefaultCacheSize))
}

// Reason names why a rerank call failed, for the response's rerankSkipped:
// "timeout", "invalidResponse" or "error". The provider's own message is not
// a reason a caller should see — it may carry whatever the upstream said.
func Reason(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case errors.Is(err, ErrInvalidResponse):
		return "invalidResponse"
	default:
		return "error"
	}
}
