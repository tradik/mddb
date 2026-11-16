package main

import (
	"log/slog"
	"time"

	"mddb/internal/envconf"
	"mddb/internal/rerank"
)

// Where the reranking model comes from (RAG-011): a stored default rerank
// configuration — the embedding_configs store with kind "rerank" — first,
// then MDDB_RERANK_*. Nothing configured means no provider and no change to
// any search, which is the default.

// rerankSetup is the provider in use and the bound on what a search may send.
type rerankSetup struct {
	provider      rerank.Provider
	maxCandidates int
	source        string // "stored config" or "environment"
}

// initRerank picks the reranking provider at startup.
func (s *Server) initRerank() {
	if cfg, err := s.defaultModelConfig(ConfigKindRerank); err == nil {
		s.InitializeRerankFromConfig(cfg)
		return
	}
	c := rerank.ConfigFromEnv()
	s.setRerank(rerank.FromEnv(), c.Normalized().MaxCandidates, "environment")
}

// InitializeRerankFromConfig switches to a stored rerank configuration.
func (s *Server) InitializeRerankFromConfig(cfg *EmbeddingConfig) {
	c := rerank.Config{Provider: cfg.Provider, APIURL: cfg.APIURL, APIKey: cfg.APIKey, Model: cfg.Model,
		Timeout: time.Duration(cfg.TimeoutMs) * time.Millisecond, MaxCandidates: cfg.MaxCandidates}
	s.setRerank(rerank.New(c, envconf.Int("MDDB_RERANK_CACHE_SIZE", rerank.DefaultCacheSize)),
		c.Normalized().MaxCandidates, "stored config")
}

// setRerank installs a provider; nil turns reranking off.
func (s *Server) setRerank(p rerank.Provider, maxCandidates int, source string) {
	if p == nil {
		s.reranker.Store(nil)
		return
	}
	s.reranker.Store(&rerankSetup{provider: p, maxCandidates: maxCandidates, source: source})
	slog.Info("reranking enabled", "source", source, "model", p.Model(), "maxCandidates", maxCandidates)
}

// rerankConfigured is the current setup, nil when reranking is off.
func (s *Server) rerankConfigured() *rerankSetup {
	return s.reranker.Load()
}

// keepStoredRerankKey keeps a rerank configuration's stored key when an
// update leaves it out: responses never carry the key, so a client that
// saves what it read sends none.
func (s *Server) keepStoredRerankKey(cfg *EmbeddingConfig) {
	if kindOf(cfg) != ConfigKindRerank || cfg.APIKey != "" {
		return
	}
	if old, err := s.GetEmbeddingConfig(cfg.ID); err == nil {
		cfg.APIKey = old.APIKey
	}
}
