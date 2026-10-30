package main

import (
	"context"
	"errors"
)

// --- Collection Config ---

// GetCollectionConfig retrieves configuration for a collection via the direct client.
func (c *DirectClient) GetCollectionConfig(ctx context.Context, collection string) (*MCPCollectionConfigResponse, error) {
	cfg, found := c.server.CollectionManager.Get(collection)
	if !found {
		cfg = &CollectionConfig{Type: "default"}
	}
	return &MCPCollectionConfigResponse{
		Collection: collection,
		// GO-035: credentials masked. This path answers an LLM, which may
		// repeat what it is given into a transcript, a log or a reply — the
		// one audience for which handing over an S3 secret key is least
		// recoverable.
		Config:     redactCollectionConfig(cfg),
		Configured: found,
	}, nil
}

// SetCollectionConfig updates configuration for a collection via the direct client.
func (c *DirectClient) SetCollectionConfig(ctx context.Context, req *MCPSetCollectionConfigRequest) error {
	if err := validateWordPressTarget(req.WordPress); err != nil {
		return err
	}
	// Merge into the stored config rather than replacing it.
	//
	// MCPSetCollectionConfigRequest covers 9 of CollectionConfig's ~18 fields,
	// and CollectionManager.Set writes whatever struct it is handed. Building
	// a fresh one erased storageBackend, quantization, spell settings and —
	// worst — Encrypted, whose false value goes straight into the encryptor,
	// so an agent updating a description left the next document in an
	// encrypted collection stored as plaintext. Same defect as the gRPC path
	// fixed in RAG-001; this is the MCP one.
	cfg := &CollectionConfig{}
	if existing, found := c.server.CollectionManager.Get(req.Collection); found && existing != nil {
		*cfg = *existing
	}
	// Only what was sent changes; the merge works on a copy, so a field that
	// fails validation leaves the stored config untouched.
	mergeMCPConfigFields(cfg, req)
	if err := mergeMCPIndexingConfig(cfg, req); err != nil {
		return err
	}
	if err := mergeMCPRetrievalConfig(cfg, req); err != nil {
		return err
	}

	// GO-043: a change needs a global admin; nil keeps the stored quota.
	if err := c.server.checkQuotaChange(ctx, req.Collection, req.Quota); err != nil {
		return err
	}
	cfg.Quota = req.Quota

	// GO-035: an agent that read this config back saw masked credentials, so
	// an empty one means "unchanged".
	if stored, found := c.server.CollectionManager.Get(req.Collection); found {
		carryOverSecrets(cfg, stored)
	} else {
		carryOverSecrets(cfg, nil)
	}
	return c.server.CollectionManager.Set(req.Collection, cfg)
}

// mergeMCPConfigFields copies the fields that need no validation, each only
// when sent. The first six used to be assigned unconditionally, so an agent
// setting a description erased the icon, the colour and the custom meta.
func mergeMCPConfigFields(cfg *CollectionConfig, req *MCPSetCollectionConfigRequest) {
	if req.Type != "" {
		cfg.Type = req.Type
	}
	if req.Description != "" {
		cfg.Description = req.Description
	}
	if req.Icon != "" {
		cfg.Icon = req.Icon
	}
	if req.Color != "" {
		cfg.Color = req.Color
	}
	if req.CustomMeta != nil {
		cfg.CustomMeta = req.CustomMeta
	}
	if req.FTS != nil {
		cfg.FTS = req.FTS
	}
	// GO-035: a nil target means "not sent". Assigning it unconditionally
	// deleted a configured publishing target whenever an agent touched
	// anything else.
	if req.WordPress != nil {
		cfg.WordPress = req.WordPress
	}
}

// mergeMCPIndexingConfig validates and copies maxRevisions (nil when not
// sent, since 0 means unlimited), the code settings and the chunking.
func mergeMCPIndexingConfig(cfg *CollectionConfig, req *MCPSetCollectionConfigRequest) error {
	if req.MaxRevisions != nil {
		if *req.MaxRevisions < 0 {
			return errors.New("invalid maxRevisions: must be >= 0 (0 = unlimited)")
		}
		cfg.MaxRevisions = *req.MaxRevisions
	}
	if req.Code != nil {
		if err := req.Code.Validate(); err != nil {
			return err
		}
		cfg.Code = req.Code
	}
	if req.Embedding != nil {
		if err := validateChunking(req.Embedding); err != nil {
			return err
		}
		cfg.Embedding = req.Embedding
	}
	return nil
}

// mergeMCPRetrievalConfig validates and copies the retrieval profile and the
// response prompt (RAG-001, RAG-002), each only when sent.
func mergeMCPRetrievalConfig(cfg *CollectionConfig, req *MCPSetCollectionConfigRequest) error {
	if req.Retrieval != nil {
		if err := req.Retrieval.Validate(); err != nil {
			return err
		}
		cfg.Retrieval = req.Retrieval
	}
	if req.ResponsePrompt != "" {
		if err := ValidateResponsePrompt(req.ResponsePrompt); err != nil {
			return err
		}
		cfg.ResponsePrompt = req.ResponsePrompt
	}
	return nil
}
