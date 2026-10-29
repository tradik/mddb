package main

import (
	"context"
	"errors"
	"mddb/internal/storage"
	proto "mddb/proto"
	"strings"
	"time"

	"mddb/internal/schema"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RegisterWebhook implements the RegisterWebhook RPC
func (g *GRPCServer) RegisterWebhook(ctx context.Context, req *proto.RegisterWebhookRequest) (*proto.WebhookProto, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}

	// Check write permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if g.server.WebhookManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "webhooks not initialized")
	}

	wh, err := g.server.WebhookManager.Register(req.Url, req.Events, req.Collection)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return &proto.WebhookProto{
		Id:         wh.ID,
		Url:        wh.URL,
		Events:     wh.Events,
		Collection: wh.Collection,
		CreatedAt:  wh.CreatedAt,
	}, nil
}

// ListWebhooks implements the ListWebhooks RPC
func (g *GRPCServer) ListWebhooks(ctx context.Context, req *proto.ListWebhooksRequest) (*proto.ListWebhooksResponse, error) {
	// Check admin permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if g.server.WebhookManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "webhooks not initialized")
	}

	hooks := g.server.WebhookManager.List()
	protoHooks := make([]*proto.WebhookProto, len(hooks))
	for i, h := range hooks {
		protoHooks[i] = &proto.WebhookProto{
			Id:         h.ID,
			Url:        h.URL,
			Events:     h.Events,
			Collection: h.Collection,
			CreatedAt:  h.CreatedAt,
		}
	}

	return &proto.ListWebhooksResponse{Webhooks: protoHooks}, nil
}

// DeleteWebhook implements the DeleteWebhook RPC
func (g *GRPCServer) DeleteWebhook(ctx context.Context, req *proto.DeleteWebhookRequest) (*proto.DeleteWebhookResponse, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}

	// Check write permission (using "*" since we need to find the webhook's collection)
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if g.server.WebhookManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "webhooks not initialized")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "missing id")
	}

	if err := g.server.WebhookManager.Delete(req.Id); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &proto.DeleteWebhookResponse{Status: "deleted", Id: req.Id}, nil
}

// SetSchema implements the SetSchema RPC
func (g *GRPCServer) SetSchema(ctx context.Context, req *proto.SetSchemaRequest) (*proto.SetSchemaResponse, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}

	// Check write permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}
	if req.Schema == "" {
		return nil, status.Error(codes.InvalidArgument, "missing schema")
	}
	if err := g.server.SchemaManager.Set(req.Collection, req.Schema); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &proto.SetSchemaResponse{Status: "ok"}, nil
}

// GetSchema implements the GetSchema RPC
func (g *GRPCServer) GetSchema(ctx context.Context, req *proto.GetSchemaRequest) (*proto.GetSchemaResponse, error) {
	// Check read permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}
	raw, found := g.server.SchemaManager.Get(req.Collection)
	return &proto.GetSchemaResponse{
		Collection: req.Collection,
		Schema:     raw,
		Enabled:    found,
	}, nil
}

// DeleteSchema implements the DeleteSchema RPC
func (g *GRPCServer) DeleteSchema(ctx context.Context, req *proto.DeleteSchemaRequest) (*proto.DeleteSchemaResponse, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}

	// Check write permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}
	if err := g.server.SchemaManager.Delete(req.Collection); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &proto.DeleteSchemaResponse{Status: "ok"}, nil
}

// ListSchemas implements the ListSchemas RPC
func (g *GRPCServer) ListSchemas(ctx context.Context, req *proto.ListSchemasRequest) (*proto.ListSchemasResponse, error) {
	// Check read permission for listing schemas (using "*" since it's a global operation)
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	schemas := g.server.SchemaManager.List()
	tenant := TenantFromContext(ctx)
	var result []*proto.SchemaInfo
	for col, raw := range schemas {
		if !CollectionInTenant(tenant, col) {
			continue
		}
		result = append(result, &proto.SchemaInfo{
			Collection: col,
			Schema:     raw,
		})
	}
	return &proto.ListSchemasResponse{Schemas: result}, nil
}

