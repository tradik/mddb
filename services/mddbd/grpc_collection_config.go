package main

import (
	proto "mddb/proto"
)

// Collection config conversions for gRPC (GO-035).
//
// CollectionConfigProto used to carry 8 of CollectionConfig's 18 fields, so a
// gRPC client reading a collection saw a partially configured one and had no
// way to tell that from a collection that really was partially configured. An
// operational tool reporting "this collection is not encrypted" because it
// asked over gRPC is worse than one that reports nothing.
//
// Two rules run through this file:
//
//   - **Secrets are write-only.** Reads return an empty `secret_key` /
//     `api_key` and a `*_set` flag saying whether one is stored. Read
//     permission on a collection is permission to read its documents, not to
//     collect the S3 credentials for the bucket underneath it.
//   - **Omitted means "leave it alone".** Every setter here treats a zero
//     value as "not sent". Booleans carry proto3 presence precisely so this
//     rule can apply to them too.

// storageConfigToProto converts stored storage settings for the wire, leaving
// the secret behind.
func storageConfigToProto(cfg *StorageConfigDef) *proto.StorageConfigProto {
	if cfg == nil {
		return nil
	}
	return &proto.StorageConfigProto{
		Endpoint:  cfg.Endpoint,
		Bucket:    cfg.Bucket,
		Region:    cfg.Region,
		AccessKey: cfg.AccessKey,
		Prefix:    cfg.Prefix,
		UseTls:    cfg.UseTLS,
		// SecretKey deliberately unset; the flag says one exists.
		SecretKeySet: cfg.SecretKey != "",
	}
}

// storageConfigFromProto applies an incoming storage block to the stored one.
//
// `existing` is the config already on disk. An empty incoming secret keeps the
// stored secret, because a client that read the config back sees a masked
// secret and would otherwise write the mask — wiping the credential on every
// round trip through a UI. To remove one, clear the whole storage block.
func storageConfigFromProto(in *proto.StorageConfigProto, existing *StorageConfigDef) *StorageConfigDef {
	if in == nil {
		return existing
	}
	out := &StorageConfigDef{
		Endpoint:  in.GetEndpoint(),
		Bucket:    in.GetBucket(),
		Region:    in.GetRegion(),
		AccessKey: in.GetAccessKey(),
		SecretKey: in.GetSecretKey(),
		Prefix:    in.GetPrefix(),
		UseTLS:    in.GetUseTls(),
	}
	if out.SecretKey == "" && existing != nil {
		out.SecretKey = existing.SecretKey
	}
	return out
}

// wordPressToProto converts a publishing target for the wire, leaving the API
// key behind.
func wordPressToProto(cfg *WordPressTargetConfig) *proto.WordPressTargetProto {
	if cfg == nil {
		return nil
	}
	return &proto.WordPressTargetProto{
		Url: cfg.URL,
		// APIKey deliberately unset; the flag says one exists.
		ApiKeySet: cfg.APIKey != "",
	}
}

// wordPressFromProto applies an incoming publishing target to the stored one,
// keeping the stored key when the client sends none. Same reasoning as
// storageConfigFromProto.
func wordPressFromProto(in *proto.WordPressTargetProto, existing *WordPressTargetConfig) *WordPressTargetConfig {
	if in == nil {
		return existing
	}
	out := &WordPressTargetConfig{
		URL:    in.GetUrl(),
		APIKey: in.GetApiKey(),
	}
	if out.APIKey == "" && existing != nil {
		out.APIKey = existing.APIKey
	}
	return out
}

// collectionConfigToProto renders a stored config for the wire, secrets masked.
func collectionConfigToProto(cfg *CollectionConfig) *proto.CollectionConfigProto {
	if cfg == nil {
		return nil
	}
	return &proto.CollectionConfigProto{
		Type:            cfg.Type,
		Description:     cfg.Description,
		Icon:            cfg.Icon,
		Color:           cfg.Color,
		CustomMeta:      cfg.CustomMeta,
		MaxRevisions:    safeInt32(cfg.MaxRevisions),
		Retrieval:       retrievalProfileToProto(cfg.Retrieval),
		ResponsePrompt:  cfg.ResponsePrompt,
		StorageBackend:  cfg.StorageBackend,
		StorageConfig:   storageConfigToProto(cfg.StorageConfig),
		Quantization:    cfg.Quantization,
		DiskOnlyVectors: cfg.DiskOnlyVectors,
		Encrypted:       cfg.Encrypted,
		TrackAccess:     cfg.TrackAccess,
		TrackHot:        cfg.TrackHot,
		SpellCorrect:    cfg.SpellCorrect,
		SpellLang:       cfg.SpellLang,
		Wordpress:       wordPressToProto(cfg.WordPress),
	}
}

// applyCollectionConfigRequest merges a SetCollectionConfig request into cfg.
//
// Every field follows "omitted means leave it alone". For strings that is an
// empty value; for the booleans it is proto3 presence, which is why they are
// declared `optional`. Without presence, a client changing only the icon would
// send encrypted=false, and CollectionManager.Set would write it — the next
// document in an encrypted collection would land as plaintext. That was the
// RAG-001 bug, and adding these fields is exactly the change that could have
// reintroduced it.
func applyCollectionConfigRequest(cfg *CollectionConfig, req *proto.SetCollectionConfigRequest) {
	if req.GetStorageBackend() != "" {
		cfg.StorageBackend = req.GetStorageBackend()
	}
	if req.GetQuantization() != "" {
		cfg.Quantization = req.GetQuantization()
	}
	if req.GetSpellLang() != "" {
		cfg.SpellLang = req.GetSpellLang()
	}
	if req.DiskOnlyVectors != nil {
		cfg.DiskOnlyVectors = req.GetDiskOnlyVectors()
	}
	if req.Encrypted != nil {
		cfg.Encrypted = req.GetEncrypted()
	}
	if req.TrackAccess != nil {
		cfg.TrackAccess = req.GetTrackAccess()
	}
	if req.TrackHot != nil {
		cfg.TrackHot = req.GetTrackHot()
	}
	if req.SpellCorrect != nil {
		cfg.SpellCorrect = req.GetSpellCorrect()
	}
	if req.GetStorageConfig() != nil {
		cfg.StorageConfig = storageConfigFromProto(req.GetStorageConfig(), cfg.StorageConfig)
	}
	if req.GetWordpress() != nil {
		cfg.WordPress = wordPressFromProto(req.GetWordpress(), cfg.WordPress)
	}
}
