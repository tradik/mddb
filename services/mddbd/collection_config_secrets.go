package main

// Secret handling for collection configuration (GO-035).
//
// A collection config can hold two credentials: the S3 secret key under
// `storageConfig`, and the mddb-sync publish key under `wordpress`. Both were
// returned in full by every read.
//
// Reading a collection's configuration requires read permission on that
// collection. That is permission to read its documents — not to collect the
// credentials for the bucket underneath them, which would reach every other
// collection sharing it. So reads mask the values and report only whether one
// is stored.
//
// Masking on its own would break the obvious client loop: read the config,
// change one field, write it back. The mask would go back as the new secret.
// Writes therefore treat an empty incoming secret as "keep the stored one" —
// which is also what a user editing a form with a blank password field means.
// To remove a credential, remove the block that holds it.

// redactCollectionConfig returns a copy safe to send to a client: secrets
// blanked, presence flags set. The stored config is not modified.
func redactCollectionConfig(cfg *CollectionConfig) *CollectionConfig {
	if cfg == nil {
		return nil
	}
	out := *cfg

	if cfg.StorageConfig != nil {
		sc := *cfg.StorageConfig
		sc.SecretKeySet = sc.SecretKey != ""
		sc.SecretKey = ""
		out.StorageConfig = &sc
	}
	if cfg.WordPress != nil {
		wp := *cfg.WordPress
		wp.APIKeySet = wp.APIKey != ""
		wp.APIKey = ""
		out.WordPress = &wp
	}
	return &out
}

// carryOverSecrets copies stored credentials into an incoming config wherever
// the client sent none, so a read-modify-write round trip does not erase them.
//
// The presence flags are cleared on the way in: they describe what a read
// found, and letting a client's copy of one reach storage would mean a config
// that claims to hold a secret it does not.
func carryOverSecrets(incoming, stored *CollectionConfig) {
	if incoming == nil {
		return
	}

	if incoming.StorageConfig != nil {
		incoming.StorageConfig.SecretKeySet = false
		if incoming.StorageConfig.SecretKey == "" &&
			stored != nil && stored.StorageConfig != nil {
			incoming.StorageConfig.SecretKey = stored.StorageConfig.SecretKey
		}
	}
	if incoming.WordPress != nil {
		incoming.WordPress.APIKeySet = false
		if incoming.WordPress.APIKey == "" &&
			stored != nil && stored.WordPress != nil {
			incoming.WordPress.APIKey = stored.WordPress.APIKey
		}
	}
}