// ValidateDocument implements the ValidateDocument RPC
func (g *GRPCServer) ValidateDocument(ctx context.Context, req *proto.ValidateDocumentRequest) (*proto.ValidateDocumentResponse, error) {
	// Check read permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}
	meta := make(map[string][]string)
	for k, v := range req.Meta {
		meta[k] = v.Values
	}
	// DOC-012: advisory findings, reported whether or not validation passes.
	warnings := schema.LintMetaStrings(meta)

	err := g.server.SchemaManager.Validate(req.Collection, meta)
	if err != nil {
		parts := strings.SplitAfter(err.Error(), ": ")
		var errMsgs []string
		if len(parts) > 1 {
			errMsgs = strings.Split(parts[len(parts)-1], "; ")
		} else {
			errMsgs = []string{err.Error()}
		}
		return &proto.ValidateDocumentResponse{Valid: false, Errors: errMsgs, Warnings: warnings}, nil
	}
	return &proto.ValidateDocumentResponse{Valid: true, Warnings: warnings}, nil
}

// UpdateDocument implements the UpdateDocument RPC - partial document update
func (g *GRPCServer) UpdateDocument(ctx context.Context, req *proto.UpdateDocumentRequest) (*proto.Document, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}

	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required fields: collection, key, lang")
	}

	if !req.UpdateMeta && !req.UpdateContent && !req.UpdateTtl {
		return nil, status.Error(codes.InvalidArgument, "no fields to update")
	}

	patch := DocPatch{HasMeta: req.UpdateMeta, ExpectedRevision: req.ExpectedRevision}
	if req.UpdateMeta {
		patch.Meta = make(map[string][]string, len(req.Meta))
		for k, v := range req.Meta {
			patch.Meta[k] = v.Values
		}
	}
	if req.UpdateContent {
		patch.Content = &req.ContentMd
	}
	if req.UpdateTtl {
		patch.TTL = &req.Ttl
	}
	saved, err := g.server.updateDocument(req.Collection, req.Key, req.Lang, patch)
	var conflict *RevisionConflictError
	switch {
	case errors.Is(err, errDocNotFound):
		return nil, status.Error(codes.NotFound, "document not found")
	case errors.As(err, &conflict):
		return nil, status.Error(codes.Aborted, conflict.Error())
	case err != nil:
		return nil, grpcError(codes.InvalidArgument, err) // GO-043: quota → RESOURCE_EXHAUSTED
	}
	return withRevision(docToProto(&saved), docRevision(&saved)), nil
}

// GetDocumentMeta implements the GetDocumentMeta RPC - returns metadata only
func (g *GRPCServer) GetDocumentMeta(ctx context.Context, req *proto.GetDocumentMetaRequest) (*proto.GetDocumentMetaResponse, error) {
	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required fields: collection, key, lang")
	}

	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	var doc storage.Doc
	err := g.server.DBViewC(req.Collection, func(tx *bolt.Tx) error {
		bByK := tx.Bucket([]byte("bykey"))
		bDocs := tx.Bucket([]byte("docs"))
		docID := bByK.Get(storage.ByKeyKey(req.Collection, req.Key, req.Lang))
		if docID == nil {
			return errDocNotFound
		}
		v := bDocs.Get(storage.DocKey(req.Collection, string(docID)))
		if v == nil {
			return errDocNotFound
		}
		d, err := loadDoc(v)
		if err != nil {
			return err
		}
		doc = *d
		return nil
	})
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil, status.Error(codes.NotFound, "document not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	if doc.ExpiresAt > 0 && doc.ExpiresAt < time.Now().Unix() {
		return nil, status.Error(codes.NotFound, "document not found")
	}

	protoMeta := make(map[string]*proto.MetaValues)
	for k, v := range doc.Meta {
		protoMeta[k] = &proto.MetaValues{Values: v}
	}

	return &proto.GetDocumentMetaResponse{
		Key:       doc.Key,
		Lang:      doc.Lang,
		Meta:      protoMeta,
		AddedAt:   doc.AddedAt,
		UpdatedAt: doc.UpdatedAt,
		ExpiresAt: doc.ExpiresAt,
	}, nil
}

// Classify implements the Classify RPC — zero-shot document classification.
func (g *GRPCServer) Classify(ctx context.Context, req *proto.ClassifyRequest) (*proto.ClassifyResponse, error) {
	if g.server.AuthManager != nil && req.Collection != "" {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if len(req.Labels) == 0 {
		return nil, status.Error(codes.InvalidArgument, "labels are required")
	}

	resp, err := g.server.classifyDocument(ctx, req.Collection, req.Key, req.Lang, req.Text, req.Labels, int(req.TopK), req.Multi, req.Threshold)
	if err != nil {
		if err.Error() == "no embedding provider configured" {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		var bad invalidInput
		if errors.As(err, &bad) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		if errors.Is(err, errNotFound) {
			return nil, status.Error(codes.NotFound, "document not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	protoResults := make([]*proto.ClassifyLabelScore, len(resp.Results))
	for i, r := range resp.Results {
		protoResults[i] = &proto.ClassifyLabelScore{
			Label: r.Label,
			Score: r.Score,
		}
	}

	return &proto.ClassifyResponse{
		Results:    protoResults,
		Model:      resp.Model,
		Dimensions: safeInt32(resp.Dimensions),
	}, nil
}

// DeleteDocument implements the DeleteDocument RPC — deletes a single document.
func (g *GRPCServer) DeleteDocument(ctx context.Context, req *proto.DeleteDocumentRequest) (*proto.DeleteDocumentResponse, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required fields: collection, key, lang")
	}
	if err := g.server.deleteDocumentInternal(req.Collection, req.Key, req.Lang); err != nil {
		// A missing document is the caller's NotFound, not a server fault.
		if errors.Is(err, errNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &proto.DeleteDocumentResponse{
		Status:     "deleted",
		Collection: req.Collection,
		Key:        req.Key,
		Lang:       req.Lang,
	}, nil
}

// DeleteCollection implements the DeleteCollection RPC — deletes all documents in a collection.
func (g *GRPCServer) DeleteCollection(ctx context.Context, req *proto.DeleteCollectionRequest) (*proto.DeleteCollectionResponse, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}

	deletedCount, err := g.server.deleteCollection(req.Collection)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &proto.DeleteCollectionResponse{
		Status:       "deleted",
		Collection:   req.Collection,
		DeletedCount: safeInt32(deletedCount),
	}, nil
}

// ListSynonyms implements the ListSynonyms RPC.
func (g *GRPCServer) ListSynonyms(ctx context.Context, req *proto.ListSynonymsRequest) (*proto.ListSynonymsResponse, error) {
	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}
	if g.server.SynonymManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "synonym manager not initialized")
	}

	synonyms := g.server.SynonymManager.List(req.Collection)
	entries := make([]*proto.SynonymEntry, 0, len(synonyms))
	for term, syns := range synonyms {
		entries = append(entries, &proto.SynonymEntry{
			Term:     term,
			Synonyms: syns,
		})
	}

	return &proto.ListSynonymsResponse{
		Collection: req.Collection,
		Entries:    entries,
		Total:      safeInt32(len(entries)),
	}, nil
}

// AddSynonym implements the AddSynonym RPC.
func (g *GRPCServer) AddSynonym(ctx context.Context, req *proto.AddSynonymRequest) (*proto.AddSynonymResponse, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}
	if req.Collection == "" || strings.TrimSpace(req.Term) == "" || len(req.Synonyms) == 0 {
		return nil, status.Error(codes.InvalidArgument, "missing required fields: collection, term, synonyms")
	}
	if g.server.SynonymManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "synonym manager not initialized")
	}
	if err := g.server.SynonymManager.Set(req.Collection, req.Term, req.Synonyms); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &proto.AddSynonymResponse{Status: "ok"}, nil
}

// DeleteSynonym implements the DeleteSynonym RPC.
func (g *GRPCServer) DeleteSynonym(ctx context.Context, req *proto.DeleteSynonymRequest) (*proto.DeleteSynonymResponse, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}
	if req.Collection == "" || req.Term == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required fields: collection, term")
	}
	if g.server.SynonymManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "synonym manager not initialized")
	}
	if err := g.server.SynonymManager.Delete(req.Collection, req.Term); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &proto.DeleteSynonymResponse{Status: "ok"}, nil
}

// ListStopwords implements the ListStopwords RPC.
func (g *GRPCServer) ListStopwords(ctx context.Context, req *proto.ListStopwordsRequest) (*proto.ListStopwordsResponse, error) {
	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}
	if g.server.StopWordManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "stopword manager not initialized")
	}

	defaults, custom := g.server.StopWordManager.List(req.Collection)
	entries := make([]*proto.StopwordEntry, 0, len(defaults)+len(custom))
	for _, w := range defaults {
		entries = append(entries, &proto.StopwordEntry{Word: w, IsDefault: true})
	}
	for _, w := range custom {
		entries = append(entries, &proto.StopwordEntry{Word: w, IsDefault: false})
	}

	return &proto.ListStopwordsResponse{
		Collection: req.Collection,
		Entries:    entries,
		Total:      safeInt32(len(entries)),
		Defaults:   safeInt32(len(defaults)),
		Custom:     safeInt32(len(custom)),
	}, nil
}

// AddStopwords implements the AddStopwords RPC.
func (g *GRPCServer) AddStopwords(ctx context.Context, req *proto.AddStopwordsRequest) (*proto.AddStopwordsResponse, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}
	if req.Collection == "" || !anyNonBlank(req.Words) {
		return nil, status.Error(codes.InvalidArgument, "missing required fields: collection, words")
	}
	if g.server.StopWordManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "stopword manager not initialized")
	}
	if err := g.server.StopWordManager.Add(req.Collection, req.Words); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &proto.AddStopwordsResponse{Status: "ok", Added: safeInt32(len(req.Words))}, nil
}

// DeleteStopwords implements the DeleteStopwords RPC.
func (g *GRPCServer) DeleteStopwords(ctx context.Context, req *proto.DeleteStopwordsRequest) (*proto.DeleteStopwordsResponse, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}
	if req.Collection == "" || len(req.Words) == 0 {
		return nil, status.Error(codes.InvalidArgument, "missing required fields: collection, words")
	}
	if g.server.StopWordManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "stopword manager not initialized")
	}
	var deleted int32
	var errs []string
	for _, w := range req.Words {
		if err := g.server.StopWordManager.Delete(req.Collection, w); err != nil {
			errs = append(errs, err.Error())
		} else {
			deleted++
		}
	}
	return &proto.DeleteStopwordsResponse{Status: "ok", Deleted: deleted, Errors: errs}, nil
}

// GetMetaKeys implements the GetMetaKeys RPC.
func (g *GRPCServer) GetMetaKeys(ctx context.Context, req *proto.GetMetaKeysRequest) (*proto.GetMetaKeysResponse, error) {
	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	meta := g.server.metaKeys(req.Collection)

	protoMeta := make(map[string]*proto.MetaValues)
	for k, v := range meta {
		protoMeta[k] = &proto.MetaValues{Values: v}
	}
	return &proto.GetMetaKeysResponse{Meta: protoMeta}, nil
}

// GetChecksum implements the GetChecksum RPC.
func (g *GRPCServer) GetChecksum(ctx context.Context, req *proto.GetChecksumRequest) (*proto.GetChecksumResponse, error) {
	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	checksum, count := g.server.collectionChecksum(req.Collection)
	return &proto.GetChecksumResponse{
		Collection:    req.Collection,
		Checksum:      checksum,
		DocumentCount: safeInt32(count),
	}, nil
}

// anyNonBlank reports whether some string has more than whitespace.
func anyNonBlank(ss []string) bool {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}
